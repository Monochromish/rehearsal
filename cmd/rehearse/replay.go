package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"rehearsal/handlers"
	"rehearsal/internal/events"
	"rehearsal/internal/hold"
	"rehearsal/internal/ident"
	"rehearsal/internal/ledger"
	"rehearsal/internal/policy"
	"rehearsal/internal/proxy"
	"rehearsal/internal/replay"
	"rehearsal/internal/review"
	"rehearsal/internal/server"
	"rehearsal/internal/webui"
	"rehearsal/scenarios"
)

// replayCommands are `rehearse record`, `rehearse replay` and
// `rehearse bench`.
func replayCommands() []*cobra.Command {
	var out string
	record := &cobra.Command{
		Use:   "record [session]",
		Short: "Save a finished session as a recording that can be replayed",
		Long: "Writes what a session tried to do, without the contents of anything: where each\n" +
			"request went and what kind of value it carried, each held command, each file change.\n" +
			"With no session named, the most recent one is used.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return saveRecording(cmd.OutOrStdout(), first(args), out)
		},
	}
	record.Flags().StringVarP(&out, "output", "o", "", "file to write (default: in Rehearsal's state folder, outside the project)")

	var opts replayOptions
	play := &cobra.Command{
		Use:   "replay <recording>",
		Short: "Play a recording back through the holds, the reviewer and the page, with no agent",
		Long: "Nothing in a replay is real. No request is sent, no command is run, and an action\n" +
			"you allow on the page is marked as performed without being performed.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.reviewerModel = defaultModel
			if m := os.Getenv(envReviewerModel); m != "" {
				opts.reviewerModel = m
			}
			return replayRecording(cmd.OutOrStdout(), args[0], opts)
		},
	}
	play.Flags().BoolVar(&opts.noOpen, "no-open", false, "do not open the page in a window")
	play.Flags().IntVar(&opts.port, "port", 0, "port for the page (default: any free port)")
	play.Flags().BoolVar(&opts.discard, "discard", false, "do not wait for a decision: describe what was held by the built-in rules, refuse it all and exit")

	var dir string
	bench := &cobra.Command{
		Use:   "bench",
		Short: "Play a set of scenarios with and without the holds and print what was stopped",
		Args:  cobra.NoArgs,
		// a scenario that is not stopped is a result, not a usage error
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var files fs.FS = scenarios.Files
			if dir != "" {
				files = os.DirFS(dir)
			}
			return runBench(cmd.OutOrStdout(), files)
		},
	}
	bench.Flags().StringVar(&dir, "scenarios", "", "folder of scenario files to use in place of the built-in ones")
	return []*cobra.Command{record, play, bench}
}

func saveRecording(w io.Writer, name, out string) (err error) {
	dir, err := findSession(name)
	if err != nil {
		return err
	}
	log, err := events.OpenExisting(filepath.Join(dir, "events.db"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, log.Close()) }()
	evs, err := log.Since(0)
	if err != nil {
		return err
	}
	rec, err := replay.FromLog(filepath.Base(dir), evs)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the recording: %w", err)
	}
	if out == "" {
		state, err := stateDir()
		if err != nil {
			return err
		}
		folder := filepath.Join(state, "recordings")
		if err := os.MkdirAll(folder, 0o700); err != nil {
			return fmt.Errorf("create the recordings folder: %w", err)
		}
		out = filepath.Join(folder, rec.Name+".json")
	}
	if err := os.WriteFile(out, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write the recording: %w", err)
	}
	fmt.Fprintf(w, "Wrote %s: %d steps. Play it with: rehearse replay %s\n", out, len(rec.Steps), out)
	return nil
}

func loadRecording(path string) (replay.Recording, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return replay.Recording{}, fmt.Errorf("read the recording: %w", err)
	}
	rec, err := replay.Parse(data)
	if err != nil {
		return rec, fmt.Errorf("%s: %w", path, err)
	}
	return rec, nil
}

// replayed is a session with no agent in it: the same log, recorder and
// holder a live session has, and stand-in values to watch for.
type replayed struct {
	dir    string
	log    *events.Log
	store  *hold.Store
	holder *hold.Holder
	player replay.Player
	keep   []string
}

