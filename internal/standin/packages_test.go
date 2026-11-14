package standin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"rehearsal/internal/events"
)

func TestInstallsFindsThePackagesNamed(t *testing.T) {
	tests := []struct {
		program string
		args    string
		want    []pkg // nil when the command is not an install of named packages
	}{
		{"npm", "install left-pad", []pkg{{"left-pad", ""}}},
		{"npm", "i left-pad@1.3.0 @scope/tool@2.0.0", []pkg{{"left-pad", "1.3.0"}, {"@scope/tool", "2.0.0"}}},
		{"npm", "install --save-dev left-pad", []pkg{{"left-pad", ""}}},
		{"npm", "install left-pad@^1.0.0 other@latest", []pkg{{"left-pad", ""}, {"other", ""}}},
		{"npm", "add @scope/tool", []pkg{{"@scope/tool", ""}}},
		{"npm", "install", nil},
		{"npm", "ci", nil},
		{"npm", "install ./local-folder ../other file:../x https://example.net/a.tgz git+https://example.net/r.git", nil},
		{"npm", "install --registry http://localhost:4873 left-pad", []pkg{{"left-pad", ""}}},
		{"npm", "run install-things", nil},
		{"npm", "publish", nil},
		{"pnpm", "add zod", []pkg{{"zod", ""}}},
		{"pnpm", "install", nil},
		{"yarn", "add zod@3.22.0", []pkg{{"zod", "3.22.0"}}},
		{"yarn", "install", nil},
		{"pip", "install requests", []pkg{{"requests", ""}}},
		{"pip", "install Requests==2.31.0 flask>=2", []pkg{{"requests", "2.31.0"}, {"flask", ""}}},
		{"pip3", "install requests[socks]==2.31.0", []pkg{{"requests", "2.31.0"}}},
		{"pip", "install -r requirements.txt", nil},
		{"pip", "install -e .", nil},
		{"pip", "install ./dist/thing-1.0-py3-none-any.whl", nil},
		{"pip", "list", nil},
		{"uv", "add httpx", []pkg{{"httpx", ""}}},
		{"uv", "pip install httpx==0.27.0", []pkg{{"httpx", "0.27.0"}}},
		{"uv", "pip install -r requirements.txt", nil},
		{"uv", "sync", nil},
		{"uv", "run pytest", nil},
		{"git", "add file.txt", nil},
	}
	for _, tt := range tests {
		got, _, ok := installs(tt.program, strings.Fields(tt.args))
		if ok != (tt.want != nil) || !slices.Equal(got, tt.want) {
			t.Errorf("%s %s: got %v (install: %v), want %v", tt.program, tt.args, got, ok, tt.want)
		}
	}
}

