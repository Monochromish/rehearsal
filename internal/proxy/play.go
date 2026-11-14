package proxy

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"

	mitm "github.com/lqqyt2423/go-mitmproxy/proxy"
)

// Play puts one request to the recorder exactly as the proxy would on
// receiving it, with no network involved at either end. It is how a
// recorded session is replayed.
//
// reply is what the server would have sent back. It is looked at only if
// the request is let through, as a real reply would be. status is 0 for a
// request that was let through, and otherwise the status of the answer
// the agent was given in its place.
func (r *Recorder) Play(method, rawURL string, header http.Header, body, reply []byte) (status int, err error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return 0, fmt.Errorf("%q is not an address a request can go to", rawURL)
	}
	if header == nil {
		header = http.Header{}
	}
	f := &mitm.Flow{Request: &mitm.Request{Method: method, URL: u, Header: header, Body: body}}
	// the ID the proxy would have given it; rand.Read does not fail
	_, _ = rand.Read(f.Id[:])
	r.Request(f)
	if f.Response != nil {
		return f.Response.StatusCode, r.Err()
	}
	f.Response = &mitm.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reply}
	r.Responseheaders(f)
	r.Response(f)
	return 0, r.Err()
}
