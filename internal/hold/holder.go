package hold

import (
	"errors"
	"fmt"
	"sync"

	"rehearsal/internal/events"
)

// Holder records held actions. Hold is safe to call from the proxy and
// from several stand-in commands at once.
type Holder struct {
	log   *events.Log
	store *Store

	mu     sync.Mutex
	count  int
	closed bool
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
