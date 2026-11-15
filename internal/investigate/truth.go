package investigate

import (
	"encoding/json"
	"time"

	"rehearsal/internal/events"
	"rehearsal/internal/ledger"
)

// truthStatement is the sentence a session's own report opens with: what
// the agent said, or was told, set against what the record shows. It is
// made by the same rules as in the session, from this record's entries,
// and is listed with the entries each half rests on. A record that
// cannot be read as a session, or has nothing to set against anything,
// has none.
func truthStatement(rec *Record) (Statement, bool) {
	evs := make([]events.Event, 0, len(rec.Entries))
	bySeq := map[int64]*Entry{}
	for i := range rec.Entries {
		e := &rec.Entries[i]
		// the time plays no part in the sentence, so one that cannot be
		// read is left at zero
		when, _ := time.Parse(time.RFC3339Nano, e.Time)
		evs = append(evs, events.Event{Seq: e.Seq, Time: when, Type: events.Type(e.Type), Data: json.RawMessage(e.Data)})
		bySeq[e.Seq] = e
	}
	l, err := ledger.Build(evs)
	if err != nil || l.Truth == nil {
		return Statement{}, false
	}
	t := l.Truth
	s := Statement{Kind: Observed, Text: t.Sentence, Refs: []Ref{}}
	for _, seq := range append(append([]int64{}, t.Said.Entries...), t.Observed.Entries...) {
		if e, ok := bySeq[seq]; ok {
			s.Refs = append(s.Refs, ref(e, `"kind":"`+t.Claim+`"`, `"failed":true`, `"commits":[]`, `"blocked":true`, `"action_id":`, `"text":`, `"conversation":true`))
		}
	}
	return s, len(s.Refs) > 0
}
