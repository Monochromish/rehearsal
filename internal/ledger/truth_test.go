package ledger

import (
	"slices"
	"strings"
	"testing"

	"rehearsal/internal/events"
)

// conversation is a request to the provider whose commands can be read.
var conversation = events.Request{ID: "r1", Host: "api.provider.test", Category: events.CategoryProvider, Conversation: true}

func claims(kindsAndWords ...string) events.AgentClaims {
	var c events.AgentClaims
	for i := 0; i+1 < len(kindsAndWords); i += 2 {
		c.Claims = append(c.Claims, events.AgentClaim{Kind: kindsAndWords[i], Words: kindsAndWords[i+1]})
	}
	return c
}

func shell(id, text string) events.AgentAction {
	return events.AgentAction{ID: id, Tool: "Bash", Kind: "shell", Text: text}
}

// truthOf builds the ledger and returns its sentence, having checked that
// it is short and that every entry it names is in the log.
func truthOf(t *testing.T, evs []events.Event) *Truth {
	t.Helper()
	l, err := Build(evs)
	if err != nil {
		t.Fatal(err)
	}
	tr := l.Truth
	if tr == nil {
		return nil
	}
	if n := len(strings.Fields(tr.Sentence)); n > 20 {
		t.Errorf("%d words, want 20 at most: %q", n, tr.Sentence)
	}
	if tr.Sentence != tr.Said.Text+" "+tr.Observed.Text {
		t.Errorf("the sentence %q is not its two halves, %q and %q", tr.Sentence, tr.Said.Text, tr.Observed.Text)
	}
	for _, half := range []TruthHalf{tr.Said, tr.Observed} {
		if len(half.Entries) == 0 {
			t.Errorf("%q rests on no entry", half.Text)
		}
		for _, seq := range half.Entries {
			if seq < 1 || seq > int64(len(evs)) {
				t.Errorf("%q names entry %d, which is not in the log", half.Text, seq)
			}
		}
	}
	return tr
}

// holds reports whether the entries named, taken together, contain every
// one of the texts.
func holds(evs []events.Event, seqs []int64, texts ...string) bool {
	var all strings.Builder
	for _, seq := range seqs {
		all.WriteString(string(evs[seq-1].Type) + " " + string(evs[seq-1].Data) + "\n")
	}
	for _, text := range texts {
		if !strings.Contains(all.String(), text) {
			return false
		}
	}
	return true
}

