package changes

import (
	"strings"
	"testing"
)

// h makes a hunk from lines that each start with a space, + or -.
func h(lines ...string) Hunk {
	return Hunk{Header: "@@ -1,3 +1,3 @@", OldStart: 1, NewStart: 1, Lines: lines}
}

func changed(path string, hunks ...Hunk) FileDiff {
	return FileDiff{Path: path, Status: Modified, Hunks: hunks}
}

func TestSortRules(t *testing.T) {
	tests := []struct {
		name   string
		file   FileDiff
		level  Level
		reason string
	}{
		// mechanical
		{"white space only", changed("src/total.go", h(" func Total() {", "-\treturn a+b", "+\treturn a + b", " }")), Mechanical, "formatting only"},
		{"a line wrapped onto two", changed("web/app.ts", h("-call(first, second)", "+call(", "+  first,", "+  second)")), Mechanical, "formatting only"},
		{"moved, nothing else", FileDiff{Path: "lib/b.go", OldPath: "lib/a.go", Status: Renamed}, Mechanical, "moved, contents unchanged"},
		{"moved and reformatted", FileDiff{Path: "lib/b.go", OldPath: "lib/a.go", Status: Renamed, Hunks: []Hunk{h("-x:=1", "+x := 1")}}, Mechanical, "moved, with formatting changes only"},
		{"lockfile", changed("package-lock.json", h("-    \"version\": \"1.0.0\"", "+    \"version\": \"1.0.1\"")), Mechanical, "lockfile"},
		{"go.sum", changed("go.sum", h("+example.test/mod v1.0.0 h1:abc=")), Mechanical, "lockfile"},
		{"generated code", changed("api/shop.pb.go", h("-x", "+y")), Mechanical, "generated file"},
		{"a minified bundle", FileDiff{Path: "public/app.min.js", Status: Added, Hunks: []Hunk{h("+!function(){}()")}}, Mechanical, "generated file"},
		{"vendored code", changed("vendor/example.test/lib/lib.go", h("-x", "+y")), Mechanical, "vendored code"},
		{"vendored, deeper", FileDiff{Path: "tools/node_modules/left-pad/index.js", Status: Added, Hunks: []Hunk{h("+module.exports = 1")}}, Mechanical, "vendored code"},

		// skim
		{"documentation", changed("docs/guide.md", h("-Old words.", "+New words.")), Skim, "documentation"},
		{"the README", changed("README.md", h("+One more line.")), Skim, "documentation"},
		{"a licence with no extension", FileDiff{Path: "LICENSE", Status: Added, Hunks: []Hunk{h("+Permission is granted.")}}, Skim, "documentation"},
		{"comments only", changed("src/total.go", h(" func Total() {", "-\t// adds", "+\t// adds the two numbers", " }")), Skim, "comments only"},
		{"comments only, Python", changed("app/main.py", h("-# old note", "+# new note")), Skim, "comments only"},
		{"a new test file", FileDiff{Path: "src/total_test.go", Status: Added, Hunks: []Hunk{h("+func TestTotal(t *testing.T) {}")}}, Skim, "new test file"},
		{"a new test file, JavaScript", FileDiff{Path: "web/src/total.spec.ts", Status: Added, Hunks: []Hunk{h("+it('adds', () => {})")}}, Skim, "new test file"},
		{"editor settings", changed(".editorconfig", h("-indent_size = 2", "+indent_size = 4")), Skim, "editor or formatter settings"},

		// read: everything else
		{"a change to code", changed("src/total.go", h("-\treturn a + b", "+\treturn a - b")), Read, "changes code"},
		{"a new source file", FileDiff{Path: "src/pay.go", Status: Added, Hunks: []Hunk{h("+package shop")}}, Read, "new file"},
		{"a deleted source file", FileDiff{Path: "src/pay.go", Status: Deleted, Hunks: []Hunk{h("-package shop")}}, Read, "deletes a file"},
		{"a binary file", FileDiff{Path: "logo.png", Status: Added, Binary: true}, Read, "a binary file, which can't be shown"},
		{"made executable", FileDiff{Path: "run.sh", Status: Modified, ModeChanged: true}, Read, "its permissions changed"},
		{"a kind of file the rules don't know", changed("data/prices.csv", h("-a,1", "+a,2")), Read, "changes code"},

		// read: looks mechanical, and is not
		{"moved, with one character of logic changed", FileDiff{Path: "lib/b.go", OldPath: "lib/a.go", Status: Renamed, Hunks: []Hunk{h("-\tif a < b {", "+\tif a <= b {")}}, Read, "moved, and changes code"},
		{"reformatted, with one token changed", changed("src/total.go", h("-\treturn a+b", "+\treturn a + c")), Read, "changes code"},
		{"white space inside quoted text", changed("src/msg.go", h("-\tname := \"ab\"", "+\tname := \"a b\"")), Read, "changes code"},
		{"indentation where it has meaning", changed("app/main.py", h(" if ready:", "     start()", "-    notify()", "+notify()")), Read, "changes code"},
		{"indentation in YAML", changed("config/app.yaml", h(" server:", "-  debug: true", "+debug: true")), Read, "changes code"},
		{"a comment the build acts on", changed("src/total.go", h("-//go:build linux", "+//go:build linux || darwin")), Read, "changes a comment that the build or a checker acts on"},
		{"a comment that silences a checker", changed("web/app.ts", h("+// @ts-ignore", " call()")), Read, "changes a comment that the build or a checker acts on"},
		{"a lockfile name inside quoted text is not a lockfile", changed("src/lock.go", h("-\tname := \"go.sum\"", "+\tname := \"go.mod\"")), Read, "changes code"},

		// read: always
		{"an assertion removed", changed("src/total_test.go", h(" func TestTotal(t *testing.T) {", "-\tif got != 3 {", "-\t\tt.Errorf(\"got %d\", got)", "-\t}", " }")), Read, "removes an assertion"},
		{"an assertion removed, JavaScript", changed("web/src/total.test.ts", h(" it('adds', () => {", "-  expect(total(1, 2)).toBe(3)", " })")), Read, "removes an assertion"},
		{"an assertion weakened", changed("tests/test_total.py", h("-    assert total(1, 2) == 3", "+    assert total(1, 2)")), Read, "changes an assertion"},
		{"a test deleted", changed("src/total_test.go", h("-func TestTotal(t *testing.T) {", "-\trun()", "-}")), Read, "deletes a test"},
		{"a test marked skipped", changed("src/total_test.go", h(" func TestTotal(t *testing.T) {", "+\tt.Skip(\"flaky\")", " \trun()")), Read, "marks a test skipped"},
		{"a test marked skipped, JavaScript", changed("web/src/total.test.ts", h("-it('adds', () => {", "+it.skip('adds', () => {")), Read, "marks a test skipped"},
		{"a test marked skipped, Python", changed("tests/test_total.py", h("+@pytest.mark.skip(reason=\"later\")", " def test_total():")), Read, "marks a test skipped"},
		{"a test file deleted", FileDiff{Path: "src/total_test.go", Status: Deleted, Hunks: []Hunk{h("-func TestTotal(t *testing.T) {}")}}, Read, "deletes a test file"},
		{"any other change to an existing test", changed("src/total_test.go", h("-\tname := \"a\"", "+\tname := \"b\"")), Read, "changes an existing test"},
		{"formatting in an existing test is still read", changed("src/total_test.go", h("-\tx:=1", "+\tx := 1")), Read, "changes an existing test"},
		{"a dependency added", changed("package.json", h("     \"vue\": \"^3.5.0\",", "+    \"left-pad\": \"^1.3.0\",")), Read, "dependency file"},
		{"a dependency added, Go", changed("go.mod", h("+require example.test/mod v1.0.0")), Read, "dependency file"},
		{"a dependency added, Python", changed("requirements-dev.txt", h("+requests==2.32.0")), Read, "dependency file"},
		{"CI configuration", changed(".github/workflows/build.yml", h("-      - run: make test", "+      - run: make")), Read, "CI configuration"},
		{"CI configuration, new", FileDiff{Path: ".gitlab-ci.yml", Status: Added, Hunks: []Hunk{h("+build:")}}, Read, "CI configuration"},
		{"the agent's own settings", FileDiff{Path: ".claude/settings.json", Status: Added, Hunks: []Hunk{h("+{}")}}, Read, "decides what runs on this computer"},
		{"a git hook", FileDiff{Path: ".husky/pre-commit", Status: Added, Hunks: []Hunk{h("+npm test")}}, Read, "decides what runs on this computer"},
		{"a shell start-up file", changed(".bashrc", h("+export PATH=/tmp/bin:$PATH")), Read, "decides what runs on this computer"},
		{"a database migration", FileDiff{Path: "db/migrations/0007_drop_orders.sql", Status: Added, Hunks: []Hunk{h("+DROP TABLE orders;")}}, Read, "database migration"},
		{"a file named like a secret", FileDiff{Path: ".env.production", Status: Added, Hunks: []Hunk{h("+MODE=live")}}, Read, "its name suggests it holds secrets"},
		{"a credential in documentation", changed("docs/guide.md", h("+Use api_key=sk-live-0123456789abcdef0123 to try it.")), Read, "adds what looks like a credential"},
		{"a credential in a new file", FileDiff{Path: "src/pay.go", Status: Added, Hunks: []Hunk{h("+const token = \"ghp_0123456789abcdefghij0123456789abcdef\"")}}, Read, "adds what looks like a credential"},
		{"documentation deleted", FileDiff{Path: "docs/guide.md", Status: Deleted, Hunks: []Hunk{h("-Words.")}}, Read, "deletes a file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := Sort([]FileDiff{tt.file})
			if len(parts) != 1 {
				t.Fatalf("sorted into %d parts, want 1: %+v", len(parts), parts)
			}
			if p := parts[0]; p.Level != tt.level || p.Reason != tt.reason {
				t.Errorf("%s: %s (%s), want %s (%s)", tt.file.Path, p.Level, p.Reason, tt.level, tt.reason)
			}
		})
	}
}

