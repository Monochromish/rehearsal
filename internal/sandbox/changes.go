package sandbox

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"rehearsal/internal/events"
	"rehearsal/internal/review"
)

const (
	Added   = "added"
	Changed = "changed"
	Deleted = "deleted"

	// deleting this many files in one session makes each deletion one to
	// look at closely
	MassDeletion = 10

	gitDir = ".git"
	// the most diff text shown for one change
	maxDiff = 100 << 10
	// the most file names listed for a folder
	maxListed = 50
)

// Change is one change waiting in the layer, with the names in it that
// look like secrets.
type Change struct {
	events.FileChange
	SecretFiles []string
}

// whiteout reports whether an entry in the layer marks a deletion. The
// kernel writes a deletion as a character device numbered 0, 0.
func whiteout(info fs.FileInfo) bool {
	if info.Mode()&fs.ModeCharDevice == 0 {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Rdev == 0
}

// opaque reports whether a folder in the layer replaces the real folder
// outright, which happens when a folder is deleted and made again.
func opaque(dir string) bool {
	buf := make([]byte, 8)
	n, err := syscall.Getxattr(dir, "user.overlay.opaque", buf)
	return err == nil && n == 1 && buf[0] == 'y'
}

// Changes compares the layer with the real project. Everything git keeps
// for itself under .git comes back as one change, apart from its settings
// and hooks, which are listed on their own.
func (b *Box) Changes() ([]Change, error) {
	leaves, err := b.walk("")
	if err != nil {
		return nil, fmt.Errorf("read the sandbox's file changes: %w", err)
	}
	var out []Change
	history := Change{FileChange: events.FileChange{Path: gitDir, Change: Changed, Dir: true}}
	if !exists(filepath.Join(b.opts.Project, gitDir)) {
		history.Change = Added
	}
	for _, c := range leaves {
		if inGit(c.Path) && c.Sensitive == "" {
			history.Files += c.Files
			history.BytesAfter += c.BytesAfter
			continue
		}
		out = append(out, c)
	}
	if history.Files > 0 {
		out = append(out, history)
	}
	deleted := 0
	for _, c := range out {
		if c.Change == Deleted {
			deleted += c.Files
		}
	}
	if deleted >= MassDeletion {
		for i := range out {
			if out[i].Change == Deleted && out[i].Sensitive == "" {
				out[i].Sensitive = fmt.Sprintf("It is part of deleting %d files in one session.", deleted)
			}
		}
	}
	slices.SortFunc(out, func(x, y Change) int { return strings.Compare(x.Path, y.Path) })
	return out, nil
}

func inGit(rel string) bool {
	return rel == gitDir || strings.HasPrefix(rel, gitDir+"/")
}

// walk lists the changes under rel. A folder that is new, or gone, is one
// change, except inside .git where every file is listed.
func (b *Box) walk(rel string) ([]Change, error) {
	entries, err := os.ReadDir(filepath.Join(b.upper, rel))
	if err != nil {
		return nil, err
	}
	var out []Change
	for _, e := range entries {
		p := path.Join(rel, e.Name())
		up, low := filepath.Join(b.upper, p), filepath.Join(b.opts.Project, p)
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		lowInfo, lowErr := os.Lstat(low)
		if lowErr != nil && !errors.Is(lowErr, fs.ErrNotExist) {
			return nil, lowErr
		}
		inReal := lowErr == nil

		switch {
		case whiteout(info):
			if inReal {
				c, err := deletion(p, low, lowInfo)
				if err != nil {
					return nil, err
				}
				out = append(out, c)
			}
		case info.IsDir() && inReal && lowInfo.IsDir():
			if opaque(up) {
				gone, err := b.replaced(p)
				if err != nil {
					return nil, err
				}
				out = append(out, gone...)
			}
			inside, err := b.walk(p)
			if err != nil {
				return nil, err
			}
			out = append(out, inside...)
		case info.IsDir() && inGit(p):
			inside, err := b.walk(p)
			if err != nil {
				return nil, err
			}
			out = append(out, inside...)
		case info.IsDir():
			files, size, err := tree(up)
			if err != nil {
				return nil, err
			}
			c := Change{FileChange: events.FileChange{Path: p, Change: Added, Dir: true, Files: len(files), BytesAfter: size}}
			for _, f := range files {
				full := path.Join(p, f)
				if c.Sensitive == "" {
					c.Sensitive = Sensitive(full)
				}
				c.SecretFiles = append(c.SecretFiles, review.SecretLooking([]string{full})...)
			}
			out = append(out, c)
		case !inReal:
			out = append(out, file(p, Added, 0, info.Size()))
		default:
			same, err := identical(up, info, low, lowInfo)
			if err != nil {
				return nil, err
			}
			if !same {
				out = append(out, file(p, Changed, lowInfo.Size(), info.Size()))
			}
		}
	}
	return out, nil
}

func file(p, change string, before, after int64) Change {
	return Change{
		FileChange:  events.FileChange{Path: p, Change: change, Files: 1, BytesBefore: before, BytesAfter: after, Sensitive: Sensitive(p)},
		SecretFiles: review.SecretLooking([]string{p}),
	}
}

func deletion(p, low string, lowInfo fs.FileInfo) (Change, error) {
	if !lowInfo.IsDir() {
		return file(p, Deleted, lowInfo.Size(), 0), nil
	}
	files, size, err := tree(low)
	if err != nil {
		return Change{}, err
	}
	c := Change{FileChange: events.FileChange{Path: p, Change: Deleted, Dir: true, Files: len(files), BytesBefore: size}}
	for _, f := range files {
		if c.Sensitive == "" {
			c.Sensitive = Sensitive(path.Join(p, f))
		}
	}
	return c, nil
}

// replaced lists what is in the real folder rel and not in the layer's
// copy of it, when that copy stands in for the whole folder.
func (b *Box) replaced(rel string) ([]Change, error) {
	entries, err := os.ReadDir(filepath.Join(b.opts.Project, rel))
	if err != nil {
		return nil, err
	}
	var out []Change
	for _, e := range entries {
		p := path.Join(rel, e.Name())
		if exists(filepath.Join(b.upper, p)) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		c, err := deletion(p, filepath.Join(b.opts.Project, p), info)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// tree lists the files under dir, relative to it, and their total size.
func tree(dir string) (files []string, size int64, err error) {
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if whiteout(info) {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		size += info.Size()
		return nil
	})
	return files, size, err
}

func identical(a string, ai fs.FileInfo, b string, bi fs.FileInfo) (bool, error) {
	if ai.Mode().Type() != bi.Mode().Type() || ai.Mode().Perm()&0o111 != bi.Mode().Perm()&0o111 {
		return false, nil
	}
	if ai.Mode()&fs.ModeSymlink != 0 {
		at, err := os.Readlink(a)
		if err != nil {
			return false, err
		}
		bt, err := os.Readlink(b)
		return at == bt, err
	}
	if !ai.Mode().IsRegular() {
		return true, nil // a socket or the like: nothing to compare
	}
	if ai.Size() != bi.Size() {
		return false, nil
	}
	ab, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	bb, err := os.ReadFile(b)
	return bytes.Equal(ab, bb), err
}

// Diff shows a change as text: a unified diff for a file, a list of
// names for a folder. It reads the layer and the real project and changes
// neither.
func (b *Box) Diff(c events.FileChange) (string, error) {
	up, real := filepath.Join(b.upper, c.Path), filepath.Join(b.opts.Project, c.Path)
	if c.Dir {
		from := up
		if c.Change == Deleted {
			from = real
		}
		var files []string
		if c.Path == gitDir {
			leaves, err := b.walk(gitDir)
			if err != nil {
				return "", err
			}
			for _, l := range leaves {
				if l.Sensitive == "" {
					files = append(files, strings.TrimPrefix(l.Path, gitDir+"/"))
				}
			}
		} else {
			var err error
			if files, _, err = tree(from); err != nil {
				return "", err
			}
		}
		text := fmt.Sprintf("%s: %d files %s\n", c.Path, len(files), c.Change)
		if c.Path == gitDir {
			text = fmt.Sprintf("git's own record of the project (.git): %d files written. This is what commits, branches and the index are made of.\n", len(files))
		}
		for i, f := range files {
			if i == maxListed {
				text += fmt.Sprintf("and %d more\n", len(files)-maxListed)
				break
			}
			text += "  " + f + "\n"
		}
		return text, nil
	}

	diff, err := exec.LookPath("diff")
	if err != nil {
		return "", errors.New("the diff program is not installed, so the change cannot be shown as text")
	}
	before, after := real, up
	switch c.Change {
	case Added:
		before = os.DevNull
	case Deleted:
		after = os.DevNull
	}
	out, err := exec.Command(diff, "-u", "--label", "a/"+c.Path, "--label", "b/"+c.Path, before, after).Output()
	var exit *exec.ExitError
	// diff exits 1 when the files differ, which is the usual case here
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return "", fmt.Errorf("compare %s: %w", c.Path, err)
	}
	if len(out) > maxDiff {
		out = append(out[:maxDiff], []byte("\n... the rest of this change is too long to show\n")...)
	}
	return string(out), nil
}

// Apply makes one change in the real project. It refuses when the real
// files it would touch have themselves changed since the session started,
// because the user would get something other than what they were shown.
func (b *Box) Apply(c events.FileChange) error {
	if c.Path != gitDir || !c.Dir {
		return b.applyOne(c)
	}
	leaves, err := b.walk(gitDir)
	if err != nil {
		return err
	}
	for _, l := range leaves {
		if l.Sensitive != "" {
			continue // decided on its own row
		}
		if err := b.applyOne(l.FileChange); err != nil {
			return err
		}
	}
	return nil
}

func (b *Box) applyOne(c events.FileChange) error {
	up, real := filepath.Join(b.upper, c.Path), filepath.Join(b.opts.Project, c.Path)
	realInfo, err := os.Lstat(real)
	inReal := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	switch c.Change {
	case Deleted:
		if !inReal {
			return nil // already gone, which is what was wanted
		}
		if err := b.unchanged(real, c.Path); err != nil {
			return err
		}
		return os.RemoveAll(real)
	case Added:
		if inReal {
			return fmt.Errorf("%s now exists in your real project and did not when the session started, so the agent's version was not written over it", c.Path)
		}
	case Changed:
		if !inReal {
			return fmt.Errorf("%s has been deleted from your real project since the session started, so the agent's change to it was not applied", c.Path)
		}
		if err := b.unchanged(real, c.Path); err != nil {
			return err
		}
		if realInfo.IsDir() {
			return fmt.Errorf("%s is a folder in your real project and a file in the sandbox, so it was left alone", c.Path)
		}
	default:
		return fmt.Errorf("unknown kind of file change %q", c.Change)
	}
	return copyTree(up, real)
}

// unchanged fails when the real file, or any file in the real folder, was
// modified after the session started.
func (b *Box) unchanged(real, rel string) error {
	return filepath.WalkDir(real, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !d.IsDir() && info.ModTime().After(b.opts.Since) {
			name, relErr := filepath.Rel(b.opts.Project, p)
			if relErr != nil {
				name = rel
			}
			return fmt.Errorf("%s has changed in your real project since the session started, so the agent's change was not applied. Nothing was written", filepath.ToSlash(name))
		}
		return nil
	})
}

// copyTree copies a file, a link or a whole folder from the layer into
// the real project.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case whiteout(info):
			return nil
		case d.IsDir():
			return os.MkdirAll(dst, info.Mode().Perm()|0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			return os.Symlink(target, dst)
		case info.Mode().IsRegular():
			return copyFile(p, dst, info.Mode().Perm())
		}
		return nil
	})
}

// copyFile writes beside the destination and renames into place, so a
// failure part-way leaves the real file as it was.
func copyFile(from, to string, mode fs.FileMode) (err error) {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close() // read-only
	tmp, err := os.CreateTemp(filepath.Dir(to), ".rehearsal-apply-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.Remove(tmp.Name()))
		}
	}()
	if _, err := io.Copy(tmp, src); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), to)
}
