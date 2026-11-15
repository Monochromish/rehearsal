// Package leftover follows the processes the wrapped command starts and
// says which of them are still running once it has exited. A process is
// known by its number and its start time together, and by nothing else:
// never by its name.
package leftover

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// EnvMark is a variable given to the wrapped command and to nothing else.
// Its value is made for the session, so a process that carries it was
// started by the command or by something the command started.
const EnvMark = "REHEARSAL_SESSION_MARK"

const (
	pollEvery = 300 * time.Millisecond
	// how long a process has to finish exiting after the command does
	// before it counts as left running
	settle = 500 * time.Millisecond
	// after the forced signal
	killWait = 2 * time.Second
	// the unit of the start time in /proc, which Linux fixes at 100 a second
	ticksPerSecond = 100
)

type Status string

const (
	Running Status = "running"
	// exited after the polite signal
	Stopped Status = "stopped"
	// did not, and was sent the forced one
	Forced Status = "forced"
	// had exited by itself before anything was sent to it
	Gone   Status = "gone"
	Failed Status = "failed"
)

// Process is one process the command started that was still running when
// the command exited.
type Process struct {
	ID  string `json:"id"`
	PID int    `json:"pid"`
	// the command line it was started with, as it may be shown
	Command string    `json:"command"`
	Started time.Time `json:"started"`
	// how long it has run; for one that is no longer running, how long it
	// had run when it was last seen
	Seconds int64 `json:"seconds"`
	// TCP ports it listens on
	Ports  []int  `json:"ports"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`

	start uint64
}

var (
	ErrNotListed = errors.New("the agent is still running, so nothing is listed yet")
	ErrClosed    = errors.New("the session is over")
)

// id tells one process from every other, including a later one that is
// given the same number.
type id struct {
	pid   int
	start uint64
}

type stat struct {
	ppid  int
	start uint64
	state byte
	name  string
}

type Tracker struct {
	proc string
	mark []byte
	show func(args []string) string

	polling sync.WaitGroup
	quit    chan struct{}
	// one stop request at a time, so a process is never signalled twice
	stopping sync.Mutex

	// the command is started through a sandbox
	wrapped bool

	mu      sync.Mutex
	root    id
	tracked map[id]bool
	// looked at and found not to carry the mark
	checked map[id]bool
	listed  bool
	closed  bool
	left    []*Process
}

// New makes a tracker. show turns a command line into the text that is
// kept and shown; nil joins the arguments as they are.
func New(show func(args []string) string) *Tracker {
	if show == nil {
		show = func(args []string) string { return strings.Join(args, " ") }
	}
	return &Tracker{
		proc: "/proc", mark: []byte(EnvMark + "=" + rand.Text()), show: show,
		quit: make(chan struct{}), tracked: map[id]bool{}, checked: map[id]bool{},
	}
}

// Wrapped says that the command is started through a sandbox with a
// process namespace of its own. The first process of that namespace is
// the sandbox's, not the agent's: it stays for as long as anything inside
// runs, and it is never listed. Call it before Follow.
func (t *Tracker) Wrapped() { t.wrapped = true }

// firstOfItsNamespace reports whether pid is process 1 of a process
// namespace directly inside this one.
func (t *Tracker) firstOfItsNamespace(pid int) bool {
	data, err := os.ReadFile(filepath.Join(t.proc, strconv.Itoa(pid), "status"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if numbers, ok := strings.CutPrefix(line, "NSpid:"); ok {
			f := strings.Fields(numbers)
			return len(f) == 2 && f[1] == "1"
		}
	}
	return false
}

// Env is the variable to start the command with.
func (t *Tracker) Env() string { return string(t.mark) }

// Follow starts watching the command, which has just been started as pid.
func (t *Tracker) Follow(pid int) {
	t.mu.Lock()
	if st, ok := t.stat(pid); ok {
		t.root = id{pid, st.start}
		t.tracked[t.root] = true
	}
	t.mu.Unlock()
	t.polling.Go(func() {
		tick := time.NewTicker(pollEvery)
		defer tick.Stop()
		for {
			t.scan()
			select {
			case <-tick.C:
			case <-t.quit:
				return
			}
		}
	})
}

// Conclude is called once, when the command has exited. It returns what
// the command started that is still running.
func (t *Tracker) Conclude() []Process {
	close(t.quit)
	t.polling.Wait()
	t.scan()
	time.Sleep(settle)
	now := t.scan()

	t.mu.Lock()
	defer t.mu.Unlock()
	var found []id
	for p := range t.tracked {
		if st, ok := now[p.pid]; ok && st.start == p.start && st.state != 'Z' && p != t.root && p.pid != os.Getpid() {
			if t.wrapped && t.firstOfItsNamespace(p.pid) {
				continue
			}
			found = append(found, p)
		}
	}
	slices.SortFunc(found, func(a, b id) int {
		if a.start != b.start {
			if a.start < b.start {
				return -1
			}
			return 1
		}
		return a.pid - b.pid
	})
	uptime := t.uptime()
	for i, p := range found {
		args := t.args(p.pid)
		if len(args) == 0 {
			args = []string{"[" + now[p.pid].name + "]"}
		}
		running := max(uptime-float64(p.start)/ticksPerSecond, 0)
		t.left = append(t.left, &Process{
			ID: "p" + strconv.Itoa(i+1), PID: p.pid, Command: t.show(args),
			Started: time.Now().Add(-time.Duration(running * float64(time.Second))).UTC().Truncate(time.Second),
			Seconds: int64(running), Ports: t.ports(p.pid), Status: Running, start: p.start,
		})
	}
	t.listed = true
	return t.copies(t.left)
}

// Processes is the list, with each process's state as it is now. listed
// is false until the command has exited.
func (t *Tracker) Processes() (listed bool, left []Process) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.listed {
		return false, []Process{}
	}
	uptime := t.uptime()
	for _, p := range t.left {
		if p.Status != Running {
			continue
		}
		if !t.alive(p) {
			p.Status = Gone
			continue
		}
		p.Seconds = int64(max(uptime-float64(p.start)/ticksPerSecond, 0))
		p.Ports = t.ports(p.PID)
	}
	return true, t.copies(t.left)
}

