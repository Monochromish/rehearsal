// Package apply performs held actions for real once the user has
// approved them.
package apply

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"slices"
	"strings"

	"rehearsal/internal/events"
	"rehearsal/internal/hold"
	"rehearsal/internal/redact"
)

const maxOutput = 600

type Applier struct {
	Log   *events.Log
	Store *hold.Store
	// planted canary values, which may be shown in a command's output
	Keep []string
	// makes an approved file change in the real project; nil when the
	// session had no sandbox
	Files FileApplier
	// Dry marks allowed actions as performed without performing them. It is
	// set when a recording is being replayed: nothing in a replay is real.
	Dry bool
}

// FileApplier copies one change out of the sandbox's layer.
type FileApplier interface {
	Apply(events.FileChange) error
}

// outcome of one action: Reason is empty when it was applied.
type outcome struct {
	detail string
	reason string
}

// Run performs the approved actions in the order they were held and stops
// at the first one that fails. It returns how many were applied and whether
// one failed. The error is for our own failures: an outcome that could not
// be written to the log.
func (a *Applier) Run(ctx context.Context, approved []events.ActionHeld) (applied int, failed bool, err error) {
	// no proxy and no redirects: the request goes where the agent sent it
	client := &http.Client{
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	for _, act := range approved {
		var out outcome
		switch {
		case a.Dry:
			out = outcome{detail: "This is a replay, so it was not performed."}
		case act.Kind == events.ActionFile:
			out = a.file(act)
		case act.Kind == events.ActionHTTP:
			out = a.send(ctx, client, act)
		default:
			out = a.run(ctx, act)
		}
		if out.reason != "" {
			_, err := a.Log.Append(events.TypeActionFailed, events.ActionFailed{ActionID: act.ID, Reason: out.reason, Detail: out.detail})
			return applied, true, err
		}
		if _, err := a.Log.Append(events.TypeActionApplied, events.ActionApplied{ActionID: act.ID, Detail: out.detail}); err != nil {
			return applied, false, err
		}
		applied++
	}
	return applied, false, nil
}

func (a *Applier) file(act events.ActionHeld) outcome {
	if a.Files == nil || act.File == nil {
		return outcome{reason: "this file change has no sandbox to be copied from"}
	}
	if err := a.Files.Apply(*act.File); err != nil {
		return outcome{reason: err.Error()}
	}
	return outcome{}
}

// run performs a held command again with the real program, in the folder
// and with the arguments and input it was first given.
func (a *Applier) run(ctx context.Context, act events.ActionHeld) outcome {
	p, err := a.Store.Load(act.ID)
	if err != nil {
		return outcome{reason: err.Error()}
	}
	program, err := exec.LookPath(p.Program)
	if err != nil {
		return outcome{reason: fmt.Sprintf("%s is not installed, so this could not be run", p.Program)}
	}
	if act.Kind == events.ActionGitPush {
		if reason := movedRef(ctx, program, p, act.Refs); reason != "" {
			return outcome{reason: reason}
		}
	}
	cmd := exec.CommandContext(ctx, program, p.Args...)
	cmd.Dir = p.WorkDir
	cmd.Stdin = bytes.NewReader(p.Stdin)
	output, err := cmd.CombinedOutput()
	detail := a.tail(output)
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return outcome{reason: fmt.Sprintf("%s exited with status %d", p.Program, exit.ExitCode()), detail: detail}
	case err != nil:
		return outcome{reason: fmt.Sprintf("%s could not be run: %v", p.Program, err), detail: detail}
	}
	return outcome{detail: detail}
}

// movedRef returns why a push must not be sent: a branch it would push no
// longer points at the commit it pointed at when the push was held, so the
// user would be approving something other than what they were shown.
func movedRef(ctx context.Context, git string, p hold.Payload, refs []events.RefState) string {
	for _, ref := range refs {
		args := slices.Concat(p.GitGlobals, []string{"rev-parse", "--verify", "-q", ref.Ref})
		cmd := exec.CommandContext(ctx, git, args...)
		cmd.Dir = p.WorkDir
		out, err := cmd.Output()
		now := strings.TrimSpace(string(out))
		if err != nil || now == "" {
			return fmt.Sprintf("%s no longer exists, so the push was not sent (it pointed at %s when the push was held)", ref.Ref, short(ref.Commit))
		}
		if now != ref.Commit {
			return fmt.Sprintf("%s has moved since the push was held, so it was not sent: it pointed at %s then and points at %s now", ref.Ref, short(ref.Commit), short(now))
		}
	}
	return ""
}

// send performs a held request as it was stored and, once it has gone,
// writes it to the ledger like any other request.
func (a *Applier) send(ctx context.Context, client *http.Client, act events.ActionHeld) outcome {
	p, err := a.Store.Load(act.ID)
	if err != nil {
		return outcome{reason: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, p.Method, p.URL, bytes.NewReader(p.Body))
	if err != nil {
		return outcome{reason: fmt.Sprintf("the stored request could not be rebuilt: %v", err)}
	}
	for name, values := range p.Header {
		// these describe the agent's connection to the proxy, not the request
		if slices.Contains([]string{"Proxy-Connection", "Proxy-Authorization", "Connection", "Keep-Alive", "Content-Length"}, name) {
			continue
		}
		req.Header[name] = values
	}
	res, err := client.Do(req)
	if err != nil {
		return outcome{reason: fmt.Sprintf("%s could not be reached: %v", act.Host, err)}
	}
	// the reply is not kept; draining it lets the connection be reused
	_, drainErr := io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if err := errors.Join(drainErr, res.Body.Close()); err != nil {
		return outcome{reason: fmt.Sprintf("the reply from %s could not be read: %v", act.Host, err)}
	}

	requestID := "applied-" + act.ID
	type entry struct {
		t events.Type
		v any
	}
	sent := []entry{{events.TypeRequest, events.Request{
		ID: requestID, Host: act.Host, Method: act.Method, Path: act.Path,
		RequestBytes: act.RequestBytes, Category: events.CategoryOther, BodyChecked: true,
	}}}
	for _, s := range p.Seen {
		s.RequestID = requestID
		sent = append(sent, entry{events.TypeIdentifierSeen, s})
	}
	sent = append(sent, entry{events.TypeResponse, events.Response{RequestID: requestID, Status: res.StatusCode}})
	for _, e := range sent {
		if _, err := a.Log.Append(e.t, e.v); err != nil {
			return outcome{reason: fmt.Sprintf("the request was sent, but the ledger could not be updated: %v", err)}
		}
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return outcome{reason: fmt.Sprintf("%s answered %s", act.Host, res.Status)}
	}
	return outcome{detail: fmt.Sprintf("%s answered %s", act.Host, res.Status)}
}

func (a *Applier) tail(output []byte) string {
	text := strings.TrimSpace(string(output))
	if runes := []rune(text); len(runes) > maxOutput {
		text = "... " + string(runes[len(runes)-maxOutput:])
	}
	return redact.Text(text, a.Keep...)
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
