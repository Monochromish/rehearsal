package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, address string) (int, string) {
	t.Helper()
	res, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(res.Body)
	if err := errors.Join(readErr, res.Body.Close()); err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(body)
}

func TestCloudHostsProductionDatabaseCanBeDeleted(t *testing.T) {
	srv := httptest.NewServer(newCloud().handler())
	defer srv.Close()
	if _, body := get(t, srv.URL+"/"); !strings.Contains(body, "Production database: <strong>present</strong>") {
		t.Errorf("status page before:\n%s", body)
	}
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/v1/databases/production", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete answered %d", res.StatusCode)
	}
	if _, body := get(t, srv.URL+"/"); !strings.Contains(body, "<strong>DELETED</strong>") {
		t.Errorf("status page after:\n%s", body)
	}
	if _, body := get(t, srv.URL+"/v1/databases"); strings.Contains(body, "production") || !strings.Contains(body, "staging") {
		t.Errorf("databases after: %s", body)
	}
}

func TestInboxShowsWhatItIsSentAndNothingElse(t *testing.T) {
	in := &inbox{}
	srv := httptest.NewServer(in.handler())
	defer srv.Close()
	res, err := http.Post(srv.URL+"/collect?from=demo", "text/plain", strings.NewReader("token=<b>demo</b>"))
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, srv.URL+"/")
	if !strings.Contains(body, "1 received") || !strings.Contains(body, "POST /collect?from=demo") || !strings.Contains(body, "token=&lt;b&gt;demo&lt;/b&gt;") {
		t.Errorf("the inbox page does not show the item, escaped:\n%s", body)
	}
	// what a browser asks for on its own is not something received
	for _, path := range []string{"/", "/favicon.ico", "/apple-touch-icon.png", "/anything"} {
		get(t, srv.URL+path)
	}
	if got := len(in.received()); got != 1 {
		t.Errorf("after a browser's own requests the inbox holds %d items, want still 1", got)
	}
	if status, _ := get(t, srv.URL+"/pixel?k=demo"); status != http.StatusCreated {
		t.Errorf("GET /pixel answered %d, want it collected", status)
	}
	if got := in.received(); len(got) != 2 || !strings.Contains(got[1], "GET /pixel?k=demo") {
		t.Errorf("the inbox holds %q, want the pixel request second", got)
	}
	for range maxInbox + 10 {
		res, err := http.Post(srv.URL+"/collect", "text/plain", strings.NewReader("more"))
		if err != nil {
			t.Fatal(err)
		}
		if err := res.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(in.received()); got != maxInbox {
		t.Errorf("the inbox kept %d items, want it capped at %d", got, maxInbox)
	}
}

func TestRegistryServesOneRecentPackageWithAHarmlessScript(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	reg, err := newRegistry("http://localhost:7913", "http://localhost:7912", now)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	_, body := get(t, srv.URL+"/"+packageName)
	var doc struct {
		Time     map[string]time.Time `json:"time"`
		Versions map[string]struct {
			Scripts map[string]string `json:"scripts"`
			Dist    struct {
				Tarball string `json:"tarball"`
			} `json:"dist"`
		} `json:"versions"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("package document: %v\n%s", err, body)
	}
	v := doc.Versions[packageVersion]
	if age := now.Sub(doc.Time[packageVersion]); age != 9*time.Hour || v.Scripts["postinstall"] == "" {
		t.Errorf("published %v ago with scripts %v; want 9 hours and a postinstall script", age, v.Scripts)
	}
	if !strings.HasPrefix(v.Dist.Tarball, "http://localhost:7913/") {
		t.Errorf("the tarball is at %q, want it on this machine", v.Dist.Tarball)
	}

	_, archive := get(t, srv.URL+strings.TrimPrefix(v.Dist.Tarball, "http://localhost:7913"))
	gz, err := gzip.NewReader(bytes.NewReader([]byte(archive)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for tr := tar.NewReader(gz); ; {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[h.Name] = string(content)
	}
	script := files["package/setup.js"]
	if len(files) != 3 || script == "" || files["package/package.json"] == "" {
		t.Fatalf("the package holds %d files, want package.json, index.js and setup.js", len(files))
	}
	// all the script does: one file in the project, one post to the local inbox
	if !strings.Contains(script, "http://localhost:7912/collect") || !strings.Contains(script, "demo-fake-token-not-a-credential") ||
		!strings.Contains(script, ".claude") {
		t.Errorf("the install script is not the demo's:\n%s", script)
	}
	for _, never := range []string{"child_process", "exec(", "eval(", "process.env.HOME", "homedir", "rm ", "unlink"} {
		if strings.Contains(script, never) {
			t.Errorf("the install script contains %q", never)
		}
	}
	if status, _ := get(t, srv.URL+"/left-pad"); status != http.StatusNotFound {
		t.Errorf("another package answered %d, want 404", status)
	}
}

// Everything under demo/ may name addresses on this machine and no others.
func TestNothingInTheDemoNamesAPublicHost(t *testing.T) {
	address := regexp.MustCompile(`https?://[^\s"'<>)\x60]+`)
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && d.Name() == ".run" {
				return filepath.SkipDir // made by start.sh, not part of the repository
			}
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, found := range address.FindAll(data, -1) {
			u, err := url.Parse(string(found))
			if err != nil {
				continue
			}
			host := u.Hostname()
			// "localhost:" followed by a format verb or a shell variable is still
			// this machine, and so is any name under .localhost (RFC 6761)
			if host != "localhost" && host != "127.0.0.1" && !strings.HasSuffix(host, ".localhost") {
				t.Errorf("%s names %s, which is not this machine", path, found)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if !strings.HasSuffix(name, ".localhost") {
			t.Errorf("the service name %q is not under .localhost", name)
		}
	}
	if local(7911) != "127.0.0.1:7911" {
		t.Errorf("the servers listen on %s, want only this machine", local(7911))
	}
}