func TestLockedPackagesAreNotNew(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"package-lock.json": `{"packages": {"node_modules/left-pad": {"version": "1.3.0"}, "node_modules/@scope/tool": {}}}`,
		"yarn.lock":         "\"zod@^3.22.0\":\n  version \"3.22.4\"\n",
		"requirements.txt":  "requests==2.31.0\nFlask>=2\ntyping_extensions\n",
		"uv.lock":           "[[package]]\nname = \"httpx\"\nversion = \"0.27.0\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name   string
		python bool
		want   bool
	}{
		{"left-pad", false, true},
		{"@scope/tool", false, true},
		{"zod", false, true},
		{"left-padding", false, false},
		{"pad", false, false},
		{"requests", true, true},
		{"flask", true, true},
		{"typing-extensions", true, true},
		{"httpx", true, true},
		{"request", true, false},
		{"http", true, false},
	}
	for _, tt := range tests {
		if got := locked(dir, pkg{name: tt.name}, tt.python); got != tt.want {
			t.Errorf("locked(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
	if locked(t.TempDir(), pkg{name: "left-pad"}, false) {
		t.Error("a package was found in a folder with no lockfile")
	}
}

func TestRate(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	old := now.Add(-400 * 24 * time.Hour)
	tests := []struct {
		name  string
		p     pkg
		f     facts
		risk  events.Risk
		words string
	}{
		{"established, no scripts", pkg{name: "left-pad"}, facts{known: true, exists: true, version: "1.3.0", published: old}, events.RiskLow, "left-pad 1.3.0: published 13 months ago"},
		{"a few hours old", pkg{name: "x"}, facts{known: true, exists: true, version: "1.0.0", published: now.Add(-9 * time.Hour)}, events.RiskHigh, "x 1.0.0: published 9 hours ago"},
		{"six days old", pkg{name: "x"}, facts{known: true, exists: true, version: "1.0.0", published: now.Add(-6 * 24 * time.Hour)}, events.RiskHigh, "published 6 days ago"},
		{"eight days old", pkg{name: "x"}, facts{known: true, exists: true, version: "1.0.0", published: now.Add(-8 * 24 * time.Hour)}, events.RiskLow, "published 8 days ago"},
		{"old, with an install script", pkg{name: "x"}, facts{known: true, exists: true, version: "2.0.0", published: old, scripts: true}, events.RiskHigh, "published 13 months ago; runs a script when installed"},
		{"new, with an install script", pkg{name: "x"}, facts{known: true, exists: true, version: "2.0.0", published: now.Add(-9 * time.Hour), scripts: true}, events.RiskHigh, "published 9 hours ago; runs a script when installed"},
		{"no such package", pkg{name: "reqeusts"}, facts{known: true}, events.RiskHigh, "reqeusts: the registry has no package of that name"},
		{"no such version", pkg{name: "x", version: "9.9.9"}, facts{known: true, version: "9.9.9"}, events.RiskHigh, "x 9.9.9: the registry has no such version"},
		{"registry unreachable", pkg{name: "x"}, facts{why: "connection refused"}, events.RiskMedium, "the registry could not be asked about it (connection refused)"},
		{"no publish time", pkg{name: "x"}, facts{known: true, exists: true, version: "1.0.0"}, events.RiskMedium, "does not say when it was published"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			risk, words := rate(tt.p, tt.f, now)
			if risk != tt.risk || !strings.Contains(words, tt.words) {
				t.Errorf("rate = %s, %q; want %s and words containing %q", risk, words, tt.risk, tt.words)
			}
		})
	}
}

// registry is a local stand-in for a package registry.
func registry(t *testing.T, pages map[string]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, ok := pages[r.URL.EscapedPath()]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, page)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestLookupNpm(t *testing.T) {
	url := registry(t, map[string]string{
		"/plain": `{"dist-tags":{"latest":"1.1.0"},"time":{"1.0.0":"2020-01-01T00:00:00Z","1.1.0":"2024-03-05T10:00:00Z"},
			"versions":{"1.0.0":{},"1.1.0":{"scripts":{"test":"node test.js"}}}}`,
		"/hooked": `{"dist-tags":{"latest":"2.0.0"},"time":{"2.0.0":"2026-05-31T03:00:00Z"},
			"versions":{"2.0.0":{"scripts":{"postinstall":"node setup.js"}}}}`,
		"/flagged":         `{"dist-tags":{"latest":"1.0.0"},"time":{"1.0.0":"2024-01-01T00:00:00Z"},"versions":{"1.0.0":{"hasInstallScript":true}}}`,
		"/@scope%2ftool":   `{"dist-tags":{"latest":"3.0.0"},"time":{"3.0.0":"2023-01-01T00:00:00Z"},"versions":{"3.0.0":{}}}`,
		"/not-json-at-all": `<html>`,
	})
	t.Setenv("npm_config_registry", url+"/")
	ctx := context.Background()

	if f := lookupNpm(ctx, pkg{name: "plain"}); !f.known || !f.exists || f.version != "1.1.0" || f.scripts || f.published.Year() != 2024 {
		t.Errorf("plain = %+v, want the latest version, 2024, no install script", f)
	}
	if f := lookupNpm(ctx, pkg{name: "plain", version: "1.0.0"}); !f.exists || f.published.Year() != 2020 {
		t.Errorf("plain@1.0.0 = %+v, want that version's own date", f)
	}
	if f := lookupNpm(ctx, pkg{name: "plain", version: "7.0.0"}); !f.known || f.exists {
		t.Errorf("plain@7.0.0 = %+v, want known and not existing", f)
	}
	if f := lookupNpm(ctx, pkg{name: "hooked"}); !f.exists || !f.scripts {
		t.Errorf("hooked = %+v, want its postinstall script found", f)
	}
	if f := lookupNpm(ctx, pkg{name: "flagged"}); !f.scripts {
		t.Errorf("flagged = %+v, want hasInstallScript honoured", f)
	}
	if f := lookupNpm(ctx, pkg{name: "@scope/tool"}); !f.exists || f.version != "3.0.0" {
		t.Errorf("@scope/tool = %+v, want it found under its escaped name", f)
	}
	if f := lookupNpm(ctx, pkg{name: "ghost"}); !f.known || f.exists {
		t.Errorf("ghost = %+v, want known and not existing", f)
	}
	if f := lookupNpm(ctx, pkg{name: "not-json-at-all"}); f.known {
		t.Errorf("an unreadable answer = %+v, want not known", f)
	}
	t.Setenv("npm_config_registry", "http://127.0.0.1:1")
	if f := lookupNpm(ctx, pkg{name: "plain"}); f.known || f.why == "" {
		t.Errorf("an unreachable registry = %+v, want not known, with a reason", f)
	}
}

func TestLookupPyPI(t *testing.T) {
	url := registry(t, map[string]string{
		"/pypi/wheeled/json": `{"info":{"version":"2.0"},"releases":{"2.0":[
			{"packagetype":"sdist","upload_time_iso_8601":"2024-05-01T10:00:05Z"},
			{"packagetype":"bdist_wheel","upload_time_iso_8601":"2024-05-01T10:00:00Z"}]}}`,
		"/pypi/source-only/json": `{"info":{"version":"0.1"},"releases":{"0.1":[{"packagetype":"sdist","upload_time_iso_8601":"2026-05-31T03:00:00Z"}]}}`,
	})
	t.Setenv("PIP_INDEX_URL", url+"/simple/")
	t.Setenv("UV_INDEX_URL", "")
	t.Setenv("UV_DEFAULT_INDEX", "")
	ctx := context.Background()

	if f := lookupPyPI(ctx, pkg{name: "wheeled"}); !f.exists || f.version != "2.0" || f.scripts || f.published.Second() != 0 {
		t.Errorf("wheeled = %+v, want version 2.0, nothing run at install, the earliest upload time", f)
	}
	if f := lookupPyPI(ctx, pkg{name: "source-only"}); !f.exists || !f.scripts {
		t.Errorf("source-only = %+v, want it marked as running code when installed", f)
	}
	if f := lookupPyPI(ctx, pkg{name: "wheeled", version: "9.9"}); !f.known || f.exists {
		t.Errorf("wheeled==9.9 = %+v, want known and not existing", f)
	}
	if f := lookupPyPI(ctx, pkg{name: "ghost"}); !f.known || f.exists {
		t.Errorf("ghost = %+v, want known and not existing", f)
	}
}
