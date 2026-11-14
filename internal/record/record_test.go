package record

import (
	"bytes"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rehearsal/internal/events"
)

func newKey(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "rehearsal")
	key, err := LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := OwnPublicKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	return key, pub, dir
}

func TestKeyIsMadeOnceAndKeptPrivate(t *testing.T) {
	key, pub, dir := newKey(t)
	if !bytes.Equal(key.Public().(ed25519.PublicKey), pub) {
		t.Fatal("the public key file is not the public half of the signing key")
	}
	info, err := os.Stat(filepath.Join(dir, privateFile))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("signing key has mode %04o, want 0600", mode)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("key folder: %v, mode %v; want 0700", err, info.Mode().Perm())
	}

	again, err := LoadOrCreateKey(dir)
	if err != nil || !key.Equal(again) {
		t.Errorf("second load gave a different key (%v): a session must not replace the key", err)
	}

	if err := os.Chmod(filepath.Join(dir, privateFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(dir); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("a key others can read was accepted (%v); want it refused with the fix", err)
	}
}

func TestParsePublicKey(t *testing.T) {
	_, pub, dir := newKey(t)
	for name, given := range map[string]string{
		"a file":      filepath.Join(dir, publicFile),
		"base64 text": KeyText(pub),
	} {
		got, err := ParsePublicKey(given)
		if err != nil || !pub.Equal(got) {
			t.Errorf("%s: ParsePublicKey = %v, %v; want the key", name, got, err)
		}
	}
	for _, bad := range []string{"", "not a key", "AAAA"} {
		if _, err := ParsePublicKey(bad); err == nil {
			t.Errorf("ParsePublicKey(%q) succeeded", bad)
		}
	}
}

