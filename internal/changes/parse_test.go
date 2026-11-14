package changes

import (
	"strings"
	"testing"
)

const sampleDiff = `diff --git a/src/total.go b/src/total.go
index 1111111..2222222 100644
--- a/src/total.go
+++ b/src/total.go
@@ -1,5 +1,5 @@
 package shop

 func Total(a, b int) int {
-	return a + b
+	return a + b + 1
 }
@@ -40,3 +40,4 @@ func Other() {
 	x := 1
+	y := 2
 	_ = x
diff --git a/src/new file.go b/src/new file.go
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/src/new file.go
@@ -0,0 +1,2 @@
+package shop
+func New() {}
\ No newline at end of file
diff --git a/src/old.go b/src/old.go
deleted file mode 100644
index 4444444..0000000
--- a/src/old.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package shop
-func Old() {}
diff --git a/docs/guide.md b/docs/manual.md
similarity index 80%
rename from docs/guide.md
rename to docs/manual.md
index 5555555..6666666 100644
--- a/docs/guide.md
+++ b/docs/manual.md
@@ -1,3 +1,3 @@
 Line one.
-Line two.
+Line 2.
 Line three.
diff --git a/moved/a.txt b/moved/b.txt
similarity index 100%
rename from moved/a.txt
rename to moved/b.txt
diff --git a/logo.png b/logo.png
new file mode 100644
index 0000000..7777777
Binary files /dev/null and b/logo.png differ
diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
diff --git "a/caf\303\251.txt" "b/caf\303\251.txt"
index 8888888..9999999 100644
--- "a/caf\303\251.txt"
+++ "b/caf\303\251.txt"
@@ -1 +1 @@
-old
+new
`

func TestParse(t *testing.T) {
	files := Parse(sampleDiff)
	type row struct {
		path, old string
		status    Status
		binary    bool
		mode      bool
		hunks     int
		add, del  int
	}
	want := []row{
		{"src/total.go", "", Modified, false, false, 2, 2, 1},
		{"src/new file.go", "", Added, false, false, 1, 2, 0},
		{"src/old.go", "", Deleted, false, false, 1, 0, 2},
		{"docs/manual.md", "docs/guide.md", Renamed, false, false, 1, 1, 1},
		{"moved/b.txt", "moved/a.txt", Renamed, false, false, 0, 0, 0},
		{"logo.png", "", Added, true, false, 0, 0, 0},
		{"run.sh", "", Modified, false, true, 0, 0, 0},
		{"café.txt", "", Modified, false, false, 1, 1, 1},
	}
	if len(files) != len(want) {
		t.Fatalf("parsed %d files, want %d: %+v", len(files), len(want), files)
	}
	for i, w := range want {
		f := files[i]
		_, add, del := Totals([]FileDiff{f})
		got := row{f.Path, f.OldPath, f.Status, f.Binary, f.ModeChanged, len(f.Hunks), add, del}
		if got != w {
			t.Errorf("file %d = %+v, want %+v", i+1, got, w)
		}
	}
	second := files[0].Hunks[1]
	if second.OldStart != 40 || second.NewStart != 40 || !strings.HasSuffix(second.Header, "func Other() {") || len(second.Lines) != 3 {
		t.Errorf("second hunk of the first file = %+v", second)
	}
	if n, add, del := Totals(files); n != 8 || add != 6 || del != 5 {
		t.Errorf("totals = %d files, +%d -%d; want 8, +6, -5", n, add, del)
	}
}

func TestParseReadsACutDiffAsFarAsItGoes(t *testing.T) {
	cut := sampleDiff[:strings.Index(sampleDiff, "+	y := 2")]
	files := Parse(cut)
	if len(files) != 1 || len(files[0].Hunks) != 2 || files[0].Hunks[1].Added() != 0 {
		t.Errorf("a diff cut inside its second hunk parsed as %+v", files)
	}
	if got := Parse(""); len(got) != 0 {
		t.Errorf("an empty diff parsed as %+v", got)
	}
}
