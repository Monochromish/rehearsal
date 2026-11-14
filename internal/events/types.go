// Package events is the append-only event log. The page, the terminal
// summary and the JSON file are all derived from it.
package events

import (
	"encoding/json"
	"time"
)

type Type string

const (
	TypeSessionStarted Type = "session_started"
	TypeRequest        Type = "request"
	TypeResponse       Type = "response"
	TypeIdentifierSeen Type = "identifier_seen"
	TypeSessionEnded   Type = "session_ended"

	TypeActionHeld     Type = "action_held"
	TypeActionReviewed Type = "action_reviewed"
	TypeActionApproved Type = "action_approved"
	TypeActionRefused  Type = "action_refused"
	TypeActionApplied  Type = "action_applied"
	TypeActionFailed   Type = "action_failed"

	TypeOutsideContent Type = "outside_content"

	TypeFirstInput  Type = "first_input"
	TypeModelUsage  Type = "model_usage"
	TypeAgentAction Type = "agent_action"
	TypeAgentResult Type = "agent_result"
	TypeReportNotes Type = "report_notes"

	TypeCommitsCreated  Type = "commits_created"
	TypeChangesCaptured Type = "changes_captured"
	TypeChangeReviewed  Type = "change_reviewed"
	TypeChangesReading  Type = "changes_reading"

	TypeSandboxStarted Type = "sandbox_started"
	TypeSensitiveWrite Type = "sensitive_write"

	TypePolicyAllowed Type = "policy_allowed"
	TypePolicyIgnored Type = "policy_ignored"
	TypeRuleOffered   Type = "rule_offered"
	TypeRuleAdded     Type = "rule_added"

	TypePackageChecked Type = "package_checked"
)

// PackageChecked records what a registry said about a package the agent
// asked to install that the project had not installed before.
type PackageChecked struct {
	// npm, pnpm, yarn, pip, pip3 or uv
	Manager string `json:"manager"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Risk    Risk   `json:"risk"`
	// the facts in plain words: "published 9 hours ago; runs a script when installed"
	Facts string `json:"facts"`
	// the install was stopped because of this package
	Blocked bool `json:"blocked"`
}

type Event struct {
	Seq  int64           `json:"seq"`
	Time time.Time       `json:"time"`
	Type Type            `json:"type"`
	Data json.RawMessage `json:"data"`
	// hash of the entry before this one, and of this one
	Prev string `json:"prev"`
	Hash string `json:"hash"`
}

// Kind separates the user's own identifiers from the ones we planted.
type Kind string

const (
	KindReal   Kind = "real"
	KindCanary Kind = "canary"
	// a secret being watched for: a value from a .env file, or anything in
	// a well-known credential format
	KindSecret Kind = "secret"
)

// Identifier is something watched for in what leaves the machine. A
// secret's Value is always empty: only its label and a salted hash are kept.
type Identifier struct {
	ID    string `json:"id"`
	Kind  Kind   `json:"kind"`
	Label string `json:"label"`
	// the variable's name, when the value was read from a .env file
	Name  string `json:"name,omitempty"`
	Value string `json:"value"`
	Hash  string `json:"hash,omitempty"`
}

type Category string

const (
	CategoryProvider Category = "provider"
	CategoryOther    Category = "other"
)

// Where is the part of a request that carried an identifier.
type Where string

const (
	WhereAddress Where = "address"
	WhereHeader  Where = "header"
	WhereBody    Where = "body"
	// the first line of a commit being pushed
	WhereCommit Where = "commit message"
)

// Form is how the identifier was written inside the request.
type Form string

const (
	FormPlain  Form = "plain"
	FormURL    Form = "url-encoded"
	FormBase64 Form = "base64"
	FormHex    Form = "hex"
)

type SessionStarted struct {
	// program name only: arguments may hold secrets
	Command       []string     `json:"command"`
	WorkDir       string       `json:"work_dir"`
	ProviderHosts []string     `json:"provider_hosts"`
	Identifiers   []Identifier `json:"identifiers"`
	// false with --no-hold
	Holding bool `json:"holding"`
	// whether the user's input passes through rehearse, so that its first
	// key can be noticed
	InputWatched bool `json:"input_watched"`
	// the version of rehearse recording the session
	ToolVersion string `json:"tool_version,omitempty"`
	// identifies the policy files in force; empty when there are none
	PolicyHash string `json:"policy_hash,omitempty"`
	// true when no agent ran: a recording was played back
	Replay bool `json:"replay,omitempty"`
	// set when no agent ran and a set of changes was reviewed on its own:
	// what the changes were, in words
	Review string `json:"review,omitempty"`
	// no signing key could be found or made, so the end of this session's
	// log will not be signed
	Unsigned bool `json:"unsigned,omitempty"`
}

// CommitsCreated lists the commits made during the session in the working
// folder's repository: those reachable from a local branch when the agent
// exited that no ref could reach when it started.
type CommitsCreated struct {
	// false when the working folder is not a git repository, or git could
	// not be asked
	Known   bool     `json:"known"`
	Commits []string `json:"commits"`
	// commits left off the list because it was too long
	Omitted int `json:"omitted,omitempty"`
}

// ChangesCaptured says what happened to the project's files between the
// agent starting and exiting. The changes themselves are a diff in the
// session's folder, not in the log.
type ChangesCaptured struct {
	// false when the working folder is not inside a git repository, in
	// which case nothing else here is set
	InRepo bool `json:"in_repo"`
	// the working tree before and after, as git trees
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// files that differ, and the lines added and removed in them
	Files   int `json:"files"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
	// the diff was too long to keep whole
	Truncated bool `json:"truncated,omitempty"`
	// why the changes could not be captured, when they could not
	Problem string `json:"problem,omitempty"`
}

