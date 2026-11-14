package changes

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo is a git repository in a temporary folder, with one commit.
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	// nothing of the developer's own git settings may reach these tests
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, who := range []string{"AUTHOR", "COMMITTER"} {
		t.Setenv("GIT_"+who+"_NAME", "Test")
		t.Setenv("GIT_"+who+"_EMAIL", "test@example.invalid")
	}
	r := repo{t, t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.write("README.md", "# shop\n\nA sample.\n")
	r.write("src/total.go", "package shop\n\nfunc Total(a, b int) int {\n\treturn a + b\n}\n")
	r.write("src/old.go", "package shop\n\n// Old is going away.\nfunc Old() {}\n")
	r.write("docs/guide.md", "Line one.\nLine two.\nLine three.\nLine four.\nLine five.\n")
	r.write(".gitignore", "build/\n*.log\n")
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "Start")
	return r
}

func (r repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (r repo) write(name, content string) {
	r.t.Helper()
	full := filepath.Join(r.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r repo) remove(name string) {
	r.t.Helper()
	if err := os.RemoveAll(filepath.Join(r.dir, filepath.FromSlash(name))); err != nil {
		r.t.Fatal(err)
	}
}

func (r repo) snapshot() string {
	r.t.Helper()
	tree, err := Snapshot(r.dir, r.t.TempDir())
	if err != nil {
		r.t.Fatal(err)
	}
	return tree
}

func (r repo) patch(from, to string) string {
	r.t.Helper()
	p, truncated, err := Patch(r.dir, from, to)
	if err != nil || truncated {
		r.t.Fatalf("patch: %v, truncated %v", err, truncated)
	}
	return p
}

func TestSnapshotsShowWhatChangedBetweenThem(t *testing.T) {
	r := newRepo(t)
	r.write("notes.txt", "untracked before the session\n")
	r.write("CANARY.txt", "canary-word-0123456789ab\n")
	before := r.snapshot()

	r.write("src/total.go", "package shop\n\nfunc Total(a, b int) int {\n\treturn a + b + 1\n}\n") // a tracked file edited
	r.write("src/new.go", "package shop\n\nfunc New() {}\n")                                       // a new file, never added
	r.remove("src/old.go")                                                                         // a deletion
	r.git("mv", "docs/guide.md", "docs/manual.md")                                                 // a rename
	r.write("README.md", "# shop\n\nChanged.\n")
	r.write("README.md", "# shop\n\nA sample.\n")              // changed, and changed back
	r.write("logo.png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR") // a binary file
	r.write("build/out.bin", "ignored by .gitignore\n")
	r.write("debug.log", "ignored too\n")
	r.write("notes.txt", "untracked, and changed in the session\n")
	r.write("rehearsal-ledger.json", "{}\n") // one of Rehearsal's own
	after := r.snapshot()

	p := r.patch(before, after)
	for _, want := range []string{
		"diff --git a/src/total.go b/src/total.go", "-\treturn a + b\n+\treturn a + b + 1",
		"diff --git a/src/new.go b/src/new.go\nnew file mode 100644",
		"diff --git a/src/old.go b/src/old.go\ndeleted file mode 100644",
		"rename from docs/guide.md\nrename to docs/manual.md",
		"Binary files /dev/null and b/logo.png differ",
		"-untracked before the session\n+untracked, and changed in the session",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the patch is missing %q:\n%s", want, p)
		}
	}
	for _, never := range []string{"README.md", "build/out.bin", "debug.log", "CANARY.txt", "rehearsal-ledger.json", "canary-word"} {
		if strings.Contains(p, never) {
			t.Errorf("the patch mentions %q, which did not change or is not the project's:\n%s", never, p)
		}
	}
	if same := r.patch(after, r.snapshot()); same != "" {
		t.Errorf("two snapshots of the same files differ:\n%s", same)
	}
}

func sum(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// The user may be in the middle of something: part of a change staged,
// more of it not, a file never added, a stash. A snapshot must leave all
// of it exactly as it was.
func TestSnapshotLeavesWorkInProgressExactlyAsItWas(t *testing.T) {
	r := newRepo(t)
	r.write("src/total.go", "package shop\n\nfunc Total(a, b int) int {\n\treturn b + a\n}\n")
	r.git("add", "src/total.go")
	r.write("src/total.go", "package shop\n\n// Total adds.\nfunc Total(a, b int) int {\n\treturn b + a\n}\n")
	r.write("docs/guide.md", "Stashed.\n")
	r.git("stash", "push", "-q", "--", "docs/guide.md")
	r.git("rm", "-q", "--cached", "src/old.go") // staged for deletion, file still there
	r.write("scratch.txt", "never added\n")

	state := func() string {
		return strings.Join([]string{
			r.git("status", "--porcelain=v2", "--untracked-files=all"),
			r.git("diff", "--cached"), r.git("diff"),
			r.git("for-each-ref"), r.git("stash", "list"), r.git("rev-parse", "HEAD"),
			sum(t, filepath.Join(r.dir, "src/total.go")), sum(t, filepath.Join(r.dir, "scratch.txt")),
		}, "\n--\n")
	}
	want := state()
	index := filepath.Join(r.dir, ".git", "index")
	indexBefore := sum(t, index)

	first := r.snapshot()
	r.write("scratch.txt", "never added, then changed\n")
	second := r.snapshot()

	if got := sum(t, index); got != indexBefore {
		t.Error("the repository's index file was written to")
	}
	if entries, err := filepath.Glob(filepath.Join(r.dir, ".git", "*.lock")); err != nil || len(entries) > 0 {
		t.Errorf("lock files left in the repository: %v (%v)", entries, err)
	}
	r.write("scratch.txt", "never added\n")
	if got := state(); got != want {
		t.Errorf("the repository is not as it was.\nbefore:\n%s\nafter:\n%s", want, got)
	}
	// and the snapshots saw the working files, not what was staged
	p := r.patch(first, second)
	if !strings.Contains(p, "+never added, then changed") || strings.Contains(p, "total.go") {
		t.Errorf("patch between the two snapshots:\n%s", p)
	}
	head := strings.TrimSpace(r.git("rev-parse", "HEAD^{tree}"))
	if fromHead := r.patch(head, first); !strings.Contains(fromHead, "+// Total adds.") || !strings.Contains(fromHead, "scratch.txt") {
		t.Errorf("the snapshot is not the working tree: unstaged and untracked work is missing:\n%s", fromHead)
	}
}

func TestSnapshotOutsideARepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	// a ceiling, so a repository above the temporary folder is not found
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if _, err := Snapshot(dir, t.TempDir()); !errors.Is(err, ErrNotARepo) {
		t.Errorf("Snapshot outside a repository: %v, want ErrNotARepo", err)
	}
}

func TestSnapshotOfARepositoryWithNoCommits(t *testing.T) {
	r := newRepo(t)
	fresh := repo{t, t.TempDir()}
	fresh.git("init", "-q", "-b", "main")
	before := fresh.snapshot()
	fresh.write("first.txt", "hello\n")
	if p := fresh.patch(before, fresh.snapshot()); !strings.Contains(p, "+hello") {
		t.Errorf("patch in a repository with no commits:\n%s", p)
	}
	_ = r
}

// From a folder inside the repository, the whole working tree is recorded,
// and Rehearsal's own files are the ones in that folder.
func TestSnapshotFromASubfolder(t *testing.T) {
	r := newRepo(t)
	sub := filepath.Join(r.dir, "src")
	before, err := Snapshot(sub, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.write("src/CANARY.txt", "canary-word-0123456789ab\n")
	r.write("docs/guide.md", "Changed outside the folder.\n")
	after, err := Snapshot(sub, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := r.patch(before, after)
	if !strings.Contains(p, "+Changed outside the folder.") || strings.Contains(p, "CANARY") {
		t.Errorf("patch from a subfolder:\n%s", p)
	}
}

func TestOverlayPutsALayerOfChangesOverATree(t *testing.T) {
	r := newRepo(t)
	r.write("pkg/a.txt", "a\n")
	r.write("pkg/deep/b.txt", "b\n")
	base := r.snapshot()

	layer := t.TempDir()
	put := func(name, content string, mode fs.FileMode) LayerFile {
		t.Helper()
		full := filepath.Join(layer, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return LayerFile{Path: name, Source: full, Mode: mode}
	}
	link := filepath.Join(layer, "latest")
	if err := os.Symlink("src/total.go", link); err != nil {
		t.Fatal(err)
	}
	tree, err := Overlay(r.dir, t.TempDir(), base, []LayerFile{
		put("src/total.go", "package shop\n\nfunc Total(a, b int) int {\n\treturn a * b\n}\n", 0o644),
		put("run.sh", "#!/bin/sh\necho hi\n", 0o755),
		put("build/out.bin", "ignored\n", 0o644),
		{Path: "src/old.go", Deleted: true},
		{Path: "pkg", Deleted: true}, // the folder was removed and made again with one file
		put("pkg/c.txt", "c\n", 0o644),
		{Path: "latest", Source: link, Mode: fs.ModeSymlink | 0o777},
		{Path: "nothing/there", Deleted: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := r.patch(base, tree)
	for _, want := range []string{
		"+\treturn a * b", "diff --git a/run.sh b/run.sh\nnew file mode 100755",
		"diff --git a/src/old.go b/src/old.go\ndeleted file mode", "deleted file mode 100644\nindex", "+c\n",
		"diff --git a/latest b/latest\nnew file mode 120000",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the patch is missing %q:\n%s", want, p)
		}
	}
	for _, gone := range []string{"a/pkg/a.txt", "a/pkg/deep/b.txt"} {
		if !strings.Contains(p, "diff --git "+gone) {
			t.Errorf("%s should be deleted with its folder:\n%s", gone, p)
		}
	}
	if strings.Contains(p, "build/out.bin") {
		t.Errorf("an ignored file in the layer is in the patch:\n%s", p)
	}
	// the real working files were not touched
	if got, err := os.ReadFile(filepath.Join(r.dir, "src/total.go")); err != nil || !strings.Contains(string(got), "a + b") {
		t.Errorf("the real file was changed: %q (%v)", got, err)
	}
}