func TestTruthSetsAClaimAgainstTheCommands(t *testing.T) {
	t.Run("tests pass, and no test command ran", func(t *testing.T) {
		evs := log(t,
			events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}},
			events.TypeRequest, conversation,
			events.TypeAgentAction, shell("t1", "ls -la"),
			events.TypeAgentResult, events.AgentResult{ID: "t1"},
			events.TypeAgentAction, shell("t2", "git status"),
			events.TypeAgentClaims, claims("tests", "All tests pass and the work is finished"),
			events.TypeSessionEnded, events.SessionEnded{},
		)
		tr := truthOf(t, evs)
		if tr == nil || tr.Sentence != "Agent said tests pass. No test command ran." || tr.Form != FormClaim {
			t.Fatalf("truth = %+v", tr)
		}
		if !slices.Equal(tr.Said.Entries, []int64{6}) || !holds(evs, tr.Said.Entries, "agent_claims", `"kind":"tests"`, "All tests pass") {
			t.Errorf("what was said rests on %v, want the entry that holds the claim", tr.Said.Entries)
		}
		// every command that was seen, none of them a test command
		if !slices.Equal(tr.Observed.Entries, []int64{3, 5}) || !holds(evs, tr.Observed.Entries, "ls -la", "git status") {
			t.Errorf("what was observed rests on %v, want the two commands that ran", tr.Observed.Entries)
		}
	})

	t.Run("no command at all rests on the entry that shows commands could be read", func(t *testing.T) {
		evs := log(t,
			events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}},
			events.TypeRequest, conversation,
			events.TypeAgentClaims, claims("tests", "Tests pass"),
			events.TypeSessionEnded, events.SessionEnded{},
		)
		tr := truthOf(t, evs)
		if tr == nil || tr.Sentence != "Agent said tests pass. No test command ran." || !slices.Equal(tr.Observed.Entries, []int64{2}) ||
			!holds(evs, tr.Observed.Entries, `"conversation":true`) {
			t.Fatalf("truth = %+v", tr)
		}
	})

	t.Run("nothing is said about commands that could not be read", func(t *testing.T) {
		evs := log(t,
			events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}},
			events.TypeRequest, events.Request{ID: "r1", Host: "api.other.test", Category: events.CategoryProvider},
			events.TypeAgentClaims, claims("tests", "Tests pass", "build", "The build passes", "lint", "Lint is clean", "push", "I pushed it", "install", "Installed it"),
			events.TypeSessionEnded, events.SessionEnded{},
		)
		if tr := truthOf(t, evs); tr != nil {
			t.Fatalf("truth = %+v, want none: the record cannot say what ran", tr)
		}
	})

	t.Run("tests pass, and the test command failed", func(t *testing.T) {
		evs := log(t,
			events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}},
			events.TypeRequest, conversation,
			events.TypeAgentAction, shell("t1", "go test ./..."),
			events.TypeAgentResult, events.AgentResult{ID: "t1"},
			events.TypeAgentAction, shell("t2", "go test ./... -run Checkout"),
			events.TypeAgentResult, events.AgentResult{ID: "t2", Failed: true},
			events.TypeAgentClaims, claims("tests", "All tests pass"),
			events.TypeSessionEnded, events.SessionEnded{},
		)
		tr := truthOf(t, evs)
		if tr == nil || tr.Sentence != "Agent said tests pass. The test command failed." {
			t.Fatalf("truth = %+v", tr)
		}
		// the last run and its result, not the earlier one that passed
		if !slices.Equal(tr.Observed.Entries, []int64{5, 6}) || !holds(evs, tr.Observed.Entries, "go test ./... -run Checkout", `"failed":true`) {
			t.Errorf("what was observed rests on %v, want the failed run and its result", tr.Observed.Entries)
		}
	})

	t.Run("a claim the record supports gives no sentence", func(t *testing.T) {
		evs := log(t,
			events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}},
			events.TypeRequest, conversation,
			events.TypeAgentAction, shell("t1", "go test ./..."),
			events.TypeAgentResult, events.AgentResult{ID: "t1", Failed: true},
			events.TypeAgentAction, shell("t2", "go test ./..."),
			events.TypeAgentResult, events.AgentResult{ID: "t2"},
			events.TypeAgentAction, shell("t3", "npm run build"),
			events.TypeAgentResult, events.AgentResult{ID: "t3"},
			events.TypeAgentAction, shell("t4", "git commit -m fix"),
			events.TypeAgentResult, events.AgentResult{ID: "t4"},
			events.TypeAgentClaims, claims("tests", "All tests pass", "build", "The build succeeds", "commit", "I committed the fix"),
			events.TypeSessionEnded, events.SessionEnded{},
			events.TypeCommitsCreated, events.CommitsCreated{Known: true, Commits: []string{"abc123"}},
		)
		if tr := truthOf(t, evs); tr != nil {
			t.Fatalf("truth = %+v, want none", tr)
		}
	})

	t.Run("the first contradicted claim is the one reported", func(t *testing.T) {
		evs := log(t,
			events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}},
			events.TypeRequest, conversation,
			events.TypeAgentAction, shell("t1", "go test ./..."),
			events.TypeAgentResult, events.AgentResult{ID: "t1"},
			events.TypeAgentAction, shell("t2", "npm run lint"),
			events.TypeAgentResult, events.AgentResult{ID: "t2", Failed: true},
			events.TypeAgentClaims, claims("tests", "Tests pass", "build", "The build passes", "lint", "Lint is clean"),
			events.TypeSessionEnded, events.SessionEnded{},
		)
		if tr := truthOf(t, evs); tr == nil || tr.Sentence != "Agent said the build passes. No build command ran." {
			t.Fatalf("truth = %+v", tr)
		}
	})

	t.Run("lint, install and commit", func(t *testing.T) {
		for claim, c := range map[string]struct {
			more []any
			want string
		}{
			"lint": {[]any{events.TypeAgentAction, shell("t1", "npx eslint ."), events.TypeAgentResult, events.AgentResult{ID: "t1", Failed: true}},
				"Agent said lint passes. The lint command failed."},
			"install": {[]any{events.TypePackageChecked, events.PackageChecked{Manager: "npm", Name: "shop-utilz", Blocked: true}},
				"Agent said it installed a package. The install of shop-utilz was stopped."},
			"commit": {[]any{events.TypeCommitsCreated, events.CommitsCreated{Known: true, Commits: []string{}}},
				"Agent said it committed. This repository has no new commit."},
		} {
			pairs := []any{events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}}, events.TypeRequest, conversation}
			pairs = append(pairs, c.more...)
			pairs = append(pairs, events.TypeAgentClaims, claims(claim, "done"), events.TypeSessionEnded, events.SessionEnded{})
			if tr := truthOf(t, log(t, pairs...)); tr == nil || tr.Sentence != c.want {
				t.Errorf("%s: truth = %+v, want %q", claim, tr, c.want)
			}
		}
	})
}

