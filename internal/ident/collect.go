package ident

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"rehearsal/internal/events"
)

const (
	IDGitEmail     = "git-email"
	IDGitName      = "git-name"
	IDUsername     = "os-username"
	IDHostname     = "hostname"
	IDClaudeEmail  = "claude-email"
	IDCanaryEmail  = "canary-email"
	IDCanaryAPIKey = "canary-api-key"
	IDCanaryWord   = "canary-word"
)

// CollectReal returns the identifiers it could read. The error lists the
// ones it could not, and the rest are still usable.
func CollectReal(workDir string) ([]events.Identifier, error) {
	var out []events.Identifier
	var failed []error
	add := func(id, label, value string, err error) {
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", strings.ToLower(label), err))
			return
		}
		value = strings.TrimSpace(value)
		if len(value) >= MinLen {
			out = append(out, events.Identifier{ID: id, Kind: events.KindReal, Label: label, Value: value})
		}
	}

	email, err := gitConfig(workDir, "user.email")
	add(IDGitEmail, "Your git email address", email, err)
	name, err := gitConfig(workDir, "user.name")
	add(IDGitName, "Your git name", name, err)

	username := ""
	u, err := user.Current()
	if err == nil {
		username = u.Username
	}
	add(IDUsername, "Your computer username", username, err)
	host, err := os.Hostname()
	add(IDHostname, "Your computer's name", host, err)

	account, err := claudeAccountEmail()
	add(IDClaudeEmail, "Your Claude account email", account, err)

	return out, errors.Join(failed...)
}

// claudeAccountEmail is the email of the account Claude Code is signed in
// to, which it keeps in its own settings file: .claude.json in the home
// folder, or in the folder CLAUDE_CONFIG_DIR names. It is "" with no error
// when the file or the field is not there, which is the case for anyone
// who doesn't use Claude Code or isn't signed in.
func claudeAccountEmail() (string, error) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil // no home folder, so no settings file in it
		}
		dir = home
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Claude Code's settings: %w", err)
	}
	var settings struct {
		Account struct {
			Email string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		// the message may quote the file, so only the fact is passed on
		return "", errors.New("the settings file of Claude Code is not readable as JSON")
	}
	return settings.Account.Email, nil
}

func gitConfig(workDir, key string) (string, error) {
	cmd := exec.Command("git", "config", "--get", key)
	cmd.Dir = workDir
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil // git's way of saying the key is not set
	}
	if err != nil {
		return "", fmt.Errorf("git config %s: %w", key, err)
	}
	return string(out), nil
}

// NewCanaries makes this session's planted values. None is valid anywhere.
func NewCanaries() []events.Identifier {
	random := func(n int) string { return strings.ToLower(rand.Text()[:n]) }
	return []events.Identifier{
		{ID: IDCanaryEmail, Kind: events.KindCanary, Label: "Planted fake email address", Value: "canary-" + random(10) + "@rehearsal-canary.example"},
		{ID: IDCanaryAPIKey, Kind: events.KindCanary, Label: "Planted fake API key", Value: "sk-rehearsal-canary-" + random(26)},
		{ID: IDCanaryWord, Kind: events.KindCanary, Label: "Planted secret word (CANARY.txt)", Value: "canary-word-" + random(12)},
	}
}

func Value(ids []events.Identifier, id string) string {
	for _, i := range ids {
		if i.ID == id {
			return i.Value
		}
	}
	return ""
}
