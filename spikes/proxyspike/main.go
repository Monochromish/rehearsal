// Throwaway feasibility spike (tests A, B, C): run a command behind an
// intercepting proxy built on go-mitmproxy and log what it sends.
//
// Usage: proxyspike [-log FILE] [-shim DIR] [-insecure] -- COMMAND [ARGS...]
//
// Logs host, method, path and body size only. Never logs headers.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
	"github.com/sirupsen/logrus"
)

type sseStat struct {
	start  time.Time
	first  time.Duration
	last   time.Duration
	events int
}

type spikeAddon struct {
	proxy.BaseAddon
	out   *os.File
	mu    sync.Mutex
	sse   map[*proxy.Flow]*sseStat
	total atomic.Int64
}

func (a *spikeAddon) logf(format string, args ...any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err := fmt.Fprintf(a.out, time.Now().Format("15:04:05.000")+" "+format+"\n", args...)
	must(err)
}

func (a *spikeAddon) Request(f *proxy.Flow) {
	a.total.Add(1)
	r := f.Request
	a.logf("REQ  %s %s %s reqBytes=%d%s", r.URL.Host, r.Method, r.URL.Path, len(r.Body), describeBody(r.Body))
}

// describeBody shows the body is readable without printing it: for a JSON
// object it reports only the model name, the stream flag and the number of
// messages.
func describeBody(body []byte) string {
	var probe struct {
		Model    string            `json:"model"`
		Stream   *bool             `json:"stream"`
		Messages []json.RawMessage `json:"messages"`
	}
	// most bodies are not this shape, which is fine: say nothing about them
	if len(body) == 0 || json.Unmarshal(body, &probe) != nil || probe.Model == "" {
		return ""
	}
	stream := probe.Stream != nil && *probe.Stream
	return fmt.Sprintf(" [readable: model=%s stream=%t messages=%d]", probe.Model, stream, len(probe.Messages))
}

func (a *spikeAddon) Response(f *proxy.Flow) {
	a.logf("RESP %s %s %s status=%d respBytes=%d", f.Request.URL.Host, f.Request.Method, f.Request.URL.Path, f.Response.StatusCode, len(f.Response.Body))
}

func (a *spikeAddon) SSEStart(f *proxy.Flow) {
	a.mu.Lock()
	a.sse[f] = &sseStat{start: time.Now()}
	a.mu.Unlock()
	a.logf("SSE  %s %s status=%d stream started", f.Request.URL.Host, f.Request.URL.Path, f.Response.StatusCode)
}

func (a *spikeAddon) SSEMessage(f *proxy.Flow) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sse[f]
	if s == nil {
		return
	}
	s.events++
	s.last = time.Since(s.start)
	if s.events == 1 {
		s.first = s.last
	}
}

func (a *spikeAddon) SSEEnd(f *proxy.Flow) {
	a.mu.Lock()
	s := a.sse[f]
	delete(a.sse, f)
	a.mu.Unlock()
	if s == nil {
		return
	}
	a.logf("SSE  %s %s stream ended events=%d firstEvent=+%dms lastEvent=+%dms", f.Request.URL.Host, f.Request.URL.Path, s.events, s.first.Milliseconds(), s.last.Milliseconds())
}

func (a *spikeAddon) WebSocketStart(f *proxy.Flow) {
	a.logf("WS   %s %s websocket opened", f.Request.URL.Host, f.Request.URL.Path)
}

func (a *spikeAddon) RequestError(f *proxy.Flow, err error) {
	a.logf("ERR  %s %s %s: %v", f.Request.URL.Host, f.Request.Method, f.Request.URL.Path, err)
}

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	port := l.Addr().(*net.TCPAddr).Port
	must(l.Close())
	return port
}

func main() {
	logPath := flag.String("log", "", "file to write the request log to (default: a temp file)")
	shimDir := flag.String("shim", "", "directory of stand-in commands to put first on PATH")
	insecure := flag.Bool("insecure", false, "do not verify upstream certificates (local test servers only)")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: proxyspike [-log FILE] [-shim DIR] [-insecure] -- COMMAND [ARGS...]")
		os.Exit(2)
	}

	// The CA, private key included, lives in a temp dir, never in the repo.
	stateDir, err := os.MkdirTemp("", "rehearsal-spike-")
	must(err)
	if *logPath == "" {
		*logPath = filepath.Join(stateDir, "requests.log")
	}
	out, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	must(err)
	logrus.SetOutput(out) // keep the library's own logging off the child's terminal
	logrus.SetLevel(logrus.WarnLevel)

	addr := fmt.Sprintf("127.0.0.1:%d", freePort())
	p, err := proxy.NewProxy(&proxy.Options{
		Addr:              addr,
		StreamLargeBodies: 5 << 20,
		SslInsecure:       *insecure,
		CaRootPath:        stateDir,
	})
	must(err)
	addon := &spikeAddon{out: out, sse: map[*proxy.Flow]*sseStat{}}
	p.AddAddon(addon)
	go func() { must(p.Start()) }()
	waitForPort(addr)

	caCert := filepath.Join(stateDir, "mitmproxy-ca-cert.pem")
	proxyURL := "http://" + addr
	env := os.Environ()
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		env = append(env, k+"="+proxyURL)
	}
	for _, k := range []string{"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"} {
		env = append(env, k+"="+caCert)
	}
	if *shimDir != "" {
		abs, err := filepath.Abs(*shimDir)
		must(err)
		env = append(env, "PATH="+abs+":"+os.Getenv("PATH"), "REHEARSAL_SHIM_LOG="+*logPath)
	}

	fmt.Fprintf(os.Stderr, "[spike] proxy %s  log %s\n", proxyURL, *logPath)
	cmd := exec.Command(flag.Arg(0), flag.Args()[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = env
	runErr := cmd.Run()
	time.Sleep(200 * time.Millisecond) // let in-flight log lines land
	fmt.Fprintf(os.Stderr, "[spike] child exited; %d requests seen; log %s\n", addon.total.Load(), *logPath)
	must(out.Close())
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		os.Exit(exit.ExitCode())
	}
	must(runErr)
}

func waitForPort(addr string) {
	for range 50 {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			must(c.Close())
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	must(fmt.Errorf("proxy did not start listening on %s", addr))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "[spike] error:", err)
		os.Exit(1)
	}
}