// List is what the API answers with.
type List struct {
	// false while the agent is still running
	Listed bool `json:"listed"`
	// whether a request to stop something would be acted on now
	Open      bool      `json:"open"`
	Processes []Process `json:"processes"`
}

func (t *Tracker) List() List {
	listed, left := t.Processes()
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	running := slices.ContainsFunc(left, func(p Process) bool { return p.Status == Running })
	return List{Listed: listed, Open: listed && !closed && running, Processes: left}
}

// Close ends the session: nothing can be stopped after it.
func (t *Tracker) Close() {
	t.stopping.Lock()
	defer t.stopping.Unlock()
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
}

// Stop ends the named processes: a polite signal first, and the forced
// one for any still there after grace. It returns each named process with
// what became of it. Only a process on the list can be named.
func (t *Tracker) Stop(ids []string, grace time.Duration) ([]Process, error) {
	t.stopping.Lock()
	defer t.stopping.Unlock()

	t.mu.Lock()
	if !t.listed || t.closed {
		defer t.mu.Unlock()
		if t.closed {
			return nil, ErrClosed
		}
		return nil, ErrNotListed
	}
	var chosen []*Process
	for i, want := range ids {
		at := slices.IndexFunc(t.left, func(p *Process) bool { return p.ID == want })
		if at < 0 || slices.Contains(ids[:i], want) {
			t.mu.Unlock()
			return nil, fmt.Errorf("%q is not a process the agent left running, or is listed twice", want)
		}
		chosen = append(chosen, t.left[at])
	}
	t.mu.Unlock()

	var wg sync.WaitGroup
	for _, p := range chosen {
		wg.Go(func() { t.stopOne(p, grace) })
	}
	wg.Wait()

	t.mu.Lock()
	defer t.mu.Unlock()
	return t.copies(chosen), nil
}

func (t *Tracker) stopOne(p *Process, grace time.Duration) {
	set := func(s Status, detail string) {
		t.mu.Lock()
		p.Status, p.Detail = s, detail
		if s == Stopped || s == Forced {
			// how long it ran in all, now that it has ended
			p.Seconds = int64(max(t.uptime()-float64(p.start)/ticksPerSecond, 0))
		}
		t.mu.Unlock()
	}
	t.mu.Lock()
	decided := p.Status != Running
	t.mu.Unlock()
	if decided {
		return
	}
	// The handle is taken before the process is looked at again. Where
	// the kernel has process handles, a signal through it cannot reach a
	// later process that was given the same number.
	h, err := os.FindProcess(p.PID)
	if err != nil {
		set(Failed, err.Error())
		return
	}
	defer func() { _ = h.Release() }() // nothing to do about a handle that will not close
	if !t.alive(p) {
		set(Gone, "")
		return
	}
	if err := h.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			set(Gone, "")
			return
		}
		set(Failed, err.Error())
		return
	}
	if t.exits(p, grace) {
		set(Stopped, "")
		return
	}
	if err := h.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		set(Failed, err.Error())
		return
	}
	if t.exits(p, killWait) {
		set(Forced, "")
		return
	}
	set(Failed, "it was sent the forced signal and has not exited")
}

func (t *Tracker) exits(p *Process, within time.Duration) bool {
	for end := time.Now().Add(within); ; time.Sleep(25 * time.Millisecond) {
		if !t.alive(p) {
			return true
		}
		if time.Now().After(end) {
			return false
		}
	}
}

func (t *Tracker) alive(p *Process) bool {
	st, ok := t.stat(p.PID)
	return ok && st.start == p.start && st.state != 'Z'
}

