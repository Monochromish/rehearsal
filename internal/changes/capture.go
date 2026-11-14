// Package changes records what happened to a project's files during a
// session and sorts it by how much of it needs reading.
package changes

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotARepo is returned for a folder that is not inside the working
// tree of a git repository.
var ErrNotARepo = errors.New("not inside a git repository")

// Files that Rehearsal itself puts in a working folder. They are never
// part of what an agent changed.
var ownFiles = []string{"CANARY.txt", "rehearsal-ledger.json", "rehearsal-report.md", "rehearsal-attestation.json"}

// MaxPatch is the most of a session's diff that is kept.
const MaxPatch = 8 << 20

// git runs git in dir and returns what it printed. Locks that git takes
// only to save itself work later are switched off: nothing here may write
// to the repository's own index.
func git(dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0"), env...)
	cmd.Stdin = stdin
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// top is the root of the working tree that dir is in.
func top(dir string) (string, error) {
	inside, err := git(dir, nil, nil, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		return "", ErrNotARepo
	}
	root, err := git(dir, nil, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(root), nil
}

// scratchIndex makes an index file of our own in scratch, starting from a
// copy of the repository's so that files git already knows to be
// unchanged are not read again. The repository's own index is only read.
func scratchIndex(root, scratch string) (string, error) {
	f, err := os.CreateTemp(scratch, "snapshot-*.index")
	if err != nil {
		return "", err
	}
	name := f.Name()
	real, err := git(root, nil, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", errors.Join(err, f.Close(), os.Remove(name))
	}
	src, err := os.Open(strings.TrimSpace(real))
	if errors.Is(err, fs.ErrNotExist) {
		// a repository nothing has been added to yet: git wants no file at all
		return name, errors.Join(f.Close(), os.Remove(name))
	}
	if err != nil {
		return "", errors.Join(err, f.Close(), os.Remove(name))
	}
	_, err = io.Copy(f, src)
	if err := errors.Join(err, src.Close(), f.Close()); err != nil {
		return "", errors.Join(err, os.Remove(name))
	}
	return name, nil
}

// Snapshot records the working tree that dir is in as a git tree and
// returns its hash: every file, tracked or not, that .gitignore does not
// leave out, apart from Rehearsal's own. It goes through an index file of
// its own in scratch, so the repository's index, its working files and
// its refs are left exactly as they were. The only thing added to the
// repository is objects that nothing refers to.
func Snapshot(dir, scratch string) (tree string, err error) {
	root, err := top(dir)
	if err != nil {
		return "", err
	}
	index, err := scratchIndex(root, scratch)
	if err != nil {
		return "", err
	}
	defer func() {
		if rerr := os.Remove(index); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			err = errors.Join(err, rerr)
		}
	}()
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := git(root, env, nil, "add", "--all"); err != nil {
		return "", err
	}
	// named from dir, which is where Rehearsal put them
	if _, err := git(dir, env, nil, append([]string{"rm", "--cached", "--quiet", "--ignore-unmatch", "--"}, ownFiles...)...); err != nil {
		return "", err
	}
	return writeTree(root, env)
}

func writeTree(root string, env []string) (string, error) {
	out, err := git(root, env, nil, "write-tree")
	return strings.TrimSpace(out), err
}

// LayerFile is one path in a layer of changes that sits over the working
// tree, such as the sandbox's.
type LayerFile struct {
	// relative to the working folder, with forward slashes
	Path string
	// the path is gone, with everything under it
	Deleted bool
	// where the new contents are, for a path that was added or changed
	Source string
	Mode   fs.FileMode
}

// Overlay returns the tree that base becomes when a layer of changes is
// put over it. dir is the working folder the layer's paths are relative
// to. Files that .gitignore leaves out are left out here too.
func Overlay(dir, scratch, base string, layer []LayerFile) (tree string, err error) {
	root, err := top(dir)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(scratch, "overlay-*.index")
	if err != nil {
		return "", err
	}
	index := f.Name()
	// git wants no file, or a valid one
	if err := errors.Join(f.Close(), os.Remove(index)); err != nil {
		return "", err
	}
	defer func() {
		if rerr := os.Remove(index); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			err = errors.Join(err, rerr)
		}
	}()
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := git(root, env, nil, "read-tree", base); err != nil {
		return "", err
	}
	// deletions first: a folder that was removed and made again is both
	for _, lf := range layer {
		if !lf.Deleted {
			continue
		}
		if _, err := git(dir, env, nil, "rm", "--cached", "-r", "--quiet", "--ignore-unmatch", "--", filepath.FromSlash(lf.Path)); err != nil {
			return "", err
		}
	}
	for _, lf := range layer {
		if lf.Deleted || !(lf.Mode.IsRegular() || lf.Mode&fs.ModeSymlink != 0) {
			continue
		}
		if _, err := git(dir, env, nil, "check-ignore", "--quiet", "--no-index", "--", filepath.FromSlash(lf.Path)); err == nil {
			continue // ignored
		}
		var blob string
		mode := "100644"
		switch {
		case lf.Mode&fs.ModeSymlink != 0:
			target, err := os.Readlink(lf.Source)
			if err != nil {
				return "", err
			}
			mode = "120000"
			if blob, err = git(dir, nil, strings.NewReader(target), "hash-object", "-w", "--stdin"); err != nil {
				return "", err
			}
		default:
			if lf.Mode&0o111 != 0 {
				mode = "100755"
			}
			// --path so that the project's own line-ending rules apply
			if blob, err = git(dir, nil, nil, "hash-object", "-w", "--path="+filepath.FromSlash(lf.Path), lf.Source); err != nil {
				return "", err
			}
		}
		prefix, err := git(dir, nil, nil, "rev-parse", "--show-prefix")
		if err != nil {
			return "", err
		}
		entry := fmt.Sprintf("%s,%s,%s", mode, strings.TrimSpace(blob), strings.TrimSpace(prefix)+lf.Path)
		if _, err := git(root, env, nil, "update-index", "--add", "--cacheinfo", entry); err != nil {
			return "", err
		}
	}
	return writeTree(root, env)
}

