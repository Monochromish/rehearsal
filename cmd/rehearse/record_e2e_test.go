package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"rehearsal/internal/events"
	"rehearsal/internal/ledger"
)

// recorded runs one whole session in a fresh repository, in which the
// wrapped command makes a commit, and returns what is needed to run more
// rehearse commands against it.
type recorded struct {
	t       *testing.T
	bin     string
	env     []string
	workDir string
	state   string
	config  string
}

func recordSession(t *testing.T) recorded {
	t.Helper()
	work, _ := repo(t)
	r := recorded{t: t, bin: buildRehearse(t), env: cleanEnv(t), workDir: work}
	for _, kv := range r.env {
		if v, ok := strings.CutPrefix(kv, "XDG_STATE_HOME="); ok {
			r.state = v
		}
		if v, ok := strings.CutPrefix(kv, "XDG_CONFIG_HOME="); ok {
			r.config = v
		}
	}
	cmd := exec.Command(r.bin, "--no-open", "--", "sh", "-c", "echo change > notes.txt && git add notes.txt && git commit -q -m 'Add notes'")
	cmd.Dir, cmd.Env = work, r.env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the session failed: %v\n%s", err, out)
	}
	return r
}

// run runs `rehearse args...` and returns its output and exit code.
func (r recorded) run(args ...string) (string, int) {
	r.t.Helper()
	cmd := exec.Command(r.bin, args...)
	cmd.Dir, cmd.Env = r.workDir, r.env
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		r.t.Fatalf("rehearse %v: %v\n%s", args, err, out)
	}
	return string(out), 0
}

func (r recorded) sessionDir() string {
	r.t.Helper()
	dirs, err := filepath.Glob(filepath.Join(r.state, "rehearsal", "sessions", "*"))
	if err != nil || len(dirs) != 1 {
		r.t.Fatalf("want one session folder, got %v (%v)", dirs, err)
	}
	return dirs[0]
}

func (r recorded) publicKey() string {
	return filepath.Join(r.config, "rehearsal", "signing-key.pub")
}

