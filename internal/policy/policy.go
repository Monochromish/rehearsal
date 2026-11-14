// Package policy reads the project's and the organisation's rules for
// held actions and decides, for one action, whether it is let through,
// held or refused, and how risky it must at least be rated.
package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"rehearsal/internal/events"
)

// ProjectFile is where a project keeps its rules, relative to its folder.
const ProjectFile = ".rehearsal/policy.yaml"

const (
	Allow  = "allow"
	Hold   = "hold"
	Refuse = "refuse"
)

// Match says which actions a rule is about. An empty field matches
// anything. Destination and Data may use * for any run of characters.
type Match struct {
	// git-push, npm-publish, gh or http
	Kind string `yaml:"kind,omitempty"`
	// where the action goes, as the page shows it
	Destination string `yaml:"destination,omitempty"`
	// what it carries: real, canary or secret, or the label of one thing
	Data string `yaml:"data,omitempty"`
}

// Rule is one line of a policy.
type Rule struct {
	Match Match `yaml:"match"`
	// allow, hold or refuse. Left out, it is hold.
	Action string `yaml:"action,omitempty"`
	// the lowest rating a matching action may be given: low, medium or high
	Risk string `yaml:"risk,omitempty"`
}

type document struct {
	Version int    `yaml:"version"`
	Rules   []Rule `yaml:"rules"`
}

// Policy is the rules in force for a session. It is read once, before
// the agent starts, and never again.
type Policy struct {
	Org, Project         []Rule
	OrgPath, ProjectPath string
	// identifies the two files' contents; empty when there is neither
	Hash string
}

// String is the rule as one readable line, which is also valid YAML for
// an entry in the rules list.
func (r Rule) String() string {
	var match []string
	for _, kv := range [][2]string{{"kind", r.Match.Kind}, {"destination", r.Match.Destination}, {"data", r.Match.Data}} {
		if kv[1] != "" {
			match = append(match, fmt.Sprintf("%s: %q", kv[0], kv[1]))
		}
	}
	line := "{match: {" + strings.Join(match, ", ") + "}, action: " + r.action()
	if r.Risk != "" {
		line += ", risk: " + r.Risk
	}
	return line + "}"
}

func (r Rule) action() string {
	if r.Action == "" {
		return Hold
	}
	return r.Action
}

var kinds = []string{"", string(events.ActionGitPush), string(events.ActionNpmPublish), string(events.ActionGh), string(events.ActionHTTP)}

func (r Rule) check() error {
	switch {
	case !slices.Contains([]string{"", Allow, Hold, Refuse}, r.Action):
		return fmt.Errorf("action is %q, and must be allow, hold or refuse", r.Action)
	case !slices.Contains([]string{"", "low", "medium", "high"}, r.Risk):
		return fmt.Errorf("risk is %q, and must be low, medium or high", r.Risk)
	case r.Action == "" && r.Risk == "":
		return errors.New("it sets neither an action nor a risk")
	case !slices.Contains(kinds, r.Match.Kind):
		return fmt.Errorf("kind is %q, and must be git-push, npm-publish, gh or http", r.Match.Kind)
	case r.Action == Allow && r.Risk != "":
		return errors.New("it allows the action and also sets a risk, which only a held action has")
	}
	return nil
}

// parse reads one policy file's contents.
func parse(data []byte, path string) ([]Rule, error) {
	var doc document
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s is not a readable policy: %w", path, err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("%s: version is %d, and this rehearse reads version 1", path, doc.Version)
	}
	for i, r := range doc.Rules {
		if err := r.check(); err != nil {
			return nil, fmt.Errorf("%s: rule %d: %w", path, i+1, err)
		}
	}
	return doc.Rules, nil
}

// Load reads the project's policy from projectDir and the organisation's
// from orgPath. Either may be missing; a named organisation file that is
// not there is an error, since someone meant it to apply.
func Load(projectDir, orgPath string) (*Policy, error) {
	p := &Policy{ProjectPath: filepath.Join(projectDir, ProjectFile), OrgPath: orgPath}
	var orgData, projectData []byte
	var err error
	if orgPath != "" {
		if orgData, err = os.ReadFile(orgPath); err != nil {
			return nil, fmt.Errorf("read the organisation policy: %w", err)
		}
		if p.Org, err = parse(orgData, orgPath); err != nil {
			return nil, err
		}
	}
	projectData, err = os.ReadFile(p.ProjectPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		projectData = nil
	case err != nil:
		return nil, fmt.Errorf("read the project policy: %w", err)
	default:
		if p.Project, err = parse(projectData, p.ProjectPath); err != nil {
			return nil, err
		}
	}
	if orgData != nil || projectData != nil {
		sum := sha256.Sum256(fmt.Appendf(nil, "rehearsal policy v1\norganisation %d\n%sproject %d\n%s", len(orgData), orgData, len(projectData), projectData))
		p.Hash = hex.EncodeToString(sum[:])
	}
	return p, nil
}

// Facts is what a rule can be matched against.
type Facts struct {
	Kind        events.ActionKind
	Destination string
	// the kinds and labels of what the action carries
	Data []string
	// what the built-in rules make of it: whether they hold it, and the
	// lowest rating they allow it
	Held  bool
	Floor events.Risk
}

// Ignored is a rule that matched an action and was not allowed to apply.
type Ignored struct {
	Rule   string `json:"rule"`
	Source string `json:"source"`
	Why    string `json:"why"`
}

// Decision is what the policy makes of one action.
type Decision struct {
	// allow, hold or refuse; empty when nothing holds the action at all
	Action string
	// the lowest rating the policy allows, empty when it sets none
	MinRisk events.Risk
	// the rule that set the action, as text; empty for the built-in rules
	Rule    string
	Ignored []Ignored
}