// Tree returns the tree that a revision names: a commit, a branch, a tag.
func Tree(dir, rev string) (string, error) {
	root, err := top(dir)
	if err != nil {
		return "", err
	}
	// a name that begins with a dash is a name, not an option
	out, err := git(root, nil, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{tree}")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("%q does not name a commit in this repository", rev)
	}
	return strings.TrimSpace(out), nil
}

// MergeBase returns the commit at which b left a.
func MergeBase(dir, a, b string) (string, error) {
	root, err := top(dir)
	if err != nil {
		return "", err
	}
	out, err := git(root, nil, nil, "merge-base", "--end-of-options", a, b)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("%q and %q have no commit in common, or one of them does not name a commit", a, b)
	}
	return strings.TrimSpace(out), nil
}

// EmptyTree returns the name this repository gives a tree with nothing in
// it, which is what a repository with no commit yet is compared against.
func EmptyTree(dir string) (string, error) {
	root, err := top(dir)
	if err != nil {
		return "", err
	}
	out, err := git(root, nil, strings.NewReader(""), "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Patch is the difference between two trees as one unified diff, with
// renames found. The user's own diff settings are not used: no outside
// program is run and nothing is converted for display. truncated says the
// diff was longer than MaxPatch and was cut at the end of a line.
func Patch(dir, from, to string) (patch string, truncated bool, err error) {
	root, err := top(dir)
	if err != nil {
		return "", false, err
	}
	out, err := git(root, nil, nil, "-c", "core.quotePath=false", "diff", "--no-color", "--no-ext-diff", "--no-textconv",
		"--find-renames", "--unified=3", "--src-prefix=a/", "--dst-prefix=b/", from, to)
	if err != nil {
		return "", false, err
	}
	if len(out) > MaxPatch {
		cut := strings.LastIndexByte(out[:MaxPatch], '\n')
		return out[:cut+1], true, nil
	}
	return out, false, nil
}
