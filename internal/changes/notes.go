package changes

import (
	"fmt"
	"slices"

	"rehearsal/internal/events"
)

// ToRead returns the parts the local model is asked about: those the
// rules put under read or skim, most in need of reading first, and at
// most limit of them.
func ToRead(parts []Part, limit int) []Part {
	var out []Part
	for _, p := range parts {
		if p.Level != Mechanical && len(out) < limit {
			out = append(out, p)
		}
	}
	return out
}

// Annotate adds what the local model said about the parts, from the
// session's log: a sentence for each part it read, and "read" in place of
// "skim" where it asked for that. It never lowers a part's level, whatever
// the log says. reading holds the entries that mark the model starting
// and finishing; while it has started and not finished, the parts it has
// yet to reach are marked as pending.
func (r *Review) Annotate(notes []events.ChangeReviewed, reading []events.ChangesReading) {
	var last *events.ChangesReading
	if len(reading) > 0 {
		last = &reading[len(reading)-1]
	}
	// which parts were going to be read is worked out before any level is
	// raised, as it was when the reading began
	planned := map[string]bool{}
	if last != nil && last.Started {
		r.Reading = true
		for _, p := range ToRead(r.Parts, last.Parts) {
			planned[p.ID] = true
		}
	}
	if last != nil && !last.Started && last.Left > 0 {
		r.ReadingNote = fmt.Sprintf("The local model did not read %d of the parts: %s.", last.Left, last.Why)
	}
	for i := range r.Parts {
		p := &r.Parts[i]
		for _, n := range notes {
			if n.PartID != p.ID {
				continue
			}
			p.Sentence = n.Sentence
			if n.Raised && p.Level == Skim {
				p.Level, p.Raised = Read, true
			}
		}
		p.Pending = planned[p.ID] && p.Sentence == ""
	}
	// a raised part moves up among those to read; nothing else moves
	slices.SortStableFunc(r.Parts, func(a, b Part) int { return b.Level.rank() - a.Level.rank() })
}
