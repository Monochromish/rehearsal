package events

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// written makes a log of n entries, closes it and returns its path.
func written(t *testing.T, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	log, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if _, err := log.Append(TypeRequest, Request{ID: "r", Host: "a.test", RequestBytes: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// tamper changes the stored rows behind the log's back.
func tamper(t *testing.T, path string, statements ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
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

func check(t *testing.T, path string) Chain {
	t.Helper()
	log, err := OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := log.Check()
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestUntouchedLogVerifies(t *testing.T) {
	path := written(t, 6)
	c := check(t, path)
	if !c.OK() || c.Entries != 6 || len(c.Head) != 64 || c.Head == ChainStart {
		t.Fatalf("Check = %+v, want 6 matching entries and a real final hash", c)
	}

	log, err := OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close() // read-only
	evs, err := log.Since(0)
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Prev != ChainStart {
		t.Errorf("the first entry carries %q, want the chain start", evs[0].Prev)
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].Prev != evs[i-1].Hash {
			t.Errorf("entry %d carries %q, want the hash of entry %d (%q)", evs[i].Seq, evs[i].Prev, evs[i-1].Seq, evs[i-1].Hash)
		}
	}
	if evs[len(evs)-1].Hash != c.Head {
		t.Errorf("final hash %q is not the last entry's (%q)", c.Head, evs[len(evs)-1].Hash)
	}
}

func TestTamperingIsFoundAtTheRightEntry(t *testing.T) {
	tests := []struct {
		name       string
		statements []string
		bad        int64
		problem    string
	}{
		{
			name:       "an entry's data edited",
			statements: []string{`UPDATE events SET data = replace(data, 'a.test', 'b.test') WHERE seq = 4`},
			bad:        4, problem: "entry 4 has been changed",
		},
		{
			name:       "an entry's time edited",
			statements: []string{`UPDATE events SET time = '2020-01-01T00:00:00Z' WHERE seq = 2`},
			bad:        2, problem: "entry 2 has been changed",
		},
		{
			name:       "an entry's type edited",
			statements: []string{`UPDATE events SET type = 'response' WHERE seq = 5`},
			bad:        5, problem: "entry 5 has been changed",
		},
		{
			name:       "an entry deleted",
			statements: []string{`DELETE FROM events WHERE seq = 3`},
			bad:        3, problem: "entry 3 is missing",
		},
		{
			name:       "the first entry deleted",
			statements: []string{`DELETE FROM events WHERE seq = 1`},
			bad:        1, problem: "entry 1 is missing",
		},
		{
			name: "two entries swapped",
			statements: []string{
				`UPDATE events SET seq = 100 WHERE seq = 3`,
				`UPDATE events SET seq = 3 WHERE seq = 4`,
				`UPDATE events SET seq = 4 WHERE seq = 100`,
			},
			bad: 3, problem: "entry 3 has been changed",
		},
		{
			name: "an entry deleted and the later ones renumbered",
			statements: []string{
				`DELETE FROM events WHERE seq = 2`,
				`UPDATE events SET seq = seq - 1 WHERE seq > 2`,
			},
			bad: 2, problem: "entry 2 has been changed",
		},
		{
			name:       "an entry's link to the one before edited",
			statements: []string{`UPDATE events SET prev = '` + strings.Repeat("ab", 32) + `' WHERE seq = 5`},
			bad:        5, problem: "entry 5 has been changed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := written(t, 6)
			tamper(t, path, tt.statements...)
			c := check(t, path)
			if c.BadEntry != tt.bad || !strings.Contains(c.Problem, tt.problem) {
				t.Errorf("Check = %+v, want it to stop at entry %d with %q", c, tt.bad, tt.problem)
			}
			if c.Entries != tt.bad-1 {
				t.Errorf("Check counted %d good entries before entry %d, want %d", c.Entries, tt.bad, tt.bad-1)
			}
		})
	}
}

// Someone who edits an entry and recomputes its own hash is caught by the
// next entry, which still carries the old one.
func TestAnEditWithItsHashRecomputedBreaksTheNextLink(t *testing.T) {
	path := written(t, 6)
	log, err := OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	evs, err := log.Since(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	third := evs[2]
	edited := `{"forged":true}`
	forged := hashOf(third.Seq, third.Time.Format(time.RFC3339Nano), string(third.Type), third.Prev, edited)
	tamper(t, path, `UPDATE events SET data = '`+edited+`', hash = '`+forged+`' WHERE seq = 3`)

	c := check(t, path)
	if c.BadEntry != 4 || !strings.Contains(c.Problem, "entry 4 does not follow") {
		t.Errorf("Check = %+v, want it to stop at entry 4, which no longer follows entry 3", c)
	}
}

func TestSealedLogTakesNoMoreEntries(t *testing.T) {
	log, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close() // the test is over
	last, err := log.Append(TypeFirstInput, FirstInput{})
	if err != nil {
		t.Fatal(err)
	}
	entries, head := log.Seal()
	if entries != 1 || head != last.Hash {
		t.Errorf("Seal = %d, %q; want 1 entry and the hash of the last one (%q)", entries, head, last.Hash)
	}
	if _, err := log.Append(TypeFirstInput, FirstInput{}); !errors.Is(err, ErrSealed) {
		t.Errorf("Append after Seal = %v, want ErrSealed", err)
	}
	c, err := log.Check()
	if err != nil || c.Entries != 1 {
		t.Errorf("Check after a refused append = %+v, %v; want still 1 entry", c, err)
	}
}

func TestReopenedLogCarriesTheChainOn(t *testing.T) {
	path := written(t, 3)
	log, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(TypeFirstInput, FirstInput{}); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if c := check(t, path); !c.OK() || c.Entries != 4 {
		t.Errorf("Check = %+v, want 4 matching entries", c)
	}
}

func TestLogWithoutAChainIsNamedAsSuch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	tamper(t, path, `CREATE TABLE events (seq INTEGER PRIMARY KEY AUTOINCREMENT, time TEXT NOT NULL, type TEXT NOT NULL, data TEXT NOT NULL)`)
	if _, err := OpenExisting(path); !errors.Is(err, ErrNoChain) {
		t.Errorf("OpenExisting = %v, want ErrNoChain", err)
	}
	if _, err := OpenExisting(filepath.Join(t.TempDir(), "missing.db")); err == nil || errors.Is(err, ErrNoChain) {
		t.Errorf("OpenExisting on a missing file = %v, want an error that says the file is not there", err)
	}
}
