// Package run launches the wrapped command and passes the terminal through
// to it untouched.
package run

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// ProxyEnv is the environment that sends a command's traffic through the
// proxy and makes it trust our certificate. It reaches only the process it
// is given to and that process's children.
func ProxyEnv(proxyURL, caCertPath string) []string {
	var env []string
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		env = append(env, k+"="+proxyURL)
	}
	for _, k := range []string{"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"} {
		env = append(env, k+"="+caCertPath)
	}
	return env
}

// Command runs command and returns its exit code. The error is for
// failures of our own (could not start it, could not restore the terminal),
// never for the command exiting non-zero.
//
// The command gets a pseudo-terminal only when we are attached to a
// terminal ourselves; otherwise input and output pass straight through, so
// pipes keep working.
//
// onFirstInput is called once, when the first byte of the user's input is
// passed on to the command. See WatchesInput for when that can be seen.
// onStart, when it is not nil, is called once with the command's process
// number as soon as it has been started.
func Command(command []string, extraEnv []string, onFirstInput func(), onStart func(pid int)) (int, error) {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = append(os.Environ(), extraEnv...)
	if onStart == nil {
		onStart = func(int) {}
	}

	var waitErr, ourErr error
	if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		waitErr, ourErr = runInPTY(cmd, onFirstInput, onStart)
	} else {
		waitErr, ourErr = runPlain(cmd, onFirstInput, onStart)
	}
	if ourErr != nil {
		return 1, ourErr
	}
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) {
		if code := exit.ExitCode(); code >= 0 {
			return code, nil
		}
		return 1, nil // killed by a signal
	}
	if waitErr != nil {
		return 1, fmt.Errorf("wait for %s: %w", command[0], waitErr)
	}
	return 0, nil
}

// WatchesInput reports whether the user's input passes through us, so
// that its first byte can be noticed. It does not in one case: input from
// a terminal with output going elsewhere. There the command is given the
// terminal itself, because a pipe in its place would change how it behaves.
func WatchesInput() bool {
	return !term.IsTerminal(int(os.Stdin.Fd())) || term.IsTerminal(int(os.Stdout.Fd()))
}

// noting passes input through and calls first when the first byte goes by.
type noting struct {
	in    io.Reader
	first func()
}

func (n *noting) Read(p []byte) (int, error) {
	c, err := n.in.Read(p)
	if c > 0 && n.first != nil {
		n.first()
		n.first = nil
	}
	return c, err
}

func runPlain(cmd *exec.Cmd, onFirstInput func(), onStart func(pid int)) (waitErr, ourErr error) {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	var stdin io.WriteCloser
	if WatchesInput() {
		cmd.Stdin = nil
		var err error
		if stdin, err = cmd.StdinPipe(); err != nil {
			return nil, fmt.Errorf("connect input to %s: %w", cmd.Path, err)
		}
	}
	// Ctrl+C reaches the command directly (same terminal group). We ignore
	// it ourselves so we are still here to print the ledger afterwards.
	signal.Ignore(syscall.SIGINT)
	defer signal.Reset(syscall.SIGINT)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	onStart(cmd.Process.Pid)
	if stdin != nil {
		go func() {
			// Both of these end with an error in the ordinary course of
			// things: the command exits, or stops reading, before our own
			// input ends. Neither says anything went wrong.
			_, _ = io.Copy(stdin, &noting{in: os.Stdin, first: onFirstInput})
			_ = stdin.Close()
		}()
	}
	stop := forward(cmd, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	return cmd.Wait(), nil
}

func runInPTY(cmd *exec.Cmd, onFirstInput func(), onStart func(pid int)) (waitErr, ourErr error) {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start %s in a terminal: %w", cmd.Path, err)
	}
	onStart(cmd.Process.Pid)
	defer func() {
		if err := ptmx.Close(); err != nil {
			ourErr = errors.Join(ourErr, fmt.Errorf("close the command's terminal: %w", err))
		}
	}()

	// Raw mode, so every key, Ctrl+C included, goes to the command.
	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, fmt.Errorf("switch the terminal to raw mode: %w", err)
	}
	defer func() {
		if err := term.Restore(int(os.Stdin.Fd()), old); err != nil {
			ourErr = errors.Join(ourErr, fmt.Errorf("restore the terminal (run `reset` to fix it): %w", err))
		}
	}()

	resize := make(chan os.Signal, 1)
	resizeErr := make(chan error, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	go func() {
		var first error
		for range resize {
			if err := pty.InheritSize(os.Stdin, ptmx); err != nil && first == nil {
				first = err
			}
		}
		resizeErr <- first
	}()
	resize <- syscall.SIGWINCH // set the starting size

	stop := forward(cmd, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	// This copy ends with an error whenever the session does (the terminal
	// closes under it), so its error says nothing useful.
	go func() { _, _ = io.Copy(ptmx, &noting{in: os.Stdin, first: onFirstInput}) }()

	drained := make(chan error, 1)
	go func() {
		_, err := io.Copy(os.Stdout, ptmx)
		drained <- err
	}()
	waitErr = cmd.Wait()
	// Let the last output through, but do not wait on a background process
	// the command may have left holding the terminal open.
	select {
	case err := <-drained:
		// EIO is how Linux reports that the command closed its terminal.
		if err != nil && !errors.Is(err, syscall.EIO) {
			ourErr = fmt.Errorf("copy the command's output: %w", err)
		}
	case <-time.After(500 * time.Millisecond):
	}

	signal.Stop(resize)
	close(resize)
	if err := <-resizeErr; err != nil {
		ourErr = errors.Join(ourErr, fmt.Errorf("resize the command's terminal: %w", err))
	}
	return waitErr, ourErr
}

func forward(cmd *exec.Cmd, sigs ...os.Signal) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	go func() {
		for s := range ch {
			// The command may already be gone, which is not worth reporting.
			if err := cmd.Process.Signal(s); err != nil && !errors.Is(err, os.ErrProcessDone) {
				fmt.Fprintf(os.Stderr, "rehearse: pass %v on to %s: %v\r\n", s, cmd.Path, err)
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(ch)
	}
}
