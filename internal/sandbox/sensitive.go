package sandbox

import (
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"rehearsal/internal/events"
)

// A rule names paths whose contents decide what runs on this machine
// without anyone asking. A pattern that ends in a slash is a folder at any
// depth; one with a slash elsewhere is that path at any depth; a bare name
// is a file of that name anywhere.
type rule struct {
	reason   string
	patterns []string
}

var rules = []rule{
	{
		"It changes the agent's own settings or hooks, which decide what the agent may do without asking.",
		[]string{
			".claude/settings.json", ".claude/settings.local.json", ".claude/hooks/", ".mcp.json",
			".cursor/hooks.json", ".cursor/mcp.json", ".codex/config.toml", ".gemini/settings.json",
		},
	},
	{
		"It changes a shell start-up file, which runs every time a terminal opens.",
		[]string{".bashrc", ".bash_profile", ".bash_login", ".profile", ".zshrc", ".zprofile", ".zshenv", ".zlogin", ".config/fish/config.fish"},
	},
	{
		"It changes git's settings or hooks, which run commands when you use git.",
		[]string{".git/config", ".git/hooks/", ".gitconfig", ".husky/"},
	},
	{
		"It changes what your project's automated builds run.",
		[]string{
			".github/workflows/", ".gitlab-ci.yml", ".circleci/", "Jenkinsfile", "azure-pipelines.yml",
			".travis.yml", "bitbucket-pipelines.yml", ".buildkite/",
		},
	},
	{
		"It changes SSH keys or settings, which decide who can log in as you and where you log in.",
		[]string{".ssh/", "authorized_keys"},
	},
	{
		"It changes a program that this computer starts by itself in the background.",
		[]string{".config/systemd/", ".config/autostart/", "etc/systemd/", "Library/LaunchAgents/", "Library/LaunchDaemons/"},
	},
}

// Sensitive returns why a change to the path rel deserves a close look,
// or "" when no rule names it. rel uses forward slashes.
func Sensitive(rel string) string {
	for _, r := range rules {
		for _, pattern := range r.patterns {
			if matches(rel, pattern) {
				return r.reason
			}
		}
	}
	return ""
}

func matches(rel, pattern string) bool {
	switch {
	case strings.HasSuffix(pattern, "/"):
		dir := strings.TrimSuffix(pattern, "/")
		return rel == dir || strings.HasPrefix(rel, pattern) || strings.Contains(rel, "/"+pattern) || strings.HasSuffix(rel, "/"+dir)
	case strings.Contains(pattern, "/"):
		return rel == pattern || strings.HasSuffix(rel, "/"+pattern)
	}
	return path.Base(rel) == pattern
}

// Watch finds sensitive writes while the command is still running. It is
// not safe for use from more than one goroutine.
type Watch struct {
	box  *Box
	told map[string]bool
	// the agent's own settings in the folders it can write to for real,
	// with when each was last modified
	real map[string]time.Time
}

// NewWatch notes the state of the real settings files so that a later
// change to one can be told.
func (b *Box) NewWatch() *Watch {
	w := &Watch{box: b, told: map[string]bool{}, real: map[string]time.Time{}}
	for _, p := range w.realSettings() {
		w.real[p] = modified(p)
	}
	return w
}

// realSettings are the settings and hook files inside the writable
// folders. A write to one of these is not held back by the layer.
func (w *Watch) realSettings() []string {
	var out []string
	for _, dir := range w.box.opts.Writable {
		for _, name := range []string{"settings.json", "settings.local.json", "hooks"} {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}

// modified is when the file, or anything in the folder, last changed. The
// zero time stands for "not there".
func modified(p string) time.Time {
	var latest time.Time
	// a path that is not there yet has nothing to walk
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	return latest
}

// New returns the sensitive writes that have appeared since the last call.
func (w *Watch) New() ([]events.SensitiveWrite, error) {
	var found []events.SensitiveWrite
	err := filepath.WalkDir(w.box.upper, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // removed while we were looking
			}
			return err
		}
		rel, err := filepath.Rel(w.box.upper, p)
		if err != nil || rel == "." || d.IsDir() {
			return err
		}
		rel = filepath.ToSlash(rel)
		if reason := Sensitive(rel); reason != "" && !w.told[rel] {
			w.told[rel] = true
			found = append(found, events.SensitiveWrite{Path: rel, Reason: reason})
		}
		return nil
	})
	for _, p := range slices.Sorted(maps.Keys(w.real)) {
		if now := modified(p); !now.Equal(w.real[p]) {
			w.real[p] = now
			found = append(found, events.SensitiveWrite{Path: p, Reason: rules[0].reason, Real: true})
		}
	}
	return found, err
}
