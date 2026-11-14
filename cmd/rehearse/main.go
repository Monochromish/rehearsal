// Command rehearse wraps an AI coding agent, shows where its requests and
// the user's data go, and holds the actions that change something outside
// this machine until the user approves them.
package main

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"rehearsal/handlers"
	"rehearsal/internal/apply"
	"rehearsal/internal/events"
	"rehearsal/internal/hold"
	"rehearsal/internal/ident"
	"rehearsal/internal/ledger"
	"rehearsal/internal/policy"
	"rehearsal/internal/proxy"
	"rehearsal/internal/record"
	"rehearsal/internal/review"
	"rehearsal/internal/run"
	"rehearsal/internal/sandbox"
	"rehearsal/internal/server"
	"rehearsal/internal/standin"
	"rehearsal/internal/webui"
)

const (
	canaryFile   = "CANARY.txt"
	ledgerFile   = "rehearsal-ledger.json"
	reportFile   = "rehearsal-report.md"
	defaultModel = "qwen3.8:27b"

	// recorded in every session and stated in attestations
	version = "0.3.0"

	envReviewerURL   = "REHEARSAL_REVIEWER_URL"
	envReviewerModel = "REHEARSAL_REVIEWER_MODEL"

	// held actions described by the model at the same time
	reviewsAtOnce = 3

	// the copy of this program in the session's folder that the stand-ins
	// are links to
	standinCopy = "rehearse-standin"
)

// platform.claude.com is where Claude Code renews its login. Held, that
// request would log the agent out part-way through a long session.
var defaultProviders = []string{"api.anthropic.com", "mcp-proxy.anthropic.com", "platform.claude.com", "api.openai.com"}

// Hosts the agent reports its own telemetry to. Writes to them are not
// held, because an agent whose telemetry fails may misbehave. They still
// appear in the ledger.
var defaultAllow = []string{"http-intake.logs.us5.datadoghq.com"}

// Hosts the agent program asks for its own updates. Their replies, like
// those of the telemetry hosts, are not listed as content the agent read.
var updateHosts = []string{"downloads.claude.ai"}

type options struct {
	port      int
	providers []string
	noOpen    bool

	noHold          bool
	noDoorCheck     bool
	allow           []string
	handlersDir     string
	reviewerURL     string
	reviewerModel   string
	reviewerTimeout time.Duration

	sandbox      bool
	sandboxWrite []string

	orgPolicy string
}

func main() {
	// Called as git, npm or gh, this binary is the stand-in for that program.
	if name := filepath.Base(os.Args[0]); slices.Contains(standin.Names, name) {
		os.Exit(standin.Main(name, os.Args[1:]))
	}
	// Started by bubblewrap, it is the first process inside the sandbox.
	if len(os.Args) > 1 && os.Args[1] == sandbox.InitArg {
		os.Exit(sandbox.Init(os.Args[2:]))
	}

	opts := options{reviewerModel: defaultModel}
	if m := os.Getenv(envReviewerModel); m != "" {
		opts.reviewerModel = m
	}
	exitCode := 0
	root := &cobra.Command{
		Use:   "rehearse [flags] <command> [args...]",
		Short: "Wrap an AI coding agent: see where your data goes, and approve what it sends out",
		Example: "  rehearse claude\n" +
			"  rehearse --no-hold claude\n" +
			"  rehearse --provider api.example.com -- my-agent --some-flag",
		Args:          cobra.MinimumNArgs(1),
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			code, err := rehearse(opts, args)
			exitCode = code
			return err
		},
	}
	// To wrap a program that shares a name with one of these, give its path.
	root.AddCommand(recordCommands()...)
	root.AddCommand(policyCommand())
	root.AddCommand(replayCommands()...)
	f := root.Flags()
	f.SetInterspersed(false) // everything after the command belongs to it
	f.IntVar(&opts.port, "port", 0, "port for the page (default: any free port)")
	f.StringSliceVar(&opts.providers, "provider", defaultProviders, "hosts to treat as the model provider")
	f.BoolVar(&opts.noOpen, "no-open", false, "do not open the page in a window")
	f.BoolVar(&opts.noHold, "no-hold", false, "hold nothing: only record where requests go")
	f.BoolVar(&opts.noDoorCheck, "no-provider-check", false, "do not hold requests to the model provider that use a second credential or upload a file")
	f.StringSliceVar(&opts.allow, "allow-host", defaultAllow, "hosts whose writes are never held (the agent's own telemetry)")
	f.StringVar(&opts.handlersDir, "handlers", "", "folder of extra handler files for stand-in answers")
	f.StringVar(&opts.reviewerURL, "reviewer-url", os.Getenv(envReviewerURL), "OpenAI-compatible address of the local model (default: Ollama on this machine, or on the Windows host under WSL; env "+envReviewerURL+")")
	f.StringVar(&opts.reviewerModel, "reviewer-model", opts.reviewerModel, "model that describes held actions (env "+envReviewerModel+")")
	f.DurationVar(&opts.reviewerTimeout, "reviewer-timeout", 45*time.Second, "how long to wait for the model on each action")
	f.BoolVar(&opts.sandbox, "sandbox", false, "experimental: keep the agent's file changes in a layer over the project until you allow them, and cut off any network that does not go through Rehearsal")
	f.StringSliceVar(&opts.sandboxWrite, "sandbox-write", nil, "with --sandbox: another path outside the project the agent may write to for real")
	f.StringVar(&opts.orgPolicy, "org-policy", os.Getenv(envOrgPolicy), "the organisation's policy file, which the project's own can only add to (env "+envOrgPolicy+")")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rehearse:", err)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

