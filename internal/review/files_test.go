package review

import (
	"strings"
	"testing"

	"rehearsal/internal/events"
)

func TestFileChangesAreRatedByPathNotByKind(t *testing.T) {
	hook := "It changes git's settings or hooks, which run commands when you use git."
	tests := []struct {
		name    string
		action  events.ActionHeld
		risk    events.Risk
		summary string
		concern string
	}{
		{
			"an ordinary edit",
			events.ActionHeld{Kind: events.ActionFile, File: &events.FileChange{Path: "src/a.go", Change: "changed", Files: 1}},
			events.RiskLow, "Change src/a.go in your project.", "only if you allow it",
		},
		{
			"a new folder",
			events.ActionHeld{Kind: events.ActionFile, File: &events.FileChange{Path: "docs", Change: "added", Dir: true, Files: 4}},
			events.RiskLow, "Add the folder docs (4 files) to your project.", "only if you allow it",
		},
		{
			"one deleted file",
			events.ActionHeld{Kind: events.ActionFile, File: &events.FileChange{Path: "old.txt", Change: "deleted", Files: 1}},
			events.RiskLow, "Delete old.txt from your project.", "keeps no copy",
		},
		{
			"git's own files",
			events.ActionHeld{Kind: events.ActionFile, File: &events.FileChange{Path: ".git", Change: "changed", Dir: true, Files: 9}},
			events.RiskLow, "Update git's own record of the project (9 files)", "a push you allow",
		},
		{
			"a sensitive path",
			events.ActionHeld{Kind: events.ActionFile, File: &events.FileChange{Path: ".git/hooks/pre-commit", Change: "added", Files: 1, Sensitive: hook}},
			events.RiskHigh, "Add .git/hooks/pre-commit to your project.", hook,
		},
		{
			"a name that looks like a secret",
			events.ActionHeld{Kind: events.ActionFile, SecretFiles: []string{".env"}, File: &events.FileChange{Path: ".env", Change: "added", Files: 1}},
			events.RiskHigh, "Add .env to your project.", "passwords or keys (.env)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Floor(tt.action); got != tt.risk {
				t.Errorf("Floor = %s, want %s", got, tt.risk)
			}
			v := ByRules(tt.action)
			if v.Risk != tt.risk || v.Source != events.ReviewByRules {
				t.Errorf("ByRules rated it %s by %s, want %s by the rules", v.Risk, v.Source, tt.risk)
			}
			if !strings.Contains(v.Summary, tt.summary) || !strings.Contains(v.Concern, tt.concern) {
				t.Errorf("ByRules = %q / %q, want %q / %q", v.Summary, v.Concern, tt.summary, tt.concern)
			}
		})
	}
}