// ChangeReviewed is what the local model said about one part of the
// session's changes. The part is named by its place in the session's diff.
type ChangeReviewed struct {
	PartID string `json:"part_id"`
	// one sentence on what the change does to how the program behaves
	Sentence string `json:"sentence"`
	// the rules put the part under "skim" and the model said to read it.
	// There is no way to record the opposite: the model can only raise.
	Raised bool `json:"raised,omitempty"`
}

// ChangesReading marks the local model starting to read the session's
// changes, and finishing.
type ChangesReading struct {
	// true at the start, false at the end
	Started bool `json:"started"`
	// at the start, how many parts it will be asked about; at the end,
	// how many it gave a sentence for
	Parts int `json:"parts"`
	// at the end: parts to read or skim that got no sentence, and why
	Left int    `json:"left,omitempty"`
	Why  string `json:"why,omitempty"`
}

// Request never carries headers, the query string or the body.
type Request struct {
	ID           string   `json:"id"`
	Host         string   `json:"host"`
	Method       string   `json:"method"`
	Path         string   `json:"path"`
	RequestBytes int64    `json:"request_bytes"`
	Category     Category `json:"category"`
	// false when the body was too large to search
	BodyChecked bool `json:"body_checked"`
	// the model a request to the provider asked for, when it names one
	Model string `json:"model,omitempty"`
	// true when the request carries a conversation in a format whose tool
	// calls can be read
	Conversation bool `json:"conversation,omitempty"`
}

// FirstInput marks the first key the user pressed that was passed on to
// the wrapped command.
type FirstInput struct{}

// ModelUsage is the token counts one reply from the provider stated. A
// count it did not state is -1.
type ModelUsage struct {
	RequestID  string `json:"request_id"`
	Model      string `json:"model"`
	Input      int    `json:"input"`
	Output     int    `json:"output"`
	CacheRead  int    `json:"cache_read"`
	CacheWrite int    `json:"cache_write"`
}

// AgentAction is something the agent did with one of its tools, read from
// the conversation it sent to the model. Text is the command or file
// path, with anything secret-looking masked.
type AgentAction struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// ReportNotes are the parts of the session report written by the local
// model. They are absent when the model gave no usable answer.
type ReportNotes struct {
	// true when the model compared the agent's final message with the
	// commands observed, even if it found no claims
	ClaimsChecked bool          `json:"claims_checked"`
	Claims        []ReportClaim `json:"claims,omitempty"`
}

// ReportClaim is something the agent said it had done, set against what
// was observed.
type ReportClaim struct {
	Claim   string `json:"claim"`
	Verdict string `json:"verdict"`
	// the observed command the verdict rests on, when there is one
	Command string `json:"command,omitempty"`
}

// AgentResult is how one of those actions turned out, by the agent's own
// report to the model.
type AgentResult struct {
	ID     string `json:"id"`
	Failed bool   `json:"failed"`
}

// Response has Status 0 and Failed set when the request got no answer.
type Response struct {
	RequestID string `json:"request_id"`
	Status    int    `json:"status"`
	Failed    bool   `json:"failed,omitempty"`
}

// IdentifierSeen says which identifier was found and where, never the
// content around it.
type IdentifierSeen struct {
	RequestID    string `json:"request_id"`
	Host         string `json:"host"`
	IdentifierID string `json:"identifier_id"`
	Where        Where  `json:"where"`
	Form         Form   `json:"form"`
	// For a value that was found encoded. Encoded is the encoded text as
	// it appeared: all of it for a planted value, and otherwise only its
	// first characters and its length, since encoded text can be decoded.
	// DecodedHash is set when decoding the text gave the watched value:
	// it is that value's salted hash, to set beside the identifier's own.
	Encoded     string `json:"encoded,omitempty"`
	DecodedHash string `json:"decoded_hash,omitempty"`
}

