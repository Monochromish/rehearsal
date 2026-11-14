package changes

import (
	"strconv"
	"strings"
)

// Status is what happened to a file.
type Status string

const (
	Added    Status = "added"
	Deleted  Status = "deleted"
	Modified Status = "modified"
	Renamed  Status = "renamed"
)

// FileDiff is one file's part of a unified diff.
type FileDiff struct {
	// the file's path afterwards, or before for a deleted file
	Path string
	// where it was, for a renamed file
	OldPath string
	Status  Status
	Binary  bool
	// the file's permissions changed, for example to executable
	ModeChanged bool
	Hunks       []Hunk
}

// Hunk is one run of changed lines with the lines around it.
type Hunk struct {
	// the line that starts with @@
	Header string
	// first line of the hunk in the old file and in the new one
	OldStart, NewStart int
	// each line with its leading space, + or -
	Lines []string
}

// Added and Removed count the hunk's changed lines.
func (h Hunk) Added() int   { return h.count('+') }
func (h Hunk) Removed() int { return h.count('-') }

func (h Hunk) count(sign byte) int {
	n := 0
	for _, l := range h.Lines {
		if l != "" && l[0] == sign {
			n++
		}
	}
	return n
}

// Parse reads a diff written by git. A diff cut short is read as far as
// it goes.
func Parse(patch string) []FileDiff {
	var files []FileDiff
	var cur *FileDiff
	var hunk *Hunk
	closeHunk := func() {
		if cur != nil && hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	closeFile := func() {
		closeHunk()
		if cur != nil {
			files = append(files, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		if rest, ok := strings.CutPrefix(line, "diff --git "); ok {
			closeFile()
			cur = &FileDiff{Status: Modified, Path: headerPath(rest)}
			continue
		}
		if cur == nil {
			continue
		}
		if hunk != nil && line != "" && strings.ContainsRune(" +-\\", rune(line[0])) {
			if line[0] != '\\' { // "\ No newline at end of file" is about the line before
				hunk.Lines = append(hunk.Lines, line)
			}
			continue
		}
		if hunk != nil && line == "" {
			hunk.Lines = append(hunk.Lines, " ") // an empty context line that lost its space
			continue
		}
		switch {
		case strings.HasPrefix(line, "@@ "):
			closeHunk()
			hunk = &Hunk{Header: line}
			hunk.OldStart, hunk.NewStart = starts(line)
		case strings.HasPrefix(line, "new file mode "):
			cur.Status = Added
		case strings.HasPrefix(line, "deleted file mode "):
			cur.Status = Deleted
		case strings.HasPrefix(line, "old mode "), strings.HasPrefix(line, "new mode "):
			cur.ModeChanged = true
		case strings.HasPrefix(line, "rename from "):
			cur.Status, cur.OldPath = Renamed, unquote(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			cur.Status, cur.Path = Renamed, unquote(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "Binary files "), line == "GIT binary patch":
			cur.Binary = true
		case strings.HasPrefix(line, "--- a/") && cur.Status == Deleted:
			cur.Path = unquote(strings.TrimPrefix(line, "--- a/"))
		case strings.HasPrefix(line, "+++ b/") && cur.Status != Renamed:
			cur.Path = unquote(strings.TrimPrefix(line, "+++ b/"))
		}
	}
	closeFile()
	return files
}

// headerPath takes the path from "a/PATH b/PATH", which is all a diff says
// about a file whose contents are not shown. It is exact when the file
// was not renamed, and a rename names both paths on lines of their own.
func headerPath(rest string) string {
	if strings.HasPrefix(rest, "\"") {
		// git quotes a path with unusual characters in it
		if end := strings.Index(rest[1:], "\" "); end >= 0 {
			return strings.TrimPrefix(unquote(rest[:end+2]), "a/")
		}
	}
	if n := (len(rest) - len("a/ b/")) / 2; n > 0 && len(rest) >= 2+n {
		return rest[2 : 2+n]
	}
	return rest
}

func unquote(path string) string {
	if strings.HasPrefix(path, "\"") {
		if u, err := strconv.Unquote(path); err == nil {
			return u
		}
	}
	// a path at the end of a "---" or "+++" line may carry a tab and a date
	if tab := strings.IndexByte(path, '\t'); tab >= 0 {
		return path[:tab]
	}
	return path
}

// starts reads the two line numbers from "@@ -12,4 +15,6 @@".
func starts(header string) (oldStart, newStart int) {
	fields := strings.Fields(header)
	if len(fields) < 3 {
		return 0, 0
	}
	num := func(s string) int {
		s = strings.TrimLeft(s, "+-")
		if comma := strings.IndexByte(s, ','); comma >= 0 {
			s = s[:comma]
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0
		}
		return n
	}
	return num(fields[1]), num(fields[2])
}

// Totals counts the files in a diff and the lines added and removed.
func Totals(files []FileDiff) (count, added, removed int) {
	for _, f := range files {
		for _, h := range f.Hunks {
			added += h.Added()
			removed += h.Removed()
		}
	}
	return len(files), added, removed
}
