// Package investigate reads one or many recorded sessions and says what
// happened in them. Every statement it makes stands beside the log
// entries it rests on, as they were stored.
package investigate

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"rehearsal/internal/events"
	"rehearsal/internal/record"
)

// RuleSet is the version of the rules. It changes whenever a rule changes
// what it reports, so that two reports can be told to come from the same
// rules.
const RuleSet = "1"

// Entry is one line of a record exactly as it was stored.
type Entry struct {
	// the name of the session, and the short label it has in this case
	Session string `json:"session"`
	Label   string `json:"label"`
	Seq     int64  `json:"seq"`
	Time    string `json:"time"`
	Type    string `json:"type"`
	Data    string `json:"data"`
	Prev    string `json:"prev"`
	Hash    string `json:"hash"`
	// why the entry cannot be relied on; empty when it matches its own
	// hash and follows the entry before it
	Problem string `json:"problem,omitempty"`
}

// Custody says whether a record is the one that was written.
type Custody struct {
	Session string `json:"session"`
	Label   string `json:"label"`
	// "session folder" or "exported record", and where it was read from
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Entries int    `json:"entries"`
	// entries that do not match their hash or do not follow the one before
	BadEntries []int64 `json:"bad_entries"`
	// what the check of the chain and of the signature found, in words
	Chain     string `json:"chain"`
	Signature string `json:"signature"`
	// the fingerprint of the key the signature was checked against
	Key string `json:"key,omitempty"`
	// true when every entry holds and the signature on the end is valid
	Verified bool `json:"verified"`
	// how a reader can make the same check without this report
	Command string `json:"command"`
}

// Record is one session's entries and what checking them found.
type Record struct {
	Custody Custody
	Entries []Entry
}

const exportSuffix = ".jsonl"

// Open reads the record or records at path: a session's folder, an
// exported record, or a folder holding several of either. Signatures are
// checked against trusted, which may be nil. A record that does not hold
// up is still read; its Custody says what is wrong with it.
func Open(path string, trusted ed25519.PublicKey) ([]Record, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open the record: %w", err)
	}
	var recs []Record
	switch {
	case !info.IsDir():
		rec, err := openExport(path, trusted)
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	case isSession(path):
		rec, err := openSession(path, trusted)
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	default:
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", path, err)
		}
		for _, e := range entries {
			full := filepath.Join(path, e.Name())
			var rec Record
			switch {
			case e.IsDir() && isSession(full):
				rec, err = openSession(full, trusted)
			case !e.IsDir() && strings.HasSuffix(e.Name(), exportSuffix):
				rec, err = openExport(full, trusted)
			default:
				continue
			}
			if err != nil {
				return nil, err
			}
			recs = append(recs, rec)
		}
		if len(recs) == 0 {
			return nil, fmt.Errorf("%s holds no session folder and no exported record (*%s)", path, exportSuffix)
		}
	}
	return Number(recs), nil
}

// FromEntries makes a record from entries already read from a log. There
// is no signature to check, and its Custody says so.
func FromEntries(session string, raw []events.Raw) Record {
	rec := Record{Custody: Custody{Session: session, Kind: "log in memory", Source: session, Command: "none: this record was not read from a file"}}
	for _, r := range raw {
		rec.Entries = append(rec.Entries, Entry{Session: session, Seq: r.Seq, Time: r.Time, Type: r.Type, Data: r.Data, Prev: r.Prev, Hash: r.Hash})
	}
	check(&rec, "", nil)
	return rec
}

// Number puts records in the order of their first entries and gives each
// its short label: S1, S2 and so on.
func Number(recs []Record) []Record {
	// oldest first, so that what one session read comes before what a later one sent
	slices.SortStableFunc(recs, func(a, b Record) int {
		return strings.Compare(firstTime(a)+a.Custody.Session, firstTime(b)+b.Custody.Session)
	})
	for i := range recs {
		label := fmt.Sprintf("S%d", i+1)
		recs[i].Custody.Label = label
		for j := range recs[i].Entries {
			recs[i].Entries[j].Label = label
		}
	}
	return recs
}

func firstTime(r Record) string {
	if len(r.Entries) == 0 {
		return ""
	}
	return r.Entries[0].Time
}

func isSession(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "events.db"))
	return err == nil
}

func openSession(dir string, trusted ed25519.PublicKey) (rec Record, err error) {
	log, err := events.OpenExisting(filepath.Join(dir, "events.db"))
	if errors.Is(err, events.ErrNoChain) {
		// too old to have a chain: nothing in it can be checked, so
		// nothing is read from it, and the case goes on without it
		return Record{Custody: Custody{
			Session: filepath.Base(dir), Kind: "session folder", Source: dir, BadEntries: []int64{},
			Chain:     "not read: this session was recorded by an older version, and its entries are not chained",
			Signature: "not checked",
			Command:   "rehearse verify " + dir,
		}}, nil
	}
	if err != nil {
		return rec, fmt.Errorf("%s: %w", dir, err)
	}
	defer func() { err = errors.Join(err, log.Close()) }()
	raw, err := log.Raw()
	if err != nil {
		return rec, fmt.Errorf("%s: %w", dir, err)
	}
	name := filepath.Base(dir)
	for _, r := range raw {
		rec.Entries = append(rec.Entries, Entry{Session: name, Seq: r.Seq, Time: r.Time, Type: r.Type, Data: r.Data, Prev: r.Prev, Hash: r.Hash})
	}
	rec.Custody = Custody{
		Session: name, Kind: "session folder", Source: dir,
		Command: "rehearse verify " + dir,
	}
	check(&rec, filepath.Join(dir, "checkpoint.json"), trusted)
	return rec, nil
}

