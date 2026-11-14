package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"text/tabwriter"

	"rehearsal/internal/changes"
)

// labelled is one part of a change with the attention a person says it
// needs, and the attention the rules give it.
type labelled struct {
	name     string
	language string
	about    string
	label    changes.Level
	part     changes.Part
}

// loadLabelled reads the labelled parts: the .diff files in the folder
// "sorting", each one part, each beginning with lines that say what it is:
//
//	# label: read
//	# language: Go
//	# about: A limit check goes from less-than to less-than-or-equal.
func loadLabelled(files fs.FS) ([]labelled, error) {
	names, err := fs.Glob(files, "sorting/*.diff")
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	var out []labelled
	for _, name := range names {
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, err
		}
		one := labelled{name: strings.TrimSuffix(path.Base(name), ".diff")}
		rest := string(data)
		for strings.HasPrefix(rest, "# ") {
			line, after, _ := strings.Cut(rest, "\n")
			rest = after
			key, value, ok := strings.Cut(strings.TrimPrefix(line, "# "), ": ")
			if !ok {
				return nil, fmt.Errorf("%s: %q is not a line of the form \"# name: value\"", name, line)
			}
			switch key {
			case "label":
				one.label = changes.Level(value)
			case "language":
				one.language = value
			case "about":
				one.about = value
			default:
				return nil, fmt.Errorf("%s: %q is not label, language or about", name, key)
			}
		}
		if !slices.Contains([]changes.Level{changes.Read, changes.Skim, changes.Mechanical}, one.label) || one.language == "" || one.about == "" {
			return nil, fmt.Errorf("%s: a labelled part says its label (read, skim or mechanical), its language and what it is about", name)
		}
		parts := changes.Sort(changes.Parse(rest))
		if len(parts) != 1 {
			return nil, fmt.Errorf("%s: it holds %d parts, and a labelled file holds exactly one", name, len(parts))
		}
		one.part = parts[0]
		out = append(out, one)
	}
	return out, nil
}

func levelRank(l changes.Level) int {
	return slices.Index([]changes.Level{changes.Mechanical, changes.Skim, changes.Read}, l)
}

// benchSorting is the third part of `rehearse bench`: each labelled part
// is put to the rules that sort a session's changes, and where they put it
// is set against its label. A part labelled read that the rules fold away
// as mechanical is the one error that hides something from a person, so
// each is printed by name and the bench fails.
func benchSorting(w io.Writer, files fs.FS) error {
	set, err := loadLabelled(files)
	if err != nil {
		return err
	}
	if len(set) == 0 {
		return nil // a folder of scenarios with no labelled parts in it
	}
	var languages []string
	for _, l := range set {
		if !slices.Contains(languages, l.language) {
			languages = append(languages, l.language)
		}
	}
	fmt.Fprintf(w, "\nSorting: %d labelled parts in %d languages and file types, each put to the rules that sort a session's changes.\n", len(set), len(languages))
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "LABELLED\tPARTS\tPLACED THERE\tGIVEN MORE ATTENTION\tGIVEN LESS")
	count := func(label changes.Level, test func(got, want int) bool) int {
		n := 0
		for _, l := range set {
			if l.label == label && test(levelRank(l.part.Level), levelRank(l.label)) {
				n++
			}
		}
		return n
	}
	same := func(got, want int) bool { return got == want }
	more := func(got, want int) bool { return got > want }
	less := func(got, want int) bool { return got < want }
	all := func(int, int) bool { return true }
	for _, label := range []changes.Level{changes.Read, changes.Skim, changes.Mechanical} {
		fmt.Fprintf(table, "%s\t%d\t%d\t%d\t%d\n", label, count(label, all), count(label, same), count(label, more), count(label, less))
	}
	if err := table.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "\nLabelled read and placed in Read: %d of %d. Labelled mechanical and folded: %d of %d.\n",
		count(changes.Read, same), count(changes.Read, all), count(changes.Mechanical, same), count(changes.Mechanical, all))

	list := func(heading string, test func(got, want int) bool) {
		fmt.Fprintln(w, heading)
		none := true
		for _, l := range set {
			if test(levelRank(l.part.Level), levelRank(l.label)) {
				none = false
				fmt.Fprintf(w, "  %s: labelled %s, placed in %s (%s)\n", l.name, l.label, l.part.Level, l.part.Reason)
			}
		}
		if none {
			fmt.Fprintln(w, "  none")
		}
	}
	list("Given more attention than labelled, which costs reading time and hides nothing:", more)
	list("Given less attention than labelled:", less)

	var folded []string
	for _, l := range set {
		if l.label == changes.Read && l.part.Level == changes.Mechanical {
			folded = append(folded, l.name)
			fmt.Fprintf(w, "A PART TO READ WAS FOLDED AWAY AS MECHANICAL: %s (%s). %s\n", l.name, l.part.Reason, l.about)
		}
	}
	fmt.Fprintf(w, "\nThe set is small, %d parts, and was written by the same hand as the rules. It shows that the rules do\n", len(set))
	fmt.Fprintln(w, "what was meant on these cases. It says little about changes nobody thought to write down.")
	if len(folded) > 0 {
		return errors.New("the sorting rules folded away as mechanical what a person has to read: " + strings.Join(folded, ", "))
	}
	return nil
}
