package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"rehearsal/internal/events"
	"rehearsal/internal/redact"
)

const (
	// how much of a reply is looked at
	maxOutsideBytes = 1 << 20
	maxHostsKept    = 50
	maxURLsKept     = 200
	maxExcerpt      = 80
)

var (
	urlInText   = regexp.MustCompile(`https?://[^\s"'<>()\[\]{}\\]+`)
	htmlComment = regexp.MustCompile(`(?s)<!--(.*?)-->`)
)

// outside remembers what has been read from servers other than the model
// provider, and so where an address first came from.
type outside struct {
	mu   sync.Mutex
	salt string
	// host -> the server whose reply first mentioned it, and when
	mentioned map[string]mention
	// every host a request has been sent to, held or not
	contacted map[string]bool
	sources   []string
}

type mention struct {
	by   string
	when time.Time
}

func newOutside(salt string) *outside {
	return &outside{salt: salt, mentioned: map[string]mention{}, contacted: map[string]bool{}}
}

// read looks at a reply from host and returns what to record about it.
// Hosts the session has already contacted are not marked as coming from
// this reply: their addresses were known before it.
func (o *outside) read(host string, body []byte, now time.Time) events.OutsideContent {
	if len(body) > maxOutsideBytes {
		body = body[:maxOutsideBytes]
	}
	ev := events.OutsideContent{Host: host, Bytes: len(body), Hosts: []string{}, URLHashes: []string{}}

	o.mu.Lock()
	defer o.mu.Unlock()
	if !slices.Contains(o.sources, host) {
		o.sources = append(o.sources, host)
	}
	for _, raw := range urlInText.FindAll(body, -1) {
		u, err := url.Parse(strings.TrimRight(string(raw), ".,;:!?"))
		if err != nil || u.Hostname() == "" {
			continue // not an address after all
		}
		named := displayHost(u)
		if !slices.Contains(ev.Hosts, named) && len(ev.Hosts) < maxHostsKept {
			ev.Hosts = append(ev.Hosts, named)
		}
		if len(ev.URLHashes) < maxURLsKept {
			sum := sha256.Sum256([]byte(o.salt + u.String()))
			ev.URLHashes = append(ev.URLHashes, hex.EncodeToString(sum[:8]))
		}
		if _, known := o.mentioned[named]; !known && !o.contacted[named] && named != host {
			o.mentioned[named] = mention{by: host, when: now}
		}
	}

	ev.ZeroWidth, ev.TagChars = hiddenCharacters(body)
	comments := htmlComment.FindAllSubmatch(body, -1)
	ev.Comments = len(comments)
	if len(comments) > 0 {
		ev.HiddenExcerpt = excerpt(string(comments[0][1]))
	}
	return ev
}

// contact notes a request to host. It returns where the host's address
// came from, if it first appeared in a reply: that holds for every request
// to the host, not only the first, or a retry would get through. It also
// returns the servers already read from, when this is the first request to
// the host and it comes after some reading.
func (o *outside) contact(host string, now time.Time) (from *events.Provenance, after []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if m, ok := o.mentioned[host]; ok {
		from = &events.Provenance{SourceHost: m.by, SecondsBefore: int(now.Sub(m.when).Seconds())}
	}
	if !o.contacted[host] {
		o.contacted[host] = true
		after = slices.Clone(o.sources)
	}
	return from, after
}

// hiddenCharacters counts characters that take up no space when shown:
// zero-width ones, and the Unicode "tag" block, which can spell out text
// no reader sees.
func hiddenCharacters(body []byte) (zeroWidth, tags int) {
	for len(body) > 0 {
		r, size := utf8.DecodeRune(body)
		body = body[size:]
		switch {
		// zero-width space, non-joiner, joiner, word joiner, and the byte order mark
		case r == 0x200B || r == 0x200C || r == 0x200D || r == 0x2060 || r == 0xFEFF:
			zeroWidth++
		case r >= 0xE0000 && r <= 0xE007F:
			tags++
		}
	}
	return zeroWidth, tags
}

func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if runes := []rune(s); len(runes) > maxExcerpt {
		s = string(runes[:maxExcerpt]) + "..."
	}
	return redact.Text(s)
}
