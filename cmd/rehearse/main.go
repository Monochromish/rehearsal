// Command rehearse wraps an AI coding agent and shows where its requests,
// and the user's data, go.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"rehearsal/internal/events"
	"rehearsal/internal/ident"
	"rehearsal/internal/ledger"
	"rehearsal/internal/proxy"
	"rehearsal/internal/run"
	"rehearsal/internal/server"
	"rehearsal/internal/webui"
)

const (
	canaryFile = "CANARY.txt"
	ledgerFile = "rehearsal-ledger.json"
)

var defaultProviders = []string{"api.anthropic.com", "mcp-proxy.anthropic.com", "api.openai.com"}

type options struct {
	port      int
	providers []string
	noOpen    bool
}

func main() {
	var opts options
	exitCode := 0
	root := &cobra.Command{
		Use:   "rehearse [flags] <command> [args...]",
		Short: "Wrap an AI coding agent and see where its requests and your data go",
		Example: "  rehearse claude\n" +
			"  rehearse --provider api.example.com -- my-agent --some-flag",
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			code, err := rehearse(opts, args)
			exitCode = code
			return err
		},
	}
	root.Flags().SetInterspersed(false) // everything after the command belongs to it
	root.Flags().IntVar(&opts.port, "port", 0, "port for the page (default: any free port)")
	root.Flags().StringSliceVar(&opts.providers, "provider", defaultProviders, "hosts to treat as the model provider")
	root.Flags().BoolVar(&opts.noOpen, "no-open", false, "do not open the page in a window")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rehearse:", err)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

// rehearse runs one session. The exit code is the wrapped command's; the
// error is for anything of ours that went wrong around it.
func rehearse(opts options, command []string) (exitCode int, err error) {
	// cleanup runs a shutdown step and keeps its error instead of losing it.
	cleanup := func(what string, fn func() error) {
		if cerr := fn(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("%s: %w", what, cerr))
		}
	}

	if _, err := exec.LookPath(command[0]); err != nil {
		return 127, fmt.Errorf("cannot find the command %q", command[0])
	}
	workDir, err := os.Getwd()
	if err != nil {
		return 1, fmt.Errorf("find the working directory: %w", err)
	}
	stateDir, err := stateDir()
	if err != nil {
		return 1, err
	}
	sessionDir := filepath.Join(stateDir, "sessions", time.Now().Format("20060102-150405")+"-"+strings.ToLower(rand.Text()[:6]))
	caDir := filepath.Join(stateDir, "ca")
	for _, d := range []string{sessionDir, caDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return 1, fmt.Errorf("create state directory: %w", err)
		}
	}

	log, err := events.Open(filepath.Join(sessionDir, "events.db"))
	if err != nil {
		return 1, err
	}
	defer cleanup("close the event log", log.Close)

	own, warn := ident.CollectReal(workDir)
	if warn != nil {
		fmt.Fprintf(os.Stderr, "rehearse: warning: not watching for some of your details:\n%v\n", warn)
	}
	canaries := ident.NewCanaries()
	identifiers := append(own, canaries...)
	removeCanary, err := plantCanaryFile(workDir, ident.Value(canaries, ident.IDCanaryWord))
	if err != nil {
		return 1, err
	}
	defer cleanup("remove "+canaryFile, removeCanary)

	// Only the program name is stored: its arguments may contain secrets.
	if _, err := log.Append(events.TypeSessionStarted, events.SessionStarted{
		Command: command[:1], WorkDir: workDir, ProviderHosts: opts.providers, Identifiers: identifiers,
	}); err != nil {
		return 1, err
	}

	proxyLog, err := os.OpenFile(filepath.Join(sessionDir, "proxy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 1, fmt.Errorf("open the proxy log: %w", err)
	}
	defer cleanup("close the proxy log", proxyLog.Close)
	recorder := proxy.NewRecorder(log, ident.NewMatcher(identifiers), opts.providers)
	px, err := proxy.Start(caDir, recorder, proxyLog)
	if err != nil {
		return 1, err
	}
	defer cleanup("stop the proxy", px.Close)

	web, err := server.Start(log, webui.FS(), opts.port)
	if err != nil {
		return 1, err
	}
	// Runs before the deferred steps above, so the page server has drained
	// by the time the event log closes.
	defer cleanup("stop the page server", func() error {
		time.Sleep(300 * time.Millisecond) // let the page receive the final events
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return web.Shutdown(ctx)
	})

	fmt.Fprintf(os.Stderr, "rehearse: watching. Ledger page: %s\n", web.URL)
	if !opts.noOpen {
		if err := openWindow(web.URL); err != nil {
			fmt.Fprintf(os.Stderr, "rehearse: could not open a window (%v). Open the address above yourself.\n", err)
		}
	}

	env := append(run.ProxyEnv(px.URL, px.CACertPath),
		"REHEARSAL_CANARY_EMAIL="+ident.Value(canaries, ident.IDCanaryEmail),
		"REHEARSAL_CANARY_API_KEY="+ident.Value(canaries, ident.IDCanaryAPIKey),
	)
	exitCode, err = run.Command(command, env)

	if _, aerr := log.Append(events.TypeSessionEnded, events.SessionEnded{ExitCode: exitCode}); aerr != nil {
		err = errors.Join(err, aerr)
	}
	if rerr := recorder.Err(); rerr != nil {
		err = errors.Join(err, fmt.Errorf("the ledger is incomplete, and requests were refused after this: %w", rerr))
	}
	cleanup("write the ledger", func() error { return report(log, workDir) })
	return exitCode, err
}

func report(log *events.Log, workDir string) error {
	evs, err := log.Since(0)
	if err != nil {
		return err
	}
	l, err := ledger.Build(evs)
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stderr, l.Text())
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the ledger: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ledgerFile), append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\nSaved to %s\n", ledgerFile)
	return nil
}

// plantCanaryFile writes the canary word to CANARY.txt. The returned
// function removes it again, unless a file of that name was already there.
func plantCanaryFile(workDir, word string) (remove func() error, err error) {
	path := filepath.Join(workDir, canaryFile)
	existed := true
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		existed = false
	} else if err != nil {
		return nil, fmt.Errorf("check for an existing %s: %w", canaryFile, err)
	}
	if err := os.WriteFile(path, []byte(word+"\n"), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", canaryFile, err)
	}
	return func() error {
		if existed {
			return nil
		}
		return os.Remove(path)
	}, nil
}

// stateDir holds the certificate authority and the session logs.
func stateDir() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "rehearsal"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find your home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "rehearsal"), nil
}

// openWindow shows the page as a standalone Edge window when running
// inside WSL. Anywhere else it does nothing: the address is already printed.
func openWindow(url string) error {
	version, err := os.ReadFile("/proc/version")
	if errors.Is(err, os.ErrNotExist) {
		return nil // not Linux, so not WSL
	}
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(string(version)), "microsoft") {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd.exe", "/c", "start", "msedge", "--app="+url)
	cmd.Dir = "/mnt/c" // cmd.exe complains when started from a Linux path
	return cmd.Run()
}
