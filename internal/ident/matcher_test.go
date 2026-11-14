package ident

import (
	"encoding/base64"
	"net/url"
	"testing"

	"rehearsal/internal/events"
)

var testIDs = []events.Identifier{
	{ID: "email", Kind: events.KindReal, Value: "ada.lovelace@example.org"},
	{ID: "name", Kind: events.KindReal, Value: "Ada Lovelace"},
	{ID: "user", Kind: events.KindReal, Value: "adal"},
	{ID: "host", Kind: events.KindReal, Value: "AdasLaptop"},
	{ID: "word", Kind: events.KindCanary, Value: "canary-word-3f9a1c0b7e2d"},
	{ID: "tiny", Kind: events.KindReal, Value: "ab"},
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestMatcherFind(t *testing.T) {
	tests := []struct {
		name string
		text string
		want map[string]events.Form // identifier ID -> form; empty means no match
	}{
		{"plain email in JSON", `{"contact":"ada.lovelace@example.org"}`, map[string]events.Form{"email": events.FormPlain}},
		{"plain name with space", `author: Ada Lovelace <x>`, map[string]events.Form{"name": events.FormPlain}},
		{"plain username in a path", `/home/adal/project/main.go`, map[string]events.Form{"user": events.FormPlain}},
		{"plain match ignores case", `machine=adaslaptop;`, map[string]events.Form{"host": events.FormPlain}},
		{"whole text is the identifier", `canary-word-3f9a1c0b7e2d`, map[string]events.Form{"word": events.FormPlain}},
		{"two identifiers at once", `adal@AdasLaptop`, map[string]events.Form{"user": events.FormPlain, "host": events.FormPlain}},
		{"after a JSON newline escape", `{"log":"line one\nadal logged in"}`, map[string]events.Form{"user": events.FormPlain}},

		{"url-encoded email in a query", `https://x.test/?q=` + url.QueryEscape("ada.lovelace@example.org") + `&n=1`, map[string]events.Form{"email": events.FormURL}},
		{"url-encoded name with plus", `name=Ada+Lovelace&x=1`, map[string]events.Form{"name": events.FormURL}},
		{"url-encoded name with %20", `name=Ada%20Lovelace`, map[string]events.Form{"name": events.FormURL}},
		{"username after a url-encoded slash", `cwd=%2Fhome%2Fadal%2Fproject`, map[string]events.Form{"user": events.FormPlain}},

		{"base64 of the value alone", b64("ada.lovelace@example.org"), map[string]events.Form{"email": events.FormBase64}},
		{"base64 inside longer data, offset 0", b64(`ada.lovelace@example.org is here`), map[string]events.Form{"email": events.FormBase64}},
		{"base64 inside longer data, offset 1", b64(`{ada.lovelace@example.org}`), map[string]events.Form{"email": events.FormBase64}},
		{"base64 inside longer data, offset 2", b64(`{"ada.lovelace@example.org"}`), map[string]events.Form{"email": events.FormBase64}},
		{"base64 canary word in JSON", `{"blob":"` + b64(`{"secret":"canary-word-3f9a1c0b7e2d"}`) + `"}`, map[string]events.Form{"word": events.FormBase64}},
		{"url-safe base64", base64.RawURLEncoding.EncodeToString([]byte("??>>ada.lovelace@example.org??>>")), map[string]events.Form{"email": events.FormBase64}},

		{"unrelated text", `The quick brown fox jumps over the lazy dog.`, nil},
		{"empty", ``, nil},
		{"username inside a longer word", `the word signadalx and Vadal and adalberto`, nil},
		{"username followed by digits", `adal2 and 9adal`, nil},
		{"email inside a longer address", `xada.lovelace@example.organic`, nil},
		{"canary word with extra characters", `canary-word-3f9a1c0b7e2d9 xcanary-word-3f9a1c0b7e2d`, nil},
		{"similar but different email", `ada.lovelace@example.com`, nil},
		{"too-short identifier is never searched", `ab ab ab`, nil},
		{"short identifier is not searched in base64", b64("adal") + " " + b64("xxadalxx"), nil},
		{"base64 of different data", b64(`ada.lovelace@example.com and canary-word-0000`), nil},
	}
	m := NewMatcher(testIDs)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := map[string]events.Form{}
			for _, match := range m.Find([]byte(tt.text)) {
				if _, dup := got[match.IdentifierID]; dup {
					t.Errorf("identifier %q reported twice", match.IdentifierID)
				}
				got[match.IdentifierID] = match.Form
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Find(%q) = %v, want %v", tt.text, got, tt.want)
			}
			for id, form := range tt.want {
				if got[id] != form {
					t.Errorf("Find(%q): identifier %q form = %q, want %q", tt.text, id, got[id], form)
				}
			}
		})
	}
}

