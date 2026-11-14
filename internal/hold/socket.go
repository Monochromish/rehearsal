package hold

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"rehearsal/internal/events"
	"rehearsal/internal/ident"
	"rehearsal/internal/redact"
	"rehearsal/internal/review"
)

// Command is what a stand-in reports about the command it is holding.
// Nothing in it is masked yet.
type Command struct {
	Kind       events.ActionKind `json:"kind"`
	Program    string            `json:"program"`
	Args       []string          `json:"args"`
	WorkDir    string            `json:"work_dir"`
	Stdin      []byte            `json:"stdin,omitempty"`
	GitGlobals []string          `json:"git_globals,omitempty"`

	Destination     string            `json:"destination"`
	Subject         string            `json:"subject,omitempty"`
	Refs            []events.RefState `json:"refs,omitempty"`
	Files           []string          `json:"files,omitempty"`
	Commits         []string          `json:"commits,omitempty"`
	Force           bool              `json:"force,omitempty"`
	Deletes         bool              `json:"deletes,omitempty"`
	Publishes       bool              `json:"publishes,omitempty"`
	ChangesAccess   bool              `json:"changes_access,omitempty"`
	Minor           bool              `json:"minor,omitempty"`
	NeedsRealAnswer bool              `json:"needs_real_answer,omitempty"`
}

// The socket accepts reports from any local process, so only these
// programs can ever be run again when held actions are applied.
var programs = map[events.ActionKind]string{
	events.ActionGitPush:    "git",
	events.ActionNpmPublish: "npm",
	events.ActionGh:         "gh",
}

// Masker decides what may be shown and what the action carries.
type Masker struct {
	Matcher *ident.Matcher
	// planted canary values, which are ours to show
	Keep []string
}

// HoldCommand records a command a stand-in is holding.
func (h *Holder) HoldCommand(c Command, m Masker) (string, error) {
	if want, ok := programs[c.Kind]; !ok || c.Program != want {
		return "", fmt.Errorf("a stand-in reported %q as a %q action, which is not one it can hold", c.Program, c.Kind)
	}
	if c.WorkDir == "" {
		return "", errors.New("a stand-in reported a command with no working directory")
	}
	carried := strings.Join(slices.Concat(c.Args, c.Files, c.Commits), "\n") + "\n" + c.Destination + "\n" + string(c.Stdin)
	files, filesOmitted := cut(c.Files, maxFilesShown)
	commits, commitsOmitted := cut(c.Commits, maxCommitsShown)
	for i, subject := range commits {
		commits[i] = redact.Text(subject, m.Keep...)
	}
	display := events.ActionHeld{
		Files:          files,
		FilesOmitted:   filesOmitted,
		Commits:        commits,
		CommitsOmitted: commitsOmitted,
		// from the whole list, not the part that is shown
		SecretFiles:     review.SecretLooking(c.Files),
		Kind:            c.Kind,
		WorkDir:         c.WorkDir,
		Command:         redact.Args(append([]string{c.Program}, c.Args...), m.Keep...),
		StdinBytes:      len(c.Stdin),
		Refs:            c.Refs,
		Destination:     redact.Text(c.Destination, m.Keep...),
		Subject:         c.Subject,
		IdentifierIDs:   identifierIDs(m.Matcher.Find([]byte(carried))),
		Force:           c.Force,
		Deletes:         c.Deletes,
		Publishes:       c.Publishes,
		ChangesAccess:   c.ChangesAccess,
		Minor:           c.Minor,
		NeedsRealAnswer: c.NeedsRealAnswer,
	}
	return h.Hold(display, Payload{
		Program: c.Program, Args: c.Args, WorkDir: c.WorkDir, Stdin: c.Stdin, GitGlobals: c.GitGlobals,
	})
}

const (
	maxFilesShown   = 100
	maxCommitsShown = 30
)

// cut returns a copy of at most limit items and how many were left out.
func cut(items []string, limit int) ([]string, int) {
	if len(items) <= limit {
		return slices.Clone(items), 0
	}
	return slices.Clone(items[:limit]), len(items) - limit
}

func identifierIDs(matches []ident.Match) []string {
	ids := []string{}
	for _, m := range matches {
		if !slices.Contains(ids, m.IdentifierID) {
			ids = append(ids, m.IdentifierID)
		}
	}
	return ids
}

const maxReport = 16 << 20

// Socket is where stand-in commands report what they are holding.
type Socket struct {
	Path   string
	dir    string
	srv    *http.Server
	served chan error
}

// Listen opens the socket in a fresh private folder. The path is kept
// short because Unix socket paths are limited to about 100 characters.
func Listen(h *Holder, m Masker) (*Socket, error) {
	dir, err := os.MkdirTemp("", "rehearse-")
	if err != nil {
		return nil, fmt.Errorf("create a folder for the stand-in socket: %w", err)
	}
	s := &Socket{Path: dir + "/hold.sock", dir: dir, served: make(chan error, 1)}
	l, err := net.Listen("unix", s.Path)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open the stand-in socket: %w", err), os.RemoveAll(dir))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hold", func(w http.ResponseWriter, r *http.Request) {
		var c Command
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReport)).Decode(&c); err != nil {
			http.Error(w, "unreadable report: "+err.Error(), http.StatusBadRequest)
			return
		}
		id, err := h.HoldCommand(c, m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, id) // the stand-in treats a short reply as a failure
	})
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { s.served <- s.srv.Serve(l) }()
	return s, nil
}

func (s *Socket) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.srv.Shutdown(ctx)
	if served := <-s.served; !errors.Is(served, http.ErrServerClosed) {
		err = errors.Join(err, fmt.Errorf("stand-in socket stopped early: %w", served))
	}
	return errors.Join(err, os.RemoveAll(s.dir))
}

// Report is the stand-in's side: it tells the rehearse process at
// socketPath about a command and returns the new action's ID.
func Report(ctx context.Context, socketPath string, c Command) (string, error) {
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://rehearse/hold", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close() // read below; nothing was written to it
	reply, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", errors.New(strings.TrimSpace(string(reply)))
	}
	if len(reply) == 0 {
		return "", errors.New("rehearse did not confirm the hold")
	}
	return string(reply), nil
}
