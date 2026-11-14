// Package standin is what runs when the wrapped agent calls git, npm or
// gh. It holds the few commands that change something outside this
// machine and hands every other command to the real program.
package standin

import (
	"slices"
	"strings"

	"rehearsal/internal/events"
)

// Plan is what to do with one command line.
type Plan struct {
	Hold bool
	// ReadOnly marks a command known to change nothing. Its requests go
	// through the proxy that records without holding, because gh and git
	// both use POST for reads.
	ReadOnly bool

	Kind            events.ActionKind
	Force           bool
	Deletes         bool
	Publishes       bool
	ChangesAccess   bool
	Minor           bool
	NeedsRealAnswer bool

	// git only
	Globals []string // options before the subcommand, such as -C <dir>
	Push    pushSpec
}

// Classify decides from the arguments alone, without running anything.
func Classify(name string, args []string) Plan {
	switch name {
	case "git":
		return classifyGit(args)
	case "npm":
		return classifyNpm(args)
	case "gh":
		return classifyGh(args)
	}
	return Plan{}
}

// git options that come before the subcommand and take a separate value
var gitGlobalsWithValue = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--config-env"}

// splitGit separates "git -C dir push origin" into [-C dir], "push", [origin].
func splitGit(args []string) (globals []string, sub string, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			return args[:i], a, args[i+1:]
		}
		if slices.Contains(gitGlobalsWithValue, a) {
			i++
		}
	}
	return args, "", nil
}

func classifyGit(args []string) Plan {
	globals, sub, rest := splitGit(args)
	// send-pack and http-push are the plumbing under push
	if sub != "push" && sub != "send-pack" && sub != "http-push" {
		return Plan{ReadOnly: true, Globals: globals}
	}
	spec := parsePush(rest)
	if spec.dryRun {
		return Plan{ReadOnly: true, Globals: globals}
	}
	return Plan{
		Hold:    true,
		Kind:    events.ActionGitPush,
		Force:   spec.force,
		Deletes: spec.deletes,
		Globals: globals,
		Push:    spec,
	}
}

type pushSpec struct {
	remote      string
	refspecs    []string
	force       bool
	deletes     bool
	all         bool
	tags        bool
	mirror      bool
	dryRun      bool
	setUpstream bool
}

func parsePush(args []string) pushSpec {
	var s pushSpec
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			positional = append(positional, args[i+1:]...)
			i = len(args)
		case a == "-o" || a == "--push-option" || a == "--repo" || a == "--receive-pack" || a == "--exec":
			i++ // takes a separate value
		case a == "--force" || strings.HasPrefix(a, "--force-with-lease") || a == "--force-if-includes":
			s.force = true
		case a == "--delete" || a == "--prune":
			s.deletes = true
		case a == "--mirror":
			s.mirror, s.force, s.deletes = true, true, true
		case a == "--all" || a == "--branches":
			s.all = true
		case a == "--tags":
			s.tags = true
		case a == "--dry-run":
			s.dryRun = true
		case a == "--set-upstream":
			s.setUpstream = true
		case strings.HasPrefix(a, "--"):
			// any other long option changes neither what is sent nor where
		case strings.HasPrefix(a, "-") && len(a) > 1:
			for _, c := range a[1:] {
				switch c {
				case 'f':
					s.force = true
				case 'd':
					s.deletes = true
				case 'n':
					s.dryRun = true
				case 'u':
					s.setUpstream = true
				case 'o':
					i++
				}
			}
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) > 0 {
		s.remote, s.refspecs = positional[0], positional[1:]
	}
	for _, r := range s.refspecs {
		if strings.HasPrefix(r, "+") {
			s.force = true
		}
		if strings.HasPrefix(r, ":") {
			s.deletes = true
		}
	}
	return s
}

// The npm command is the first argument that names one. Arguments before
// it can be option values ("npm --registry URL publish"), so a plain
// "first word that is not an option" would be wrong.
var npmCommands = strings.Fields(`access add adduser audit bugs cache ci cit completion config dedupe
	deprecate diff dist-tag docs doctor edit exec explain find-dupes fund get help hook i info init install
	install-ci-test install-test it link list login logout ls org outdated owner pack ping pkg prefix profile
	prune publish query rebuild remove repo restart rm root run run-script sbom search set shrinkwrap star
	stars start stop team test token uninstall unpublish unstar update version view whoami x`)

func classifyNpm(args []string) Plan {
	for _, a := range args {
		if !slices.Contains(npmCommands, a) {
			continue
		}
		if a == "publish" && !slices.Contains(args, "--dry-run") {
			return Plan{Hold: true, Kind: events.ActionNpmPublish, Publishes: true}
		}
		break
	}
	// not ReadOnly: other npm commands can write to the registry too, and
	// the holding proxy should still catch those
	return Plan{}
}

type ghRule struct {
	needsAnswer bool
	// deletes a repository or a release
	deletes bool
	// opens or comments on an issue or pull request, or adds a label
	minor bool
}