func reviewed(id string, risk events.Risk) events.ActionReviewed {
	return events.ActionReviewed{ActionID: id, Summary: "s", Concern: "c", Risk: risk, Source: events.ReviewByRules}
}

func TestTruthSetsAClaimAgainstWhatWasHeld(t *testing.T) {
	push := events.ActionHeld{ID: "a1", Kind: events.ActionGitPush, Command: []string{"git", "push", "origin", "main"}, Destination: "origin", IdentifierIDs: []string{}}
	start := []any{
		events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, Holding: true},
		events.TypeRequest, conversation,
		events.TypeAgentAction, shell("t1", "git push origin main"),
		events.TypeAgentResult, events.AgentResult{ID: "t1"},
		events.TypeActionHeld, push,
		events.TypeAgentClaims, claims("push", "I pushed the branch to origin"),
		events.TypeSessionEnded, events.SessionEnded{},
		events.TypeActionReviewed, reviewed("a1", events.RiskMedium),
	}

	t.Run("it pushed, and the push was held and refused", func(t *testing.T) {
		evs := log(t, append(slices.Clone(start), events.TypeActionRefused, events.ActionRefused{ActionID: "a1"})...)
		tr := truthOf(t, evs)
		if tr == nil || tr.Sentence != "Agent said it pushed. The push was held and refused." {
			t.Fatalf("truth = %+v", tr)
		}
		if !slices.Equal(tr.Said.Entries, []int64{6}) || !holds(evs, tr.Said.Entries, "I pushed the branch") {
			t.Errorf("what was said rests on %v", tr.Said.Entries)
		}
		if !slices.Equal(tr.Observed.Entries, []int64{5, 9}) || !holds(evs, tr.Observed.Entries, "action_held", `"git","push"`, "action_refused", `"action_id":"a1"`) {
			t.Errorf("what was observed rests on %v, want the hold and the refusal", tr.Observed.Entries)
		}
	})

	t.Run("a push that was allowed and sent is what the agent said", func(t *testing.T) {
		evs := log(t, append(slices.Clone(start),
			events.TypeActionApproved, events.ActionApproved{ActionID: "a1"},
			events.TypeActionApplied, events.ActionApplied{ActionID: "a1"})...)
		if tr := truthOf(t, evs); tr != nil {
			t.Fatalf("truth = %+v, want none", tr)
		}
	})

	t.Run("allowed and failed", func(t *testing.T) {
		evs := log(t, append(slices.Clone(start),
			events.TypeActionApproved, events.ActionApproved{ActionID: "a1"},
			events.TypeActionFailed, events.ActionFailed{ActionID: "a1", Reason: "the branch has moved"})...)
		if tr := truthOf(t, evs); tr == nil || tr.Sentence != "Agent said it pushed. The push was allowed and failed." {
			t.Fatalf("truth = %+v", tr)
		}
	})

	t.Run("nothing is said until the session is over", func(t *testing.T) {
		if tr := truthOf(t, log(t, start...)); tr != nil {
			t.Fatalf("truth = %+v while a decision is still waited for", tr)
		}
	})

	t.Run("it deleted something, and the deletion was held and refused", func(t *testing.T) {
		evs := log(t,
			events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, Holding: true},
			events.TypeActionHeld, events.ActionHeld{ID: "a1", Kind: events.ActionHTTP, Method: "DELETE", Host: "cloud.test", Path: "/v1/databases/production", Deletes: true, Destination: "cloud.test", IdentifierIDs: []string{}},
			events.TypeAgentClaims, claims("deletion", "I deleted the old database"),
			events.TypeSessionEnded, events.SessionEnded{},
			events.TypeActionReviewed, reviewed("a1", events.RiskHigh),
			events.TypeActionRefused, events.ActionRefused{ActionID: "a1"},
		)
		if tr := truthOf(t, evs); tr == nil || tr.Sentence != "Agent said it deleted something. The deletion was held and refused." {
			t.Fatalf("truth = %+v", tr)
		}
	})
}

