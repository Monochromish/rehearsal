// Package ident collects the identifiers Rehearsal watches for and finds
// them inside outgoing requests.
package ident

import (
	"bytes"
	"encoding/base64"
	"net/url"

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
}

func NewMatcher(ids []events.Identifier) *Matcher {
	m := &Matcher{}
	for _, id := range ids {
		if len(id.Value) < MinLen {
			continue
		}
		m.add(id.ID, events.FormPlain, []byte(id.Value), true)
		for _, enc := range []string{url.QueryEscape(id.Value), url.PathEscape(id.Value)} {
			if enc != id.Value {
				m.add(id.ID, events.FormURL, []byte(enc), true)
			}
		}
		if len(id.Value) >= minBase64Len {
			for _, p := range base64Patterns([]byte(id.Value)) {
				m.add(id.ID, events.FormBase64, p, false)
			}
		}
	}
	return m
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
	// part of a word: "%2Fhiman" (URL escape) or "\nhiman" (JSON escape).
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
