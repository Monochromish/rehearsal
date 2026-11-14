package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"rehearsal/internal/events"
)

// The test binary stands in for rehearse as the sandbox's first process.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == InitArg {
		os.Exit(Init(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func TestSensitive(t *testing.T) {
	tests := []struct {
		path string
		want string // a word from the reason, "" for not sensitive
	}{
		{".claude/settings.json", "agent's own settings"},
		{".claude/settings.local.json", "agent's own settings"},
		{".claude/hooks/pre-tool.sh", "agent's own settings"},
		{"packages/web/.claude/settings.json", "agent's own settings"},
		{".mcp.json", "agent's own settings"},
		{".rehearsal/policy.yaml", "Rehearsal's own rules"},
		{".bashrc", "shell start-up"},
		{"dotfiles/.zshrc", "shell start-up"},
		{".config/fish/config.fish", "shell start-up"},
		{".git/config", "git's settings or hooks"},
		{".git/hooks/pre-commit", "git's settings or hooks"},
		{".husky/pre-push", "git's settings or hooks"},
		{".gitconfig", "git's settings or hooks"},
		{".github/workflows/ci.yml", "automated builds"},
		{".gitlab-ci.yml", "automated builds"},
		{"Jenkinsfile", "automated builds"},
		{".circleci/config.yml", "automated builds"},
		{".ssh/config", "SSH"},
		{".ssh/authorized_keys", "SSH"},
		{"deploy/authorized_keys", "SSH"},
		{".config/systemd/user/sync.service", "starts by itself"},
		{".config/autostart/app.desktop", "starts by itself"},

		{"README.md", ""},
		{"src/settings.json", ""},
		{".claude/notes.md", ""},
		{".git/objects/ab/cdef", ""},
		{".git/HEAD", ""},
		{".github/ISSUE_TEMPLATE.md", ""},
		{"docs/bashrc.md", ""},
		{"my.gitconfig.bak", ""},
		{"src/hooks/useThing.ts", ""},
	}
	for _, tt := range tests {
		got := Sensitive(tt.path)
		if (tt.want == "") != (got == "") || !strings.Contains(got, tt.want) {
			t.Errorf("Sensitive(%q) = %q, want a reason containing %q", tt.path, got, tt.want)
		}
	}
}

// project makes a real project folder with a few files in it.
func project(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "project")
	for name, content := range map[string]string{
		"a.txt":             "one\n",
		"b.txt":             "two\n",
		"same.txt":          "same\n",
		"tool.sh":           "#!/bin/sh\n",
		"old/x.txt":         "x\n",
		"old/deep/y.txt":    "y\n",
		"swap/in-real.txt":  "r\n",
		".git/HEAD":         "ref: refs/heads/main\n",
		".git/config":       "[core]\n",
		".git/objects/keep": "k\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// boxed runs script in a sandbox over a fresh project and returns the box,
// still holding its layer, and the script's output.
func boxed(t *testing.T, proj, script string, writable ...string) (*Box, string) {
	t.Helper()
	if err := Available(); err != nil {
		t.Skipf("no sandbox on this machine: %v", err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hidden := filepath.Join(t.TempDir(), "hidden")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "secret.txt"), []byte("ours\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := New(Options{
		Project: proj, Dir: filepath.Join(t.TempDir(), "box"), Self: self,
		Hide: []string{hidden}, Visible: []string{filepath.Dir(self)}, Writable: writable,
		Since: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Errorf("close the box: %v", err)
		}
		if err := b.Remove(); err != nil {
			t.Errorf("remove the box: %v", err)
		}
	})
	args := b.Command([]string{"sh", "-c", script, "sh", hidden})
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the sandboxed command failed: %v\n%s", err, out)
	}
	return b, string(out)
}

func find(t *testing.T, changes []Change, path string) Change {
	t.Helper()
	for _, c := range changes {
		if c.Path == path {
			return c
		}
	}
	var have []string
	for _, c := range changes {
		have = append(have, c.Change+" "+c.Path)
	}
	t.Fatalf("no change for %s; have %v", path, have)
	return Change{}
}

func TestChangesAreReadFromTheLayer(t *testing.T) {
	proj := project(t)
	b, out := boxed(t, proj, `
		echo changed > a.txt
		rm b.txt
		echo same > same.txt
		chmod +x tool.sh
		rm -rf old
		mkdir -p new/sub && echo n > new/n.txt && echo s > new/sub/s.txt && echo K > new/.env
		rm -rf swap && mkdir swap && echo fresh > swap/fresh.txt
		echo obj > .git/objects/new && echo 'ref: refs/heads/other' > .git/HEAD
		mkdir -p .git/hooks && echo '#!/bin/sh' > .git/hooks/pre-push
		echo '[alias]' >> .git/config
	`)
	changes, err := b.Changes()
	if err != nil {
		t.Fatalf("Changes: %v\n%s", err, out)
	}

	want := map[string]events.FileChange{
		"a.txt":               {Change: Changed, Files: 1, BytesBefore: 4, BytesAfter: 8},
		"b.txt":               {Change: Deleted, Files: 1, BytesBefore: 4},
		"tool.sh":             {Change: Changed, Files: 1, BytesBefore: 10, BytesAfter: 10},
		"old":                 {Change: Deleted, Dir: true, Files: 2, BytesBefore: 4},
		"new":                 {Change: Added, Dir: true, Files: 3, BytesAfter: 6},
		"swap/in-real.txt":    {Change: Deleted, Files: 1, BytesBefore: 2},
		"swap/fresh.txt":      {Change: Added, Files: 1, BytesAfter: 6},
		".git":                {Change: Changed, Dir: true, Files: 2, BytesAfter: 26},
		".git/hooks/pre-push": {Change: Added, Files: 1, BytesAfter: 10},
		".git/config":         {Change: Changed, Files: 1, BytesBefore: 7, BytesAfter: 15},
	}
	for path, w := range want {
		got := find(t, changes, path).FileChange
		sensitive := got.Sensitive
		got.Path, got.Sensitive = "", ""
		if got != w {
			t.Errorf("%s = %+v, want %+v", path, got, w)
		}
		if strings.HasPrefix(path, ".git/") != (sensitive != "") {
			t.Errorf("%s: sensitive = %q", path, sensitive)
		}
	}
	if len(changes) != len(want) {
		var have []string
		for _, c := range changes {
			have = append(have, c.Change+" "+c.Path)
		}
		t.Errorf("got %d changes, want %d: %v", len(changes), len(want), have)
	}
	if got := find(t, changes, "new").SecretFiles; !slices.Equal(got, []string{"new/.env"}) {
		t.Errorf("the new folder's secret-looking files = %v, want new/.env", got)
	}

	// nothing has touched the real project
	if got, err := os.ReadFile(filepath.Join(proj, "a.txt")); err != nil || string(got) != "one\n" {
		t.Errorf("the real a.txt = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(proj, "new")); err == nil {
		t.Error("the new folder exists in the real project")
	}
}

func TestDeletingManyFilesMakesEachDeletionSensitive(t *testing.T) {
	proj := project(t)
	for i := range MassDeletion {
		if err := os.WriteFile(filepath.Join(proj, "old", string(rune('a'+i))+".log"), []byte("l\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := boxed(t, proj, "rm -rf old && rm b.txt")
	changes, err := b.Changes()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"old", "b.txt"} {
		if c := find(t, changes, path); !strings.Contains(c.Sensitive, "deleting 13 files") {
			t.Errorf("%s: sensitive = %q, want it to say how many files are being deleted", path, c.Sensitive)
		}
	}

	few, _ := boxed(t, project(t), "rm b.txt")
	changes, err = few.Changes()
	if err != nil {
		t.Fatal(err)
	}
	if c := find(t, changes, "b.txt"); c.Sensitive != "" {
		t.Errorf("one deleted file was marked sensitive: %q", c.Sensitive)
	}
}

func TestDiffAndApply(t *testing.T) {
	proj := project(t)
	b, _ := boxed(t, proj, "echo changed > a.txt && rm b.txt && mkdir new && echo n > new/n.txt && rm -rf old && echo obj > .git/objects/new && mkdir -p .git/hooks && echo h > .git/hooks/pre-push")
	// the project's files were written just before the box was made
	b.opts.Since = time.Now()
	changes, err := b.Changes()
	if err != nil {
		t.Fatal(err)
	}

	diff, err := b.Diff(find(t, changes, "a.txt").FileChange)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--- a/a.txt", "+++ b/a.txt", "-one", "+changed"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff of a.txt lacks %q:\n%s", want, diff)
		}
	}
	if diff, err := b.Diff(find(t, changes, "b.txt").FileChange); err != nil || !strings.Contains(diff, "-two") {
		t.Errorf("diff of the deleted b.txt: %v\n%s", err, diff)
	}
	if diff, err := b.Diff(find(t, changes, "new").FileChange); err != nil || !strings.Contains(diff, "n.txt") {
		t.Errorf("listing of the new folder: %v\n%s", err, diff)
	}

	for _, path := range []string{"a.txt", "b.txt", "new", "old", ".git"} {
		if err := b.Apply(find(t, changes, path).FileChange); err != nil {
			t.Fatalf("apply %s: %v", path, err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(proj, "a.txt")); err != nil || string(got) != "changed\n" {
		t.Errorf("a.txt after apply = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(proj, "new", "n.txt")); err != nil || string(got) != "n\n" {
		t.Errorf("new/n.txt after apply = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(proj, ".git", "objects", "new")); err != nil || string(got) != "obj\n" {
		t.Errorf(".git/objects/new after apply = %q, %v", got, err)
	}
	for _, gone := range []string{"b.txt", "old"} {
		if _, err := os.Stat(filepath.Join(proj, gone)); err == nil {
			t.Errorf("%s still exists after its deletion was applied", gone)
		}
	}
	// the hook has a row of its own and was not allowed
	if _, err := os.Stat(filepath.Join(proj, ".git", "hooks", "pre-push")); err == nil {
		t.Error("applying git's record also wrote the hook, which is decided separately")
	}
	leftovers, err := filepath.Glob(filepath.Join(proj, ".rehearsal-apply-*"))
	if err != nil || len(leftovers) != 0 {
		t.Errorf("apply left temporary files behind: %v (%v)", leftovers, err)
	}
}

func TestApplyRefusesWhenTheRealProjectHasMoved(t *testing.T) {
	proj := project(t)
	b, _ := boxed(t, proj, "echo changed > a.txt && rm b.txt && echo new > c.txt && rm -rf old && echo gone > d.txt")
	b.opts.Since = time.Now()
	changes, err := b.Changes()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	// the user works on the same files while the session waits
	edit := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(proj, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	edit("a.txt", "the user's\n")
	edit("b.txt", "the user's\n")
	edit("c.txt", "the user's\n")
	edit("old/deep/y.txt", "the user's\n")

	tests := []struct{ path, want string }{
		{"a.txt", "a.txt has changed in your real project since the session started"},
		{"b.txt", "b.txt has changed in your real project since the session started"},
		{"c.txt", "c.txt now exists in your real project"},
		{"old", "old/deep/y.txt has changed in your real project since the session started"},
	}
	for _, tt := range tests {
		err := b.Apply(find(t, changes, tt.path).FileChange)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("apply %s = %v, want it refused with %q", tt.path, err, tt.want)
		}
	}
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "old/deep/y.txt"} {
		if got, err := os.ReadFile(filepath.Join(proj, name)); err != nil || string(got) != "the user's\n" {
			t.Errorf("%s = %q, %v; the user's version was not left alone", name, got, err)
		}
	}
	// a change to a file the user did not touch still goes through
	if err := b.Apply(find(t, changes, "d.txt").FileChange); err != nil {
		t.Errorf("apply d.txt: %v", err)
	}
}

func TestOutsideTheProjectIsReadOnlyHiddenOrPrivate(t *testing.T) {
	writable := filepath.Join(t.TempDir(), "state-of-the-agent")
	if err := os.MkdirAll(writable, 0o755); err != nil {
		t.Fatal(err)
	}
	// a system temp folder that the sandbox does not replace
	outside, err := os.MkdirTemp("/var/tmp", "rehearsal-test-")
	if err != nil {
		t.Skipf("cannot make a folder in /var/tmp: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(outside); err != nil {
			t.Error(err)
		}
	})
	if err := os.WriteFile(filepath.Join(outside, "theirs.txt"), []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, out := boxed(t, project(t), `
		hidden="$1"
		echo "read-outside: $(cat `+outside+`/theirs.txt)"
		if echo x > `+outside+`/written.txt 2>/dev/null; then echo "write-outside: allowed"; else echo "write-outside: refused"; fi
		if echo x >> `+outside+`/theirs.txt 2>/dev/null; then echo "append-outside: allowed"; else echo "append-outside: refused"; fi
		echo "hidden: $(ls -A "$hidden" | wc -l) entries"
		if echo x > `+writable+`/kept.txt 2>/dev/null; then echo "write-writable: allowed"; else echo "write-writable: refused"; fi
		echo x > /tmp/scratch.txt && echo "tmp: writable"
		echo "pid: $$"
	`, writable)

	for _, want := range []string{
		"read-outside: theirs", "write-outside: refused", "append-outside: refused",
		"hidden: 0 entries", "write-writable: allowed", "tmp: writable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("inside the sandbox, want %q; output:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "written.txt")); err == nil {
		t.Error("a file written inside the sandbox appeared outside the project")
	}
	if got, err := os.ReadFile(filepath.Join(writable, "kept.txt")); err != nil || string(got) != "x\n" {
		t.Errorf("the writable folder did not keep the write: %q, %v", got, err)
	}
	if _, err := os.Stat("/tmp/scratch.txt"); err == nil {
		t.Error("the sandbox's /tmp is the real one")
	}
}

func TestWatchNotesSensitiveWritesOnce(t *testing.T) {
	proj := project(t)
	settings := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(settings, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settings, "settings.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := Available(); err != nil {
		t.Skipf("no sandbox on this machine: %v", err)
	}
	b, err := New(Options{Project: proj, Dir: filepath.Join(t.TempDir(), "box"), Self: self, Visible: []string{filepath.Dir(self)}, Writable: []string{settings}, Since: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
		if err := b.Remove(); err != nil {
			t.Error(err)
		}
	})
	w := b.NewWatch()
	if found, err := w.New(); err != nil || len(found) != 0 {
		t.Fatalf("before anything ran: %v, %v", found, err)
	}

	time.Sleep(20 * time.Millisecond)
	args := b.Command([]string{"sh", "-c", `mkdir -p .github/workflows && echo 'on: push' > .github/workflows/ci.yml && echo ok > notes.txt && echo '{"hooks":{}}' > ` + settings + `/settings.json`})
	if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	found, err := w.New()
	if err != nil {
		t.Fatal(err)
	}
	want := []events.SensitiveWrite{
		{Path: ".github/workflows/ci.yml", Reason: Sensitive(".github/workflows/ci.yml")},
		{Path: filepath.Join(settings, "settings.json"), Reason: Sensitive(".claude/settings.json"), Real: true},
	}
	if !slices.Equal(found, want) {
		t.Errorf("New = %+v\nwant  %+v", found, want)
	}
	if again, err := w.New(); err != nil || len(again) != 0 {
		t.Errorf("the same writes were reported twice: %v, %v", again, err)
	}
}
