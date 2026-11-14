package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"rehearsal/internal/events"
	"rehearsal/internal/investigate"
	"rehearsal/internal/record"
	"rehearsal/internal/replay"
)

type investigateOptions struct {
	ask     string
	key     string
	out     string
	asJSON  bool
	noModel bool
}

func investigateCommand() *cobra.Command {
	var opts investigateOptions
	cmd := &cobra.Command{
		Use:   "investigate [session or folder]",
		Short: "Say what happened in one recorded session or several, with the log entries each statement rests on",
		Long: "Reads a session's folder, an exported record, or a folder holding several of either.\n" +
			"With nothing named, the most recent session is read.\n\n" +
			"Each record's chain and signature are checked first. The findings come from rules alone\n" +
			"and are the same every time for the same records. A local model, when there is one,\n" +
			"adds a short account of each finding; every sentence of it must cite entries, and code\n" +
			"removes any sentence that does not. The report is written to " + investigate.ReportFile + ".",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInvestigate(cmd.Context(), cmd.OutOrStdout(), first(args), opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.ask, "ask", "", "a question to answer from the record, with citations, in place of the report")
	f.StringVar(&opts.key, "key", "", "public key to check signatures against, as a file or as text (default: this computer's own)")
	f.StringVarP(&opts.out, "output", "o", "", "where to write the report (default: beside what was read)")
	f.BoolVar(&opts.asJSON, "json", false, "print the whole case as JSON in place of the summary")
	f.BoolVar(&opts.noModel, "no-model", false, "do not ask the local model for anything: rules only")
	return cmd
}

// investigateTarget is what to read: a file or folder as given, a named
// session, or the most recent session.
func investigateTarget(name string) (string, error) {
	if name != "" {
		if _, err := os.Stat(name); err == nil {
			return filepath.Abs(name)
		}
	}
	return findSession(name)
}

func trustedKey(given string) (ed25519.PublicKey, error) {
	if given != "" {
		return record.ParsePublicKey(given)
	}
	key, err := ownPublicKey()
	if err != nil {
		return nil, nil // nothing to check signatures against; the report says so
	}
	return key, nil
}

// investigator returns the local model, or nil when there is none to ask.
func investigator(opts investigateOptions) investigate.Asker {
	if opts.noModel {
		return nil
	}
	model := defaultModel
	if m := os.Getenv(envReviewerModel); m != "" {
		model = m
	}
	r, err := newReviewer(options{reviewerURL: os.Getenv(envReviewerURL), reviewerModel: model, reviewerTimeout: 90 * time.Second})
	if err != nil || r.model == nil {
		return nil
	}
	return r.model
}

func runInvestigate(ctx context.Context, w io.Writer, name string, opts investigateOptions) error {
	target, err := investigateTarget(name)
	if err != nil {
		return err
	}
	trusted, err := trustedKey(opts.key)
	if err != nil {
		return err
	}
	recs, err := investigate.Open(target, trusted)
	if err != nil {
		return err
	}
	c := investigate.Investigate(recs, version)
	model := investigator(opts)

	if opts.ask != "" {
		custody(w, c)
		fmt.Fprintf(w, "\nQuestion: %s\n\n", opts.ask)
		// a nil *review.Model inside the interface would not compare equal to nil
		var a investigate.Answer
		if model == nil {
			a = c.Ask(ctx, nil, opts.ask)
		} else {
			a = c.Ask(ctx, model, opts.ask)
		}
		_, err := io.WriteString(w, c.AnswerText(a))
		return err
	}
	if model != nil {
		c.Narrate(ctx, model)
	}
	if opts.asJSON {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(c)
	}
	out := opts.out
	if out == "" {
		dir := target
		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			dir = filepath.Dir(target)
		}
		out = filepath.Join(dir, investigate.ReportFile)
	}
	if err := os.WriteFile(out, []byte(c.Markdown()), 0o600); err != nil {
		return fmt.Errorf("write the report: %w", err)
	}
	custody(w, c)
	fmt.Fprintf(w, "\n%s, most serious first.\n", countOf(len(c.Findings), "finding", "findings"))
	for _, f := range c.Findings {
		var cites []string
		for _, r := range f.Refs {
			cites = append(cites, r.Cite())
		}
		fmt.Fprintf(w, "\n  %s  %s\n      rests on %s\n", f.ID, f.Title, strings.Join(cites, " "))
		for _, note := range []string{f.UnverifiedNote, f.RecordNote} {
			if note != "" {
				fmt.Fprintf(w, "      %s\n", note)
			}
		}
	}
	if c.ModelNote != "" {
		fmt.Fprintf(w, "\n%s\n", c.ModelNote)
	}
	fmt.Fprintf(w, "\nThe report, with every entry as it was stored and how each finding was reached:\n  %s\n", out)
	fmt.Fprintf(w, "Rehearsal %s, rule set %s. The same records give the same findings every time.\n", c.ToolVersion, c.RuleSet)
	return nil
}

func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// custody prints, before anything else, whether each record holds up.
func custody(w io.Writer, c *investigate.Case) {
	fmt.Fprintln(w, "Custody")
	for _, cu := range c.Records {
		state := "intact"
		if !cu.Verified {
			state = "NOT VERIFIED"
		}
		fmt.Fprintf(w, "  %s  %s: %s\n", cu.Label, cu.Session, state)
		fmt.Fprintf(w, "      chain: %s\n      signature: %s\n      to check it yourself: %s\n", cu.Chain, cu.Signature, cu.Command)
	}
}

