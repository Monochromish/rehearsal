package ident

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"rehearsal/internal/events"
)

func TestFindFormats(t *testing.T) {
	const canary = "sk-rehearsal-canary-abcdefghijklmnopqrstuvwxyz"
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"AWS access key", "key AKIAIOSFODNN7EXAMPLE end", []string{"format-aws-key"}},
		{"AWS temporary key", "ASIAY34FZKBOKMUTVV7A", []string{"format-aws-key"}},
		{"GitHub personal token", "ghp_0123456789abcdefghijABCDEFGHIJ012345", []string{"format-github-token"}},
		{"GitHub fine-grained token", "github_pat_11ABCDEFG0123456789_abcdefghij", []string{"format-github-token"}},
		{"Stripe live key", "sk_live_0123456789abcdefABCDEF01", []string{"format-stripe-key"}},
		{"Stripe restricted test key", "rk_test_0123456789abcdefABCDEF01", []string{"format-stripe-key"}},
		{"Anthropic key", "sk-ant-api03-0123456789abcdefghijklmnop", []string{"format-anthropic-key"}},
		{"OpenAI key", "sk-proj-0123456789abcdefghijklmnop", []string{"format-openai-key"}},
		{"OpenAI key, old style", "sk-0123456789abcdefghijklmnopqrstuv", []string{"format-openai-key"}},
		{"Slack bot token", "xoxb-123456789012-abcdefghijkl", []string{"format-slack-token"}},
		{"private key", "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNz", []string{"format-private-key"}},
		{"RSA private key", "-----BEGIN RSA PRIVATE KEY-----", []string{"format-private-key"}},
		{"signed web token", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N", []string{"format-jwt"}},
		{"two kinds at once", "AKIAIOSFODNN7EXAMPLE and xoxb-123456789012-abcdefghijkl", []string{"format-aws-key", "format-slack-token"}},
		{"the same kind twice counts once", "ghp_0123456789abcdefghijABCDEFGHIJ012345 ghp_9876543210abcdefghijABCDEFGHIJ012345", []string{"format-github-token"}},

		{"our own canary key is not a secret", "key=" + canary, nil},
		{"canary beside a real key", canary + " sk-proj-0123456789abcdefghijklmnop", []string{"format-openai-key"}},
		{"ordinary text", "The quick brown fox pushes to main.", nil},
		{"a public key", "-----BEGIN PUBLIC KEY-----", nil},
		{"too short to be an AWS key", "AKIA1234", nil},
		{"a word starting with sk-", "sk-short", nil},
		{"a certificate", "-----BEGIN CERTIFICATE-----", nil},
		{"lower-case aws-like", "akiaiosfodnn7example", nil},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindFormats([]byte(tt.text), canary)
			if !slices.Equal(got, tt.want) {
				t.Errorf("FindFormats(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestFormatIdentifiersCarryNoValue(t *testing.T) {
	ids := FormatIdentifiers()
	if len(ids) != 8 {
		t.Fatalf("got %d format entries, want 8", len(ids))
	}
	for _, id := range ids {
		if id.Kind != events.KindSecret || id.Value != "" || id.Label == "" {
			t.Errorf("%s: kind %q value %q label %q; want a labelled secret with no value", id.ID, id.Kind, id.Value, id.Label)
		}
	}
}

func TestEnvSecrets(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".env", strings.Join([]string{
		"# settings",
		"PORT=3000",
		"NODE_ENV=production",
		"DEBUG=true",
		"STRIPE_KEY=live-key-value-0123",
		`export DB_PASSWORD="correct horse battery"`,
		"SESSION_SECRET='single-quoted-secret' ",
		"API_TOKEN=tok-with-comment-123 # rotate monthly",
		"SHORT_KEY=abc",
		"BUILD_ID=Zk3vQ9xT2mLp8Wb5Rn7Yc1Hd",
		"GREETING=hello there everyone and welcome",
		"",
	}, "\n"))
	write(".env.local", "STRIPE_KEY=live-key-value-0123\nDATABASE_URL=postgres://app:pw@db.internal/shop\n")
	write(".environment", "OTHER_SECRET=must-not-be-read-0123\n")
	write("notes.txt", "API_KEY=not-an-env-file-0123\n")

	ids, secrets, err := EnvSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	var labels, values []string
	for i, id := range ids {
		labels = append(labels, id.Label)
		values = append(values, secrets[i].Value)
		if id.ID != secrets[i].ID {
			t.Errorf("identifier %d is %q but its secret is %q", i, id.ID, secrets[i].ID)
		}
		if id.Kind != events.KindSecret || id.Value != "" {
			t.Errorf("%s: kind %q, value %q; a secret's value must never be in its identifier", id.Label, id.Kind, id.Value)
		}
		if _, err := hex.DecodeString(id.Hash); err != nil || len(id.Hash) != 64 {
			t.Errorf("%s: hash %q is not a SHA-256 in hex", id.Label, id.Hash)
		}
		if strings.Contains(id.Label, secrets[i].Value) || strings.Contains(id.Hash, secrets[i].Value) {
			t.Errorf("%s: the value leaks into the label or hash", id.Label)
		}
	}
	slices.Sort(labels)
	slices.Sort(values)
	wantLabels := []string{
		"Value of API_TOKEN from .env",
		"Value of BUILD_ID from .env",
		"Value of DATABASE_URL from .env.local",
		"Value of DB_PASSWORD from .env",
		"Value of SESSION_SECRET from .env",
		"Value of STRIPE_KEY from .env",
	}
	wantValues := []string{
		"Zk3vQ9xT2mLp8Wb5Rn7Yc1Hd",
		"correct horse battery",
		"live-key-value-0123",
		"postgres://app:pw@db.internal/shop",
		"single-quoted-secret",
		"tok-with-comment-123",
	}
	if !slices.Equal(labels, wantLabels) {
		t.Errorf("watched entries:\n got %q\nwant %q", labels, wantLabels)
	}
	if !slices.Equal(values, wantValues) {
		t.Errorf("watched values:\n got %q\nwant %q", values, wantValues)
	}

	// a second read salts differently, so hashes cannot be compared across sessions
	again, _, err := EnvSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Hash == ids[0].Hash {
		t.Error("two sessions produced the same hash for a secret: the salt is not per session")
	}
}

func TestEnvSecretsWithNoFiles(t *testing.T) {
	ids, secrets, err := EnvSecrets(t.TempDir())
	if err != nil || len(ids) != 0 || len(secrets) != 0 {
		t.Errorf("EnvSecrets in an empty folder = %v, %v, %v; want nothing and no error", ids, secrets, err)
	}
}

func TestMatcherFindsSecretsAndHex(t *testing.T) {
	ids := []events.Identifier{{ID: "word", Kind: events.KindCanary, Value: "canary-word-3f9a1c0b7e2d"}, {ID: "env-1", Kind: events.KindSecret}}
	m := NewMatcher(ids, Secret{ID: "env-1", Value: "live-key-value-0123"})
	hexOf := func(s string) string { return hex.EncodeToString([]byte(s)) }
	tests := []struct {
		name string
		text string
		id   string
		form events.Form
	}{
		{"secret in plain", "key=live-key-value-0123&x=1", "env-1", events.FormPlain},
		{"secret in hex", "blob=" + hexOf("live-key-value-0123"), "env-1", events.FormHex},
		{"secret in upper-case hex", strings.ToUpper(hexOf("live-key-value-0123")), "env-1", events.FormHex},
		{"canary in hex inside a longer hex string", "00ff" + hexOf("canary-word-3f9a1c0b7e2d") + "00ff", "word", events.FormHex},
		{"hex of something else", hexOf("some other unrelated value"), "", ""},
		{"neither", "nothing to see", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.Find([]byte(tt.text))
			if tt.id == "" {
				if len(got) != 0 {
					t.Errorf("Find = %v, want nothing", got)
				}
				return
			}
			if len(got) != 1 || got[0].IdentifierID != tt.id || got[0].Form != tt.form {
				t.Errorf("Find = %v, want %s as %s", got, tt.id, tt.form)
			}
		})
	}
}