func TestNewCanariesAreUniqueAndSearchable(t *testing.T) {
	a, b := NewCanaries(), NewCanaries()
	if len(a) != 3 {
		t.Fatalf("got %d canaries, want 3", len(a))
	}
	for i := range a {
		if a[i].Value == b[i].Value {
			t.Errorf("canary %q is the same in two sessions", a[i].ID)
		}
		if a[i].Kind != events.KindCanary {
			t.Errorf("canary %q has kind %q", a[i].ID, a[i].Kind)
		}
		if got := NewMatcher(a).Find([]byte("x " + a[i].Value + " y")); len(got) != 1 || got[0].IdentifierID != a[i].ID {
			t.Errorf("canary %q not found in text that contains it: %v", a[i].ID, got)
		}
	}
}

func TestMaskReplacesEveryFormOfAWatchedValueWithItsName(t *testing.T) {
	ids := []events.Identifier{
		{ID: "user", Kind: events.KindReal, Label: "Your computer username", Value: "adal"},
		{ID: "env-1", Kind: events.KindSecret, Label: "Value of PAYMENTS_API_KEY from .env", Name: "PAYMENTS_API_KEY"},
	}
	key := "pk live/9f3a7c2e41b8d605"
	m := NewMatcher(ids, Secret{ID: "env-1", Value: key})
	tests := []struct{ name, text, want string }{
		{"nothing watched", "k=1&page=2", "k=1&page=2"},
		{"empty", "", ""},
		{"plain, in a query", "k=" + key + "&v=1", "k=[PAYMENTS_API_KEY]&v=1"},
		{"URL-escaped", "k=" + url.QueryEscape(key), "k=[PAYMENTS_API_KEY]"},
		{"in another case", "k=" + "PK LIVE/9F3A7C2E41B8D605", "k=[PAYMENTS_API_KEY]"},
		{"base64, with what surrounds it", "d=" + b64("x"+key+"y") + "&v=1", "d=[PAYMENTS_API_KEY]&v=1"},
		{"twice, and a detail by its label", "/home/adal/" + key + "/" + key, "/home/[Your computer username]/[PAYMENTS_API_KEY]/[PAYMENTS_API_KEY]"},
		{"inside a longer word", "xadalx", "x[Your computer username]x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.Mask(tt.text)
			if got != tt.want {
				t.Errorf("Mask(%q) = %q, want %q", tt.text, got, tt.want)
			}
			if found := m.Find([]byte(got)); len(found) > 0 {
				t.Errorf("Mask(%q) = %q, which still carries %v", tt.text, got, found)
			}
		})
	}
}

func TestMaskShowsNothingWhenAValueSurvives(t *testing.T) {
	// Find lowers "É" and Mask does not, so Mask can't place this one
	m := NewMatcher([]events.Identifier{{ID: "name", Kind: events.KindReal, Label: "Your name", Value: "Émile Zola"}})
	if got := m.Mask("author=ÉMILE ZOLA"); got != NotShown {
		t.Errorf("Mask = %q, want %q", got, NotShown)
	}
}