// Sighting is one watched value found in a held action: which one, where,
// in what form, and for an encoded one the same evidence as IdentifierSeen.
type Sighting struct {
	IdentifierID string `json:"identifier_id"`
	Where        Where  `json:"where"`
	Form         Form   `json:"form"`
	Encoded      string `json:"encoded,omitempty"`
	DecodedHash  string `json:"decoded_hash,omitempty"`
}

type SessionEnded struct {
	ExitCode int `json:"exit_code"`
}

type ActionKind string

const (
	ActionGitPush    ActionKind = "git-push"
	ActionNpmPublish ActionKind = "npm-publish"
	ActionGh         ActionKind = "gh"
	ActionHTTP       ActionKind = "http"
	// a change the agent made to the project's files inside the sandbox
	ActionFile ActionKind = "file"
)

type Risk string

const (
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

// ActionHeld is what may be shown about a held action. Anything that looks
// like a secret is already masked in it. The exact arguments, headers and
// body needed to replay the action stay in the session's private folder.
type ActionHeld struct {
	ID   string     `json:"id"`
	Kind ActionKind `json:"kind"`

	// stand-in commands
	WorkDir    string   `json:"work_dir,omitempty"`
	Command    []string `json:"command,omitempty"`
	StdinBytes int      `json:"stdin_bytes,omitempty"`
	// git push: each local ref being pushed and the commit it pointed at
	Refs []RefState `json:"refs,omitempty"`
	// git push: files added or changed by the commits being pushed, and
	// those commits' first lines. Long lists are cut, with the rest counted.
	Files          []string `json:"files,omitempty"`
	FilesOmitted   int      `json:"files_omitted,omitempty"`
	Commits        []string `json:"commits,omitempty"`
	CommitsOmitted int      `json:"commits_omitted,omitempty"`
	// the pushed files whose names look like secrets, never cut
	SecretFiles []string `json:"secret_files,omitempty"`

	// held requests
	RequestID string `json:"request_id,omitempty"`
	Method    string `json:"method,omitempty"`
	Host      string `json:"host,omitempty"`
	Path      string `json:"path,omitempty"`
	// the address's scheme and query string. A watched value in the path
	// or the query is replaced by its name, so the address can be shown.
	Scheme       string `json:"scheme,omitempty"`
	Query        string `json:"query,omitempty"`
	RequestBytes int64  `json:"request_bytes,omitempty"`
	ContentType  string `json:"content_type,omitempty"`

	Destination string `json:"destination"`
	// git push: the name the remote is configured under, such as "origin"
	Remote string `json:"remote,omitempty"`
	// what is being sent, in a few words: "branch main", "left-pad@1.0.1"
	Subject       string   `json:"subject,omitempty"`
	IdentifierIDs []string `json:"identifier_ids"`
	// held requests: each watched value found, with where and in what form
	Sightings []Sighting `json:"sightings,omitempty"`
	Force     bool       `json:"force,omitempty"`
	// removes something on the far side: a repository, release, branch or tag
	Deletes   bool `json:"deletes,omitempty"`
	Publishes bool `json:"publishes,omitempty"`
	// changes who can see or change a repository: its visibility, its
	// collaborators or its branch protection
	ChangesAccess bool `json:"changes_access,omitempty"`
	// one of the few kinds of action that may be rated low: a push of a new
	// branch that is not the default one, opening or commenting on an issue
	// or pull request, adding labels
	Minor bool `json:"minor,omitempty"`
	// the agent may have needed the real result to carry on correctly
	NeedsRealAnswer bool `json:"needs_real_answer,omitempty"`

	// goes to the model provider in a way not seen before in the session:
	// under a different credential, or as a file upload
	ProviderDoor bool `json:"provider_door,omitempty"`
	// set when the destination's address first appeared in content fetched
	// from another server
	Provenance *Provenance `json:"provenance,omitempty"`
	// the servers whose content had already been read, when this action's
	// destination was first contacted after that
	OutsideSources []string `json:"outside_sources,omitempty"`

	// set for a file change made in the sandbox
	File *FileChange `json:"file,omitempty"`

	// the lowest rating the session's policy allows this action, and the
	// rule that held it; both empty when no policy rule applied
	PolicyRisk Risk   `json:"policy_risk,omitempty"`
	PolicyRule string `json:"policy_rule,omitempty"`
}

// PolicyAllowed records an action the built-in rules would have held that
// a policy rule let through instead.
type PolicyAllowed struct {
	Kind        ActionKind `json:"kind"`
	Destination string     `json:"destination"`
	Subject     string     `json:"subject,omitempty"`
	Rule        string     `json:"rule"`
}

// PolicyIgnored records a policy rule that was not allowed to apply.
type PolicyIgnored struct {
	Rule string `json:"rule"`
	// project or organisation
	Source string `json:"source"`
	Why    string `json:"why"`
}

// RuleOffered records that the page offered to add a rule to the project's
// policy, because the same kind of action to the same place has been
// allowed in several sessions.
type RuleOffered struct {
	ID          string     `json:"id"`
	Kind        ActionKind `json:"kind"`
	Destination string     `json:"destination"`
	// the rule as it would be written to the file
	Line string `json:"line"`
	// sessions in which it was allowed before this one
	Times int `json:"times"`
}

// RuleAdded records that the user accepted an offered rule and it was
// written to the project's policy file.
type RuleAdded struct {
	ID   string `json:"id"`
	Line string `json:"line"`
	Path string `json:"path"`
}

// FileChange is one change to the project waiting in the sandbox's layer:
// a file, or a whole folder that was added or deleted.
type FileChange struct {
	// relative to the project, with forward slashes
	Path string `json:"path"`
	// added, changed or deleted
	Change string `json:"change"`
	Dir    bool   `json:"dir,omitempty"`
	// how many files the change covers
	Files       int   `json:"files"`
	BytesBefore int64 `json:"bytes_before"`
	BytesAfter  int64 `json:"bytes_after"`
	// why the path is one to look at closely, when it is
	Sensitive string `json:"sensitive,omitempty"`
}

// SensitiveWrite records, while the agent is still running, that it wrote
// to a path that can change how tools on this machine behave.
type SensitiveWrite struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	// true when the write was to a folder the sandbox leaves writable, so it
	// has already taken effect; false when it is waiting in the layer
	Real bool `json:"real,omitempty"`
}

