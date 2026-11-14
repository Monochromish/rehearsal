package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"rehearsal/internal/events"
)

// OfferAfter is how many earlier sessions an action of one kind to one
// place must have been allowed in before a rule for it is offered.
const OfferAfter = 3

const approvalsFile = "approvals.json"

// Approvals remembers, across sessions, how often the user has allowed
// each kind of action to each destination from each project. It holds
// kinds and destinations only, never what was sent.
type Approvals struct {
	path string
	// project, kind and destination -> sessions it was allowed in
	Sessions map[string]int `json:"sessions"`
}

// Allowed names one kind of action to one destination.
type Allowed struct {
	Kind        events.ActionKind
	Destination string
}

// LoadApprovals reads the counts kept in stateDir.
func LoadApprovals(stateDir string) (*Approvals, error) {
	a := &Approvals{path: filepath.Join(stateDir, approvalsFile), Sessions: map[string]int{}}
	data, err := os.ReadFile(a.path)
	if errors.Is(err, fs.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the record of what you have allowed before: %w", err)
	}
	if err := json.Unmarshal(data, a); err != nil {
		return nil, fmt.Errorf("%s is not readable: %w", a.path, err)
	}
	if a.Sessions == nil {
		a.Sessions = map[string]int{}
	}
	return a, nil
}

func key(project string, x Allowed) string {
	return project + "\n" + string(x.Kind) + "\n" + x.Destination
}

// Times is how many sessions x was allowed in from project.
func (a *Approvals) Times(project string, x Allowed) int {
	return a.Sessions[key(project, x)]
}

// Record adds one session to the count of each of allowed, which must not
// contain the same entry twice, and saves the counts.
func (a *Approvals) Record(project string, allowed []Allowed) error {
	if len(allowed) == 0 {
		return nil
	}
	for _, x := range allowed {
		a.Sessions[key(project, x)]++
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the record of what you have allowed: %w", err)
	}
	if err := os.WriteFile(a.path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("save the record of what you have allowed: %w", err)
	}
	return nil
}

// Covers reports whether some rule in either file already speaks about
// actions like x, in which case there is nothing to offer.
func (p *Policy) Covers(x Allowed) bool {
	f := Facts{Kind: x.Kind, Destination: x.Destination}
	_, org := first(p.Org, f)
	_, project := first(p.Project, f)
	return org || project
}

// ProjectSum identifies the contents of the project's policy file, so a
// change to it during a session can be told. It is empty when there is no
// file or it cannot be read.
func ProjectSum(projectDir string) string {
	data, err := os.ReadFile(filepath.Join(projectDir, ProjectFile))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
