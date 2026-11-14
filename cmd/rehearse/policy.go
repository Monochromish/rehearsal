package main

import (
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"rehearsal/internal/events"
	"rehearsal/internal/ledger"
	"rehearsal/internal/policy"
	"rehearsal/internal/review"
)

const envOrgPolicy = "REHEARSAL_ORG_POLICY"

// policyCommand is `rehearse policy check`.
func policyCommand() *cobra.Command {
	orgPath := os.Getenv(envOrgPolicy)
	check := &cobra.Command{
		Use:           "check",
		Short:         "Print the policy in force in this folder, and any rule that has no effect",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			workDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("find the working directory: %w", err)
			}
			p, err := policy.Load(workDir, orgPath)
			if err != nil {
				return err
			}
			printPolicy(cmd.OutOrStdout(), p)
			return nil
		},
	}
	check.Flags().StringVar(&orgPath, "org-policy", orgPath, "the organisation's policy file (env "+envOrgPolicy+")")
	cmd := &cobra.Command{Use: "policy", Short: "Work with the policy file", Args: cobra.NoArgs}
	cmd.AddCommand(check)
	return cmd
}

func printPolicy(w io.Writer, p *policy.Policy) {
	fmt.Fprintln(w, "Built-in rules, which no policy can loosen:")
	fmt.Fprintln(w, "  always held, and always high risk: a force push, a deletion on the far side, any publish,")
	fmt.Fprintln(w, "  a change to who can access a repository, any DELETE request, anything carrying one of")
	fmt.Fprintln(w, "  your details, a planted value or a secret, a push of files that look like secrets, a second")
	fmt.Fprintln(w, "  login or an upload at the model provider, and a request to an address that came from fetched content.")
	list := func(title, path string, rules []policy.Rule) {
		switch {
		case path == "":
			fmt.Fprintf(w, "\n%s: none named.\n", title)
		case len(rules) == 0:
			fmt.Fprintf(w, "\n%s (%s): no rules.\n", title, path)
		default:
			fmt.Fprintf(w, "\n%s (%s):\n", title, path)
			for _, r := range rules {
				fmt.Fprintf(w, "  %s\n", r)
			}
		}
	}
	list("Organisation policy", p.OrgPath, p.Org)
	projectPath := p.ProjectPath
	if p.Project == nil && !pathExists(projectPath) {
		fmt.Fprintf(w, "\nProject policy: there is no %s.\n", policy.ProjectFile)
	} else {
		list("Project policy", projectPath, p.Project)
	}
	fmt.Fprintln(w, "\nThe first matching rule of each file counts. A project rule can only be as strict as the")
	fmt.Fprintln(w, "organisation's for the same action, or stricter.")
	if ignored := p.Check(); len(ignored) > 0 {
		fmt.Fprintln(w, "\nRules with no effect:")
		for _, ig := range ignored {
			fmt.Fprintf(w, "  %s (%s): %s\n", ig.Rule, ig.Source, ig.Why)
		}
	} else {
		fmt.Fprintln(w, "\nNo rule can be told to have no effect. A rule may still be ignored for a particular action;")
		fmt.Fprintln(w, "the page and the report say so when that happens.")
	}
	hash := p.Hash
	if hash == "" {
		hash = "none (no policy file)"
	}
	fmt.Fprintf(w, "\nPolicy hash: %s\n", hash)
}

// labelsOf turns the IDs of what an action carries into the words a
// policy rule can match on: each identifier's kind, and its label.
func labelsOf(identifiers []events.Identifier) func(ids []string) []string {
	return func(ids []string) []string {
		var out []string
		for _, id := range identifiers {
			if slices.Contains(ids, id.ID) {
				out = append(out, string(id.Kind), id.Label)
			}
		}
		return out
	}
}

// offerRules offers a rule for each kind of action and destination among
// the actions waiting for a decision that the user has allowed in enough
// earlier sessions, when no rule speaks about it yet. Only actions that
// may be let through are offered: never one the built-in rules rate high.
func offerRules(log *events.Log, p *policy.Policy, approvals *policy.Approvals, project string, actions []ledger.Action) (map[string]policy.Rule, error) {
	offers := map[string]policy.Rule{}
	var seen []policy.Allowed
	for _, a := range actions {
		x := policy.Allowed{Kind: a.Kind, Destination: a.Destination}
		if a.Decision != ledger.Undecided || a.Kind == events.ActionFile || review.Floor(a.ActionHeld) == events.RiskHigh ||
			slices.Contains(seen, x) || p.Covers(x) {
			continue
		}
		seen = append(seen, x)
		times := approvals.Times(project, x)
		if times < policy.OfferAfter {
			continue
		}
		rule := policy.Rule{Match: policy.Match{Kind: string(x.Kind), Destination: x.Destination}, Action: policy.Allow}
		id := fmt.Sprintf("r%d", len(offers)+1)
		if _, err := log.Append(events.TypeRuleOffered, events.RuleOffered{
			ID: id, Kind: x.Kind, Destination: x.Destination, Line: rule.String(), Times: times,
		}); err != nil {
			return nil, err
		}
		offers[id] = rule
	}
	return offers, nil
}

// allowedThisSession lists each kind of action and destination the user
// allowed and that was then performed, once each.
func allowedThisSession(l ledger.Ledger) []policy.Allowed {
	var out []policy.Allowed
	for _, a := range l.Actions {
		x := policy.Allowed{Kind: a.Kind, Destination: a.Destination}
		if a.Outcome == ledger.Applied && a.Kind != events.ActionFile && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}