// gh commands that create or change something. A command in neither table
// is handed to the real gh as it is, and the proxy still holds any write
// it makes.
var ghWrites = map[string]ghRule{
	"pr create": {needsAnswer: true, minor: true}, "pr merge": {}, "pr close": {}, "pr reopen": {}, "pr edit": {},
	"pr comment": {minor: true}, "pr review": {}, "pr ready": {}, "pr lock": {}, "pr unlock": {}, "pr update-branch": {},
	"pr revert": {needsAnswer: true},

	"issue create": {needsAnswer: true, minor: true}, "issue close": {}, "issue reopen": {}, "issue edit": {},
	"issue comment": {minor: true}, "issue delete": {}, "issue lock": {}, "issue unlock": {}, "issue pin": {},
	"issue unpin": {}, "issue transfer": {}, "issue develop": {},

	"release create": {}, "release delete": {deletes: true}, "release edit": {},
	"release upload": {}, "release delete-asset": {},

	"repo create": {}, "repo delete": {deletes: true}, "repo edit": {}, "repo rename": {}, "repo archive": {},
	"repo unarchive": {}, "repo fork": {}, "repo sync": {},

	"gist create": {needsAnswer: true}, "gist edit": {}, "gist delete": {}, "gist rename": {},

	"workflow run": {}, "workflow enable": {}, "workflow disable": {},
	"run rerun": {}, "run cancel": {}, "run delete": {},
	"secret set": {}, "secret delete": {}, "variable set": {}, "variable delete": {},
	"label create": {minor: true}, "label edit": {}, "label delete": {}, "label clone": {},
}

// The parts of GitHub's API that decide who can see or change a
// repository: collaborators, branch protection and rule sets.
var accessPaths = []string{"/collaborators", "/protection", "/rulesets"}

var ghReads = []string{
	"pr list", "pr view", "pr status", "pr diff", "pr checks",
	"issue list", "issue view", "issue status",
	"release list", "release view",
	"repo list", "repo view",
	"gist list", "gist view",
	"workflow list", "workflow view", "run list", "run view",
	"label list", "secret list", "variable list", "variable get",
}

func classifyGh(args []string) Plan {
	var words []string
	rest := args
	for i := 0; i < len(args) && len(words) < 2; i++ {
		a := args[i]
		switch {
		case a == "-R" || a == "--repo" || a == "--hostname":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			words = append(words, a)
			rest = args[i+1:]
		}
	}
	if len(words) == 0 {
		return Plan{}
	}
	if words[0] == "api" {
		return classifyGhAPI(args)
	}
	if words[0] == "search" || words[0] == "status" {
		return Plan{ReadOnly: true}
	}
	command := strings.Join(words, " ")
	if rule, ok := ghWrites[command]; ok {
		p := Plan{Hold: true, Kind: events.ActionGh, Deletes: rule.deletes, Minor: rule.minor, NeedsRealAnswer: rule.needsAnswer}
		switch command {
		case "repo edit":
			p.ChangesAccess = hasOption(rest, "--visibility")
		case "repo create":
			p.ChangesAccess = slices.Contains(rest, "--public")
		case "pr edit", "issue edit":
			p.Minor = onlyAddsLabels(rest)
		}
		return p
	}
	return Plan{ReadOnly: slices.Contains(ghReads, command)}
}

func hasOption(args []string, name string) bool {
	return slices.ContainsFunc(args, func(a string) bool { return a == name || strings.HasPrefix(a, name+"=") })
}

// onlyAddsLabels reports whether an edit does nothing but add labels:
// every option is --add-label.
func onlyAddsLabels(args []string) bool {
	adds := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--add-label":
			adds = true
			i++
		case strings.HasPrefix(a, "--add-label="):
			adds = true
		case a == "-R" || a == "--repo":
			i++
		case strings.HasPrefix(a, "-"):
			return false
		}
	}
	return adds
}

// classifyGhAPI holds "gh api" calls that write. GraphQL always uses POST,
// so a GraphQL call is held only when its text is a mutation or cannot be
// seen (it comes from a file).
func classifyGhAPI(args []string) Plan {
	method, hasFields, graphql, query := "", false, false, ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() string { // the value of the option at i, in either "-X v" or "--x=v" form
			if _, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(a, "--") {
				return v
			}
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case a == "-X" || a == "--method" || strings.HasPrefix(a, "--method="):
			method = strings.ToUpper(value())
		case strings.HasPrefix(a, "-X") && len(a) > 2:
			method = strings.ToUpper(a[2:])
		case a == "-f" || a == "-F" || a == "--field" || a == "--raw-field" ||
			strings.HasPrefix(a, "--field=") || strings.HasPrefix(a, "--raw-field="):
			hasFields = true
			if q, ok := strings.CutPrefix(value(), "query="); ok {
				query = q
			}
		case a == "--input" || strings.HasPrefix(a, "--input="):
			hasFields = true
			value()
		case a == "graphql":
			graphql = true
		}
	}
	if method == "" {
		method = "GET"
		if hasFields {
			method = "POST"
		}
	}
	held := Plan{
		Hold: true, Kind: events.ActionGh, Deletes: method == "DELETE",
		ChangesAccess: slices.ContainsFunc(args, func(a string) bool {
			return slices.ContainsFunc(accessPaths, func(p string) bool { return strings.Contains(a, p) })
		}),
	}
	switch {
	case method == "GET" || method == "HEAD":
		return Plan{ReadOnly: true}
	case !graphql:
		return held
	case query == "" || strings.HasPrefix(query, "@"):
		return held
	case strings.HasPrefix(strings.TrimSpace(query), "mutation"):
		return held
	}
	return Plan{ReadOnly: true}
}
