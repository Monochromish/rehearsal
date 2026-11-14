package sandbox

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// Init is what runs first inside the sandbox. The sandbox has a network
// of its own with nothing in it, so Init listens on the addresses the
// proxies have outside and passes each connection out through a Unix
// socket. Then it runs the command and exits with its status.
//
// args: [--outbox DIR] [--alive SOCKET] [--relay HOST:PORT=SOCKET]... -- command...
func Init(args []string) int {
	var outbox, alive string
	var relays [][2]string
	for len(args) > 0 && args[0] != "--" {
		if len(args) < 2 {
			return fail("%s is missing its value", args[0])
		}
		switch args[0] {
		case "--outbox":
			outbox = args[1]
		case "--alive":
			alive = args[1]
		case "--relay":
			addr, socket, ok := strings.Cut(args[1], "=")
			if !ok {
				return fail("--relay wants HOST:PORT=SOCKET, got %q", args[1])
			}
			relays = append(relays, [2]string{addr, socket})
		default:
			return fail("unknown option %s", args[0])
		}
		args = args[2:]
	}
	if len(args) < 2 {
		return fail("no command to run")
	}
	command := args[1:]

	for _, r := range relays {
		l, err := net.Listen("tcp", r[0])
		if err != nil {
			return fail("listen on %s inside the sandbox: %v", r[0], err)
		}
		go serve(l, r[1])
	}

	var running atomic.Bool
	running.Store(true)
	if alive != "" {
		conn, err := net.Dial("unix", alive)
		if err != nil {
			return fail("reach Rehearsal from inside the sandbox: %v", err)
		}
		go func() {
			// Nothing is ever sent on this connection. It ends when the
			// process on the other side does.
			_, _ = io.Copy(io.Discard, conn)
			if !running.Load() {
				return
			}
			fmt.Fprint(os.Stderr, "\r\nrehearse: Rehearsal has stopped. This session has no network from here on, and its file changes stay in the sandbox.\r\n")
			if outbox != "" {
				stamp := strconv.FormatInt(time.Now().Unix(), 10)
				// there is nobody left to report a failed write to
				_ = os.WriteFile(filepath.Join(outbox, cutFile), []byte(stamp), 0o600)
			}
		}()
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Ctrl+C reaches the command through the terminal. This process has to
	// outlive it to pass on its exit status.
	signal.Ignore(syscall.SIGINT)
	if err := cmd.Start(); err != nil {
		running.Store(false)
		return fail("start %s inside the sandbox: %v", command[0], err)
	}
	passed := make(chan os.Signal, 1)
	signal.Notify(passed, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for s := range passed {
			_ = cmd.Process.Signal(s) // it may already be gone
		}
	}()
	err := cmd.Wait()
	running.Store(false)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		return exit.ExitCode()
	}
	return 1
}

func fail(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "rehearse: sandbox: "+format+"\n", args...)
	return 125
}

func serve(l net.Listener, socket string) {
	for {
		in, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			out, err := net.Dial("unix", socket)
			if err != nil {
				_ = in.Close() // Rehearsal has gone: the caller sees a dropped connection
				return
			}
			join(in, out)
		}()
	}
}