func TestTruthSaysWhatTheAgentWasTold(t *testing.T) {
	held := []events.ActionHeld{
		{ID: "a1", Kind: events.ActionHTTP, Method: "POST", Host: "inbox.test", Path: "/collect", Destination: "inbox.test", IdentifierIDs: []string{"env-1"}},
		{ID: "a2", Kind: events.ActionHTTP, Method: "GET", Host: "inbox.test", Path: "/pixel", Destination: "inbox.test", IdentifierIDs: []string{"env-1"}},
		{ID: "a3", Kind: events.ActionHTTP, Method: "DELETE", Host: "cloud.test", Path: "/v1/databases/production", Deletes: true, Destination: "cloud.test", IdentifierIDs: []string{}},
		{ID: "a4", Kind: events.ActionGitPush, Command: []string{"git", "push", "--force", "origin", "main"}, Force: true, Destination: "origin", IdentifierIDs: []string{}},
	}
	// session is one with these actions held and then decided: approved
	// and performed when named in applied, refused otherwise
	session := func(actions []events.ActionHeld, applied ...string) []events.Event {
		pairs := []any{events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, Holding: true,
			Identifiers: []events.Identifier{{ID: "env-1", Kind: events.KindSecret, Label: "Value of PAYMENTS_API_KEY from .env"}}}}
		for _, a := range actions {
			pairs = append(pairs, events.TypeActionHeld, a)
		}
		pairs = append(pairs, events.TypeSessionEnded, events.SessionEnded{})
		for _, a := range actions {
			pairs = append(pairs, events.TypeActionReviewed, reviewed(a.ID, events.RiskHigh))
		}
		for _, a := range actions {
			if slices.Contains(applied, a.ID) {
				pairs = append(pairs, events.TypeActionApproved, events.ActionApproved{ActionID: a.ID}, events.TypeActionApplied, events.ActionApplied{ActionID: a.ID})
			} else {
				pairs = append(pairs, events.TypeActionRefused, events.ActionRefused{ActionID: a.ID})
			}
		}
		return log(t, pairs...)
	}

	t.Run("four held and none performed", func(t *testing.T) {
		evs := session(held)
		tr := truthOf(t, evs)
		if tr == nil || tr.Form != FormTold || tr.Sentence != "The agent was told 4 actions worked, including deleting database production. None has happened." {
			t.Fatalf("truth = %+v", tr)
		}
		if !slices.Equal(tr.Said.Entries, []int64{2, 3, 4, 5}) || !holds(evs, tr.Said.Entries, "action_held", "/v1/databases/production", `"method":"DELETE"`) {
			t.Errorf("what the agent was told rests on %v, want the four held actions", tr.Said.Entries)
		}
		if !slices.Equal(tr.Observed.Entries, []int64{11, 12, 13, 14}) || !holds(evs, tr.Observed.Entries, "action_refused", `"action_id":"a3"`) {
			t.Errorf("what happened rests on %v, want the four refusals", tr.Observed.Entries)
		}
	})

	t.Run("one of four performed", func(t *testing.T) {
		if tr := truthOf(t, session(held, "a4")); tr == nil || tr.Sentence != "The agent was told 4 actions worked, including deleting database production. 1 has happened." {
			t.Fatalf("truth = %+v", tr)
		}
	})

	t.Run("all performed: what the agent was told is now so", func(t *testing.T) {
		if tr := truthOf(t, session(held, "a1", "a2", "a3", "a4")); tr != nil {
			t.Fatalf("truth = %+v, want none", tr)
		}
	})

	t.Run("one held", func(t *testing.T) {
		if tr := truthOf(t, session(held[2:3])); tr == nil || tr.Sentence != "The agent was told deleting database production worked. It has not happened." {
			t.Fatalf("truth = %+v", tr)
		}
	})

	t.Run("the most serious is named from its fields", func(t *testing.T) {
		if tr := truthOf(t, session([]events.ActionHeld{held[3], held[0]})); tr == nil ||
			tr.Sentence != "The agent was told 2 actions worked, including sending a secret to inbox.test. None has happened." {
			t.Fatalf("truth = %+v", tr)
		}
		if tr := truthOf(t, session(held[3:])); tr == nil || tr.Sentence != "The agent was told a force push worked. It has not happened." {
			t.Fatalf("truth = %+v", tr)
		}
	})

	t.Run("a contradicted claim comes before what the agent was told", func(t *testing.T) {
		evs := session(held)
		// the claim is written when the agent exits, before the decisions
		withClaim := log(t,
			events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, Holding: true},
			events.TypeActionHeld, held[3],
			events.TypeAgentClaims, claims("push", "Pushed to origin"),
			events.TypeSessionEnded, events.SessionEnded{},
			events.TypeActionReviewed, reviewed("a4", events.RiskHigh),
			events.TypeActionRefused, events.ActionRefused{ActionID: "a4"},
		)
		if tr := truthOf(t, withClaim); tr == nil || tr.Form != FormClaim || tr.Sentence != "Agent said it pushed. The push was held and refused." {
			t.Fatalf("truth = %+v", tr)
		}
		_ = evs
	})

	t.Run("nothing held and nothing claimed gives no sentence", func(t *testing.T) {
		evs := log(t,
			events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, Holding: true},
			events.TypeRequest, conversation,
			events.TypeAgentAction, shell("t1", "go test ./..."),
			events.TypeSessionEnded, events.SessionEnded{},
		)
		if tr := truthOf(t, evs); tr != nil {
			t.Fatalf("truth = %+v, want none", tr)
		}
	})

	t.Run("file changes in a sandbox and actions a policy refused are not counted", func(t *testing.T) {
		evs := log(t,
			events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, Holding: true},
			events.TypeActionHeld, events.ActionHeld{ID: "a1", Kind: events.ActionFile, Destination: "your project folder", IdentifierIDs: []string{}, File: &events.FileChange{Path: "a.go"}},
			events.TypeActionHeld, held[2],
			events.TypeActionRefused, events.ActionRefused{ActionID: "a3", Policy: "{match: {kind: http}, action: refuse}"},
			events.TypeSessionEnded, events.SessionEnded{},
			events.TypeActionReviewed, reviewed("a1", events.RiskLow),
			events.TypeActionReviewed, reviewed("a3", events.RiskHigh),
			events.TypeActionRefused, events.ActionRefused{ActionID: "a1"},
		)
		if tr := truthOf(t, evs); tr != nil {
			t.Fatalf("truth = %+v, want none", tr)
		}
	})

	t.Run("it is the first line printed and the top of the report", func(t *testing.T) {
		l, err := Build(session(held))
		if err != nil {
			t.Fatal(err)
		}
		const want = "The agent was told 4 actions worked, including deleting database production. None has happened."
		if text := l.Text(); !strings.HasPrefix(text, "\n"+want+"\n") {
			t.Errorf("the terminal summary does not begin with the sentence:\n%s", text)
		}
		if md := l.Markdown(); !strings.HasPrefix(md, "# Rehearsal report: agent\n\n"+want+"\n\nThe first part rests on entries 2, 3, 4 and 5 of the record, the second on entries 11, 12, 13 and 14.") {
			t.Errorf("the report does not begin with the sentence and its entries:\n%s", md)
		}
	})
}

