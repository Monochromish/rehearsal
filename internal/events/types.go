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
