package main

import (
	"net"
	"strings"
	"testing"

	"rehearsal/internal/ledger"
)

// A name under .localhost is this computer whatever the system's resolver
// says. The name here is made up, so where the resolver does not know
// .localhost this passes only because rehearse never asks it: once in the
// proxy, for the read that goes straight through, and once when rehearse
// itself sends the write that was held and then allowed.
func TestNamesUnderLocalhostReachThisComputer(t *testing.T) {
	needCurl(t)
	srv := newUploads(t)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	host := "rehearsal-test-shop.localhost:" + port
	s := start(t, t.TempDir(), `echo "read got $(curl -sS -o /dev/null -w '%{http_code}' http://`+host+`/list)"; curl -sS -o /dev/null -d order=1 http://`+host+`/upload`)

	v := s.waitFor(ledger.PhaseDeciding)
	if !strings.Contains(s.output.String(), "read got 201") {
		t.Fatalf("the read did not reach the server through the proxy:\n%s", s.output)
	}
	if got := srv.received(); len(got) != 1 || got[0] != "GET /list " {
		t.Fatalf("before the decision the server received %q, want only the read", got)
	}
	if len(v.Actions) != 1 || v.Actions[0].Host != host || v.Actions[0].Destination != host {
		t.Fatalf("held %+v, want the one write, to %s by that name", v.Actions, host)
	}

	s.approve(v.Actions[0].ID)
	code, l := s.finish()
	if got := srv.received(); len(got) != 2 || got[1] != "POST /upload order=1" {
		t.Errorf("after approval the server received %q, want the write as well", got)
	}
	if code != 0 || l.Actions[0].Outcome != ledger.Applied {
		t.Errorf("exit %d, outcome %q (%s); want 0 and applied", code, l.Actions[0].Outcome, l.Actions[0].Detail)
	}
	if len(l.Servers) != 1 || l.Servers[0].Host != host || l.Servers[0].Requests != 2 {
		t.Errorf("ledger servers = %+v, want %s with both requests", l.Servers, host)
	}
}