// newReplayed sets up a session to play a recording into. With holding
// off nothing is held, which shows what the recording does unprotected.
func newReplayed(holding bool) (*replayed, error) {
	dir, err := os.MkdirTemp("", "rehearse-replay-")
	if err != nil {
		return nil, fmt.Errorf("create a folder for the replay: %w", err)
	}
	r := &replayed{dir: dir}
	fail := func(err error) (*replayed, error) { return nil, errors.Join(err, r.close()) }
	if r.log, err = events.Open(filepath.Join(dir, "events.db")); err != nil {
		return fail(err)
	}

	// Recordings say what kind of value a request carried. These are the
	// values of each kind this replay watches for and puts back in.
	canaries := ident.NewCanaries()
	detail := events.Identifier{ID: "replay-detail", Kind: events.KindReal, Label: "A detail of yours (a stand-in, for the replay)", Value: "replay-user-" + strings.ToLower(rand.Text()[:10])}
	secret := ident.Secret{ID: "replay-secret", Value: "replay-secret-" + strings.ToLower(rand.Text()), Salt: rand.Text()}
	identifiers := append(slices.Clone(canaries), detail,
		events.Identifier{ID: secret.ID, Kind: events.KindSecret, Label: "A secret from the project (a stand-in, for the replay)", Hash: ident.SaltedHash(secret.Salt, secret.Value)})
	identifiers = append(identifiers, ident.FormatIdentifiers()...)
	matcher := ident.NewMatcher(identifiers, secret)
	for _, c := range canaries {
		r.keep = append(r.keep, c.Value)
	}

	recorder := proxy.NewRecorder(r.log, matcher, defaultProviders)
	if holding {
		if r.store, err = hold.NewStore(filepath.Join(dir, "held")); err != nil {
			return fail(err)
		}
		r.holder = hold.NewHolder(r.log, r.store)
		answers, err := handlers.Shipped()
		if err != nil {
			return fail(fmt.Errorf("load the handler files: %w", err))
		}
		recorder.HoldWrites(proxy.Holding{
			Holder: r.holder, Handlers: answers, Writes: true, Allow: slices.Clone(defaultAllow), Canaries: r.keep, ProviderDoor: true,
		})
	}
	r.player = replay.Player{
		Log: r.log, Recorder: recorder, Holder: r.holder, Masker: hold.Masker{Matcher: matcher, Keep: r.keep},
		Values: map[string]string{
			string(events.KindCanary): ident.Value(canaries, ident.IDCanaryWord),
			string(events.KindReal):   detail.Value,
			string(events.KindSecret): secret.Value,
		},
		Now: time.Now,
	}
	if _, err := r.log.Append(events.TypeSessionStarted, events.SessionStarted{
		Command: []string{"replay"}, ProviderHosts: defaultProviders, Identifiers: identifiers,
		Holding: holding, ToolVersion: version, Replay: true,
	}); err != nil {
		return fail(err)
	}
	return r, nil
}

// play runs the recording and ends the session.
func (r *replayed) play(rec replay.Recording) (replay.Result, error) {
	res, err := r.player.Play(rec)
	if r.holder != nil {
		r.holder.Close()
	}
	if _, aerr := r.log.Append(events.TypeSessionEnded, events.SessionEnded{}); aerr != nil {
		err = errors.Join(err, aerr)
	}
	return res, err
}

func (r *replayed) close() error {
	var err error
	if r.log != nil {
		err = r.log.Close()
	}
	return errors.Join(err, os.RemoveAll(r.dir))
}

type replayOptions struct {
	noOpen, discard bool
	port            int
	reviewerModel   string
}

func replayRecording(w io.Writer, path string, opts replayOptions) (err error) {
	rec, err := loadRecording(path)
	if err != nil {
		return err
	}
	r, err := newReplayed(true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, r.close()) }()

	var web *server.Server
	if !opts.discard {
		if web, err = server.Start(r.log, webui.FS(), opts.port); err != nil {
			return err
		}
		defer func() {
			time.Sleep(700 * time.Millisecond) // let the page read the final state
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err = errors.Join(err, web.Shutdown(ctx))
		}()
		fmt.Fprintf(os.Stderr, "rehearse: replaying %q. Nothing in a replay is real. Page: %s\n", rec.Name, web.URL)
		if !opts.noOpen {
			if _, oerr := openWindow(web.URL); oerr != nil {
				fmt.Fprintf(os.Stderr, "rehearse: could not open a window (%v). Open the address above yourself.\n", oerr)
			}
		}
	}

	if _, err := r.play(rec); err != nil {
		return err
	}
	if r.holder.Count() > 0 {
		if opts.discard {
			err = describeByRules(r.log, true)
		} else {
			reviewer, rerr := newReviewer(options{reviewerModel: opts.reviewerModel, reviewerURL: os.Getenv(envReviewerURL), reviewerTimeout: 45 * time.Second})
			if rerr != nil {
				return rerr
			}
			d := decider{log: r.log, store: r.store, reviewer: reviewer, web: web, keep: r.keep, policy: &policy.Policy{}, dry: true}
			_, err = d.run()
		}
		if err != nil {
			return err
		}
	}
	l, err := ledger.FromLog(r.log)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Replay of %q. No request was sent and no command was run.\n%s", rec.Name, l.Text())
	return nil
}

// describeByRules gives every held action the built-in rules' description
// and, when refuse is set, refuses each one that is still undecided.
func describeByRules(log *events.Log, refuse bool) error {
	l, err := ledger.FromLog(log)
	if err != nil {
		return err
	}
	for _, a := range l.Actions {
		v := review.ByRules(a.ActionHeld)
		if _, err := log.Append(events.TypeActionReviewed, events.ActionReviewed{
			ActionID: a.ID, Summary: v.Summary, Risk: v.Risk, Concern: v.Concern, Source: v.Source,
		}); err != nil {
			return err
		}
		if refuse && a.Decision == ledger.Undecided {
			if _, err := log.Append(events.TypeActionRefused, events.ActionRefused{ActionID: a.ID}); err != nil {
				return err
			}
		}
	}
	return nil
}

