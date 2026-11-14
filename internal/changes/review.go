package changes

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"rehearsal/internal/events"
)

// File is the name of a session's diff in its folder.
const File = "changes.diff"

// Review is a session's changes as a person is shown them: sorted into
// parts, most in need of reading first.
type Review struct {
	// false until the agent has exited and its changes have been recorded
	Captured bool `json:"captured"`
	// false when the session did not run inside a git repository, in which
	// case there is nothing else to show
	InRepo bool `json:"in_repo"`
	// why the changes could not be recorded, when they could not
	Problem string `json:"problem,omitempty"`
	// the diff was too long to keep whole
	Truncated bool   `json:"truncated,omitempty"`
	Parts     []Part `json:"parts"`
}

// Load reads a session's changes from its folder. captured is what the
// session's log says was recorded, or nil when it says nothing yet.
func Load(sessionDir string, captured *events.ChangesCaptured) (Review, error) {
	r := Review{Parts: []Part{}}
	if captured == nil {
		return r, nil
	}
	r.Captured, r.InRepo, r.Problem, r.Truncated = true, captured.InRepo, captured.Problem, captured.Truncated
	if !r.InRepo || r.Problem != "" {
		return r, nil
	}
	patch, err := os.ReadFile(filepath.Join(sessionDir, File))
	if errors.Is(err, fs.ErrNotExist) {
		r.Problem = "the session's diff is no longer in its folder"
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if parts := Sort(Parse(string(patch))); parts != nil {
		r.Parts = parts
	}
	return r, nil
}

// Part returns the part with the given ID.
func (r Review) Part(id string) (Part, bool) {
	for _, p := range r.Parts {
		if p.ID == id {
			return p, true
		}
	}
	return Part{}, false
}
