package main

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"time"

	"rehearsal/internal/events"
)

// the most commits listed for one session
const maxCommitsListed = 500

// realGit runs the real git in dir. This process's PATH never has the
// stand-ins on it, only the wrapped command's does.
func realGit(dir, stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	return string(out), err
}

// refsNow returns what every ref of the repository in dir points at. ok is
// false when dir is not in a git repository or git cannot be run.
func refsNow(dir string) (tips []string, ok bool) {
	if _, err := realGit(dir, "", "rev-parse", "--git-dir"); err != nil {
		return nil, false
	}
	out, err := realGit(dir, "", "for-each-ref", "--format=%(objecttype) %(objectname)")
	if err != nil {
		return nil, false
	}
	for line := range strings.Lines(out) {
		kind, id, found := strings.Cut(strings.TrimSpace(line), " ")
		if found && (kind == "commit" || kind == "tag") {
			tips = append(tips, id)
		}
	}
	// a detached HEAD is on no ref
	if head, err := realGit(dir, "", "rev-parse", "--quiet", "--verify", "HEAD^{commit}"); err == nil {
		tips = append(tips, strings.TrimSpace(head))
	}
	slices.Sort(tips)
	return slices.Compact(tips), true
}

// commitsSince lists the commits reachable from a local branch or HEAD now
// that none of the earlier tips could reach.
func commitsSince(dir string, before []string, known bool) events.CommitsCreated {
	made := events.CommitsCreated{Commits: []string{}}
	if !known {
		return made
	}
	args := []string{"rev-list", "--branches", "--stdin"}
	if _, err := realGit(dir, "", "rev-parse", "--quiet", "--verify", "HEAD^{commit}"); err == nil {
		args = append(args, "HEAD")
	}
	var exclude strings.Builder
	for _, tip := range before {
		exclude.WriteString("^" + tip + "\n")
	}
	out, err := realGit(dir, exclude.String(), args...)
	if err != nil {
		return made
	}
	made.Known = true
	for line := range strings.Lines(out) {
		if id := strings.TrimSpace(line); id != "" {
			made.Commits = append(made.Commits, id)
		}
	}
	if extra := len(made.Commits) - maxCommitsListed; extra > 0 {
		made.Commits, made.Omitted = made.Commits[:maxCommitsListed], extra
	}
	return made
}
