package loopback

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNamed(t *testing.T) {
	for host, want := range map[string]bool{
		"cloud.localhost":          true,
		"attacker-inbox.localhost": true,
		"a.b.LOCALHOST":            true,
		"cloud.localhost.":         true,
		"localhost":                false,
		".localhost":               false,
		"localhost.example.test":   false,
		"notlocalhost":             false,
		"127.0.0.1":                false,
		"":                         false,
	} {
		if got := Named(host); got != want {
			t.Errorf("Named(%q) = %v, want %v", host, got, want)
		}
	}
}

// counting is a server on 127.0.0.1 that counts the connections made to it.
func counting(t *testing.T) (port string, connections *atomic.Int32) {
	t.Helper()
	connections = &atomic.Int32{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "reached as "+r.Host) // fails only when the client has gone
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port, connections
}

// The name is made up, so a system resolver that knows nothing of
// .localhost would fail it. It must be reached all the same.
func TestDialContextReachesANameUnderLocalhostWithoutLookingItUp(t *testing.T) {
	port, _ := counting(t)
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: DialContext}, Timeout: 10 * time.Second}
	res, err := client.Get("http://no-such-name-anywhere.localhost:" + port + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	if cerr := res.Body.Close(); err != nil || cerr != nil {
		t.Fatal(err, cerr)
	}
	if want := "reached as no-such-name-anywhere.localhost:" + port; string(body) != want {
		t.Errorf("body = %q, want %q", body, want)
	}
	// any other name still goes to the resolver, which has never heard of this one
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if conn, err := DialContext(ctx, "tcp", "no-such-name.invalid:"+port); err == nil {
		_ = conn.Close()
		t.Error("a name outside .localhost was connected to without a lookup")
	}
}

// ask sends one request line to the relay and returns the status line.
func ask(t *testing.T, r *Relay, request string) (net.Conn, *bufio.Reader, string) {
	t.Helper()
	conn, err := net.Dial("tcp", r.URL.Host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	in := bufio.NewReader(conn)
	status, err := in.ReadString('\n')
	if err != nil {
		t.Fatalf("no answer to %q: %v", request, err)
	}
	return conn, in, strings.TrimSpace(status)
}

func TestRelayConnectsOnlyToNamesUnderLocalhost(t *testing.T) {
	port, connections := counting(t)
	r, err := NewRelay()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	}()

	conn, in, status := ask(t, r, "CONNECT shop.localhost:"+port+" HTTP/1.1\r\nHost: shop.localhost:"+port+"\r\n\r\n")
	if status != "HTTP/1.1 200 Connection Established" {
		t.Fatalf("CONNECT to a name under .localhost answered %q", status)
	}
	if _, err := in.ReadString('\n'); err != nil { // the blank line that ends the answer
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: shop.localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	reply, err := io.ReadAll(in)
	if err != nil || !strings.Contains(string(reply), "reached as shop.localhost") {
		t.Errorf("through the relay the server answered %q (%v)", reply, err)
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("the server saw %d connections, want 1", got)
	}

	// everything else is turned away, and nothing is connected to
	for _, request := range []string{
		"CONNECT 127.0.0.1:" + port + " HTTP/1.1\r\nHost: 127.0.0.1:" + port + "\r\n\r\n",
		"CONNECT localhost:" + port + " HTTP/1.1\r\nHost: localhost:" + port + "\r\n\r\n",
		"CONNECT example.test:" + port + " HTTP/1.1\r\nHost: example.test:" + port + "\r\n\r\n",
		"GET http://shop.localhost:" + port + "/ HTTP/1.1\r\nHost: shop.localhost:" + port + "\r\n\r\n",
	} {
		if _, _, status := ask(t, r, request); status != "HTTP/1.1 403 Forbidden" {
			t.Errorf("%q answered %q, want 403", strings.SplitN(request, "\r\n", 2)[0], status)
		}
	}
	if got := connections.Load(); got != 1 {
		t.Errorf("the server saw %d connections after the refusals, want still 1", got)
	}

	// a name under .localhost with nothing listening is a gateway error, not a hang
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := free.Addr().(*net.TCPAddr).Port
	if err := free.Close(); err != nil {
		t.Fatal(err)
	}
	target := net.JoinHostPort("nothing.localhost", strconv.Itoa(closed))
	if _, _, status := ask(t, r, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n"); status != "HTTP/1.1 502 Bad Gateway" {
		t.Errorf("CONNECT to a closed port answered %q, want 502", status)
	}
}
