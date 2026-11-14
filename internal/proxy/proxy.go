package proxy

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"

	mitm "github.com/lqqyt2423/go-mitmproxy/proxy"
	"github.com/sirupsen/logrus"
)

// Larger request bodies are streamed through without being searched.
const maxBodyInMemory = 32 << 20

type Proxy struct {
	URL        string
	CACertPath string // the certificate the wrapped command must trust
	p          *mitm.Proxy
	served     chan error
}

// Start generates (or reuses) the certificate authority in caDir and
// serves on a free local port. The library's own log lines go to logOut.
func Start(caDir string, rec *Recorder, logOut io.Writer) (*Proxy, error) {
	logrus.SetOutput(logOut)
	logrus.SetLevel(logrus.WarnLevel)

	// The library cannot report which port it got, so pick a free one first.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("find a free port for the proxy: %w", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		return nil, fmt.Errorf("find a free port for the proxy: %w", err)
	}

	p, err := mitm.NewProxy(&mitm.Options{Addr: addr, StreamLargeBodies: maxBodyInMemory, CaRootPath: caDir})
	if err != nil {
		return nil, fmt.Errorf("set up proxy: %w", err)
	}
	p.AddAddon(rec)
	served := make(chan error, 1)
	go func() { served <- p.Start() }()

	for range 100 {
		select {
		case err := <-served:
			return nil, fmt.Errorf("proxy stopped while starting on %s: %w", addr, err)
		default:
		}
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close() // a probe connection, nothing was sent on it
			return &Proxy{URL: "http://" + addr, CACertPath: filepath.Join(caDir, "mitmproxy-ca-cert.pem"), p: p, served: served}, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, errors.Join(fmt.Errorf("proxy did not start listening on %s", addr), p.Close())
}

// Close stops the proxy. It also reports a proxy that had already died
// during the session.
func (p *Proxy) Close() error {
	closeErr := p.p.Close()
	select {
	case err := <-p.served:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.Join(closeErr, fmt.Errorf("proxy stopped early: %w", err))
		}
	case <-time.After(2 * time.Second):
		return errors.Join(closeErr, errors.New("proxy did not stop within 2 seconds"))
	}
	return closeErr
}
