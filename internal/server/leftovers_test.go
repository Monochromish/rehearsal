package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"rehearsal/internal/leftover"
)

// stopper stands in for the tracker: one process, and a note of what it
// was asked to stop.
type stopper struct {
	list  leftover.List
	asked [][]string
}

func (s *stopper) List() leftover.List { return s.list }

func (s *stopper) Stop(ids []string) ([]leftover.Process, error) {
	if !s.list.Listed {
		return nil, leftover.ErrNotListed
	}
	s.asked = append(s.asked, ids)
	out := []leftover.Process{}
	for _, id := range ids {
		at := slices.IndexFunc(s.list.Processes, func(p leftover.Process) bool { return p.ID == id })
		if at < 0 {
			return nil, errors.New("not a process the agent left running")
		}
		s.list.Processes[at].Status = leftover.Stopped
		out = append(out, s.list.Processes[at])
	}
	return out, nil
}

func TestLeftoversAreListedAndStoppedOnlyByThePage(t *testing.T) {
	log := recorded(t, t.TempDir(), "20260101-090000-aaaaaa", "/home/ada/shop", nil)
	defer func() {
		if err := log.Close(); err != nil {
			t.Error(err)
		}
	}()
	s, err := Start(log, fstest.MapFS{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()

	post := func(body string, header ...string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, s.URL+"/api/leftovers/stop", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", s.URL)
		req.Header.Set("Content-Type", "application/json")
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		text, err := io.ReadAll(res.Body)
		if cerr := res.Body.Close(); err != nil || cerr != nil {
			t.Fatal(err, cerr)
		}
		return res.StatusCode, string(text)
	}

	// a session that follows nothing answers with an empty list, in full
	var list leftover.List
	getInto(t, s.URL+"/api/leftovers", &list)
	if list.Listed || list.Open || list.Processes == nil || len(list.Processes) != 0 {
		t.Errorf("with nothing followed: %+v", list)
	}
	if status, _ := post(`{"stop":[]}`); status != http.StatusConflict {
		t.Errorf("a stop with nothing followed was answered %d, want 409", status)
	}

	fake := &stopper{list: leftover.List{Processes: []leftover.Process{}}}
	s.ShowLeftovers(fake)
	if status, _ := post(`{"stop":[]}`); status != http.StatusConflict {
		t.Errorf("a stop while the agent runs was answered %d, want 409", status)
	}

	fake.list = leftover.List{Listed: true, Open: true, Processes: []leftover.Process{{
		ID: "p1", PID: 4312, Command: "python3 -m http.server 8000", Started: time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC),
		Seconds: 250, Ports: []int{8000}, Status: leftover.Running,
	}}}
	status, body := get(t, s.URL+"/api/leftovers")
	const want = `{"listed":true,"open":true,"processes":[{"id":"p1","pid":4312,"command":"python3 -m http.server 8000","started":"2026-01-10T09:00:00Z","seconds":250,"ports":[8000],"status":"running"}]}`
	if status != http.StatusOK || body != want {
		t.Errorf("the list was answered %d:\n%s\nwant\n%s", status, body, want)
	}

	// only this page may stop anything, and only by naming what is listed
	for name, c := range map[string]struct {
		body   string
		header []string
		status int
	}{
		"another site":   {`{"stop":["p1"]}`, []string{"Origin", "http://example.net"}, http.StatusForbidden},
		"a form":         {`stop=p1`, []string{"Content-Type", "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType},
		"an extra field": {`{"stop":["p1"],"pid":1}`, nil, http.StatusBadRequest},
		"not listed":     {`{"stop":["4312"]}`, nil, http.StatusBadRequest},
	} {
		if got, text := post(c.body, c.header...); got != c.status {
			t.Errorf("%s: answered %d (%s), want %d", name, got, strings.TrimSpace(text), c.status)
		}
	}
	if fake.list.Processes[0].Status != leftover.Running {
		t.Fatal("a refused request stopped the process")
	}

	status, body = post(`{"stop":["p1"]}`)
	var answer struct {
		Processes []leftover.Process `json:"processes"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil || status != http.StatusOK {
		t.Fatalf("stop answered %d: %s (%v)", status, body, err)
	}
	if len(answer.Processes) != 1 || answer.Processes[0].PID != 4312 || answer.Processes[0].Status != leftover.Stopped {
		t.Errorf("stop reported %+v", answer.Processes)
	}
}