const (
	whyBuiltIn = "the built-in rules always hold an action like this, and no policy can loosen them"
	whyOrg     = "the organisation policy holds or refuses this, and a project policy can only be stricter"
)

// Decide applies the rules to one action. The first matching rule of each
// file counts. The organisation's rule is a floor under the project's, and
// the built-in rules for high-risk actions are a floor under both.
func (p *Policy) Decide(f Facts) Decision {
	d := Decision{}
	if f.Held {
		d.Action = Hold
	}
	if p == nil {
		return d
	}
	builtInHigh := f.Held && f.Floor == events.RiskHigh
	strict := map[string]int{"": 0, Allow: 0, Hold: 1, Refuse: 2}

	org, orgMatched := first(p.Org, f)
	if orgMatched {
		switch {
		case org.action() == Allow && builtInHigh:
			d.Ignored = append(d.Ignored, Ignored{org.String(), "organisation", whyBuiltIn})
		case org.action() == Allow:
			d.Action, d.Rule = Allow, org.String()
		default:
			d.Action, d.Rule = org.action(), org.String()
		}
		d.MinRisk = higher(d.MinRisk, events.Risk(org.Risk))
	}
	if project, ok := first(p.Project, f); ok {
		switch {
		case project.action() == Allow && builtInHigh:
			d.Ignored = append(d.Ignored, Ignored{project.String(), "project", whyBuiltIn})
		case project.action() == Allow && orgMatched && org.action() != Allow:
			d.Ignored = append(d.Ignored, Ignored{project.String(), "project", whyOrg})
		case project.action() == Allow:
			if d.Action != Refuse {
				d.Action, d.Rule = Allow, project.String()
			}
		case strict[project.action()] > strict[d.Action] || d.Action == Allow:
			d.Action, d.Rule = project.action(), project.String()
		}
		d.MinRisk = higher(d.MinRisk, events.Risk(project.Risk))
	}
	if d.Action == Allow && !f.Held {
		d.Action, d.Rule = "", "" // nothing was going to hold it anyway
	}
	return d
}

func higher(a, b events.Risk) events.Risk {
	rank := map[events.Risk]int{"": 0, events.RiskLow: 1, events.RiskMedium: 2, events.RiskHigh: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func first(rules []Rule, f Facts) (Rule, bool) {
	for _, r := range rules {
		if r.Match.matches(f) {
			return r, true
		}
	}
	return Rule{}, false
}

func (m Match) matches(f Facts) bool {
	if m.Kind != "" && m.Kind != string(f.Kind) {
		return false
	}
	if m.Destination != "" && !glob(m.Destination, f.Destination) {
		return false
	}
	if m.Data != "" && !slices.ContainsFunc(f.Data, func(d string) bool { return glob(m.Data, d) }) {
		return false
	}
	return true
}

// glob matches text against a pattern in which * stands for any run of
// characters. Letter case is ignored.
func glob(pattern, text string) bool {
	quoted := strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*")
	re, err := regexp.Compile("(?i)^" + quoted + "$")
	return err == nil && re.MatchString(text)
}

// Check lists the rules that can be told, without any action to hand, to
// have no effect: a project rule that would loosen an organisation rule,
// and any rule that would let through something the built-in rules hold.
func (p *Policy) Check() []Ignored {
	var out []Ignored
	builtIn := func(r Rule) bool {
		return r.action() == Allow && (r.Match.Data != "" || r.Match.Kind == string(events.ActionNpmPublish))
	}
	for _, r := range p.Org {
		if builtIn(r) {
			out = append(out, Ignored{r.String(), "organisation", whyBuiltIn})
		}
	}
	for _, r := range p.Project {
		switch {
		case builtIn(r):
			out = append(out, Ignored{r.String(), "project", whyBuiltIn})
		case r.action() == Allow && slices.ContainsFunc(p.Org, func(o Rule) bool { return o.action() != Allow && o.Match.covers(r.Match) }):
			out = append(out, Ignored{r.String(), "project", whyOrg})
		}
	}
	return out
}

// covers reports whether every action that other matches is matched by m
// too, as far as that can be told from the two patterns alone.
func (m Match) covers(other Match) bool {
	field := func(mine, theirs string) bool {
		return mine == "" || (theirs != "" && glob(mine, theirs))
	}
	return field(m.Kind, other.Kind) && field(m.Destination, other.Destination) && field(m.Data, other.Data)
}

// AppendRule adds one rule to the project's policy file as one line, and
// creates the file when there is none. If the result is not a policy with
// exactly that one rule more, the file is put back as it was.
func AppendRule(projectDir string, r Rule) error {
	path := filepath.Join(projectDir, ProjectFile)
	old, err := os.ReadFile(path)
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return fmt.Errorf("read the project policy: %w", err)
	}
	var before []Rule
	content := "version: 1\nrules:\n"
	if !missing {
		if before, err = parse(old, path); err != nil {
			return err
		}
		content = string(old)
		// an empty list written as "rules: []" cannot be added to as it stands
		content = strings.Replace(content, "rules: []", "rules:", 1)
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
	}
	content += "  - " + r.String() + "\n"
	after, err := parse([]byte(content), path)
	if err != nil || len(after) != len(before)+1 || after[len(after)-1] != r {
		return fmt.Errorf("the rule could not be added to %s as one line without changing what the file means, so the file was left as it is. Add it by hand: %s", path, r.String())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create the policy folder: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write the project policy: %w", err)
	}
	return nil
}
