package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestCompareShowsTheTwoWorldsSideBySide(t *testing.T) {
	without := &world{cloud: newCloud(), inbox: &inbox{}}
	with := &world{cloud: newCloud(), inbox: &inbox{}}
	// each world's own services, and the page on the first one's cloud host
	inboxA := httptest.NewServer(without.inbox.handler())
	defer inboxA.Close()
	cloudA := httptest.NewServer(comparing(without.cloud.handler(), without, with))
	defer cloudA.Close()

	state := func() compareState {
		t.Helper()
		status, body := get(t, cloudA.URL+"/compare/state")
		var s compareState
		if err := json.Unmarshal([]byte(body), &s); err != nil || status != http.StatusOK {
			t.Fatalf("state answered %d: %s (%v)", status, body, err)
		}
		return s
	}
	if s := state(); len(s.Without.Inbox) != 0 || len(s.With.Inbox) != 0 || !s.Without.Production || !s.With.Production || s.With.Inbox == nil {
		t.Fatalf("before anything: %+v", s)
	}

	// what an agent with nothing in front of it does to the first world
	const typed = `judge's <b>pass</b> word 100%!`
	res, err := http.Post(inboxA.URL+"/collect", "text/plain", strings.NewReader("PAYMENTS_API_KEY="+typed))
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodDelete, cloudA.URL+"/v1/databases/production", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res, err = http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("the cloud host behind the page did not take the delete: %v", err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}

	s := state()
	if len(s.Without.Inbox) != 1 || !strings.Contains(s.Without.Inbox[0], typed) || s.Without.Production {
		t.Errorf("the world without Rehearsal: %+v, want the post in its inbox and its database gone", s.Without)
	}
	if len(s.With.Inbox) != 0 || !s.With.Production {
		t.Errorf("the world with Rehearsal: %+v, want it untouched", s.With)
	}
	// the cloud host is still the cloud host
	if _, body := get(t, cloudA.URL+"/"); !strings.Contains(body, "<strong>DELETED</strong>") {
		t.Errorf("the cloud host's own page:\n%s", body)
	}
}

// The page is two columns of plain text in the Rehearsal page's own
// colours and sizes, and what it shows is put in as text.
func TestComparePageUsesThePagesOwnPaletteAndSizes(t *testing.T) {
	w := &world{cloud: newCloud(), inbox: &inbox{}}
	srv := httptest.NewServer(comparing(w.cloud.handler(), w, w))
	defer srv.Close()
	status, page := get(t, srv.URL+"/compare")
	if status != http.StatusOK {
		t.Fatalf("the page answered %d", status)
	}
	left, right := strings.Index(page, ">Without Rehearsal<"), strings.Index(page, ">With Rehearsal<")
	if left < 0 || right < 0 || left > right {
		t.Errorf("the columns are not headed Without Rehearsal and then With Rehearsal")
	}

	theme, err := os.ReadFile("../../web/src/theme.ts")
	if err != nil {
		t.Fatal(err)
	}
	light, dark, ok := strings.Cut(string(theme), "dark: {")
	if !ok {
		t.Fatal("theme.ts has no dark palette where one was expected")
	}
	// every colour is one of the page's, under the same name, in the same theme
	lightCSS, darkCSS, ok := strings.Cut(page, "prefers-color-scheme: dark")
	if !ok {
		t.Fatal("the page has no colours for the dark theme")
	}
	token := regexp.MustCompile(`(--[a-z]+): (#[0-9a-f]{6});`)
	for css, palette := range map[string]string{lightCSS: light, darkCSS: dark} {
		found := token.FindAllStringSubmatch(css, -1)
		if len(found) != 3 {
			t.Errorf("%d colours defined for a theme, want the three the page uses: %v", len(found), found)
		}
		for _, m := range found {
			if !strings.Contains(palette, "'"+m[1]+"': '"+m[2]+"'") {
				t.Errorf("%s: %s is not that token's value in theme.ts", m[1], m[2])
			}
		}
	}
	if stray := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|rgb|hsl`).FindAllString(token.ReplaceAllString(page, ""), -1); len(stray) != 0 {
		t.Errorf("colours outside the palette: %v", stray)
	}
	for _, m := range regexp.MustCompile(`font-size: (\d+)px`).FindAllStringSubmatch(page, -1) {
		if !strings.Contains("14 18 24 32", m[1]) {
			t.Errorf("font size %spx is not one the Rehearsal page uses", m[1])
		}
	}
	for _, boxed := range []string{"border", "box-shadow", "background-image", "<img", "<svg"} {
		if strings.Contains(page, boxed) {
			t.Errorf("the page has %q: it is meant to be plain text, nothing boxed and no pictures", boxed)
		}
	}
	// what an inbox received is shown as text, never as markup
	if strings.Contains(page, "innerHTML") || !strings.Contains(page, "item.textContent = text") {
		t.Error("the page does not set what it shows as text")
	}
}

func TestTheSecondCopysPortsMayNotOverlapTheFirsts(t *testing.T) {
	for _, with := range []int{7911, 7912, 7913, 7909} {
		if err := run(7911, with); err == nil || !strings.Contains(err.Error(), "overlap") {
			t.Errorf("run(7911, %d) = %v, want it refused for overlapping", with, err)
		}
	}
}
