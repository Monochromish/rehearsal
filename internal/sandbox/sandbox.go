// Package sandbox runs the wrapped command inside bubblewrap: the project
// behind a copy-on-write layer, the rest of the file system read-only, and
// no network except a path to the Rehearsal proxy.
package sandbox

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// InitArg is the first argument that makes this program act as the
// sandbox's first process. See Init.
const InitArg = "__sandbox-init"

const cutFile = "network-cut"

// Options describes one sandbox.
type Options struct {
	// the real project folder. Inside the sandbox the same path shows the
	// project with the agent's changes on top.
	Project string
	// a private folder for the layer and the sockets, on a Linux file system
	Dir string
	// this program, at a path the sandbox can see
	Self string
	// folders replaced by empty ones inside: Rehearsal's own state and keys
	Hide []string
	// paths under a hidden folder or /tmp that must stay readable inside
	Visible []string
	// paths outside the project the agent may write to for real
	Writable []string
	// the proxies' addresses on this machine, as host:port
	Proxies []string
	// real files modified after this were changed during the session
	Since time.Time
}

// Box is a sandbox that is ready to run a command in.
type Box struct {
	opts   Options
	upper  string
	work   string
	run    string // sockets, visible inside
	outbox string // the one folder of ours the inside can write to

	mu        sync.Mutex
	listeners []net.Listener
	conns     map[net.Conn]struct{}
	closed    bool
}

// Available reports why a sandbox cannot be made on this machine, or nil.
func Available() error {
	if _, err := exec.LookPath("bwrap"); err != nil {
		return errors.New("bubblewrap is not installed (the bwrap command). On Ubuntu: sudo apt install bubblewrap")
	}
	return nil
}

// New prepares the layer and starts the paths from the sandbox to the
// proxies. Nothing runs inside it until the command from Command is started.
func New(o Options) (*Box, error) {
	if err := Available(); err != nil {
		return nil, err
	}
	// The sockets go in a folder with a short path: a Unix socket's path
	// may be 107 bytes at most, and a session folder's can be longer.
	run, err := os.MkdirTemp("", "rehearse-box-")
	if err != nil {
		return nil, fmt.Errorf("create a folder for the sandbox's sockets: %w", err)
	}
	b := &Box{
		opts:  o,
		upper: filepath.Join(o.Dir, "upper"), work: filepath.Join(o.Dir, "work"),
		run: run, outbox: filepath.Join(o.Dir, "outbox"),
		conns: map[net.Conn]struct{}{},
	}
	for _, d := range []string{b.upper, b.work, b.outbox} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, errors.Join(fmt.Errorf("create the sandbox folder: %w", err), os.RemoveAll(run))
		}
	}
	for i, addr := range o.Proxies {
		if err := b.relay(b.proxySocket(i), addr); err != nil {
			return nil, errors.Join(err, b.Close())
		}
	}
	if err := b.relay(b.aliveSocket(), ""); err != nil {
		return nil, errors.Join(err, b.Close())
	}
	return b, nil
}

// Layer is the folder holding the agent's versions of the files it changed.
func (b *Box) Layer() string { return b.upper }

func (b *Box) proxySocket(i int) string { return filepath.Join(b.run, fmt.Sprintf("proxy-%d.sock", i)) }
func (b *Box) aliveSocket() string      { return filepath.Join(b.run, "alive.sock") }

// relay accepts connections on a Unix socket and joins each to the TCP
// address target. A Unix socket is a file, so it is reachable from inside
// the sandbox's own network namespace, where this machine's ports are not.
// With no target the connection is only held open: it is how the inside
// tells that this process is still alive.
func (b *Box) relay(socket, target string) error {
	l, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("open a path into the sandbox: %w", err)
	}
	b.mu.Lock()
	b.listeners = append(b.listeners, l)
	b.mu.Unlock()
	go func() {
		for {
			in, err := l.Accept()
			if err != nil {
				return // closed
			}
			if !b.keep(in) {
				return
			}
			if target == "" {
				continue
			}
			go func() {
				out, err := net.Dial("tcp", target)
				if err != nil {
					_ = in.Close() // the proxy has stopped; the caller sees a dropped connection
					return
				}
				join(in, out)
				b.forget(in)
			}()
		}
	}()
	return nil
}

// keep remembers a connection so Close can end it. It reports false, and
// closes the connection, when the box is already closed.
func (b *Box) keep(c net.Conn) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		_ = c.Close()
		return false
	}
	b.conns[c] = struct{}{}
	return true
}

func (b *Box) forget(c net.Conn) {
	b.mu.Lock()
	delete(b.conns, c)
	b.mu.Unlock()
}

// join copies both ways until either side is done, then closes both. The
// copies end with an error whenever a connection does, which is the
// ordinary way for them to end.
func join(a, b net.Conn) {
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		// lets the other side finish reading what was already sent
		if half, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = half.CloseWrite()
		}
		done <- struct{}{}
	}
	go pipe(a, b)
	go pipe(b, a)
	<-done
	<-done
	_ = a.Close()
	_ = b.Close()
}

// Command is the command line that runs agent inside the sandbox.
func (b *Box) Command(agent []string) []string {
	args := []string{
		"bwrap",
		"--unshare-user", "--unshare-net", "--unshare-pid", "--unshare-ipc",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
	}
	for _, p := range b.opts.Hide {
		if exists(p) {
			args = append(args, "--tmpfs", p)
		}
	}
	for _, p := range append([]string{b.run}, b.opts.Visible...) {
		if exists(p) {
			args = append(args, "--ro-bind", p, p)
		}
	}
	for _, p := range append([]string{b.outbox}, b.opts.Writable...) {
		if exists(p) {
			args = append(args, "--bind", p, p)
		}
	}
	args = append(args,
		"--overlay-src", b.opts.Project,
		"--overlay", b.upper, b.work, b.opts.Project,
		"--chdir", b.opts.Project,
		"--", b.opts.Self, InitArg, "--outbox", b.outbox, "--alive", b.aliveSocket(),
	)
	for i, addr := range b.opts.Proxies {
		args = append(args, "--relay", addr+"="+b.proxySocket(i))
	}
	return append(append(args, "--"), agent...)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// NetworkCut reports when the inside noticed that this side had gone
// while the command was still running. It can only be true for a session
// whose rehearse process died.
func NetworkCut(dir string) (time.Time, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "outbox", cutFile))
	if err != nil {
		return time.Time{}, false
	}
	unix, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(unix, 0), true
}

// Close ends the paths into the sandbox. Anything still running inside
// has no network from then on.
func (b *Box) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	var err error
	for _, l := range b.listeners {
		if cerr := l.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
			err = errors.Join(err, cerr)
		}
	}
	for c := range b.conns {
		_ = c.Close() // one that is mid-request, or the keep-alive
	}
	b.listeners = nil
	clear(b.conns)
	return errors.Join(err, os.RemoveAll(b.run))
}

// Remove deletes the layer. The kernel leaves a folder nobody can read
// inside the overlay's work folder, so permissions are opened up first.
func (b *Box) Remove() error {
	err := filepath.WalkDir(b.opts.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable until its parent is opened up, which comes first
		}
		if d.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return nil
	})
	return errors.Join(err, os.RemoveAll(b.opts.Dir))
}
