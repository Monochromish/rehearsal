package changes

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"rehearsal/internal/redact"
	"rehearsal/internal/review"
	"rehearsal/internal/sandbox"
)

// Level is how much attention a change needs.
type Level string

const (
	// nothing a person needs to read: shown folded
	Mechanical Level = "mechanical"
	// worth a glance
	Skim Level = "skim"
	// has to be read
	Read Level = "read"
)

func (l Level) rank() int {
	switch l {
	case Read:
		return 3
	case Skim:
		return 2
	}
	return 1
}

// Part is one piece of a session's changes, small enough to judge on its
// own: one hunk of a changed file, or a whole file when it is new,
// deleted, renamed or binary.
type Part struct {
	// stable for a given diff: "c1", "c2" and so on, in the diff's order
	ID   string `json:"id"`
	Path string `json:"path"`
	// where the file was, when it was renamed
	OldPath string `json:"old_path,omitempty"`
	Status  Status `json:"status"`
	// the part is the whole file, not one hunk of it
	Whole  bool `json:"whole"`
	Binary bool `json:"binary,omitempty"`
	// the first line of the part in the file as it is now; 0 for a whole file
	Line    int `json:"line,omitempty"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
	// the least attention the rules allow it, and why, in a few words
	Level  Level  `json:"level"`
	Reason string `json:"reason"`
	// what the local model said the change does, when it has read it
	Sentence string `json:"sentence,omitempty"`
	// the rules said skim and the model said read
	Raised bool `json:"raised,omitempty"`
	// the model is going to read it and has not yet
	Pending bool `json:"pending,omitempty"`
	// the part of the diff this is, for showing
	Text string `json:"-"`
}

// Sort splits a diff into parts and gives each a level by rules alone.
// The rules set a minimum: nothing here is ever a reason to read less.
// Parts come back with those to read first, then those to skim, then the
// mechanical ones, each group in the diff's order.
func Sort(files []FileDiff) []Part {
	var parts []Part
	for _, f := range files {
		whole := f.Status != Modified || f.Binary || len(f.Hunks) == 0
		if whole {
			p := Part{Path: f.Path, OldPath: f.OldPath, Status: f.Status, Whole: true, Binary: f.Binary}
			_, p.Added, p.Removed = Totals([]FileDiff{f})
			p.Level, p.Reason = wholeFile(f)
			var text strings.Builder
			for _, h := range f.Hunks {
				text.WriteString(hunkText(h))
			}
			p.Text = text.String()
			parts = append(parts, p)
			continue
		}
		for _, h := range f.Hunks {
			p := Part{Path: f.Path, Status: f.Status, Line: h.NewStart, Added: h.Added(), Removed: h.Removed(), Text: hunkText(h)}
			p.Level, p.Reason = hunk(f.Path, h)
			parts = append(parts, p)
		}
	}
	for i := range parts {
		parts[i].ID = fmt.Sprintf("c%d", i+1)
	}
	slices.SortStableFunc(parts, func(a, b Part) int { return b.Level.rank() - a.Level.rank() })
	return parts
}

func hunkText(h Hunk) string {
	return h.Header + "\n" + strings.Join(h.Lines, "\n") + "\n"
}

// wholeFile judges a file that is new, gone, moved, binary or changed
// only in its permissions.
func wholeFile(f FileDiff) (Level, string) {
	level, reason, known := byPath(f.Path, f.Status)
	if known && level == Read {
		return level, reason
	}
	// before what the path says of lesser things: a guide that only moved
	// is not worth even a glance
	if f.Status == Renamed && len(f.Hunks) == 0 && !f.ModeChanged && !f.Binary {
		return Mechanical, "moved, contents unchanged"
	}
	if known {
		return level, reason
	}
	isTest := testFile(f.Path)
	switch {
	case f.Status == Deleted && isTest:
		return Read, "deletes a test file"
	case f.Status == Deleted:
		return Read, "deletes a file"
	case f.Binary:
		return Read, "a binary file, which can't be shown"
	case f.Status == Renamed:
		// as much attention as the most demanding change made on the way
		level, reason := Mechanical, "moved, with formatting changes only"
		for _, h := range f.Hunks {
			if l, r := hunk(f.Path, h); l.rank() > level.rank() {
				level, reason = l, "moved, and "+r
			}
		}
		return level, reason
	case f.ModeChanged && len(f.Hunks) == 0:
		return Read, "its permissions changed"
	case f.Status == Added && isTest:
		return Skim, "new test file"
	case f.Status == Added:
		if secretText(f.Hunks...) {
			return Read, "adds what looks like a credential"
		}
		return Read, "new file"
	}
	return Read, "changes a file"
}

// hunk judges one run of changed lines in a file that already existed.
func hunk(file string, h Hunk) (Level, string) {
	if secretText(h) {
		return Read, "adds what looks like a credential"
	}
	if level, reason, ok := byPath(file, Modified); ok {
		return level, reason
	}
	if testFile(file) {
		return Read, testChange(h)
	}
	if formattingOnly(file, h) {
		return Mechanical, "formatting only"
	}
	if only, directive := commentsOnly(file, h); only && directive {
		return Read, "changes a comment that the build or a checker acts on"
	} else if only {
		return Skim, "comments only"
	}
	return Read, "changes code"
}

// What a path alone says about a file, strictest first. ok is false when
// the path says nothing and the contents have to be looked at.
func byPath(file string, status Status) (level Level, reason string, ok bool) {
	name := path.Base(file)
	lower := strings.ToLower(file)
	switch {
	case len(review.SecretLooking([]string{file})) > 0:
		return Read, "its name suggests it holds secrets", true
	case under(lower, ciPaths...):
		return Read, "CI configuration", true
	case sandbox.Sensitive(file) != "":
		return Read, "decides what runs on this computer", true
	case migration(lower):
		return Read, "database migration", true
	case slices.Contains(manifests, name) || requirements.MatchString(name):
		return Read, "dependency file", true
	case under(lower, "vendor/", "node_modules/", "third_party/", ".yarn/"):
		return Mechanical, "vendored code", true
	case slices.Contains(lockfiles, name):
		return Mechanical, "lockfile", true
	case generated(lower):
		return Mechanical, "generated file", true
	}
	if status == Deleted {
		return "", "", false // a deletion of anything else is judged by what it is
	}
	switch {
	case slices.Contains(docExts, path.Ext(lower)) || under(lower, "docs/", "doc/") || slices.Contains(docNames, strings.ToUpper(strings.TrimSuffix(name, path.Ext(name)))):
		return Skim, "documentation", true
	case slices.Contains(editorFiles, name) || under(lower, ".vscode/", ".idea/"):
		return Skim, "editor or formatter settings", true
	}
	return "", "", false
}

var (
	ciPaths = []string{".github/workflows/", ".gitlab-ci.yml", ".circleci/", "jenkinsfile", "azure-pipelines.yml", ".travis.yml", "bitbucket-pipelines.yml", ".buildkite/"}
	// files that say what a project depends on
	manifests = []string{
		"package.json", "go.mod", "pyproject.toml", "Pipfile", "setup.py", "setup.cfg", "Cargo.toml", "Gemfile",
		"pom.xml", "build.gradle", "build.gradle.kts", "composer.json", "Package.swift", "pubspec.yaml", "mix.exs",
	}
	requirements = regexp.MustCompile(`^requirements[\w.-]*\.txt$`)
	lockfiles    = []string{
		"package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb", "go.sum", "Cargo.lock",
		"poetry.lock", "uv.lock", "Pipfile.lock", "composer.lock", "Gemfile.lock", "Package.resolved", "pubspec.lock", "mix.lock",
	}
	docExts     = []string{".md", ".mdx", ".rst", ".adoc", ".txt"}
	docNames    = []string{"README", "LICENSE", "LICENCE", "CHANGELOG", "CONTRIBUTING", "NOTICE", "AUTHORS", "CODE_OF_CONDUCT"}
	editorFiles = []string{".editorconfig", ".prettierrc", ".prettierrc.json", ".prettierignore", ".gitignore", ".gitattributes", ".clang-format", ".rustfmt.toml", "rustfmt.toml", ".gofmt"}
)

// under reports whether file is, or is inside, one of the named paths at
// any depth. A name ending in a slash is a folder.
func under(file string, names ...string) bool {
	for _, n := range names {
		if strings.HasSuffix(n, "/") {
			if strings.HasPrefix(file, n) || strings.Contains(file, "/"+n) {
				return true
			}
		} else if file == n || strings.HasSuffix(file, "/"+n) {
			return true
		}
	}
	return false
}

var generatedName = regexp.MustCompile(`(\.pb\.go|\.pb\.cc|\.pb\.h|_pb2\.py|_generated\.go|\.gen\.go|\.generated\.\w+|\.min\.js|\.min\.css|\.map|\.snap)$`)

func generated(file string) bool {
	return generatedName.MatchString(file) || under(file, "dist/", "__snapshots__/", "__generated__/")
}

func migration(file string) bool {
	return under(file, "migrations/", "migrate/", "db/migrate/", "alembic/versions/") || path.Base(file) == "schema.prisma"
}

var testName = regexp.MustCompile(`(_test\.(go|py|rb|exs)|\.(test|spec)\.[cm]?[jt]sx?|(^|/)test_[^/]*\.py|Tests?\.(java|kt|cs|swift)|_spec\.rb)$`)

func testFile(file string) bool {
	return testName.MatchString(file) || under(strings.ToLower(file), "tests/", "__tests__/", "spec/")
}

var (
	assertion = regexp.MustCompile(`\b(assert\w*|expect|require\.\w+|t\.(Error|Errorf|Fatal|Fatalf|Fail|FailNow)|should\w*|XCTAssert\w*|verify|check)\s*[.(!]|\bassert\s+\S|\.to(Be|Equal|Have|Throw|Match|Contain)\w*\(`)
	testDecl  = regexp.MustCompile(`^\s*(func Test\w*\(|def test_\w*\(|(it|test|describe)\s*\(|@Test\b|#\[test\]|fn test_\w*\()`)
	skipped   = regexp.MustCompile(`\bt\.Skip(f|Now)?\(|\b(it|test|describe)\.(skip|todo)\s*\(|\bx(it|describe|test)\s*\(|@(pytest\.mark\.(skip|skipif|xfail)|unittest\.skip\w*|Disabled|Ignore)\b|#\[ignore\]|\bpending\s*\(`)
)

// testChange says what a hunk does to a test that already existed. Any
// change to one is read; this picks the most serious thing to say.
func testChange(h Hunk) string {
	count := func(sign byte, re *regexp.Regexp) int {
		n := 0
		for _, l := range h.Lines {
			if l != "" && l[0] == sign && re.MatchString(l[1:]) {
				n++
			}
		}
		return n
	}
	switch {
	case count('+', skipped) > count('-', skipped):
		return "marks a test skipped"
	case count('-', testDecl) > count('+', testDecl):
		return "deletes a test"
	case count('-', assertion) > count('+', assertion):
		return "removes an assertion"
	case count('-', assertion) > 0:
		return "changes an assertion"
	}
	return "changes an existing test"
}

// secretText reports whether a line being added holds something shaped
// like a credential.
func secretText(hunks ...Hunk) bool {
	for _, h := range hunks {
		for _, l := range h.Lines {
			if l != "" && l[0] == '+' && redact.Text(l[1:]) != l[1:] {
				return true
			}
		}
	}
	return false
}

// Languages in which the space at the start of a line is part of the
// meaning, so a change to it is never formatting.
var indentMatters = []string{".py", ".pyi", ".yaml", ".yml", ".haml", ".pug", ".coffee", ".nim", ".fs", ".hs", ".mk"}

// formattingOnly reports whether a hunk changes nothing but white space
// outside of quoted text: the removed lines and the added ones are the
// same once that is taken out.
func formattingOnly(file string, h Hunk) bool {
	if slices.Contains(indentMatters, path.Ext(strings.ToLower(file))) || strings.EqualFold(path.Base(file), "Makefile") {
		return false
	}
	var before, after strings.Builder
	for _, l := range h.Lines {
		switch {
		case l == "":
		case l[0] == '-':
			before.WriteString(squeeze(l[1:]))
		case l[0] == '+':
			after.WriteString(squeeze(l[1:]))
		}
	}
	return before.String() == after.String()
}

// squeeze removes white space from a line, except inside quotes, where it
// is part of a value.
func squeeze(line string) string {
	var out strings.Builder
	var quote rune
	escaped := false
	for _, r := range line {
		switch {
		case quote != 0:
			out.WriteRune(r)
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'' || r == '`':
			quote = r
			out.WriteRune(r)
		case r == ' ' || r == '\t' || r == '\r':
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// How a line that is only a comment starts, by kind of file.
var commentStarts = map[string][]string{
	".go": {"//", "/*", "*"}, ".js": {"//", "/*", "*"}, ".jsx": {"//", "/*", "*"}, ".ts": {"//", "/*", "*"}, ".tsx": {"//", "/*", "*"},
	".mjs": {"//", "/*", "*"}, ".cjs": {"//", "/*", "*"}, ".java": {"//", "/*", "*"}, ".kt": {"//", "/*", "*"}, ".c": {"//", "/*", "*"},
	".h": {"//", "/*", "*"}, ".cc": {"//", "/*", "*"}, ".cpp": {"//", "/*", "*"}, ".hpp": {"//", "/*", "*"}, ".cs": {"//", "/*", "*"},
	".swift": {"//", "/*", "*"}, ".rs": {"//", "/*", "*"}, ".scala": {"//", "/*", "*"}, ".php": {"//", "/*", "*", "#"}, ".dart": {"//", "/*", "*"},
	".vue": {"//", "/*", "*", "<!--"}, ".css": {"/*", "*"}, ".scss": {"//", "/*", "*"},
	".py": {"#"}, ".rb": {"#"}, ".sh": {"#"}, ".bash": {"#"}, ".zsh": {"#"}, ".pl": {"#"}, ".r": {"#"}, ".toml": {"#"}, ".yaml": {"#"}, ".yml": {"#"},
	".ex": {"#"}, ".exs": {"#"}, ".tf": {"#", "//"}, ".sql": {"--"}, ".lua": {"--"}, ".hs": {"--"}, ".html": {"<!--"}, ".xml": {"<!--"},
}

// Comments that a compiler, a build or a checker reads, so that changing
// one changes what happens.
var directive = regexp.MustCompile(`^(//\s*go:|//go:|//\s*\+build|//\s*nolint|//\s*eslint-|/\*\s*eslint-|//\s*@ts-|//\s*prettier-ignore|//\s*istanbul|#!|#\s*type:\s*ignore|#\s*noqa|#\s*pragma|#\s*pylint:|#\s*mypy:|#\s*frozen_string_literal|#\s*shellcheck|#\s*rubocop:|//\s*swiftlint:|//\s*NOLINT|#\s*-\*-|//\s*cgo|#cgo)`)

// commentsOnly reports whether every changed line of a hunk is a comment
// or blank, and whether any of them is one that tools act on.
func commentsOnly(file string, h Hunk) (only, acts bool) {
	starts, known := commentStarts[path.Ext(strings.ToLower(file))]
	if !known {
		return false, false
	}
	changed := 0
	for _, l := range h.Lines {
		if l == "" || (l[0] != '+' && l[0] != '-') {
			continue
		}
		text := strings.TrimSpace(l[1:])
		if text == "" {
			continue
		}
		changed++
		if !slices.ContainsFunc(starts, func(s string) bool { return strings.HasPrefix(text, s) }) {
			return false, false
		}
		if directive.MatchString(text) {
			acts = true
		}
	}
	return changed > 0, acts
}
