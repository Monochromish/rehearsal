package hold

import (
	"errors"
	"fmt"
	"sync"

	"rehearsal/internal/events"
	"rehearsal/internal/policy"
	"rehearsal/internal/review"
)

// Holder records held actions. Hold is safe to call from the proxy and
// from several stand-in commands at once.
type Holder struct {
	log   *events.Log
	store *Store

	mu     sync.Mutex
	count  int
	closed bool

	policy *policy.Policy
	labels func(ids []string) []string
	// policy rules already reported as ignored
	ignored map[string]bool
}

func NewHolder(log *events.Log, store *Store) *Holder {
	return &Holder{log: log, store: store}
}

// Hold stores the payload, writes the action_held event and returns the
// new action's ID. If either step fails nothing is kept, and the caller
// must not let the action through.
func (h *Holder) Hold(display events.ActionHeld, payload Payload) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return "", errors.New("the session is over, nothing more can be held")
	}
	h.count++
	display.ID = fmt.Sprintf("a%d", h.count)
	if display.IdentifierIDs == nil {
		display.IdentifierIDs = []string{}
	}
	if err := h.store.Save(display.ID, payload); err != nil {
		return "", err
	}
	if _, err := h.log.Append(events.TypeActionHeld, display); err != nil {
		return "", errors.Join(err, h.store.Remove(display.ID))
	}
	return display.ID, nil
}

// UsePolicy makes Consider apply the session's policy. labels turns the
// IDs of what an action carries into the words a rule can match: each
// identifier's kind and its label. It must be called before anything is held.
func (h *Holder) UsePolicy(p *policy.Policy, labels func(ids []string) []string) {
	h.policy, h.labels = p, labels
	h.ignored = map[string]bool{}
}

// HasRules reports whether a policy could change what happens to an action
// that the built-in rules would let through.
func (h *Holder) HasRules() bool {
	return h.policy != nil && len(h.policy.Org)+len(h.policy.Project) > 0
}

// What became of an action that was put to the policy.
const (
	Kept    = "held"
	Passed  = "pass"
	Refused = "refused"
)

// Outcome is what Consider did with an action.
type Outcome struct {
	// Kept, Passed or Refused; empty when nothing holds the action
	Action string
	ID     string
	// the policy rule behind a pass or a refusal
	Rule string
}

// Consider puts an action to the policy and acts on the answer. builtIn
// says whether the built-in rules hold it. A held action is stored as by
// Hold. A refused one is recorded as held and refused at once, with
// nothing kept to replay. A passed one is only noted.
func (h *Holder) Consider(display events.ActionHeld, payload Payload, builtIn bool) (Outcome, error) {
	d := policy.Decision{}
	if builtIn {
		d.Action = policy.Hold
	}
	if h.policy != nil {
		var data []string
		if h.labels != nil {
			data = h.labels(display.IdentifierIDs)
		}
		d = h.policy.Decide(policy.Facts{
			Kind: display.Kind, Destination: display.Destination, Data: data,
			Held: builtIn, Floor: review.Floor(display),
		})
	}
	for _, ig := range d.Ignored {
		h.mu.Lock()
		told := h.ignored[ig.Source+ig.Rule]
		h.ignored[ig.Source+ig.Rule] = true
		h.mu.Unlock()
		if !told {
			if _, err := h.log.Append(events.TypePolicyIgnored, events.PolicyIgnored{Rule: ig.Rule, Source: ig.Source, Why: ig.Why}); err != nil {
				return Outcome{}, err
			}
		}
	}
	display.PolicyRisk, display.PolicyRule = d.MinRisk, d.Rule
	switch d.Action {
	case policy.Allow:
		_, err := h.log.Append(events.TypePolicyAllowed, events.PolicyAllowed{
			Kind: display.Kind, Destination: display.Destination, Subject: display.Subject, Rule: d.Rule,
		})
		return Outcome{Action: Passed, Rule: d.Rule}, err
	case policy.Hold:
		id, err := h.Hold(display, payload)
		return Outcome{Action: Kept, ID: id, Rule: d.Rule}, err
	case policy.Refuse:
		// nothing is kept to replay: a refused action is never performed
		id, err := h.Hold(display, Payload{})
		if err != nil {
			return Outcome{}, err
		}
		_, err = h.log.Append(events.TypeActionRefused, events.ActionRefused{ActionID: id, Policy: d.Rule})
		return Outcome{Action: Refused, ID: id, Rule: d.Rule}, err
	}
	return Outcome{}, nil
}

// Close makes every later Hold fail, so the set of held actions is fixed
// before the user is asked to decide.
func (h *Holder) Close() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
}

func (h *Holder) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}
