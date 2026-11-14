// Package handlers holds the stand-in answers given to held requests.
// There is one JSON file per service in this folder, named after its host.
// A request to a host with no file, or matching none of a file's
// responses, gets a plain 200 with an empty JSON object.
package handlers

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
)

//go:embed *.json
var shipped embed.FS

// File is one service's handler file.
type File struct {
	Host      string     `json:"host"`
	About     string     `json:"about"`
	Responses []Response `json:"responses"`
}

// Response is the answer for requests matching Method and Path. Path is a
// pattern in which * stands for one path segment.
type Response struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type,omitempty"`
	Body        string `json:"body,omitempty"`
	// The agent is likely to use something from the real answer (a new
	// item's number, say), which a stand-in cannot know.
	NeedsRealAnswer bool `json:"needs_real_answer,omitempty"`
}

// Answer is what the agent is told in place of the real server's reply.
type Answer struct {
	Status          int
	ContentType     string
	Body            []byte
	NeedsRealAnswer bool
}

var fallback = Answer{Status: http.StatusOK, ContentType: "application/json", Body: []byte("{}")}

var methods = []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// Set is every loaded handler file, by host.
type Set struct {
	files map[string]File
}

// Shipped loads the handler files built into the binary.
func Shipped() (*Set, error) {
	return load(shipped)
}

// WithDir adds the handler files in dir. A file for a host that already
// has one replaces it.
func (s *Set) WithDir(dir string) (*Set, error) {
	extra, err := load(os.DirFS(dir))
	if err != nil {
		return nil, fmt.Errorf("handlers in %s: %w", dir, err)
	}
	merged := &Set{files: map[string]File{}}
	for host, f := range s.files {
		merged.files[host] = f
	}
	for host, f := range extra.files {
		merged.files[host] = f
	}
	return merged, nil
}

func load(fsys fs.FS) (*Set, error) {
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	s := &Set{files: map[string]File{}}
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		var f File
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields() // a misspelt field would otherwise be silently ignored
		if err := dec.Decode(&f); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if err := f.check(); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		host := strings.ToLower(f.Host)
		if _, dup := s.files[host]; dup {
			return nil, fmt.Errorf("%s: a second file for host %q", name, f.Host)
		}
		s.files[host] = f
	}
	return s, nil
}

func (f File) check() error {
	if f.Host == "" {
		return fmt.Errorf(`"host" is missing`)
	}
	for i, r := range f.Responses {
		if !slices.Contains(methods, r.Method) {
			return fmt.Errorf("response %d: method %q is not one of %s", i+1, r.Method, strings.Join(methods, ", "))
		}
		if r.Status < 100 || r.Status > 599 {
			return fmt.Errorf("response %d: status %d is not an HTTP status", i+1, r.Status)
		}
		if _, err := path.Match(r.Path, "/"); err != nil || !strings.HasPrefix(r.Path, "/") {
			return fmt.Errorf("response %d: path %q is not a pattern starting with /", i+1, r.Path)
		}
	}
	return nil
}

// Answer picks the stand-in answer for a held request.
func (s *Set) Answer(method, hostname, requestPath string) Answer {
	f, ok := s.files[strings.ToLower(hostname)]
	if !ok {
		return fallback
	}
	for _, r := range f.Responses {
		// the pattern was checked when the file was loaded
		if matched, _ := path.Match(r.Path, requestPath); r.Method == method && matched {
			return Answer{Status: r.Status, ContentType: r.ContentType, Body: []byte(r.Body), NeedsRealAnswer: r.NeedsRealAnswer}
		}
	}
	return fallback
}

// Hosts lists the hosts that have a handler file.
func (s *Set) Hosts() []string {
	hosts := make([]string, 0, len(s.files))
	for h := range s.files {
		hosts = append(hosts, h)
	}
	slices.Sort(hosts)
	return hosts
}
