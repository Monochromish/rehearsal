package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"rehearsal/internal/changes"
	"rehearsal/scenarios"
)

// The labelled set is what the sorting is measured on: enough parts, in
// enough languages, with each label used and the traps among them.
func TestTheLabelledSetIsWhatItSaysItIs(t *testing.T) {
	set, err := loadLabelled(scenarios.Files)
	if err != nil {
		t.Fatal(err)
	}
	if len(set) < 30 {
		t.Errorf("%d labelled parts, want at least 30", len(set))
	}
	byLanguage, byLabel := map[string]int{}, map[changes.Level]int{}
	names := map[string]bool{}
	for _, l := range set {
		byLanguage[l.language]++
		byLabel[l.label]++
		names[l.name[strings.Index(l.name, "-")+1:]] = true
	}
	for _, language := range []string{"Go", "JavaScript", "TypeScript", "Python"} {
		if byLanguage[language] < 3 {
			t.Errorf("%d parts in %s, want at least 3", byLanguage[language], language)
		}
	}
	for _, label := range []changes.Level{changes.Read, changes.Skim, changes.Mechanical} {
		if byLabel[label] < 5 {
			t.Errorf("%d parts labelled %s, want at least 5", byLabel[label], label)
		}
	}
	// what looks mechanical and is not
	for _, trap := range []string{
		"go-renamed-with-one-character", "go-address-inside-a-reformat", "go-build-constraint",
		"ts-lint-rule-switched-off", "py-type-check-switched-off", "js-test-expectation-weakened", "go-test-skipped",
	} {
		if !names[trap] {
			t.Errorf("the set has no part called %s", trap)
		}
	}
	// the one error that hides something: a part to read, folded away
	for _, l := range set {
		if l.label == changes.Read && l.part.Level == changes.Mechanical {
			t.Errorf("%s is labelled read and the rules fold it away (%s)", l.name, l.part.Reason)
		}
	}
}

func TestBenchMeasuresTheSorting(t *testing.T) {
	cmd := exec.Command(buildRehearse(t), "bench")
	cmd.Dir, cmd.Env = t.TempDir(), cleanEnv(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bench: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Sorting: ", " labelled parts in ", "LABELLED", "GIVEN MORE ATTENTION", "Labelled read and placed in Read: ",
		"Labelled mechanical and folded: ", "Given less attention than labelled:", "written by the same hand as the rules",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("bench output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "FOLDED AWAY") {
		t.Errorf("bench says a part to read was folded away:\n%s", out)
	}
}

// A part someone has to read, which the rules fold away, is named and
// fails the bench. Here the label is wrong on purpose: a lockfile called
// something to read.
func TestAPartToReadThatIsFoldedAwayFailsTheBench(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("quiet.json", `{"version":1,"name":"quiet","benign":true,"about":"A read.","unprotected":"A page is fetched.",
		"steps":[{"request":{"method":"GET","url":"https://docs.example.org/x"}}]}`)
	lock := "diff --git a/yarn.lock b/yarn.lock\n--- a/yarn.lock\n+++ b/yarn.lock\n@@ -1,2 +1,2 @@\n-left-pad@1.0.0:\n+left-pad@1.0.1:\n   resolved \"https://registry.example.org/left-pad\"\n"
	write("sorting/01-lock-called-read.diff", "# label: read\n# language: YAML\n# about: A lockfile that someone says must be read.\n"+lock)
	write("sorting/02-lock.diff", "# label: mechanical\n# language: YAML\n# about: The same lockfile, labelled as the rules see it.\n"+lock)

	cmd := exec.Command(buildRehearse(t), "bench", "--scenarios", dir)
	cmd.Dir, cmd.Env = t.TempDir(), cleanEnv(t)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("bench exited 0 with a part to read folded away:\n%s", out)
	}
	for _, want := range []string{
		"A PART TO READ WAS FOLDED AWAY AS MECHANICAL: 01-lock-called-read (lockfile)",
		"Labelled read and placed in Read: 0 of 1. Labelled mechanical and folded: 1 of 1.",
		"01-lock-called-read: labelled read, placed in mechanical (lockfile)",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("bench output lacks %q:\n%s", want, out)
		}
	}

	// a labelled file has to say what it is, and hold one part
	write("sorting/03-unlabelled.diff", lock)
	cmd = exec.Command(buildRehearse(t), "bench", "--scenarios", dir)
	cmd.Dir, cmd.Env = t.TempDir(), cleanEnv(t)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "03-unlabelled.diff") {
		t.Errorf("a file with no label was accepted: %v\n%s", err, out)
	}
}