func (t *Tracker) copies(of []*Process) []Process {
	out := make([]Process, 0, len(of))
	for _, p := range of {
		c := *p
		c.Ports = append([]int{}, p.Ports...)
		out = append(out, c)
	}
	return out
}

// scan adds to what is tracked: a process whose parent is tracked, and a
// process that carries the mark. It returns every process it saw.
func (t *Tracker) scan() map[int]stat {
	now := map[int]stat{}
	entries, err := os.ReadDir(t.proc)
	if err != nil {
		return now
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if st, ok := t.stat(pid); ok {
			now[pid] = st
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	current := func(p id) bool {
		st, ok := now[p.pid]
		return ok && st.start == p.start
	}
	for p := range t.tracked {
		if !current(p) && p != t.root {
			delete(t.tracked, p)
		}
	}
	for p := range t.checked {
		if !current(p) {
			delete(t.checked, p)
		}
	}
	for pid, st := range now {
		me := id{pid, st.start}
		// kernel threads have no environment and are nobody's children
		if t.tracked[me] || t.checked[me] || pid == 2 || st.ppid == 2 {
			continue
		}
		env, err := os.ReadFile(filepath.Join(t.proc, strconv.Itoa(pid), "environ"))
		switch {
		case err == nil && t.marked(env):
			t.tracked[me] = true
		case err == nil && len(env) > 0, errors.Is(err, fs.ErrPermission):
			t.checked[me] = true
		}
	}
	for added := true; added; {
		added = false
		for pid, st := range now {
			me := id{pid, st.start}
			if t.tracked[me] {
				continue
			}
			if parent, ok := now[st.ppid]; ok && t.tracked[id{st.ppid, parent.start}] {
				t.tracked[me] = true
				added = true
			}
		}
	}
	return now
}

func (t *Tracker) marked(env []byte) bool {
	return slices.ContainsFunc(bytes.Split(env, []byte{0}), func(kv []byte) bool { return bytes.Equal(kv, t.mark) })
}

// stat reads what /proc says of one process. The name is in brackets and
// may itself hold brackets and spaces, so the fields are counted from the
// last closing bracket.
func (t *Tracker) stat(pid int) (stat, bool) {
	data, err := os.ReadFile(filepath.Join(t.proc, strconv.Itoa(pid), "stat"))
	if err != nil {
		return stat{}, false
	}
	open, shut := bytes.IndexByte(data, '('), bytes.LastIndexByte(data, ')')
	if open < 0 || shut < open {
		return stat{}, false
	}
	f := strings.Fields(string(data[shut+1:]))
	if len(f) < 20 || len(f[0]) != 1 {
		return stat{}, false
	}
	ppid, err1 := strconv.Atoi(f[1])
	start, err2 := strconv.ParseUint(f[19], 10, 64)
	if err1 != nil || err2 != nil {
		return stat{}, false
	}
	return stat{ppid: ppid, start: start, state: f[0][0], name: string(data[open+1 : shut])}, true
}

func (t *Tracker) args(pid int) []string {
	data, err := os.ReadFile(filepath.Join(t.proc, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil
	}
	data = bytes.TrimRight(data, "\x00")
	if len(data) == 0 {
		return nil
	}
	return strings.Split(string(data), "\x00")
}

// uptime is the seconds since the machine started, which is what a
// process's start time is counted from.
func (t *Tracker) uptime() float64 {
	data, err := os.ReadFile(filepath.Join(t.proc, "uptime"))
	if err != nil {
		return 0
	}
	f := strings.Fields(string(data))
	if len(f) == 0 {
		return 0
	}
	up, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	return up
}

// ports is the TCP ports pid listens on: the listening sockets of its
// network, matched against the sockets it holds open.
func (t *Tracker) ports(pid int) []int {
	ports := []int{}
	dir := filepath.Join(t.proc, strconv.Itoa(pid))
	fds, err := os.ReadDir(filepath.Join(dir, "fd"))
	if err != nil {
		return ports
	}
	held := map[string]bool{}
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
		if err != nil {
			continue
		}
		if inode, ok := strings.CutPrefix(target, "socket:["); ok {
			held[strings.TrimSuffix(inode, "]")] = true
		}
	}
	if len(held) == 0 {
		return ports
	}
	for _, table := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(filepath.Join(dir, "net", table))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			// sl local remote state ... inode: the state for listening is 0A
			f := strings.Fields(line)
			if len(f) < 10 || f[3] != "0A" || !held[f[9]] {
				continue
			}
			at := strings.LastIndexByte(f[1], ':')
			port, err := strconv.ParseUint(f[1][at+1:], 16, 16)
			if err != nil || at < 0 {
				continue
			}
			if !slices.Contains(ports, int(port)) {
				ports = append(ports, int(port))
			}
		}
	}
	slices.Sort(ports)
	return ports
}