// benchRow is one scenario's result.
type benchRow struct {
	name    string
	benign  bool
	steps   int
	through int // steps that go ahead with nothing in the way
	held    int
	high    int
	stopped int // installs stopped
	passed  int // steps that still went ahead with the holds on
}

func (b benchRow) with() string {
	var parts []string
	if b.held > 0 {
		parts = append(parts, fmt.Sprintf("%d held (%d high)", b.held, b.high))
	}
	if b.stopped > 0 {
		parts = append(parts, fmt.Sprintf("%d install stopped", b.stopped))
	}
	if b.passed > 0 {
		parts = append(parts, fmt.Sprintf("%d let through", b.passed))
	}
	return strings.Join(parts, ", ")
}

// result says whether the holds did what the scenario needs: an attack
// must meet a high-risk hold or a stopped install, and ordinary work must
// meet neither.
func (b benchRow) result() string {
	caught := b.high > 0 || b.stopped > 0
	switch {
	case b.benign && caught:
		return "FALSE HOLD"
	case b.benign:
		return "fine"
	case caught:
		return "stopped"
	}
	return "NOT STOPPED"
}

func benchOne(rec replay.Recording) (row benchRow, err error) {
	row = benchRow{name: rec.Name, benign: rec.Benign, steps: len(rec.Steps)}

	open, err := newReplayed(false)
	if err != nil {
		return row, err
	}
	res, err := open.play(rec)
	if err = errors.Join(err, open.close()); err != nil {
		return row, err
	}
	row.through = res.Sent + res.Ran

	held, err := newReplayed(true)
	if err != nil {
		return row, err
	}
	defer func() { err = errors.Join(err, held.close()) }()
	if res, err = held.play(rec); err != nil {
		return row, err
	}
	row.passed = res.Sent + res.Ran
	l, err := ledger.FromLog(held.log)
	if err != nil {
		return row, err
	}
	row.held = len(l.Actions)
	for _, a := range l.Actions {
		if review.Floor(a.ActionHeld) == events.RiskHigh {
			row.high++
		}
	}
	for _, p := range l.Packages {
		if p.Blocked {
			row.stopped++
		}
	}
	return row, nil
}

// loadScenarios reads every .json file in files, in name order.
func loadScenarios(files fs.FS) ([]replay.Recording, error) {
	names, err := fs.Glob(files, "*.json")
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	if len(names) == 0 {
		return nil, errors.New("there are no scenario files (*.json) to run")
	}
	var out []replay.Recording
	for _, name := range names {
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, err
		}
		rec, err := replay.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if rec.Name != strings.TrimSuffix(name, ".json") {
			return nil, fmt.Errorf("%s: its name is %q, and a scenario is named after its file", name, rec.Name)
		}
		if rec.Unprotected == "" {
			return nil, fmt.Errorf("%s: a scenario says what would happen unprotected", name)
		}
		out = append(out, rec)
	}
	return out, nil
}

func runBench(w io.Writer, files fs.FS) error {
	recs, err := loadScenarios(files)
	if err != nil {
		return err
	}
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "SCENARIO\tKIND\tWITHOUT REHEARSAL\tWITH REHEARSAL\tRESULT")
	missed, falseHolds := 0, 0
	for _, rec := range recs {
		row, err := benchOne(rec)
		if err != nil {
			return fmt.Errorf("scenario %s: %w", rec.Name, err)
		}
		kind := "attack"
		if rec.Benign {
			kind = "ordinary work"
		}
		fmt.Fprintf(table, "%s\t%s\t%d of %d steps go ahead\t%s\t%s\n", row.name, kind, row.through, row.steps, row.with(), row.result())
		switch row.result() {
		case "NOT STOPPED":
			missed++
		case "FALSE HOLD":
			falseHolds++
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(w, "\nWhat each would do with nothing in the way:")
	for _, rec := range recs {
		fmt.Fprintf(w, "  %s: %s\n", rec.Name, rec.Unprotected)
	}
	fmt.Fprintf(w, "\n%d scenarios. Attacks not stopped: %d. Ordinary work held as high risk: %d.\n", len(recs), missed, falseHolds)
	fmt.Fprintln(w, "\"Stopped\" means held as high risk for the user's decision, or an install refused. The scenarios are")
	fmt.Fprintln(w, "replayed against the rules with no agent, no network and no model: they show what the rules do with")
	fmt.Fprintln(w, "these inputs, not how an agent behaves.")
	err = nil
	if missed > 0 || falseHolds > 0 {
		err = fmt.Errorf("%d attacks were not stopped and %d ordinary scenarios were held as high risk", missed, falseHolds)
	}
	return errors.Join(err, benchInvestigation(w, recs), benchSorting(w, files))
}
