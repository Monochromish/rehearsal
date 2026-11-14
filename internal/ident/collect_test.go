package ident

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeAccountEmail(t *testing.T) {
	write := func(t *testing.T, content string) {
		t.Helper()
		dir := t.TempDir()
		t.Setenv("CLAUDE_CONFIG_DIR", dir)
		if content == "" {
			return // no settings file at all
		}
		if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name, content, want string
		wantErr             bool
	}{
		{"signed in", `{"numStartups": 4, "oauthAccount": {"accountUuid": "u-1", "emailAddress": "ada@example.org"}}`, "ada@example.org", false},
		{"no settings file", "", "", false},
		{"not signed in", `{"numStartups": 4}`, "", false},
		{"an account with no email", `{"oauthAccount": {"accountUuid": "u-1"}}`, "", false},
		{"a file that is not JSON", `{"oauthAccount": {"emailAddress": "ada@example.org"`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			write(t, tt.content)
			got, err := claudeAccountEmail()
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Errorf("claudeAccountEmail() = %q, %v; want %q, error %v", got, err, tt.want, tt.wantErr)
			}
			// the error is shown to the user, and must not quote the file
			if err != nil && strings.Contains(err.Error(), "ada@example.org") {
				t.Errorf("the error quotes the settings file: %v", err)
			}
		})
	}
}

func TestCollectRealWatchesTheClaudeAccountEmailWhenThereIsOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	labelled := func() string {
		ids, _ := CollectReal(t.TempDir()) // the error lists what this machine lacks, such as a git email
		for _, id := range ids {
			if id.ID == IDClaudeEmail {
				return string(id.Kind) + " | " + id.Label + " | " + id.Value
			}
		}
		return ""
	}
	if got := labelled(); got != "" {
		t.Errorf("with no settings file, the account email is watched as %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"oauthAccount": {"emailAddress": "ada@example.org"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := labelled(), "real | Your Claude account email | ada@example.org"; got != want {
		t.Errorf("the account email is watched as %q, want %q", got, want)
	}
}
