// Package loopback connects to names under .localhost, which are reserved
// to mean this computer (RFC 6761). Go asks the system's resolver for them
// like any other name, and not every system answers: WSL's does not. Here
// such a name is never looked up. It is connected to on the loopback
// address, as curl and browsers do.
package loopback

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Named reports whether host, given without a port, is a name under
// .localhost. Plain "localhost" is not one: every system resolves that.
func Named(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return strings.HasSuffix(host, ".localhost") && host != ".localhost"
}

// DialContext connects to addr as a net.Dialer does, except that a name
// under .localhost is connected to on 127.0.0.1, or on ::1 when nothing
// answers there, without being looked up.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	host, port, err := net.SplitHostPort(addr)
	if err != nil || !Named(host) {
		return d.DialContext(ctx, network, addr)
	}
	conn, err := d.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		if conn6, err6 := d.DialContext(ctx, network, net.JoinHostPort("::1", port)); err6 == nil {
			return conn6, nil
		}
	}
	return conn, err
}

// Relay is a proxy that connects only to names under .localhost. The proxy
// library makes its own connections and offers no way to change how, but
// it will go through another proxy when told to, so it is told to use
// this one for those names.
type Relay struct {
	// the address to give as the upstream proxy
	URL *url.URL
	l   net.Listener
}

// NewRelay starts a relay on a free port of this computer.
func NewRelay() (*Relay, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for the .localhost relay: %w", err)
	}
	r := &Relay{URL: &url.URL{Scheme: "http", Host: l.Addr().String()}, l: l}
	go r.serve()
	return r, nil
}

func (r *Relay) Close() error {
	return r.l.Close()
}

func (r *Relay) serve() {
	for {
		conn, err := r.l.Accept()
		if err != nil {
			return // closed
		}
		go relay(conn)
	}
}

// how long a client has to say where it wants to go
const askTimeout = 10 * time.Second

// relay answers one CONNECT request. Anything else, and any destination
// that is not under .localhost, is refused without a connection being made.
func relay(client net.Conn) {
	defer client.Close() // nothing to report to: the client sees the connection end
	if err := client.SetReadDeadline(time.Now().Add(askTimeout)); err != nil {
		return
	}
	in := bufio.NewReader(client)
	req, err := http.ReadRequest(in)
	if err != nil {
		return
	}
	host, _, err := net.SplitHostPort(req.Host)
	if req.Method != http.MethodConnect || err != nil || !Named(host) {
		_, _ = io.WriteString(client, "HTTP/1.1 403 Forbidden\r\n\r\n") // the client is being turned away either way
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	server, err := DialContext(ctx, "tcp", req.Host)
	cancel()
	if err != nil {
		_, _ = io.WriteString(client, "HTTP/1.1 502 Bad Gateway\r\n\r\n") // as above
		return
	}
	defer server.Close() // closed again below to end the copy; the second close only reports that
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := client.SetReadDeadline(time.Time{}); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(server, in) // ends when either side closes; there is no one to tell
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, server) // as above
		done <- struct{}{}
	}()
	<-done
	// closing both ends the other copy
	_ = errors.Join(client.Close(), server.Close())
	<-done
}
