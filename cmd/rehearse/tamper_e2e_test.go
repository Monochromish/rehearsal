package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// demo/tamper.sh changes one character in a copy of a session's record.
// Whichever entry is picked, the check names that entry, and the record
// itself still verifies afterwards.
func TestTamperingWithACopyIsNoticedAtTheEntryPicked(t *testing.T) {
	r := recordSession(t)
	script, err := filepath.Abs(filepath.Join("..", "..", "demo", "tamper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	before, code := r.run("export", "--format", "jsonl")
	if code != 0 {
		t.Fatalf("export: %s", before)
	}
	entries := strings.Count(before, "\n")
	if entries < 5 {
		t.Fatalf("the session has only %d entries", entries)
	}

	tamper := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.Command("sh", append([]string{script}, args...)...)
		cmd.Dir, cmd.Env = r.workDir, append(r.env, "REHEARSE="+r.bin)
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tamper.sh with %q: %v\n%s", input, err, out)
		}
		return string(out)
	}

	intact := fmt.Sprintf("%d entries read; each one matches its hash and follows the entry before it", entries)
	for pick := 1; pick <= entries; pick++ {
		out := tamper(fmt.Sprintf("%d\n", pick))
		first, after, ok := strings.Cut(out, "The copy, after the change:")
		if !ok {
			t.Fatalf("entry %d: nothing after the change:\n%s", pick, out)
		}
		if !strings.Contains(first, fmt.Sprintf("has %d entries", entries)) || !strings.Contains(first, ": intact") ||
			!strings.Contains(first, intact) || !strings.Contains(first, "signature: valid") {
			t.Errorf("entry %d: the copy did not hold before the change:\n%s", pick, first)
		}
		if !strings.Contains(first, fmt.Sprintf("Changed one character in entry %d of the copy", pick)) {
			t.Errorf("entry %d: the output does not say what was changed:\n%s", pick, first)
		}
		// that entry and no other
		want := fmt.Sprintf("%d entries read; 1 does not hold: entry %d. Every other entry matches its hash and follows the entry before it", entries, pick)
		if !strings.Contains(after, want) || !strings.Contains(after, "NOT VERIFIED") || strings.Contains(after, intact) {
			t.Errorf("entry %d: after the change the check says:\n%s\nwant it to name the entry: %q", pick, after, want)
		}
	}

	// an answer that is not an entry number is asked again
	out := tamper(fmt.Sprintf("0\nfive\n%d\n\n2\n", entries+1))
	if strings.Count(out, fmt.Sprintf("A number from 1 to %d.", entries)) != 4 || !strings.Contains(out, "1 does not hold: entry 2.") {
		t.Errorf("answers that are not entry numbers:\n%s", out)
	}
	// the session can be named
	if out := tamper("3\n", filepath.Base(r.sessionDir())); !strings.Contains(out, "1 does not hold: entry 3.") {
		t.Errorf("with the session named:\n%s", out)
	}

	// the record itself is as it was
	if after, code := r.run("export", "--format", "jsonl"); code != 0 || after != before {
		t.Errorf("the record's export changed (exit %d)", code)
	}
	if out, code := r.run("verify"); code != 0 {
		t.Errorf("the record no longer verifies:\n%s", out)
	}
}
