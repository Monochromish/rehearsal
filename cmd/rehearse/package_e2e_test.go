package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rehearsal/internal/events"
	"rehearsal/internal/ledger"
)

// fakeProgram puts a script called name on a fresh folder for PATH. It
// stands in for the real package manager, and writes its arguments to a
// file so the test can tell whether an install went ahead.
func fakeProgram(t *testing.T, name string) (dir, ran string) {
	t.Helper()
	dir = t.TempDir()
	ran = filepath.Join(t.TempDir(), "ran.txt")
	script := "#!/bin/sh\necho \"$@\" >> " + ran + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, ran
}

func TestPackageInstallsAreCheckedAgainstTheRegistry(t *testing.T) {
	recent := time.Now().Add(-9*time.Hour - 10*time.Minute).UTC().Format(time.RFC3339)
	pages := map[string]string{
		"/fresh-pkg": `{"dist-tags":{"latest":"1.0.0"},"time":{"1.0.0":"` + recent + `"},"versions":{"1.0.0":{"scripts":{"postinstall":"node setup.js"}}}}`,
		"/old-pkg":   `{"dist-tags":{"latest":"4.2.0"},"time":{"4.2.0":"2021-02-03T04:05:06Z"},"versions":{"4.2.0":{"scripts":{"test":"node t.js"}}}}`,
	}
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, page)
	}))
	t.Cleanup(registry.Close)
	address := strings.Replace(registry.URL, "127.0.0.1", "localhost", 1)

	bin, ran := fakeProgram(t, "npm")
	work := t.TempDir()
	lock := `{"packages": {"node_modules/known-pkg": {"version": "1.0.0"}}}`
	if err := os.WriteFile(filepath.Join(work, "package-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	script := strings.Join([]string{
		`npm install fresh-pkg; echo "fresh exit $?"`,
		`npm install old-pkg; echo "old exit $?"`,
		`npm install ghost-pkg; echo "ghost exit $?"`,
		`npm install known-pkg; echo "known exit $?"`,
		`npm install; echo "bare exit $?"`,
	}, "; ")
	cmd := exec.Command(buildRehearse(t), "--no-open", "--", "sh", "-c", script)
	cmd.Dir = work
	cmd.Env = cleanEnv(t, "npm_config_registry="+address, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	if err != nil {
		t.Fatalf("rehearse: %v\n%s", err, out)
	}

	for _, want := range []string{
		"fresh exit 1", "old exit 0", "ghost exit 1", "known exit 0", "bare exit 0",
		"this npm install was stopped before it ran",
		"fresh-pkg 1.0.0: published 9 hours ago; runs a script when installed",
		"ghost-pkg: the registry has no package of that name",
		"Nothing was installed.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	went := read(t, ran)
	if went != "install old-pkg\ninstall known-pkg\ninstall\n" {
		t.Errorf("the real npm was run with:\n%swant only the established package, the locked one and the bare install", went)
	}

	var l ledger.Ledger
	data, err := os.ReadFile(sessionFile(t, cmd.Env, "rehearsal-ledger.json"))
	if err != nil || json.Unmarshal(data, &l) != nil {
		t.Fatalf("ledger: %v", err)
	}
	type row struct {
		name    string
		risk    events.Risk
		blocked bool
	}
	var got []row
	for _, p := range l.Packages {
		got = append(got, row{p.Name, p.Risk, p.Blocked})
	}
	want := []row{{"fresh-pkg", events.RiskHigh, true}, {"old-pkg", events.RiskLow, false}, {"ghost-pkg", events.RiskHigh, true}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("packages in the ledger = %v, want %v (the locked one is not looked up)", got, want)
	}
	// the registry was asked through the proxy, so the questions are in the ledger
	host := strings.TrimPrefix(address, "http://")
	asked := 0
	for _, s := range l.Servers {
		if s.Host == host {
			asked = s.Requests
		}
	}
	if asked != 3 {
		t.Errorf("the ledger shows %d requests to the registry, want 3: %+v", asked, l.Servers)
	}
	report := read(t, sessionFile(t, cmd.Env, "rehearsal-report.md"))
	if !strings.Contains(report, "## Packages the agent asked to install") || !strings.Contains(report, "high risk, stopped") {
		t.Errorf("the report does not list the packages:\n%s", report)
	}
}

func TestPipInstallOfANonExistentPackageIsStopped(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pypi/requests/json" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"info":{"version":"2.31.0"},"releases":{"2.31.0":[{"packagetype":"bdist_wheel","upload_time_iso_8601":"2023-05-22T15:12:44Z"}]}}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(registry.Close)
	address := strings.Replace(registry.URL, "127.0.0.1", "localhost", 1)

	bin, ran := fakeProgram(t, "pip")
	cmd := exec.Command(buildRehearse(t), "--no-open", "--", "sh", "-c",
		`pip install reqeusts; echo "typo exit $?"; pip install requests==2.31.0; echo "real exit $?"`)
	cmd.Dir = t.TempDir()
	cmd.Env = cleanEnv(t, "PIP_INDEX_URL="+address+"/simple", "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	if err != nil {
		t.Fatalf("rehearse: %v\n%s", err, out)
	}
	for _, want := range []string{"typo exit 1", "real exit 0", "reqeusts: the registry has no package of that name"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if went := read(t, ran); went != "install requests==2.31.0\n" {
		t.Errorf("the real pip was run with %q, want only the package that exists", went)
	}
}
