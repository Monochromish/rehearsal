package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"rehearsal/internal/events"
	"rehearsal/internal/ident"
	"rehearsal/internal/leftover"
	"rehearsal/internal/redact"
)

const (
	// how long a process has to exit on the polite signal before it is
	// sent the forced one
	stopGrace = 3 * time.Second
	// the most of a command line that is kept
	maxCommandShown = 300
)

// leftovers is what the agent started that is still running after it has
// exited: the list, the record of it, and stopping what the user chooses.
// Only a process on the list can be stopped, and the list holds only what
// descends from the agent.
type leftovers struct {
	tracker *leftover.Tracker
	log     *events.Log

	mu sync.Mutex
	// processes whose outcome is already in the log
	recorded map[string]bool
	// closed by the first request to stop something, or to stop nothing
	answered chan struct{}
	answer   sync.Once
}

func followLeftovers(log *events.Log, matcher *ident.Matcher, keep []string) *leftovers {
	// A command line is kept the way a held command is: credentials
	// hidden, and a watched secret replaced by its name.
	show := func(args []string) string {
		text := strings.Join(redact.Args(args, keep...), " ")
		for _, found := range matcher.Find([]byte(text)) {
			if matcher.IsSecret(found.IdentifierID) {
				text = matcher.Mask(text)
				break
			}
		}
		if r := []rune(text); len(r) > maxCommandShown {
			text = string(r[:maxCommandShown]) + "..."
		}
		return text
	}
	return &leftovers{tracker: leftover.New(show), log: log, recorded: map[string]bool{}, answered: make(chan struct{})}
}

// list is called once, when the agent has exited. It writes one entry to
// the log for each process still running, and with stopAll stops them.
func (l *leftovers) list(stopAll bool) error {
	var err error
	var ids []string
	for _, p := range l.tracker.Conclude() {
		ids = append(ids, p.ID)
		if _, aerr := l.log.Append(events.TypeProcessLeft, events.ProcessLeft{
			ID: p.ID, PID: p.PID, Command: p.Command, Started: p.Started, Seconds: p.Seconds, Ports: p.Ports,
		}); aerr != nil {
			err = errors.Join(err, aerr)
		}
	}
	if stopAll && len(ids) > 0 {
		_, serr := l.Stop(ids)
		err = errors.Join(err, serr)
	}
	return err
}

func (l *leftovers) List() leftover.List { return l.tracker.List() }

// Stop ends the named processes and writes what became of each to the
// log, once.
func (l *leftovers) Stop(ids []string) ([]leftover.Process, error) {
	stopped, err := l.tracker.Stop(ids, stopGrace)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, p := range stopped {
		if l.recorded[p.ID] {
			continue
		}
		l.recorded[p.ID] = true
		if _, aerr := l.log.Append(events.TypeProcessStopped, events.ProcessStopped{
			ID: p.ID, PID: p.PID, Outcome: string(p.Status), Detail: p.Detail,
		}); aerr != nil {
			err = errors.Join(err, aerr)
		}
	}
	l.answer.Do(func() { close(l.answered) })
	return stopped, err
}

// finish ends the session for what was left running, and prints it. When
// the page is where the session is shown and nothing has been chosen yet,
// it waits for the page to say which to stop. Otherwise whatever has not
// been stopped is left running.
func (l *leftovers) finish(onPage bool, url string) error {
	defer l.tracker.Close()
	list := l.tracker.List()
	if len(list.Processes) == 0 {
		return nil
	}
	chosen := false
	select {
	case <-l.answered:
		chosen = true
	default:
	}
	if onPage && list.Open && !chosen {
		fmt.Fprintf(os.Stderr, "\nrehearse: %s the agent started still running. Choose which to stop on the page: %s\n          (Ctrl+C here leaves them running)\n",
			processes(len(list.Processes)), url)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		select {
		case <-l.answered:
			chosen = true
		case <-ctx.Done():
		}
		stop()
	}
	l.tracker.Close() // waits for a stop that is under way
	list = l.tracker.List()

	fmt.Fprintf(os.Stderr, "\nrehearse: what the agent started that was still running when it exited:\n")
	running := 0
	for _, p := range list.Processes {
		if p.Status == leftover.Running {
			running++
		}
		fmt.Fprintf(os.Stderr, "  %s: process %d, %s%s: %s\n", outcomeWords(p), p.PID, ran(p), listening(p.Ports), p.Command)
	}
	if running > 0 && !chosen {
		fmt.Fprintf(os.Stderr, "Nothing was stopped. Run with --stop-leftovers to stop everything the agent leaves running.\n")
	}
	return nil
}

func processes(n int) string {
	if n == 1 {
		return "1 process"
	}
	return fmt.Sprintf("%d processes", n)
}

func outcomeWords(p leftover.Process) string {
	switch p.Status {
	case leftover.Running:
		return "left running"
	case leftover.Stopped:
		return "stopped"
	case leftover.Forced:
		return "stopped by force"
	case leftover.Gone:
		return "already gone"
	default:
		return "could not be stopped (" + p.Detail + ")"
	}
}

func ran(p leftover.Process) string {
	d := time.Duration(p.Seconds) * time.Second
	verb := "running for "
	if p.Status != leftover.Running {
		verb = "ran for "
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%s%ds", verb, p.Seconds)
	case d < time.Hour:
		return fmt.Sprintf("%s%dm %ds", verb, p.Seconds/60, p.Seconds%60)
	default:
		return fmt.Sprintf("%s%dh %dm", verb, p.Seconds/3600, p.Seconds%3600/60)
	}
}

func listening(ports []int) string {
	if len(ports) == 0 {
		return ""
	}
	words := make([]string, len(ports))
	for i, p := range ports {
		words[i] = fmt.Sprint(p)
	}
	if len(ports) == 1 {
		return ", listening on port " + words[0]
	}
	return ", listening on ports " + strings.Join(words, ", ")
}
