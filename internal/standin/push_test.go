package standin

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo makes a repository with one commit on main and a remote named
// origin that points at a bare repository. Nothing has been pushed.
func newRepo(t *testing.T) (dir, bare string, g gitRunner) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	dir, bare = filepath.Join(root, "work"), filepath.Join(root, "remote.git")
	g = gitRunner{real: real, globals: []string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}}
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(real, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "--bare", bare)
	git("init", "-q", "-b", "main", dir)
	git(append(g.globals, "commit", "-q", "--allow-empty", "-m", "first")...)
	git(append(g.globals, "remote", "add", "origin", bare)...)
	return dir, bare, g
}

func commit(t *testing.T, g gitRunner, message string) string {
	t.Helper()
	if out, err := exec.Command(g.real, append(g.globals, "commit", "-q", "--allow-empty", "-m", message)...).CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	head, ok := g.out("rev-parse", "HEAD")
	if !ok {
		t.Fatal("cannot read HEAD")
	}
	return head
}

func TestPushFacts(t *testing.T) {
	_, bare, g := newRepo(t)
	first, _ := g.out("rev-parse", "HEAD")

	t.Run("first push of a branch", func(t *testing.T) {
		facts, err := pushFacts(g, parsePush([]string{"origin", "main"}))
		if err != nil {
			t.Fatal(err)
		}
		if facts.destination != bare || facts.remote != "origin" {
			t.Errorf("destination = %q under the name %q, want the remote's address %q under origin", facts.destination, facts.remote, bare)
		}
		if len(facts.refs) != 1 || facts.refs[0].Ref != "refs/heads/main" || facts.refs[0].Commit != first {
			t.Errorf("refs = %+v, want refs/heads/main at %s", facts.refs, first)
		}
		want := "To " + bare + "\n * [new branch]      main -> main\n"
		if facts.stderr != want {
			t.Errorf("output = %q, want %q", facts.stderr, want)
		}
		if facts.subject != "main" {
			t.Errorf("subject = %q, want main", facts.subject)
		}
	})

	t.Run("which pushes are minor", func(t *testing.T) {
		if _, ok := g.out("branch", "feature"); !ok {
			t.Fatal("cannot create a branch")
		}
		tests := []struct {
			name  string
			args  []string
			setup func()
			minor bool
		}{
			{"a new branch with an ordinary name", []string{"origin", "feature"}, nil, true},
			{"main, when the remote's default is not known", []string{"origin", "main"}, nil, false},
			{"a new branch and main together", []string{"origin", "feature", "main"}, nil, false},
			{"a new branch pushed under the name main", []string{"origin", "feature:main"}, nil, false},
			{"a deletion", []string{"origin", "--delete", "feature"}, nil, false},
			{"every branch", []string{"--all", "origin"}, nil, false},
			{"a branch the remote already has", []string{"origin", "feature"}, func() {
				head, _ := g.out("rev-parse", "HEAD")
				g.out("update-ref", "refs/remotes/origin/feature", head)
			}, false},
			{"main, when the remote's default is known to be trunk", []string{"origin", "main"}, func() {
				g.out("update-ref", "-d", "refs/remotes/origin/feature")
				g.out("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
			}, true},
			{"trunk, the known default", []string{"origin", "main:trunk"}, nil, false},
		}
		for _, tt := range tests {
			if tt.setup != nil {
				tt.setup()
			}
			facts, err := pushFacts(g, parsePush(tt.args))
			if err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if facts.minor != tt.minor {
				t.Errorf("%s: minor = %t, want %t", tt.name, facts.minor, tt.minor)
			}
		}
		// leave the repository as the later subtests expect it
		g.out("symbolic-ref", "-d", "refs/remotes/origin/HEAD")
	})

	t.Run("no arguments means the current branch", func(t *testing.T) {
		facts, err := pushFacts(g, parsePush(nil))
		if err != nil {
			t.Fatal(err)
		}
		if len(facts.refs) != 1 || facts.refs[0].Ref != "refs/heads/main" {
			t.Errorf("refs = %+v, want refs/heads/main", facts.refs)
		}
	})

	t.Run("HEAD is the current branch", func(t *testing.T) {
		facts, err := pushFacts(g, parsePush([]string{"origin", "HEAD"}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(facts.stderr, "main -> main") || facts.refs[0].Ref != "refs/heads/main" {
			t.Errorf("output = %q refs = %+v, want main -> main", facts.stderr, facts.refs)
		}
	})

	t.Run("setting the upstream is announced", func(t *testing.T) {
		facts, err := pushFacts(g, parsePush([]string{"-u", "origin", "main"}))
		if err != nil {
			t.Fatal(err)
		}
		if want := "branch 'main' set up to track 'origin/main'.\n"; facts.stdout != want {
			t.Errorf("stdout = %q, want %q", facts.stdout, want)
		}
	})

	t.Run("a ref that does not exist fails as git does", func(t *testing.T) {
		_, err := pushFacts(g, parsePush([]string{"origin", "no-such-branch"}))
		if err == nil || !strings.Contains(err.Error(), "src refspec no-such-branch does not match any") {
			t.Errorf("err = %v, want git's src refspec error", err)
		}
	})

	t.Run("deleting a remote branch", func(t *testing.T) {
		facts, err := pushFacts(g, parsePush([]string{"origin", "--delete", "old"}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(facts.stderr, " - [deleted]         old") || len(facts.refs) != 0 {
			t.Errorf("output = %q refs = %+v, want a deletion and no local refs", facts.stderr, facts.refs)
		}
	})

	// from here on the remote is known to have the first commit
	if _, ok := g.out("update-ref", "refs/remotes/origin/main", first); !ok {
		t.Fatal("cannot set the remote-tracking ref")
	}

	t.Run("nothing new", func(t *testing.T) {
		facts, err := pushFacts(g, parsePush([]string{"origin", "main"}))
		if err != nil {
			t.Fatal(err)
		}
		if facts.stderr != "Everything up-to-date\n" {
			t.Errorf("output = %q, want Everything up-to-date", facts.stderr)
		}
	})

	second := commit(t, g, "second")

	t.Run("an ordinary update", func(t *testing.T) {
		facts, err := pushFacts(g, parsePush([]string{"origin", "main"}))
		if err != nil {
			t.Fatal(err)
		}
		want := "To " + bare + "\n   " + first[:7] + ".." + second[:7] + "  main -> main\n"
		if facts.stderr != want {
			t.Errorf("output = %q, want %q", facts.stderr, want)
		}
		if facts.refs[0].Commit != second {
			t.Errorf("held commit = %s, want the new one %s", facts.refs[0].Commit, second)
		}
	})

	t.Run("a forced update", func(t *testing.T) {
		facts, err := pushFacts(g, parsePush([]string{"--force", "origin", "main"}))
		if err != nil {
			t.Fatal(err)
		}
		if want := " + " + first[:7] + "..." + second[:7] + " main -> main (forced update)"; !strings.Contains(facts.stderr, want) {
			t.Errorf("output = %q, want it to contain %q", facts.stderr, want)
		}
	})

	t.Run("a different name on the far side", func(t *testing.T) {
		facts, err := pushFacts(g, parsePush([]string{"origin", "main:review"}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(facts.stderr, " * [new branch]      main -> review") || facts.subject != "review" {
			t.Errorf("output = %q subject = %q, want main -> review", facts.stderr, facts.subject)
		}
	})
}
