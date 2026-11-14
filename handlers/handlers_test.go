package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShippedAnswers(t *testing.T) {
	set, err := Shipped()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name               string
		method, host, path string
		status             int
		body               string
		needsRealAnswer    bool
	}{
		{"listed response", http.MethodPost, "localhost", "/upload", 201, `{"stored":true}`, false},
		{"host is matched ignoring case", http.MethodPost, "LocalHost", "/upload", 201, `{"stored":true}`, false},
		{"marked as needing the real answer", http.MethodPost, "localhost", "/tickets", 201, `{"created":true}`, true},
		{"star matches one segment", http.MethodDelete, "localhost", "/files/report.txt", 204, "", false},
		{"star does not match two segments", http.MethodDelete, "localhost", "/files/a/b", 200, "{}", false},
		{"same path, other method", http.MethodPut, "localhost", "/upload", 200, "{}", false},
		{"unlisted path", http.MethodPost, "localhost", "/elsewhere", 200, "{}", false},
		{"host with no file", http.MethodPost, "api.example.test", "/upload", 200, "{}", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := set.Answer(tt.method, tt.host, tt.path)
			if got.Status != tt.status || string(got.Body) != tt.body || got.NeedsRealAnswer != tt.needsRealAnswer {
				t.Errorf("Answer(%s %s%s) = {%d %q needs=%t}, want {%d %q needs=%t}",
					tt.method, tt.host, tt.path, got.Status, got.Body, got.NeedsRealAnswer, tt.status, tt.body, tt.needsRealAnswer)
			}
		})
	}
}

func TestWithDir(t *testing.T) {
	write := func(t *testing.T, dir, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base, err := Shipped()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("adds a host and replaces one", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "tracker.json", `{"host":"tracker.test","responses":[{"method":"POST","path":"/items","status":202,"body":"ok"}]}`)
		write(t, dir, "localhost.json", `{"host":"localhost","responses":[{"method":"POST","path":"/upload","status":418}]}`)
		set, err := base.WithDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := set.Answer("POST", "tracker.test", "/items"); got.Status != 202 || string(got.Body) != "ok" {
			t.Errorf("new host answered {%d %q}, want {202 ok}", got.Status, got.Body)
		}
		if got := set.Answer("POST", "localhost", "/upload"); got.Status != 418 {
			t.Errorf("replaced host answered %d, want 418", got.Status)
		}
		if got := base.Answer("POST", "localhost", "/upload"); got.Status != 201 {
			t.Errorf("the shipped set was changed: /upload now answers %d", got.Status)
		}
	})

	bad := []struct {
		name, content, wantErr string
	}{
		{"not JSON", `{"host":`, "unexpected EOF"},
		{"misspelt field", `{"host":"x.test","responces":[]}`, "unknown field"},
		{"no host", `{"responses":[]}`, `"host" is missing`},
		{"GET cannot be held", `{"host":"x.test","responses":[{"method":"GET","path":"/","status":200}]}`, `method "GET"`},
		{"impossible status", `{"host":"x.test","responses":[{"method":"POST","path":"/","status":99}]}`, "status 99"},
		{"path without a slash", `{"host":"x.test","responses":[{"method":"POST","path":"upload","status":200}]}`, `path "upload"`},
		{"broken pattern", `{"host":"x.test","responses":[{"method":"POST","path":"/[","status":200}]}`, `path "/["`},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "bad.json", tt.content)
			_, err := base.WithDir(dir)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), "bad.json") {
				t.Errorf("err = %v, want one naming bad.json and containing %q", err, tt.wantErr)
			}
		})
	}
}
