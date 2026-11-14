package events

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS events (
	seq  INTEGER PRIMARY KEY AUTOINCREMENT,
	time TEXT NOT NULL,
	type TEXT NOT NULL,
	data TEXT NOT NULL
);`

// Log rows are only ever inserted and read, never changed or deleted.
type Log struct {
	db   *sql.DB
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func Open(path string) (*Log, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open event log %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, errors.Join(fmt.Errorf("create event log %s: %w", path, err), db.Close())
	}
	return &Log{db: db, subs: map[chan Event]struct{}{}}, nil
}

func (l *Log) Append(t Type, payload any) (Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode %s event: %w", t, err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ev := Event{Time: time.Now().UTC(), Type: t, Data: data}
	res, err := l.db.Exec(`INSERT INTO events (time, type, data) VALUES (?, ?, ?)`,
		ev.Time.Format(time.RFC3339Nano), string(t), string(data))
	if err != nil {
		return Event{}, fmt.Errorf("write %s event: %w", t, err)
	}
	if ev.Seq, err = res.LastInsertId(); err != nil {
		return Event{}, fmt.Errorf("write %s event: %w", t, err)
	}
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
	rows, err := l.db.Query(`SELECT seq, time, type, data FROM events WHERE seq > ? ORDER BY seq`, seq)
	if err != nil {
		return nil, fmt.Errorf("read event log: %w", err)
	}
	defer rows.Close() // read-only; rows.Err below reports a failed read

	var out []Event
	for rows.Next() {
		var ev Event
		var ts, typ, data string
		if err := rows.Scan(&ev.Seq, &ts, &typ, &data); err != nil {
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