func TestRecordedSessionVerifiesAndTamperingIsNamed(t *testing.T) {
	r := recordSession(t)
	out, code := r.run("verify")
	if code != 0 || !strings.Contains(out, "Record verified") {
		t.Fatalf("verify on an untouched session: exit %d\n%s", code, out)
	}
	// by name as well as by default
	if out, code := r.run("verify", filepath.Base(r.sessionDir())); code != 0 {
		t.Fatalf("verify by session name: exit %d\n%s", code, out)
	}

	// what the page and the report say about the record
	raw, err := os.ReadFile(filepath.Join(r.workDir, "rehearsal-ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	var l ledger.Ledger
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	if !l.Record.Verified || l.Record.Entries < 3 || l.ToolVersion != version {
		t.Errorf("ledger record = %+v, tool version %q; want a verified record and version %s", l.Record, l.ToolVersion, version)
	}
	report, err := os.ReadFile(filepath.Join(r.workDir, "rehearsal-report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "## Record") || !strings.Contains(string(report), "the chain verified") {
		t.Errorf("the report does not say the record verified:\n%s", report)
	}

	// a program that shares a name with a command can still be wrapped
	recordedSession := r.sessionDir()
	script := filepath.Join(r.workDir, "verify")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho wrapped-program-ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, code := r.run("--no-open", "./verify"); code != 0 || !strings.Contains(out, "wrapped-program-ran") {
		t.Errorf("rehearse ./verify: exit %d, want the program to be wrapped and run\n%s", code, out)
	}
	// that was a session of its own; the rest of the test is about the first
	all, err := filepath.Glob(filepath.Join(r.state, "rehearsal", "sessions", "*"))
	if err != nil || len(all) != 2 {
		t.Fatalf("want two session folders after wrapping ./verify, got %v (%v)", all, err)
	}
	for _, dir := range all {
		if dir != recordedSession {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}
	}

	db, err := sql.Open("sqlite", "file:"+filepath.Join(r.sessionDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE events SET data = replace(data, 'sh', 'zsh') WHERE seq = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	out, code = r.run("verify")
	if code == 0 || !strings.Contains(out, "record does not verify, starting at entry 1") {
		t.Errorf("verify after editing entry 1: exit %d, want non-zero and the entry named\n%s", code, out)
	}
	if out, code := r.run("attest"); code == 0 {
		t.Errorf("attest signed a statement about a record that does not verify:\n%s", out)
	}
}

func TestExportIsOneEventPerLineExactlyAsHashed(t *testing.T) {
	r := recordSession(t)
	out, code := r.run("export", "--format", "jsonl")
	if code != 0 {
		t.Fatalf("export: exit %d\n%s", code, out)
	}
	var lines []map[string]json.RawMessage
	scan := bufio.NewScanner(strings.NewReader(out))
	scan.Buffer(nil, 1<<20)
	for scan.Scan() {
		var line map[string]json.RawMessage
		if err := json.Unmarshal(scan.Bytes(), &line); err != nil {
			t.Fatalf("line %d is not JSON: %v\n%s", len(lines)+1, err, scan.Text())
		}
		for _, field := range []string{"session", "seq", "time", "type", "data", "prev", "hash"} {
			if _, ok := line[field]; !ok {
				t.Errorf("line %d has no %q field: %s", len(lines)+1, field, scan.Text())
			}
		}
		lines = append(lines, line)
	}
	if len(lines) < 3 {
		t.Fatalf("export gave %d lines, want at least the start, the commits and the end:\n%s", len(lines), out)
	}
	var types []string
	for _, l := range lines {
		types = append(types, string(l["type"]))
	}
	for _, want := range []string{`"session_started"`, `"commits_created"`, `"session_ended"`} {
		if !strings.Contains(strings.Join(types, " "), want) {
			t.Errorf("export has no %s line; types: %v", want, types)
		}
	}

	// Every line carries exactly what was hashed, so the chain can be
	// recomputed from the export alone.
	log, err := events.OpenExisting(filepath.Join(r.sessionDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := log.Since(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if len(stored) != len(lines) {
		t.Fatalf("export has %d lines and the log has %d entries", len(lines), len(stored))
	}
	prev := events.ChainStart
	for i, ev := range stored {
		line := lines[i]
		if !bytes.Equal(line["data"], ev.Data) {
			t.Errorf("line %d: data is not byte for byte what the log holds\n got %s\nwant %s", i+1, line["data"], ev.Data)
		}
		var seq int64
		var at, typ, before, hash string
		for field, into := range map[string]any{"seq": &seq, "time": &at, "type": &typ, "prev": &before, "hash": &hash} {
			if err := json.Unmarshal(line[field], into); err != nil {
				t.Fatalf("line %d, field %s: %v", i+1, field, err)
			}
		}
		if before != prev {
			t.Errorf("line %d: prev is %s, want the hash of the line before, %s", i+1, before, prev)
		}
		sum := sha256.Sum256(fmt.Appendf(nil, "%d\n%s\n%s\n%s\n%s", seq, at, typ, before, line["data"]))
		if hex.EncodeToString(sum[:]) != hash {
			t.Errorf("line %d: the hash recomputed from the exported fields is not the one exported", i+1)
		}
		prev = hash
	}
	if out, code := r.run("export", "--format", "csv"); code == 0 {
		t.Errorf("export accepted a format it does not have:\n%s", out)
	}
}

func TestAttestationNamesTheCommitAndFailsWhenAltered(t *testing.T) {
	r := recordSession(t)
	head := git(t, r.workDir, "rev-parse", "HEAD")
	file := filepath.Join(t.TempDir(), "attestation.json")
	if out, code := r.run("attest", "-o", file); code != 0 {
		t.Fatalf("attest: exit %d\n%s", code, out)
	}

	out, code := r.run("verify-attestation", file, "--key", r.publicKey())
	if code != 0 || !strings.Contains(out, "Attestation verified") {
		t.Fatalf("verify-attestation: exit %d\n%s", code, out)
	}
	for _, want := range []string{head, "Commits made in the session: 1", "Actions held: 0, approved: 0, refused: 0", "rehearse " + version, "Policy: none"} {
		if !strings.Contains(out, want) {
			t.Errorf("verify-attestation output lacks %q:\n%s", want, out)
		}
	}

	// the key can be given as text too, which is how a CI secret holds it
	pemText, err := os.ReadFile(r.publicKey())
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(pemText)
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	asText := base64.StdEncoding.EncodeToString(parsed.(ed25519.PublicKey))
	if out, code := r.run("verify-attestation", file, "--key", asText); code != 0 {
		t.Errorf("verify-attestation with the key as text: exit %d\n%s", code, out)
	}

	if out, code := r.run("verify-attestation", file); code == 0 {
		t.Errorf("verify-attestation without --key succeeded:\n%s", out)
	}

	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	altered := bytes.Replace(original, []byte(head), []byte(strings.Repeat("0", len(head))), 1)
	if bytes.Equal(altered, original) {
		t.Fatalf("the attestation does not contain the commit %s:\n%s", head, original)
	}
	if err := os.WriteFile(file, altered, 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = r.run("verify-attestation", file, "--key", r.publicKey())
	if code == 0 || !strings.Contains(out, "signature does not match") {
		t.Errorf("an attestation with its commit replaced: exit %d, want non-zero\n%s", code, out)
	}
}

// The private key signs things and is written nowhere but its own file.
func TestPrivateKeyIsWrittenNowhereElse(t *testing.T) {
	r := recordSession(t)
	keyFile := filepath.Join(r.config, "rehearsal", "signing-key.pem")
	pemText, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(pemText)
	if block == nil {
		t.Fatalf("%s is not PEM", keyFile)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	seed := parsed.(ed25519.PrivateKey).Seed()
	forms := [][]byte{
		[]byte(base64.StdEncoding.EncodeToString(block.Bytes)),
		[]byte(base64.StdEncoding.EncodeToString(seed)),
		[]byte(base64.RawURLEncoding.EncodeToString(seed)),
		[]byte(hex.EncodeToString(seed)),
		seed,
	}

	attestation := filepath.Join(r.workDir, "rehearsal-attestation.json")
	if out, code := r.run("attest"); code != 0 {
		t.Fatalf("attest: exit %d\n%s", code, out)
	}
	exported, code := r.run("export")
	if code != 0 {
		t.Fatalf("export: exit %d\n%s", code, exported)
	}
	if _, err := os.Stat(attestation); err != nil {
		t.Fatalf("attest did not write %s: %v", attestation, err)
	}

	looked := 0
	search := func(name string, data []byte) {
		looked++
		for _, form := range forms {
			if bytes.Contains(data, form) {
				t.Errorf("%s contains the private key", name)
			}
		}
	}
	search("the export", []byte(exported))
	// the log, the checkpoint, the reports, the attestation and the
	// repository itself, objects included
	for _, root := range []string{r.state, r.workDir} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			search(path, data)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if looked < 6 {
		t.Errorf("only %d files were searched; the log, checkpoint, ledger, report and attestation should all be among them", looked)
	}

	other, err := os.ReadFile(r.publicKey())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(other, forms[0]) {
		t.Error("the public key file contains the private key")
	}
}
