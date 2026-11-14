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
)

type Event struct {
	Seq  int64           `json:"seq"`
	Time time.Time       `json:"time"`
	Type Type            `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Kind separates the user's own identifiers from the ones we planted.
type Kind string

const (
	KindReal   Kind = "real"
	KindCanary Kind = "canary"
)

type Identifier struct {
	ID    string `json:"id"`
	Kind  Kind   `json:"kind"`
	Label string `json:"label"`
	Value string `json:"value"`
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
)

// Form is how the identifier was written inside the request.
type Form string

const (
	FormPlain  Form = "plain"
	FormURL    Form = "url-encoded"
	FormBase64 Form = "base64"
)

type SessionStarted struct {
	// program name only: arguments may hold secrets
	Command       []string     `json:"command"`
	WorkDir       string       `json:"work_dir"`
	ProviderHosts []string     `json:"provider_hosts"`
	Identifiers   []Identifier `json:"identifiers"`
	// false with --no-hold
	Holding bool `json:"holding"`
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
	RequestID    string `json:"request_id,omitempty"`
	Method       string `json:"method,omitempty"`
	Host         string `json:"host,omitempty"`
	Path         string `json:"path,omitempty"`
	RequestBytes int64  `json:"request_bytes,omitempty"`
	ContentType  string `json:"content_type,omitempty"`

	Destination string `json:"destination"`
	// what is being sent, in a few words: "branch main", "left-pad@1.0.1"
	Subject       string   `json:"subject,omitempty"`
	IdentifierIDs []string `json:"identifier_ids"`
	Force         bool     `json:"force,omitempty"`
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