// rehearse runs one session. The exit code is the wrapped command's, or 1
// when an approved action failed. The error is for anything of ours that
// went wrong around it.
func rehearse(opts options, command []string) (exitCode int, err error) {
	// cleanup runs a shutdown step and keeps its error instead of losing it.
	cleanup := func(what string, fn func() error) {
		if cerr := fn(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("%s: %w", what, cerr))
		}
	}

	if _, err := exec.LookPath(command[0]); err != nil {
		return 127, fmt.Errorf("cannot find the command %q", command[0])
	}
	if opts.sandbox {
		if opts.noHold {
			return 1, errors.New("--sandbox cannot be used with --no-hold: the file changes it keeps back are held actions")
		}
		if err := sandbox.Available(); err != nil {
			return 1, fmt.Errorf("--sandbox: %w", err)
		}
	}
	workDir, err := os.Getwd()
	if err != nil {
		return 1, fmt.Errorf("find the working directory: %w", err)
	}
	stateDir, err := stateDir()
	if err != nil {
		return 1, err
	}
	// Read once, here. A policy file that cannot be read stops the session:
	// carrying on without rules someone wrote would be the wrong surprise.
	// Nothing the agent does to the file later changes this session.
	pol, err := policy.Load(workDir, opts.orgPolicy)
	if err != nil {
		return 1, err
	}
	policyAtStart := policy.ProjectSum(workDir)
	sessionDir := filepath.Join(stateDir, "sessions", time.Now().Format("20060102-150405")+"-"+strings.ToLower(rand.Text()[:6]))
	caDir := filepath.Join(stateDir, "ca")
	for _, d := range []string{sessionDir, caDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return 1, fmt.Errorf("create state directory: %w", err)
		}
	}

	log, err := events.Open(filepath.Join(sessionDir, "events.db"))
	if err != nil {
		return 1, err
	}
	defer cleanup("close the event log", log.Close)

	// The key is found, or made, before the agent starts, so that a problem
	// with it is said now and not after an hour's session.
	signer, keyErr := signingKey()
	if keyErr != nil {
		fmt.Fprintf(os.Stderr, "rehearse: warning: the record of this session will not be signed, and `rehearse verify` will say so:\n%v\n", keyErr)
	}
	refsBefore, inRepo := refsNow(workDir)

	own, warn := ident.CollectReal(workDir)
	if warn != nil {
		fmt.Fprintf(os.Stderr, "rehearse: warning: not watching for some of your details:\n%v\n", warn)
	}
	canaries := ident.NewCanaries()
	identifiers := append(own, canaries...)
	var keep []string // canary values: ours, so they may be shown
	for _, c := range canaries {
		keep = append(keep, c.Value)
	}
	// Secrets are watched for but never stored: the log gets a label and
	// a salted hash, and the values stay in memory.
	envIDs, secrets, warn := ident.EnvSecrets(workDir)
	if warn != nil {
		fmt.Fprintf(os.Stderr, "rehearse: warning: could not read every .env file, so some secrets are not watched for:\n%v\n", warn)
	}
	identifiers = append(identifiers, envIDs...)
	identifiers = append(identifiers, ident.FormatIdentifiers()...)
	matcher := ident.NewMatcher(identifiers, secrets...)
	removeCanary, err := plantCanaryFile(workDir, ident.Value(canaries, ident.IDCanaryWord))
	if err != nil {
		return 1, err
	}
	defer cleanup("remove "+canaryFile, removeCanary)

	// Only the program name is stored: its arguments may contain secrets.
	if _, err := log.Append(events.TypeSessionStarted, events.SessionStarted{
		Command: command[:1], WorkDir: workDir, ProviderHosts: opts.providers, Identifiers: identifiers,
		Holding: !opts.noHold, InputWatched: run.WatchesInput(), ToolVersion: version, PolicyHash: pol.Hash,
		Unsigned: keyErr != nil,
	}); err != nil {
		return 1, err
	}
	for _, ig := range pol.Check() {
		if _, err := log.Append(events.TypePolicyIgnored, events.PolicyIgnored{Rule: ig.Rule, Source: ig.Source, Why: ig.Why}); err != nil {
			return 1, err
		}
	}

	proxyLog, err := os.OpenFile(filepath.Join(sessionDir, "proxy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 1, fmt.Errorf("open the proxy log: %w", err)
	}
	defer cleanup("close the proxy log", proxyLog.Close)

	// Everything through which the agent can still add to the session. It
	// is all stopped as soon as the agent exits, so that what the user is
	// asked to decide on cannot grow afterwards.
	var intake []func()
	stopIntake := sync.OnceFunc(func() {
		for _, stop := range slices.Backward(intake) {
			stop()
		}
	})
	defer stopIntake()

	recorders := []*proxy.Recorder{proxy.NewRecorder(log, matcher, opts.providers)}
	recorders[0].OwnHosts(slices.Concat(opts.allow, updateHosts))
	env := []string{
		"REHEARSAL_CANARY_EMAIL=" + ident.Value(canaries, ident.IDCanaryEmail),
		"REHEARSAL_CANARY_API_KEY=" + ident.Value(canaries, ident.IDCanaryAPIKey),
	}

	var holder *hold.Holder
	var store *hold.Store
	var reviewer reviewer
	// what a sandbox must be able to see of ours, and the second proxy
	var shimDir, socketDir, passURL string
	if !opts.noHold {
		if store, err = hold.NewStore(filepath.Join(sessionDir, "held")); err != nil {
			return 1, err
		}
		// whatever happens, nothing held outlives the session
		defer cleanup("delete the held actions", store.RemoveAll)
		holder = hold.NewHolder(log, store)
		holder.UsePolicy(pol, labelsOf(identifiers))

		answers, err := handlers.Shipped()
		if err == nil && opts.handlersDir != "" {
			answers, err = answers.WithDir(opts.handlersDir)
		}
		if err != nil {
			return 1, fmt.Errorf("load the handler files: %w", err)
		}
		recorders[0].HoldWrites(proxy.Holding{
			Holder: holder, Handlers: answers, Writes: true, Allow: opts.allow, Canaries: keep, ProviderDoor: !opts.noDoorCheck,
		})

		if reviewer, err = newReviewer(opts); err != nil {
			return 1, err
		}
		if reviewer.model != nil {
			// Load the model while the agent works, so the first review is
			// not the slow one. Start-up does not wait for this, and it is
			// allowed to fail: the model may not be running yet, and a
			// review that still cannot reach it falls back to the rules.
			warm, stopWarmUp := context.WithTimeout(context.Background(), 2*time.Minute)
			defer stopWarmUp()
			go func() { _ = reviewer.model.WarmUp(warm) }()
		}

		// git and gh use POST for reads, so commands the stand-ins know to
		// be read-only go through a second proxy that records but never holds.
		// It still holds a request that carries one of the watched values.
		passRecorder := recorders[0].Sibling()
		passRecorder.HoldWrites(proxy.Holding{Holder: holder, Handlers: answers, Canaries: keep})
		recorders = append(recorders, passRecorder)
		pass, err := proxy.Start(caDir, passRecorder, proxyLog)
		if err != nil {
			return 1, err
		}
		intake = append(intake, func() { cleanup("stop the pass-through proxy", pass.Close) })

		socket, err := hold.Listen(holder, hold.Masker{Matcher: matcher, Keep: keep})
		if err != nil {
			return 1, err
		}
		intake = append(intake, func() { cleanup("close the stand-in socket", socket.Close) })
		socketDir, passURL = filepath.Dir(socket.Path), pass.URL

		shimDir = filepath.Join(sessionDir, "bin")
		if err := installStandins(shimDir); err != nil {
			return 1, err
		}
		defer cleanup("remove the stand-ins", func() error { return os.RemoveAll(shimDir) })
		env = append(env,
			"PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
			standin.EnvShimDir+"="+shimDir,
			standin.EnvSocket+"="+socket.Path,
			standin.EnvPassProxy+"="+pass.URL,
		)
	}

	px, err := proxy.Start(caDir, recorders[0], proxyLog)
	if err != nil {
		return 1, err
	}
	intake = append(intake, func() { cleanup("stop the proxy", px.Close) })
	env = append(env, run.ProxyEnv(px.URL, px.CACertPath)...)

	web, err := server.Start(log, webui.FS(), opts.port)
	if err != nil {
		return 1, err
	}
	// Runs before the deferred steps above, so the page server has drained
	// by the time the event log closes.
	defer cleanup("stop the page server", func() error {
		time.Sleep(700 * time.Millisecond) // let the page read the final state
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return web.Shutdown(ctx)
	})

	fmt.Fprintf(os.Stderr, "rehearse: watching. Ledger page: %s\n", web.URL)
	if !opts.noOpen {
		if err := openWindow(web.URL); err != nil {
			fmt.Fprintf(os.Stderr, "rehearse: could not open a window (%v). Open the address above yourself.\n", err)
		}
	}

	var box *sandbox.Box
	var files *fileChanges
	if opts.sandbox {
		keyDir, err := record.KeyDir()
		if err != nil {
			return 1, err
		}
		writable := sandboxWritable(opts.sandboxWrite)
		box, err = sandbox.New(sandbox.Options{
			Project: workDir, Dir: filepath.Join(sessionDir, "sandbox"),
			Self: filepath.Join(shimDir, standinCopy),
			// the log, the held payloads, the certificate authority's key
			// and the signing key are ours and not for the agent to read,
			// and nor are the user's own keys and cloud logins
			Hide:     append([]string{stateDir, keyDir}, sandboxHidden()...),
			Visible:  []string{shimDir, px.CACertPath, socketDir},
			Writable: writable,
			Proxies:  []string{strings.TrimPrefix(px.URL, "http://"), strings.TrimPrefix(passURL, "http://")},
			Since:    canaryPlanted(workDir),
		})
		if err != nil {
			return 1, fmt.Errorf("--sandbox: %w", err)
		}
		intake = append(intake, func() { cleanup("close the sandbox's network", box.Close) })
		if _, err := log.Append(events.TypeSandboxStarted, events.SandboxStarted{Writable: writable}); err != nil {
			return 1, err
		}
		files = &fileChanges{box: box, log: log, holder: holder, workDir: workDir, keep: keep, byID: map[string]events.FileChange{}}
		web.ShowDiffs(files.diff)
		files.watch()
		command = box.Command(command)
	}

	changed := startChanges(workDir, sessionDir)
	exitCode, err = run.Command(command, env, func() {
		// a failed write here costs one line of the report, not the session
		_, _ = log.Append(events.TypeFirstInput, events.FirstInput{})
	})
	cleanup("record what changed in the project", func() error { return changed.finish(log, box) })

	if files != nil {
		// while the stand-ins can still be told about, so the list the user
		// decides on is complete before it closes
		cleanup("list the sandbox's file changes", files.hold)
	}
	if policy.ProjectSum(workDir) != policyAtStart {
		if _, aerr := log.Append(events.TypeSensitiveWrite, events.SensitiveWrite{
			Path: policy.ProjectFile, Real: true,
			Reason: "It changes Rehearsal's own rules for this project. The change had no effect on this session: the rules were read before the agent started.",
		}); aerr != nil {
			err = errors.Join(err, aerr)
		}
	}
	stopIntake()
	if holder != nil {
		holder.Close()
	}
	if _, aerr := log.Append(events.TypeSessionEnded, events.SessionEnded{ExitCode: exitCode}); aerr != nil {
		err = errors.Join(err, aerr)
	}
	for _, r := range recorders {
		if rerr := r.Err(); rerr != nil {
			err = errors.Join(err, fmt.Errorf("the ledger is incomplete, and requests were refused after this: %w", rerr))
		}
	}

	if holder != nil && holder.Count() > 0 {
		d := decider{log: log, store: store, reviewer: reviewer, web: web, keep: keep, policy: pol, workDir: workDir, stateDir: stateDir}
		if box != nil {
			d.files = box
		}
		failed, derr := d.run()
		err = errors.Join(err, derr)
		if failed && exitCode == 0 {
			exitCode = 1
		}
	}
	if files != nil {
		cleanup("clear the sandbox", files.finish)
	}
	// After the decision: in a sandbox the agent's commits are file changes
	// until they are applied, and only then are they in the repository.
	if _, aerr := log.Append(events.TypeCommitsCreated, commitsSince(workDir, refsBefore, inRepo)); aerr != nil {
		err = errors.Join(err, aerr)
	}
	if reviewer.model != nil {
		cleanup("add the model's notes to the report", func() error {
			return writeNotes(log, reviewer.model, recorders[0].FinalText())
		})
	}
	cleanup("write the ledger", func() error { return report(log, sessionDir) })
	if signer != nil {
		cleanup("sign the record", func() error {
			// Sealed first: an entry written after the signature would
			// make the record fail to verify.
			entries, finalHash := log.Seal()
			return record.WriteCheckpoint(sessionDir, record.SignCheckpoint(signer, filepath.Base(sessionDir), entries, finalHash, time.Now()))
		})
	}
	return exitCode, err
}

