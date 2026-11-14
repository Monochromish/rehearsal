// Package ledger turns the event log into the summary of which servers
// were contacted and which identifiers each one received.
package ledger

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"rehearsal/internal/events"
)

type Ledger struct {
	Command   []string   `json:"command"`
	WorkDir   string     `json:"work_dir"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	ExitCode  *int       `json:"exit_code"`
	Totals    Totals     `json:"totals"`
	// every identifier we looked for, sent or not
	Watching []events.Identifier `json:"watching"`
	Servers  []Server            `json:"servers"`
}

type Totals struct {
	Requests int `json:"requests"`
	Servers  int `json:"servers"`
	// distinct (server, identifier) pairs
	IdentifiersSent int `json:"identifiers_sent"`
	FlaggedServers  int `json:"flagged_servers"`
}

type Server struct {
	Host      string          `json:"host"`
	Category  events.Category `json:"category"`
	Requests  int             `json:"requests"`
	FirstSeen time.Time       `json:"first_seen"`
	LastSeen  time.Time       `json:"last_seen"`
	// not a model provider, yet it received an identifier
	Flagged  bool       `json:"flagged"`
	Received []Received `json:"received"`
}

type Received struct {
	events.Identifier
	Requests int            `json:"requests"`
	Where    []events.Where `json:"where"`
}

func Build(evs []events.Event) (Ledger, error) {
	l := Ledger{Watching: []events.Identifier{}, Servers: []Server{}}
	byID := map[string]events.Identifier{}
	servers := map[string]*Server{}
	seenIn := map[string]map[string]bool{} // host+identifier -> request IDs

	for _, ev := range evs {
		switch ev.Type {
		case events.TypeSessionStarted:
			var d events.SessionStarted
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			l.Command, l.WorkDir, l.StartedAt, l.Watching = d.Command, d.WorkDir, ev.Time, d.Identifiers
			for _, id := range d.Identifiers {
				byID[id.ID] = id
			}
		case events.TypeRequest:
			var d events.Request
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			s := servers[d.Host]
			if s == nil {
				s = &Server{Host: d.Host, Category: d.Category, FirstSeen: ev.Time, Received: []Received{}}
				servers[d.Host] = s
			}
			s.Requests++
			s.LastSeen = ev.Time
			l.Totals.Requests++
		case events.TypeIdentifierSeen:
			var d events.IdentifierSeen
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			s, id := servers[d.Host], byID[d.IdentifierID]
			if s == nil || id.ID == "" {
				return l, fmt.Errorf("event %d names an unknown server or identifier (%s, %s)", ev.Seq, d.Host, d.IdentifierID)
			}
			r := findReceived(s, id)
			key := d.Host + "\x00" + d.IdentifierID
			if seenIn[key] == nil {
				seenIn[key] = map[string]bool{}
			}
			if !seenIn[key][d.RequestID] {
				seenIn[key][d.RequestID] = true
				r.Requests++
			}
			if !containsWhere(r.Where, d.Where) {
				r.Where = append(r.Where, d.Where)
			}
		case events.TypeSessionEnded:
			var d events.SessionEnded
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			t, code := ev.Time, d.ExitCode
			l.EndedAt, l.ExitCode = &t, &code
		}
	}

	for _, s := range servers {
		s.Flagged = s.Category != events.CategoryProvider && len(s.Received) > 0
		if s.Flagged {
			l.Totals.FlaggedServers++
		}
		l.Totals.IdentifiersSent += len(s.Received)
		l.Servers = append(l.Servers, *s)
	}
	l.Totals.Servers = len(l.Servers)
	sort.Slice(l.Servers, func(i, j int) bool {
		a, b := l.Servers[i], l.Servers[j]
		if a.Flagged != b.Flagged {
			return a.Flagged
		}
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return a.Host < b.Host
	})
	return l, nil
}

func decode(ev events.Event, into any) error {
	if err := json.Unmarshal(ev.Data, into); err != nil {
		return fmt.Errorf("event %d (%s) is unreadable: %w", ev.Seq, ev.Type, err)
	}
	return nil
}

func findReceived(s *Server, id events.Identifier) *Received {
	for i := range s.Received {
		if s.Received[i].ID == id.ID {
			return &s.Received[i]
		}
	}
	s.Received = append(s.Received, Received{Identifier: id, Where: []events.Where{}})
	return &s.Received[len(s.Received)-1]
}

func containsWhere(list []events.Where, w events.Where) bool {
	for _, x := range list {
		if x == w {
			return true
		}
	}
	return false
}

// Text is the plain-text ledger printed in the terminal.
func (l Ledger) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nRehearsal ledger: %s to %s\n",
		plural(l.Totals.Requests, "request"), plural(l.Totals.Servers, "server"))
	groups := []struct {
		title string
		cat   events.Category
	}{
		{"MODEL PROVIDER", events.CategoryProvider},
		{"EVERYTHING ELSE", events.CategoryOther},
	}
	for _, g := range groups {
		fmt.Fprintf(&b, "\n%s\n", g.title)
		n := 0
		for _, s := range l.Servers {
			if s.Category != g.cat {
				continue
			}
			n++
			mark := " "
			if s.Flagged {
				mark = "!"
			}
			fmt.Fprintf(&b, " %s %-44s %s\n", mark, s.Host, plural(s.Requests, "request"))
			for _, r := range s.Received {
				kind := "yours"
				if r.Kind == events.KindCanary {
					kind = "planted"
				}
				fmt.Fprintf(&b, "       received: %s (%s) in %s\n", r.Label, kind, plural(r.Requests, "request"))
			}
		}
		if n == 0 {
			b.WriteString("   (none)\n")
		}
	}
	if l.Totals.FlaggedServers > 0 {
		fmt.Fprintf(&b, "\n! %s outside the model provider received your details.\n", plural(l.Totals.FlaggedServers, "server"))
	}
	return b.String()
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
