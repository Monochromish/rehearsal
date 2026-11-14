// Package ident collects the identifiers Rehearsal watches for and finds
// them inside outgoing requests.
package ident

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"slices"
	"strings"

	"rehearsal/internal/events"
)

const (
	// Shorter values (a two-letter username, say) match by accident too often.
	MinLen = 4
	// Short base64 fragments turn up by chance inside unrelated encoded data.
	minBase64Len = 8
)

type Match struct {
	IdentifierID string
	Form         events.Form
}

type needle struct {
	id      string
	form    events.Form
	pattern []byte
	// matched ignoring case, and never inside a longer run of letters or digits
	wholeWord bool
}

type Matcher struct {
	needles []needle
	// what each identifier is called where its value is masked
	names map[string]string
}

// NewMatcher watches for the identifiers that have a value, and for the
// secrets, whose values are given separately because they are never put
// in an Identifier.
func NewMatcher(ids []events.Identifier, secrets ...Secret) *Matcher {
	m := &Matcher{names: map[string]string{}}
	for _, id := range ids {
		m.names[id.ID] = id.Label
		if id.Name != "" {
			m.names[id.ID] = id.Name
		}
		m.watch(id.ID, id.Value)
	}
	for _, s := range secrets {
		m.watch(s.ID, s.Value)
	}
	return m
}

func (m *Matcher) watch(id, value string) {
	if len(value) < MinLen {
		return
	}
	m.add(id, events.FormPlain, []byte(value), true)
	for _, enc := range []string{url.QueryEscape(value), url.PathEscape(value)} {
		if enc != value {
			m.add(id, events.FormURL, []byte(enc), true)
		}
	}
	if len(value) >= minBase64Len {
		for _, p := range base64Patterns([]byte(value)) {
			m.add(id, events.FormBase64, p, false)
		}
		// hex in either case; as with base64, a short run of hex digits
		// turns up by chance
		lower := hex.EncodeToString([]byte(value))
		m.add(id, events.FormHex, []byte(lower), false)
		m.add(id, events.FormHex, []byte(strings.ToUpper(lower)), false)
	}
}

func (m *Matcher) add(id string, form events.Form, pattern []byte, wholeWord bool) {
	if wholeWord {
		pattern = bytes.ToLower(pattern)
	}
	for _, n := range m.needles {
		if n.id == id && n.wholeWord == wholeWord && bytes.Equal(n.pattern, pattern) {
			return
		}
	}
	m.needles = append(m.needles, needle{id: id, form: form, pattern: pattern, wholeWord: wholeWord})
}

// base64Patterns returns the fragments that must appear whenever value is
// base64-encoded, alone or in the middle of longer data. Base64 turns every
// 3 bytes into 4 characters, so the output depends on where the value
// starts (3 possible positions). Characters that also depend on the
// neighbouring bytes are trimmed off.
func base64Patterns(value []byte) [][]byte {
	var out [][]byte
	for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		for offset := range 3 {
			padded := append(make([]byte, offset), value...)
			s := enc.EncodeToString(padded)
			start := []int{0, 2, 3}[offset]
			end := len(s)
			if len(padded)%3 != 0 {
				end-- // the last character also encodes whatever follows
			}
			out = append(out, []byte(s[start:end]))
		}
	}
	return out
}

// Find reports each identifier at most once, in the first form found.
func (m *Matcher) Find(text []byte) []Match {
	if len(text) == 0 {
		return nil
	}
	var lower []byte
	var out []Match
	found := map[string]bool{}
	for _, n := range m.needles {
		if found[n.id] {
			continue
		}
		hit := false
		if n.wholeWord {
			if lower == nil {
				lower = bytes.ToLower(text)
			}
			hit = containsWord(lower, n.pattern)
		} else {
			hit = bytes.Contains(text, n.pattern)
		}
		if hit {
			found[n.id] = true
			out = append(out, Match{IdentifierID: n.id, Form: n.form})
		}
	}
	return out
}

// NotShown stands in for text that still held a watched value after
// masking.
const NotShown = "[not shown: it carries a watched value]"

// Mask returns text with every watched value in it replaced by the value's
// name in square brackets, so that where a value went can be shown without
// showing the value. It masks more readily than Find matches: a value
// inside a longer word is masked too.
func (m *Matcher) Mask(text string) string {
	if text == "" {
		return ""
	}
	b := []byte(text)
	lower := asciiLower(b)
	type span struct {
		start, end int
		id         string
	}
	var spans []span
	for _, n := range m.needles {
		hay := b
		if n.wholeWord {
			hay = lower
		}
		for from := 0; from < len(hay); {
			i := bytes.Index(hay[from:], n.pattern)
			if i < 0 {
				break
			}
			start := from + i
			end := start + len(n.pattern)
			if !n.wholeWord {
				// an encoded value: the characters either side of the
				// match carry parts of it too, so the whole run goes
				for start > 0 && isEncoded(b[start-1]) {
					start--
				}
				for end < len(b) && isEncoded(b[end]) {
					end++
				}
				for end < len(b) && b[end] == '=' {
					end++ // base64 padding
				}
			}
			spans = append(spans, span{start, end, n.id})
			from = end
		}
	}
	slices.SortFunc(spans, func(x, y span) int { return cmp.Compare(x.start, y.start) })
	var out strings.Builder
	at := 0
	for _, s := range spans {
		if s.start < at {
			if s.end > at {
				at = s.end // overlaps the one before: already masked up to here
			}
			continue
		}
		out.Write(b[at:s.start])
		name := m.names[s.id]
		if name == "" {
			name = "a watched value"
		}
		out.WriteString("[" + name + "]")
		at = s.end
	}
	out.Write(b[at:])
	// Find lowers every letter and Mask only the ASCII ones, so a value
	// with other capitals could be missed above. Show nothing then.
	if len(m.Find([]byte(out.String()))) > 0 {
		return NotShown
	}
	return out.String()
}

// Names lists what the watched values found in text are called.
func (m *Matcher) Names(text []byte) []string {
	var out []string
	for _, found := range m.Find(text) {
		name := m.names[found.IdentifierID]
		if name == "" {
			name = "a watched value"
		}
		out = append(out, name)
	}
	return out
}

func asciiLower(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return out
}

// isEncoded reports whether b can be part of base64 or hex text. The
// padding sign is left out: it only ends such text, and it also separates
// a name from a value in an address.
func isEncoded(b byte) bool {
	return isAlnum(b) || b == '+' || b == '/' || b == '_' || b == '-'
}

func containsWord(text, pattern []byte) bool {
	for from := 0; ; {
		i := bytes.Index(text[from:], pattern)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(pattern)
		if startsWord(text, start, pattern[0]) && (end == len(text) || !isAlnum(pattern[len(pattern)-1]) || !isAlnum(text[end])) {
			return true
		}
		from = start + 1
	}
}

func startsWord(text []byte, start int, first byte) bool {
	if start == 0 || !isAlnum(first) || !isAlnum(text[start-1]) {
		return true
	}
	// The letter or digit before us may be the tail of an escape and not
	// part of a word: "%2Fadal" (URL escape) or "\nadal" (JSON escape).
	if start >= 3 && text[start-3] == '%' && isHex(text[start-2]) && isHex(text[start-1]) {
		return true
	}
	if start >= 2 && text[start-2] == '\\' && bytes.IndexByte([]byte("nrtbf"), text[start-1]) >= 0 {
		return true
	}
	return false
}

func isAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}