func TestClaims(t *testing.T) {
	for text, want := range map[string]string{
		"All tests pass.": "tests",
		"I ran the suite and all 14 tests passed with no errors.": "tests",
		"The build succeeds and lint is clean.":                   "build lint",
		"I've committed the change and pushed it to origin.":      "commit push",
		"Installed left-pad. Deleted the old database.":           "install deletion",
		"Summary:\n- Tests are passing\n- Pushed to main":         "tests push",
		// what was not done, is still to do, or is only possible
		"The tests did not pass.":                              "",
		"Tests fail on main.":                                  "",
		"I couldn't run the tests, so I can't say they pass.":  "",
		"Once the tests pass, the branch can be pushed.":       "",
		"The change is ready to be committed.":                 "",
		"You should run the tests to check that they pass.":    "",
		"I have not pushed anything.":                          "",
		"If the build passes I will push.":                     "",
		"":                                                     "",
		"Refactored the checkout module and updated the docs.": "",
	} {
		var kinds []string
		for _, c := range Claims(text) {
			kinds = append(kinds, c.Kind)
			if c.Words == "" || !strings.Contains(strings.ReplaceAll(text, "\n", " "), strings.TrimSuffix(c.Words, "...")) {
				t.Errorf("%q: the claim's words %q are not from the message", text, c.Words)
			}
		}
		if got := strings.Join(kinds, " "); got != want {
			t.Errorf("%q: claims %q, want %q", text, got, want)
		}
	}
	long := Claims("All of the tests in every one of the packages that I touched pass now and so do the ones in every other package of this repository.")
	if len(long) != 1 || len(strings.Fields(long[0].Words)) != 14 || !strings.HasSuffix(long[0].Words, "...") {
		t.Errorf("a long sentence is kept as %+v, want its first 14 words", long)
	}
}
