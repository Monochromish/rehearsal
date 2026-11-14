package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The app window is another program. It is started in its own folder with
// the address the session is served at as an argument, since it may not
// be handed this process's environment. A name that leads to no program
// is an error, so that the caller falls back to the browser.
func TestTheAppWindowIsStartedWithTheSessionsAddress(t *testing.T) {
	dir := t.TempDir()
	got := filepath.Join(dir, "got.txt")
	app := filepath.Join(dir, "app.sh")
	if err := os.WriteFile(app, []byte("#!/bin/sh\necho \"$1 $2 in $(pwd)\" > \""+got+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := openApp(app, "http://localhost:7950"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(got)
		if err == nil && strings.TrimSpace(string(data)) == "-url http://localhost:7950 in "+dir {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the app was not started with the address: %q (%v)", data, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	notAProgram := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notAProgram, []byte("words\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{filepath.Join(dir, "no-such-app"), dir, notAProgram} {
		if err := openApp(missing, "http://localhost:7950"); err == nil {
			t.Errorf("openApp(%s) succeeded, want an error so the browser is used", missing)
		}
	}
}