// exported is one line of `rehearse export`.
type exported struct {
	Session string          `json:"session"`
	Seq     int64           `json:"seq"`
	Time    string          `json:"time"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
	Prev    string          `json:"prev"`
	Hash    string          `json:"hash"`
}

func openExport(path string, trusted ed25519.PublicKey) (rec Record, err error) {
	f, err := os.Open(path)
	if err != nil {
		return rec, fmt.Errorf("open the exported record: %w", err)
	}
	defer f.Close() // read-only; the scanner reports a failed read
	name := strings.TrimSuffix(filepath.Base(path), exportSuffix)
	lines := bufio.NewScanner(f)
	lines.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for n := 1; lines.Scan(); n++ {
		text := strings.TrimSpace(lines.Text())
		if text == "" {
			continue
		}
		var line exported
		if err := json.Unmarshal([]byte(text), &line); err != nil || line.Hash == "" {
			rec.Entries = append(rec.Entries, Entry{Seq: int64(n), Type: "unreadable", Data: text, Problem: "this line of the exported record is not readable as an entry"})
			continue
		}
		if line.Session != "" {
			name = line.Session
		}
		rec.Entries = append(rec.Entries, Entry{Seq: line.Seq, Time: line.Time, Type: line.Type, Data: string(line.Data), Prev: line.Prev, Hash: line.Hash})
	}
	if err := lines.Err(); err != nil {
		return rec, fmt.Errorf("read %s: %w", path, err)
	}
	for i := range rec.Entries {
		rec.Entries[i].Session = name
	}
	rec.Custody = Custody{
		Session: name, Kind: "exported record", Source: path,
		Command: "for each line of " + path + ", the SHA-256 of seq, time, type, prev and data, joined by single newlines, must equal hash, and prev must equal the hash of the line before (docs/attestation.md)",
	}
	check(&rec, strings.TrimSuffix(path, exportSuffix)+".checkpoint.json", trusted)
	return rec, nil
}

// check recomputes every entry's hash and its link to the entry before,
// marks each entry that fails, and then checks the signature on the end.
func check(rec *Record, checkpoint string, trusted ed25519.PublicKey) {
	c := &rec.Custody
	c.Entries = len(rec.Entries)
	c.BadEntries = []int64{}
	head := events.ChainStart
	for i := range rec.Entries {
		e := &rec.Entries[i]
		switch {
		case e.Problem != "":
		case events.HashOf(e.Seq, e.Time, e.Type, e.Prev, e.Data) != e.Hash:
			e.Problem = "this entry has been changed since it was written: its contents no longer give its hash"
		case e.Prev != head:
			e.Problem = "this entry does not follow the one before it: an entry was changed, removed or put in here"
			if i > 0 && rec.Entries[i-1].Problem == "" {
				rec.Entries[i-1].Problem = "the entry after this one does not follow it, so one of the two was changed or an entry between them was removed"
			}
		}
		head = e.Hash
	}
	chain := events.Chain{Head: events.ChainStart}
	for _, e := range rec.Entries {
		if e.Problem != "" {
			c.BadEntries = append(c.BadEntries, e.Seq)
			if chain.BadEntry == 0 {
				chain.BadEntry, chain.Problem = e.Seq, e.Problem
			}
			continue
		}
		if chain.BadEntry == 0 {
			chain.Entries, chain.Head = e.Seq, e.Hash
		}
	}
	if len(c.BadEntries) > 0 {
		c.Chain = fmt.Sprintf("%d entries read; %s not hold: %s. Every other entry matches its hash and follows the entry before it", c.Entries, entriesDo(c.BadEntries), numbers(c.BadEntries))
		c.Signature = "not checked: with the chain broken, the hash that was signed cannot be reached"
		return
	}
	c.Chain = fmt.Sprintf("%d entries read; each one matches its hash and follows the entry before it", c.Entries)
	if trusted == nil {
		c.Signature = "not checked: there is no key on this computer to check it against, and none was given"
		return
	}
	c.Key = record.Fingerprint(trusted)
	v := record.VerifyChain(chain, checkpoint, c.Session, trusted)
	if !v.OK() {
		c.Signature = "does not hold: " + strings.TrimSpace(v.Problem)
		return
	}
	c.Signature = fmt.Sprintf("valid: the last entry was signed at %s, checked against the key %s", v.SignedAt.Format("2006-01-02 15:04:05 UTC"), c.Key)
	c.Verified = true
}

func entriesDo(seqs []int64) string {
	if len(seqs) == 1 {
		return "1 does"
	}
	return fmt.Sprintf("%d do", len(seqs))
}

func numbers(seqs []int64) string {
	parts := make([]string, len(seqs))
	for i, s := range seqs {
		parts[i] = fmt.Sprintf("entry %d", s)
	}
	return strings.Join(parts, ", ")
}