// benchCase is one case's result from the investigation.
type benchCase struct {
	name    string
	benign  bool
	expect  []string
	found   []string
	missing []string
	// steps that carried a watched value; those a search for the value's
	// plain text in what was sent would find; those the findings report
	carried, plain, reported int
}

func (b benchCase) result() string {
	switch {
	case b.benign && len(b.found) > 0:
		return "UNEXPECTED FINDING"
	case b.benign:
		return "no findings"
	case len(b.missing) > 0:
		return "NOT RECONSTRUCTED: no " + strings.Join(b.missing, ", ")
	}
	return "chain reconstructed"
}

// investigateCase plays the recordings of one case, each into a session
// of its own with the holds on, and applies the investigation's rules to
// the records that leaves.
func investigateCase(name string, recs []replay.Recording) (row benchCase, err error) {
	row = benchCase{name: name, benign: true}
	var records []investigate.Record
	for _, rec := range recs {
		row.benign = row.benign && rec.Benign
		row.expect = append(row.expect, rec.Expect...)
		session, err := newReplayed(true)
		if err != nil {
			return row, err
		}
		res, err := session.play(rec)
		var raw []events.Raw
		if err == nil {
			raw, err = session.log.Raw()
		}
		if err = errors.Join(err, session.close()); err != nil {
			return row, err
		}
		row.carried += res.Carried
		row.plain += res.Plain
		records = append(records, investigate.FromEntries(rec.Name, raw))
	}
	c := investigate.Investigate(investigate.Number(records), version)
	row.reported = c.Leaks()
	for _, f := range c.Findings {
		whole := len(f.Chain) >= 2
		for _, r := range f.Refs {
			if _, ok := c.Entry(r); !ok {
				whole = false
			}
		}
		if whole && !slices.Contains(row.found, f.Kind) {
			row.found = append(row.found, f.Kind)
		}
	}
	for _, kind := range row.expect {
		if !slices.Contains(row.found, kind) {
			row.missing = append(row.missing, kind)
		}
	}
	// an attack that lists nothing to look for must still give a finding
	if !row.benign && len(row.expect) == 0 && len(row.found) == 0 {
		row.missing = []string{"finding of any kind"}
	}
	return row, nil
}

// benchInvestigation is the second half of `rehearse bench`: whether the
// investigation rebuilds each attack's chain from the record alone, and
// finds nothing in ordinary work.
func benchInvestigation(w io.Writer, recs []replay.Recording) error {
	var names []string
	cases := map[string][]replay.Recording{}
	for _, rec := range recs {
		name := rec.Case
		if name == "" {
			name = rec.Name
		}
		if _, known := cases[name]; !known {
			names = append(names, name)
		}
		cases[name] = append(cases[name], rec)
	}
	fmt.Fprintln(w, "\nInvestigation: each case is replayed with the holds on, and the rules are applied to the record it leaves.")
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "CASE\tKIND\tSESSIONS\tFINDINGS\tRESULT")
	var failed, attacks, rebuilt, ordinary, clean, carried, plain, reported int
	for _, name := range names {
		row, err := investigateCase(name, cases[name])
		if err != nil {
			return fmt.Errorf("case %s: %w", name, err)
		}
		kind := "attack"
		if row.benign {
			kind = "ordinary work"
			ordinary++
			if len(row.found) == 0 {
				clean++
			}
		} else {
			attacks++
			if len(row.missing) == 0 {
				rebuilt++
			}
		}
		if strings.HasPrefix(row.result(), "NOT") || strings.HasPrefix(row.result(), "UNEXPECTED") {
			failed++
		}
		carried, plain, reported = carried+row.carried, plain+row.plain, reported+row.reported
		found := strings.Join(row.found, ", ")
		if found == "" {
			found = "none"
		}
		fmt.Fprintf(table, "%s\t%s\t%d\t%s\t%s\n", row.name, kind, len(cases[name]), found, row.result())
	}
	if err := table.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "\nAttacks whose chain was reconstructed: %d of %d. Ordinary cases with no findings: %d of %d.\n", rebuilt, attacks, clean, ordinary)
	fmt.Fprintln(w, "\"Reconstructed\" means the rules reported every kind of finding the scenario says it shows (any finding,")
	fmt.Fprintln(w, "when it names none), each with a chain of two steps or more in which every cited entry exists in the record.")
	fmt.Fprintf(w, "\nLeaks: %d steps in these scenarios carry a watched value out.\n", carried)
	fmt.Fprintf(w, "  Found by searching what was sent for the value's plain text: %d.\n", plain)
	fmt.Fprintf(w, "  Reported by the investigation's rules: %d.\n", reported)
	fmt.Fprintln(w, "The plain-text search is given what was sent, which the record does not keep; it misses a value that was")
	fmt.Fprintln(w, "encoded. The rules work from the record alone, where the recorder matched encoded forms as well.")
	fmt.Fprintln(w, "\nThe scenarios are synthetic and were written by the same hand as the rules, so this measures whether the")
	fmt.Fprintln(w, "rules do what they were meant to on known inputs. It says nothing about attacks nobody thought of.")
	if failed > 0 {
		return fmt.Errorf("the investigation did not give the expected result in %d cases", failed)
	}
	return nil
}
