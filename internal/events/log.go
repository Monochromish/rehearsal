package events

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS events (
	seq  INTEGER PRIMARY KEY,
	time TEXT NOT NULL,
	type TEXT NOT NULL,
	data TEXT NOT NULL,
	prev TEXT NOT NULL,
	hash TEXT NOT NULL
);`

// ChainStart is what the first entry carries in place of the hash of an
// entry before it.
const ChainStart = "0000000000000000000000000000000000000000000000000000000000000000"

// ErrSealed is returned by Append once the log has been sealed.
var ErrSealed = errors.New("the record is sealed: the session is over and nothing more can be added")

// ErrNoChain is returned when a log was written by a version that did not
// chain its entries.
var ErrNoChain = errors.New("this session was recorded by an older version, and its entries are not chained")

// Log rows are only ever inserted and read, never changed or deleted. Each
// row carries the hash of the row before it, so a later change to any row
// shows when the chain is recomputed.
type Log struct {
	db   *sql.DB
	mu   sync.Mutex
	subs map[chan Event]struct{}
	// the last entry written
	seq    int64
	head   string
	sealed bool
}

// Open creates the log at path, or opens one that is already there to
// carry on writing to it.
func Open(path string) (*Log, error) {
	return open(path, true)
}

// OpenExisting opens a finished session's log for reading. It fails when
// there is no log at path, and with ErrNoChain when the log has no chain.
func OpenExisting(path string) (*Log, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}
	return open(path, false)
}

func open(path string, create bool) (*Log, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open event log %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	l := &Log{db: db, subs: map[chan Event]struct{}{}, head: ChainStart}
	if create {
		if _, err := db.Exec(schema); err != nil {
			return nil, errors.Join(fmt.Errorf("create event log %s: %w", path, err), db.Close())
		}
	}
	err = db.QueryRow(`SELECT seq, hash FROM events ORDER BY seq DESC LIMIT 1`).Scan(&l.seq, &l.head)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil && !create:
		// the only way this query fails on a readable log is a missing column
		return nil, errors.Join(fmt.Errorf("%w (%v)", ErrNoChain, err), db.Close())
	case err != nil:
		return nil, errors.Join(fmt.Errorf("read event log %s: %w", path, err), db.Close())
	}
	return l, nil
}

// canonical is the form of an entry that is hashed: its number, time, type,
// the hash of the entry before it, and its data, one per line, exactly as
// they are stored.
func canonical(seq int64, time, typ, prev, data string) string {
	return strconv.FormatInt(seq, 10) + "\n" + time + "\n" + typ + "\n" + prev + "\n" + data
}

func hashOf(seq int64, time, typ, prev, data string) string {
	sum := sha256.Sum256([]byte(canonical(seq, time, typ, prev, data)))
	return hex.EncodeToString(sum[:])
}

func (l *Log) Append(t Type, payload any) (Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode %s event: %w", t, err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sealed {
		return Event{}, fmt.Errorf("write %s event: %w", t, ErrSealed)
	}
	ev := Event{Seq: l.seq + 1, Time: time.Now().UTC(), Type: t, Data: data, Prev: l.head}
	stamp := ev.Time.Format(time.RFC3339Nano)
	ev.Hash = hashOf(ev.Seq, stamp, string(t), ev.Prev, string(data))
	if _, err := l.db.Exec(`INSERT INTO events (seq, time, type, data, prev, hash) VALUES (?, ?, ?, ?, ?, ?)`,
		ev.Seq, stamp, string(t), string(data), ev.Prev, ev.Hash); err != nil {
		return Event{}, fmt.Errorf("write %s event: %w", t, err)
	}
	l.seq, l.head = ev.Seq, ev.Hash
	for ch := range l.subs {
		select {
		case ch <- ev:
		default:
			// too slow to keep up: drop it, it can reconnect and catch up
			delete(l.subs, ch)
			close(ch)
		}
	}
	return ev, nil
}

// Since returns the events after seq, oldest first.
func (l *Log) Since(seq int64) ([]Event, error) {
	rows, err := l.db.Query(`SELECT seq, time, type, data, prev, hash FROM events WHERE seq > ? ORDER BY seq`, seq)
	if err != nil {
		return nil, fmt.Errorf("read event log: %w", err)
	}
	defer rows.Close() // read-only; rows.Err below reports a failed read

	var out []Event
	for rows.Next() {
		var ev Event
		var ts, typ, data string
		if err := rows.Scan(&ev.Seq, &ts, &typ, &data, &ev.Prev, &ev.Hash); err != nil {
			return nil, fmt.Errorf("read event log: %w", err)
		}
		if ev.Time, err = time.Parse(time.RFC3339Nano, ts); err != nil {
			return nil, fmt.Errorf("event %d has a bad time: %w", ev.Seq, err)
		}
		ev.Type, ev.Data = Type(typ), json.RawMessage(data)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read event log: %w", err)
	}
	return out, nil
}

// Chain is the result of recomputing the log's chain of hashes.
type Chain struct {
	// entries read before the check stopped
	Entries int64 `json:"entries"`
	// hash of the last entry that matched, ChainStart when there is none
	Head string `json:"head"`
	// the first entry that does not match; 0 when every entry does
	BadEntry int64 `json:"bad_entry"`
	// what is wrong with it, for a person
	Problem string `json:"problem,omitempty"`
}

// OK reports whether every entry matched.
func (c Chain) OK() bool { return c.BadEntry == 0 }

// Check recomputes the chain from the stored rows. It stops at the first
// entry that is missing, has been changed, or does not follow the one
// before it. An error means the log could not be read at all.
//
// On its own this cannot notice entries removed from the end, or a log
// rewritten from some point on with every hash recomputed. The signed
// checkpoint covers those.
func (l *Log) Check() (Chain, error) {
	rows, err := l.db.Query(`SELECT seq, time, type, data, prev, hash FROM events ORDER BY seq`)
	if err != nil {
		return Chain{}, fmt.Errorf("read event log: %w", err)
	}
	defer rows.Close() // read-only; rows.Err below reports a failed read

	c := Chain{Head: ChainStart}
	for rows.Next() {
		var seq int64
		var ts, typ, data, prev, hash string
		if err := rows.Scan(&seq, &ts, &typ, &data, &prev, &hash); err != nil {
			return Chain{}, fmt.Errorf("read event log: %w", err)
		}
		want := c.Entries + 1
		switch {
		case seq != want:
			c.BadEntry, c.Problem = want, fmt.Sprintf("entry %d is missing", want)
		case hashOf(seq, ts, typ, prev, data) != hash:
			c.BadEntry, c.Problem = seq, fmt.Sprintf("entry %d has been changed since it was written", seq)
		case prev != c.Head:
			c.BadEntry, c.Problem = seq, fmt.Sprintf("entry %d does not follow the entry before it", seq)
		}
		if !c.OK() {
			return c, nil
		}
		c.Entries, c.Head = seq, hash
	}
	if err := rows.Err(); err != nil {
		return Chain{}, fmt.Errorf("read event log: %w", err)
	}
	return c, nil
}

// Seal ends the log: every later Append fails. It returns the number of
// entries and the hash of the last one, which is what a checkpoint signs.
func (l *Log) Seal() (entries int64, head string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sealed = true
	return l.seq, l.head
}

// Subscribe delivers events appended from now on. The channel is closed
// when stop is called or the reader falls too far behind.
func (l *Log) Subscribe() (events <-chan Event, stop func()) {
	ch := make(chan Event, 1024)
	l.mu.Lock()
	l.subs[ch] = struct{}{}
	l.mu.Unlock()
	return ch, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if _, ok := l.subs[ch]; ok {
			delete(l.subs, ch)
			close(ch)
		}
	}
}

func (l *Log) Close() error {
	return l.db.Close()
}
