package changes

import (
	"slices"
	"strings"
	"testing"

	"rehearsal/internal/events"
)

func ids(parts []Part) string {
	var out []string
	for _, p := range parts {
		out = append(out, p.ID+":"+string(p.Level))
	}
	return strings.Join(out, " ")
}

func four() Review {
	return Review{Captured: true, InRepo: true, Parts: []Part{
		{ID: "c1", Path: "src/refund.js", Level: Read, Reason: "changes code"},
		{ID: "c2", Path: "README.md", Level: Skim, Reason: "documentation"},
		{ID: "c3", Path: "src/refund.js", Level: Skim, Reason: "comments only"},
		{ID: "c4", Path: "package-lock.json", Level: Mechanical, Reason: "lockfile"},
	}}
}

// The model's word can move a part from skim to read. Nothing it says, and
// nothing in the log, moves a part the other way or touches a mechanical one.
func TestTheReviewerCanRaiseAPartAndNeverLowerOne(t *testing.T) {
	r := four()
	r.Annotate([]events.ChangeReviewed{
		{PartID: "c1", Sentence: "Rounds to the nearest cent."},
		{PartID: "c3", Sentence: "Removes the check on negative amounts, in a line that looks like a comment.", Raised: true},
		{PartID: "c4", Sentence: "Says the lockfile matters.", Raised: true},
		{PartID: "c9", Sentence: "A part that is not in the diff."},
	}, nil)
	if got := ids(r.Parts); got != "c1:read c3:read c2:skim c4:mechanical" {
		t.Errorf("parts = %s", got)
	}
	c3, _ := r.Part("c3")
	c1, _ := r.Part("c1")
	c4, _ := r.Part("c4")
	if !c3.Raised || c1.Raised || c4.Raised || c4.Level != Mechanical {
		t.Errorf("raised: c1 %v, c3 %v, c4 %v (%s)", c1.Raised, c3.Raised, c4.Raised, c4.Level)
	}
	if c1.Sentence != "Rounds to the nearest cent." || c3.Reason != "comments only" {
		t.Errorf("c1 %q, c3 reason %q", c1.Sentence, c3.Reason)
	}
	if r.Reading || r.ReadingNote != "" {
		t.Errorf("reading = %v, note %q, with nothing in the log about it", r.Reading, r.ReadingNote)
	}
}

// While the model is reading, the parts it has yet to reach are pending,
// and no others. When it has finished, what it left is said once.
func TestPendingPartsAndWhatWasLeft(t *testing.T) {
	r := four()
	r.Annotate([]events.ChangeReviewed{{PartID: "c1", Sentence: "Rounds to the nearest cent."}}, []events.ChangesReading{{Started: true, Parts: 2}})
	var pending []string
	for _, p := range r.Parts {
		if p.Pending {
			pending = append(pending, p.ID)
		}
	}
	if !r.Reading || !slices.Equal(pending, []string{"c2"}) {
		t.Errorf("reading = %v, pending = %v, want c2 alone: c1 is done, c3 is past the budget, c4 is mechanical", r.Reading, pending)
	}

	r = four()
	r.Annotate(nil, []events.ChangesReading{{Started: true, Parts: 3}, {Parts: 1, Left: 2, Why: "the 90 seconds allowed for it ran out"}})
	if r.Reading || r.ReadingNote != "The local model did not read 2 of the parts: the 90 seconds allowed for it ran out." {
		t.Errorf("reading = %v, note %q", r.Reading, r.ReadingNote)
	}
	for _, p := range r.Parts {
		if p.Pending {
			t.Errorf("%s is pending after the reading ended", p.ID)
		}
	}
	if got := ToRead(four().Parts, 2); len(got) != 2 || got[0].ID != "c1" || got[1].ID != "c2" {
		t.Errorf("ToRead = %s", ids(got))
	}
	if got := ToRead(four().Parts, 10); len(got) != 3 {
		t.Errorf("ToRead with room to spare = %s, want the three that are not mechanical", ids(got))
	}
}