// signedSession writes a log of n entries in a session folder, signs its
// end and closes it.
func signedSession(t *testing.T, key ed25519.PrivateKey, n int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "20260101-000000-abcdef")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	log, err := events.Open(filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if _, err := log.Append(events.TypeRequest, events.Request{ID: "r", Host: "a.test", RequestBytes: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	entries, head := log.Seal()
	if err := WriteCheckpoint(dir, SignCheckpoint(key, filepath.Base(dir), entries, head, time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func verify(t *testing.T, dir string, trusted ed25519.PublicKey) Verdict {
	t.Helper()
	log, err := events.OpenExisting(filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := Verify(log, dir, trusted)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	return v
}

func tamper(t *testing.T, dir string, statements ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSignedSessionVerifies(t *testing.T) {
	key, pub, _ := newKey(t)
	dir := signedSession(t, key, 5)
	v := verify(t, dir, pub)
	if !v.OK() || v.Entries != 5 || v.SignedAt.IsZero() {
		t.Fatalf("Verify = %+v, want a verified record of 5 entries", v)
	}
}

func TestVerifyFindsWhatTheChainAloneCannot(t *testing.T) {
	key, pub, _ := newKey(t)
	_, otherPub, _ := newKey(t)

	t.Run("the last entries removed", func(t *testing.T) {
		dir := signedSession(t, key, 5)
		tamper(t, dir, `DELETE FROM events WHERE seq >= 4`)
		v := verify(t, dir, pub)
		if v.OK() || v.BadEntry != 4 || !strings.Contains(v.Problem, "entry 4 is missing") {
			t.Errorf("Verify = %+v, want it to say entry 4 is missing", v)
		}
	})

	t.Run("an entry edited", func(t *testing.T) {
		dir := signedSession(t, key, 5)
		tamper(t, dir, `UPDATE events SET data = '{}' WHERE seq = 2`)
		v := verify(t, dir, pub)
		if v.OK() || v.BadEntry != 2 {
			t.Errorf("Verify = %+v, want it to stop at entry 2", v)
		}
	})

	t.Run("an entry added after signing", func(t *testing.T) {
		dir := signedSession(t, key, 5)
		log, err := events.Open(filepath.Join(dir, "events.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := log.Append(events.TypeFirstInput, events.FirstInput{}); err != nil {
			t.Fatal(err)
		}
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
		v := verify(t, dir, pub)
		if v.OK() || v.BadEntry != 6 || !strings.Contains(v.Problem, "added after the record was signed") {
			t.Errorf("Verify = %+v, want it to say entry 6 was added after signing", v)
		}
	})

	t.Run("the whole log rewritten with a valid chain", func(t *testing.T) {
		dir := signedSession(t, key, 5)
		// same length, written at different times, so a different final hash
		other := signedSession(t, key, 5)
		checkpoint, err := os.ReadFile(filepath.Join(dir, checkpointFile))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(other, checkpointFile), checkpoint, 0o600); err != nil {
			t.Fatal(err)
		}
		v := verify(t, other, pub)
		if v.OK() || !strings.Contains(v.Problem, "not the one that was signed") {
			t.Errorf("Verify = %+v, want it to say the log is not the one signed", v)
		}
	})

	t.Run("signed by someone else", func(t *testing.T) {
		dir := signedSession(t, key, 5)
		v := verify(t, dir, otherPub)
		if v.OK() || !strings.Contains(v.Problem, "different key") {
			t.Errorf("Verify = %+v, want it to refuse another key's signature", v)
		}
	})

	t.Run("the checkpoint altered", func(t *testing.T) {
		dir := signedSession(t, key, 5)
		tamper(t, dir, `DELETE FROM events WHERE seq = 5`)
		c, err := ReadCheckpoint(dir)
		if err != nil {
			t.Fatal(err)
		}
		c.Entries = 4 // to match the shortened log
		if err := WriteCheckpoint(dir, c); err != nil {
			t.Fatal(err)
		}
		v := verify(t, dir, pub)
		if v.OK() || !strings.Contains(v.Problem, "signature does not match") {
			t.Errorf("Verify = %+v, want the altered checkpoint's signature refused", v)
		}
	})

	t.Run("no checkpoint", func(t *testing.T) {
		dir := signedSession(t, key, 5)
		if err := os.Remove(filepath.Join(dir, checkpointFile)); err != nil {
			t.Fatal(err)
		}
		v := verify(t, dir, pub)
		if v.OK() || !strings.Contains(v.Problem, "no signed checkpoint") {
			t.Errorf("Verify = %+v, want it to say there is no signed checkpoint", v)
		}
	})
}

func statement() Statement {
	ended := time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC)
	return Statement{
		Session: "20260101-100000-abcdef", ToolVersion: "0.3.0",
		StartedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), EndedAt: &ended,
		CommitsKnown: true, Commits: []string{"1111111111111111111111111111111111111111"},
		Actions: ActionCounts{Held: 3, Approved: 2, Refused: 1},
		Chain:   ChainEnd{Entries: 42, FinalHash: strings.Repeat("ab", 32)},
	}
}

func TestAttestationRoundTrip(t *testing.T) {
	key, pub, _ := newKey(t)
	file, err := Attest(key, statement())
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyAttestation(file, pub)
	if err != nil {
		t.Fatalf("a fresh attestation did not verify: %v", err)
	}
	if got.Type != StatementType || got.Session != "20260101-100000-abcdef" || got.Actions != (ActionCounts{3, 2, 1}) ||
		len(got.Commits) != 1 || got.Chain.Entries != 42 || got.PolicyHash != "" {
		t.Errorf("statement read back as %+v", got)
	}

	// laid out differently, it says the same thing and still verifies
	var loose bytes.Buffer
	if err := json.Indent(&loose, file, "", "\t"); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAttestation(loose.Bytes(), pub); err != nil {
		t.Errorf("re-indented attestation did not verify: %v", err)
	}
}

func TestAlteredAttestationFails(t *testing.T) {
	key, pub, _ := newKey(t)
	otherKey, otherPub, _ := newKey(t)
	file, err := Attest(key, statement())
	if err != nil {
		t.Fatal(err)
	}
	alter := func(old, replacement string) []byte {
		t.Helper()
		if !bytes.Contains(file, []byte(old)) {
			t.Fatalf("the attestation does not contain %q:\n%s", old, file)
		}
		return bytes.Replace(file, []byte(old), []byte(replacement), 1)
	}
	forged, err := Attest(otherKey, statement())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		file []byte
		key  ed25519.PublicKey
		want string
	}{
		{"a count changed", alter(`"refused": 1`, `"refused": 0`), pub, "signature does not match"},
		{"a commit swapped", alter("1111111111", "2222222222"), pub, "signature does not match"},
		{"the final hash changed", alter("abababab", "cdcdcdcd"), pub, "signature does not match"},
		{"a field added", alter(`"statement": {`, `"statement": {"extra": true,`), pub, "signature does not match"},
		{"signed with another key", forged, pub, "different key"},
		{"the key swapped for the forger's", alter(KeyText(pub), KeyText(otherPub)), pub, "different key"},
		{"checked against the wrong key", file, otherPub, "different key"},
		{"not an attestation", []byte(`{"hello":"world"}`), pub, "not readable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := VerifyAttestation(tt.file, tt.key)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("VerifyAttestation = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}