func TestSortSplitsIntoPartsAndPutsWhatNeedsReadingFirst(t *testing.T) {
	parts := Sort(Parse(sampleDiff))
	type row struct {
		id, path string
		whole    bool
		line     int
		level    Level
	}
	var got []row
	for _, p := range parts {
		got = append(got, row{p.ID, p.Path, p.Whole, p.Line, p.Level})
	}
	want := []row{
		// one part for each hunk of a changed file, each whole file one part
		{"c1", "src/total.go", false, 1, Read},
		{"c2", "src/total.go", false, 40, Read},
		{"c3", "src/new file.go", true, 0, Read},
		{"c4", "src/old.go", true, 0, Read},
		{"c7", "logo.png", true, 0, Read},
		{"c8", "run.sh", true, 0, Read},
		{"c9", "café.txt", false, 1, Skim},
		{"c5", "docs/manual.md", true, 0, Skim},
		{"c6", "moved/b.txt", true, 0, Mechanical},
	}
	// the renamed guide comes before the text file within Skim: diff order
	want[6], want[7] = want[7], want[6]
	if len(got) != len(want) {
		t.Fatalf("%d parts, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("part %d = %+v, want %+v", i+1, got[i], want[i])
		}
	}
	first := parts[0]
	if first.Added != 1 || first.Removed != 1 || !strings.HasPrefix(first.Text, "@@ -1,5 +1,5 @@\n package shop\n") || !strings.Contains(first.Text, "+\treturn a + b + 1\n") {
		t.Errorf("first part = %+v", first)
	}
	if renamed := parts[6]; renamed.OldPath != "docs/guide.md" || renamed.Reason != "documentation" {
		t.Errorf("the renamed guide = %+v", renamed)
	}
}
