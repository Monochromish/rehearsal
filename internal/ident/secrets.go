package ident

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"rehearsal/internal/events"
)

// Secret is a watched secret's value. It lives in memory for the length
// of the session and is never written anywhere.
type Secret struct {
	ID    string
	Value string
}

// The shortest .env value worth watching. Shorter ones ("true", a port
// number) would match by accident.
const minSecretLen = 8

// Names that say a .env entry holds a credential.
var secretName = regexp.MustCompile(`(?i)(KEY|TOKEN|SECRET|PASSWORD|PASSWD|PWD|CREDENTIAL|AUTH|PRIVATE|DSN|DATABASE_URL|CONNECTION)`)

// EnvSecrets reads the .env and .env.* files in dir and returns the
// entries that hold credentials: an Identifier for each, carrying a label
// and a salted hash but no value, and the values themselves for the
// matcher. An entry counts when its name says so or its value looks random.
func EnvSecrets(dir string) ([]events.Identifier, []Secret, error) {
	names, err := filepath.Glob(filepath.Join(dir, ".env*"))
	if err != nil {
		return nil, nil, err
	}
	salt := rand.Text()
	var ids []events.Identifier
	var secrets []Secret
	var failed []error
	for _, path := range names {
		base := filepath.Base(path)
		if base != ".env" && !strings.HasPrefix(base, ".env.") {
			continue
		}
		entries, err := readEnv(path)
		if err != nil {
			failed = append(failed, err)
			continue
		}
		for _, e := range entries {
			if len(e.value) < minSecretLen || !(secretName.MatchString(e.name) || looksRandom(e.value)) {
				continue
			}
			if slices.ContainsFunc(secrets, func(s Secret) bool { return s.Value == e.value }) {
				continue
			}
			id := fmt.Sprintf("env-%d", len(secrets)+1)
			sum := sha256.Sum256([]byte(salt + e.value))
			ids = append(ids, events.Identifier{
				ID: id, Kind: events.KindSecret,
				Label: fmt.Sprintf("Value of %s from %s", e.name, base),
				Name:  e.name,
				Hash:  hex.EncodeToString(sum[:]),
			})
			secrets = append(secrets, Secret{ID: id, Value: e.value})
		}
	}
	return ids, secrets, errors.Join(failed...)
}

type envEntry struct{ name, value string }

func readEnv(path string) (entries []envEntry, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	defer f.Close() // read-only
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		line = strings.TrimPrefix(line, "export ")
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		} else if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i]) // an unquoted value ends at a comment
		}
		entries = append(entries, envEntry{strings.TrimSpace(name), value})
	}
	if err := lines.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return entries, nil
}

// looksRandom reports whether a value has the look of a generated
// credential: long, and mixing at least three kinds of character.
func looksRandom(value string) bool {
	if len(value) < 20 || strings.ContainsAny(value, " \t") {
		return false
	}
	var lower, upper, digit, other int
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			lower = 1
		case r >= 'A' && r <= 'Z':
			upper = 1
		case r >= '0' && r <= '9':
			digit = 1
		default:
			other = 1
		}
	}
	return lower+upper+digit+other >= 3
}

// A format is a well-known shape of credential. Anything of that shape is
// treated as a secret wherever it came from.
type format struct {
	id    string
	label string
	re    *regexp.Regexp
}

var formats = []format{
	{"format-aws-key", "Something that looks like an AWS access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"format-github-token", "Something that looks like a GitHub token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})\b`)},
	{"format-stripe-key", "Something that looks like a Stripe key", regexp.MustCompile(`\b[sr]k_(?:live|test)_[A-Za-z0-9]{16,}\b`)},
	{"format-anthropic-key", "Something that looks like an Anthropic API key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	{"format-openai-key", "Something that looks like an OpenAI API key", regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}`)},
	{"format-slack-token", "Something that looks like a Slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"format-private-key", "A private key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"format-jwt", "Something that looks like a signed web token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
}

// FormatIdentifiers are the watched entries for the well-known formats.
// They carry no value: what is watched for is a shape.
func FormatIdentifiers() []events.Identifier {
	ids := make([]events.Identifier, len(formats))
	for i, f := range formats {
		ids[i] = events.Identifier{ID: f.id, Kind: events.KindSecret, Label: f.label}
	}
	return ids
}

// FindFormats returns the IDs of the formats present in text. A match that
// is one of skip (our own planted values) does not count.
func FindFormats(text []byte, skip ...string) []string {
	var found []string
	for _, f := range formats {
		for _, match := range f.re.FindAll(text, -1) {
			m := string(match)
			// the OpenAI shape also fits Anthropic keys, which have their own entry
			if f.id == "format-openai-key" && strings.HasPrefix(m, "sk-ant-") {
				continue
			}
			if slices.ContainsFunc(skip, func(s string) bool { return s != "" && strings.Contains(m, s) }) {
				continue
			}
			found = append(found, f.id)
			break
		}
	}
	return found
}