func signingKey() (ed25519.PrivateKey, error) {
	dir, err := record.KeyDir()
	if err != nil {
		return nil, err
	}
	return record.LoadOrCreateKey(dir)
}

// writeNotes adds the one part of the report that the local model writes:
// the agent's closing claims set against the commands it was seen to run.
// It is an extra. When the model gives no usable answer the report has no
// claims section, so the model's errors are dropped here on purpose.
func writeNotes(log *events.Log, model *review.Model, finalText string) error {
	l, err := current(log)
	if err != nil {
		return err
	}
	if l.Totals.Requests == 0 && len(l.Actions) == 0 {
		return nil // nothing happened that needs summing up
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var notes events.ReportNotes
	if lines := l.CommandLines(); finalText != "" && len(lines) > 0 {
		if claims, err := model.Claims(ctx, finalText, lines); err == nil {
			notes.ClaimsChecked = true
			for _, c := range claims {
				claim := events.ReportClaim{Claim: c.Claim, Verdict: string(c.Verdict)}
				if c.Command > 0 {
					claim.Command = l.Report.Commands[c.Command-1].Text
				}
				notes.Claims = append(notes.Claims, claim)
			}
		}
	}
	if !notes.ClaimsChecked {
		return nil
	}
	_, err = log.Append(events.TypeReportNotes, notes)
	return err
}

// reviewer is the local model, or the reason there is none.
type reviewer struct {
	model *review.Model
	note  string
}

// newReviewer fails only for an address the user gave that cannot be
// used. When no address was given and none can be worked out, held actions
// are described by the built-in rules instead.
func newReviewer(opts options) (reviewer, error) {
	url := opts.reviewerURL
	if url == "" {
		var err error
		if url, err = review.DefaultURL(); err != nil {
			return reviewer{note: fmt.Sprintf("no local model could be located (%v)", err)}, nil
		}
	}
	model, err := review.NewModel(url, opts.reviewerModel, opts.reviewerTimeout)
	return reviewer{model: model}, err
}

// decider takes a session from "the agent has exited with actions held" to
// "each one is applied, refused or failed".
type decider struct {
	log      *events.Log
	store    *hold.Store
	reviewer reviewer
	web      *server.Server
	keep     []string
	// where approved file changes are copied from; nil without a sandbox
	files apply.FileApplier

	policy   *policy.Policy
	workDir  string
	stateDir string
	// a replay: allowed actions are marked as performed and nothing is
	// done, and nothing is remembered or offered for later sessions
	dry bool
}

func (d decider) run() (failed bool, err error) {
	// Ctrl+C at any point from here means: discard, and stop what is running.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	l, err := current(d.log)
	if err != nil {
		return false, err
	}
	fmt.Fprintf(os.Stderr, "\nrehearse: %s held and not yet performed. Reading them...\n", count(len(l.Actions), "action"))
	// A few at a time, each written to the log as it finishes so the page
	// can show it straight away.
	var (
		wg        sync.WaitGroup
		slots     = make(chan struct{}, reviewsAtOnce)
		mu        sync.Mutex
		reviewErr error
	)
	for _, a := range l.Actions {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			v, err := d.describe(ctx, a)
			if err == nil {
				_, err = d.log.Append(events.TypeActionReviewed, events.ActionReviewed{
					ActionID: a.ID, Summary: v.Summary, Risk: v.Risk, Concern: v.Concern, Source: v.Source, Note: v.Note,
				})
			}
			if err != nil {
				mu.Lock()
				reviewErr = errors.Join(reviewErr, err)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if reviewErr != nil {
		return false, reviewErr
	}

	var approvals *policy.Approvals
	offers := map[string]policy.Rule{}
	if !d.dry {
		if approvals, err = policy.LoadApprovals(d.stateDir); err != nil {
			return false, err
		}
		if offers, err = offerRules(d.log, d.policy, approvals, d.workDir, l.Actions); err != nil {
			return false, err
		}
	}

	decision := server.Decision{Discard: true}
	waiting := slices.ContainsFunc(l.Actions, func(a ledger.Action) bool { return a.Decision == ledger.Undecided })
	if waiting {
		fmt.Fprintf(os.Stderr, "rehearse: decide what happens to them on the page: %s\n          (Ctrl+C here discards them all)\n", d.web.URL)
		select {
		case decision = <-d.web.Decisions():
		case <-ctx.Done():
		}
	}

	var approved []events.ActionHeld
	for _, a := range l.Actions {
		if a.Decision != ledger.Undecided {
			continue // refused by the policy when it was held
		}
		if !decision.Discard && slices.Contains(decision.Approve, a.ID) {
			approved = append(approved, a.ActionHeld)
			_, err = d.log.Append(events.TypeActionApproved, events.ActionApproved{ActionID: a.ID})
		} else {
			_, err = d.log.Append(events.TypeActionRefused, events.ActionRefused{ActionID: a.ID})
		}
		if err != nil {
			return false, err
		}
	}
	// File changes go first: a push that was held needs the commits the
	// agent made, and those are file changes until they are applied.
	slices.SortStableFunc(approved, func(x, y events.ActionHeld) int {
		return cmp.Compare(btoi(x.Kind != events.ActionFile), btoi(y.Kind != events.ActionFile))
	})
	applier := apply.Applier{Log: d.log, Store: d.store, Keep: d.keep, Files: d.files, Dry: d.dry}
	_, failed, err = applier.Run(ctx, approved)
	if err != nil || decision.Discard || d.dry {
		return failed, err
	}

	// A rule is written only because the user ticked it, and only now.
	for _, id := range decision.Rules {
		rule := offers[id]
		if aerr := policy.AppendRule(d.workDir, rule); aerr != nil {
			err = errors.Join(err, aerr)
			continue
		}
		if _, aerr := d.log.Append(events.TypeRuleAdded, events.RuleAdded{ID: id, Line: rule.String(), Path: policy.ProjectFile}); aerr != nil {
			err = errors.Join(err, aerr)
		}
	}
	after, lerr := current(d.log)
	if lerr != nil {
		return failed, errors.Join(err, lerr)
	}
	return failed, errors.Join(err, approvals.Record(d.workDir, allowedThisSession(after)))
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (d decider) describe(ctx context.Context, a ledger.Action) (review.Verdict, error) {
	// A file change is described from what is on disk. Its contents are
	// not sent to the model.
	if a.Kind == events.ActionFile {
		return review.ByRules(a.ActionHeld), nil
	}
	if d.reviewer.model == nil {
		v := review.ByRules(a.ActionHeld)
		v.Note = d.reviewer.note
		return v, nil
	}
	payload, err := d.store.Load(a.ID)
	if err != nil {
		return review.Verdict{}, err
	}
	content := payload.Body
	if a.Kind != events.ActionHTTP {
		content = payload.Stdin
	}
	var carries []string
	for _, id := range a.Carries {
		carries = append(carries, id.Label)
	}
	return d.reviewer.model.Review(ctx, a.ActionHeld, carries, review.Excerpt(content, d.keep)), nil
}

func current(log *events.Log) (ledger.Ledger, error) {
	return ledger.FromLog(log)
}

// report prints the ledger and writes it, with the session report, in the
// session's folder. They are not written in the project: an agent reads
// what is in its working folder, and the ledger holds the user's own
// details.
func report(log *events.Log, sessionDir string) error {
	l, err := current(log)
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stderr, l.Text())
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the ledger: %w", err)
	}
	ledgerPath, reportPath := filepath.Join(sessionDir, ledgerFile), filepath.Join(sessionDir, reportFile)
	if err := os.WriteFile(ledgerPath, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(reportPath, []byte(l.Markdown()), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\nSaved:\n  %s\n  %s\n", underHome(ledgerPath), underHome(reportPath))
	return nil
}

// installStandins puts git, npm and gh in dir, each a link to a copy of
// this program. The copy is in the session's own folder because every git
// call the agent makes starts it, and that has to be quick.
func installStandins(dir string) (err error) {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find this program to make the stand-ins: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the stand-in folder: %w", err)
	}
	src, err := os.Open(self)
	if err != nil {
		return fmt.Errorf("make the stand-ins: %w", err)
	}
	defer src.Close() // read-only
	const copyName = standinCopy
	dst, err := os.OpenFile(filepath.Join(dir, copyName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return fmt.Errorf("make the stand-ins: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		return errors.Join(fmt.Errorf("make the stand-ins: %w", err), dst.Close())
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("make the stand-ins: %w", err)
	}
	for _, name := range standin.Names {
		if err := os.Symlink(copyName, filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("make the %s stand-in: %w", name, err)
		}
	}
	return nil
}

// plantCanaryFile writes the canary word to CANARY.txt. The returned
// function removes it again, unless a file of that name was already there.
func plantCanaryFile(workDir, word string) (remove func() error, err error) {
	path := filepath.Join(workDir, canaryFile)
	existed := true
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		existed = false
	} else if err != nil {
		return nil, fmt.Errorf("check for an existing %s: %w", canaryFile, err)
	}
	if err := os.WriteFile(path, []byte(word+"\n"), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", canaryFile, err)
	}
	return func() error {
		if existed {
			return nil
		}
		return os.Remove(path)
	}, nil
}

// underHome writes a path inside the home folder with a ~ in place of
// the folder, which is shorter and keeps the username off the screen.
func underHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return path
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}

// stateDir holds the certificate authority and the session logs.
func stateDir() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "rehearsal"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find your home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "rehearsal"), nil
}

// openWindow shows the page as a standalone Edge window when running
// inside WSL. Anywhere else it does nothing: the address is already printed.
func openWindow(url string) error {
	version, err := os.ReadFile("/proc/version")
	if errors.Is(err, os.ErrNotExist) {
		return nil // not Linux, so not WSL
	}
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(string(version)), "microsoft") {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd.exe", "/c", "start", "msedge", "--app="+url)
	cmd.Dir = "/mnt/c" // cmd.exe complains when started from a Linux path
	return cmd.Run()
}

func count(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