// SandboxStarted records how the agent was confined.
type SandboxStarted struct {
	// paths outside the project that the agent could write to for real
	Writable []string `json:"writable"`
}

// Provenance says where a destination's address was first seen.
type Provenance struct {
	SourceHost string `json:"source_host"`
	// how long before this request the content was read
	SecondsBefore int `json:"seconds_before"`
}

// OutsideContent records that a reply from a server other than the model
// provider was read. It keeps the hostnames the reply mentions and hashes
// of the addresses in it, never the reply itself.
type OutsideContent struct {
	RequestID string   `json:"request_id"`
	Host      string   `json:"host"`
	Bytes     int      `json:"bytes"`
	Hosts     []string `json:"hosts"`
	URLHashes []string `json:"url_hashes"`
	// text a person would not see when the content is displayed
	ZeroWidth int `json:"zero_width,omitempty"`
	TagChars  int `json:"tag_chars,omitempty"`
	Comments  int `json:"comments,omitempty"`
	// a few words from the first hidden comment, with secrets masked
	HiddenExcerpt string `json:"hidden_excerpt,omitempty"`
	// for each host first named in this content, the words around its
	// address, with secrets masked
	Mentions []Mention `json:"mentions,omitempty"`
	// the reply came from a host the agent program talks to for itself,
	// such as its telemetry or its update check
	Background bool `json:"background,omitempty"`
}

// Mention is where outside content named a host: a short excerpt of the
// text around the address, and how that text was hidden from a reader,
// when it was.
type Mention struct {
	Host    string `json:"host"`
	Excerpt string `json:"excerpt"`
	// "an HTML comment", or "" when the text was in plain sight
	Hidden string `json:"hidden,omitempty"`
}

type RefState struct {
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
}

type ReviewSource string

const (
	ReviewByModel ReviewSource = "model"
	ReviewByRules ReviewSource = "rules"
)

type ActionReviewed struct {
	ActionID string       `json:"action_id"`
	Summary  string       `json:"summary"`
	Risk     Risk         `json:"risk"`
	Concern  string       `json:"concern"`
	Source   ReviewSource `json:"source"`
	// why the model's answer was not used, when Source is rules
	Note string `json:"note,omitempty"`
}

type ActionApproved struct {
	ActionID string `json:"action_id"`
}

type ActionRefused struct {
	ActionID string `json:"action_id"`
	// the policy rule that refused it, when it was not the user
	Policy string `json:"policy,omitempty"`
}

type ActionApplied struct {
	ActionID string `json:"action_id"`
	Detail   string `json:"detail"`
}

type ActionFailed struct {
	ActionID string `json:"action_id"`
	Reason   string `json:"reason"`
	Detail   string `json:"detail,omitempty"`
}
