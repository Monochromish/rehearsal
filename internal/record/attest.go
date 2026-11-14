package record

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// StatementType names the format, so a later change can be told apart.
const StatementType = "rehearsal-attestation/v1"

// Statement is what an attestation says about a session. Everything in it
// comes from the session's verified log.
type Statement struct {
	Type        string     `json:"type"`
	Session     string     `json:"session"`
	ToolVersion string     `json:"tool_version"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at"`
	// false when the commits could not be observed; Commits is then empty
	// and means "unknown", not "none"
	CommitsKnown   bool     `json:"commits_known"`
	Commits        []string `json:"commits"`
	CommitsOmitted int      `json:"commits_omitted"`
	// hash of the policy in force during the session; empty when there was none
	PolicyHash string       `json:"policy_hash"`
	Actions    ActionCounts `json:"actions"`
	Chain      ChainEnd     `json:"chain"`
}

type ActionCounts struct {
	Held     int `json:"held"`
	Approved int `json:"approved"`
	Refused  int `json:"refused"`
}

type ChainEnd struct {
	Entries   int64  `json:"entries"`
	FinalHash string `json:"final_hash"`
}

// attestation is the file: the statement, the key that signed it, and the
// signature over the statement with its insignificant white space removed.
type attestation struct {
	Statement json.RawMessage `json:"statement"`
	PublicKey string          `json:"public_key"`
	Signature string          `json:"signature"`
}

func signedBytes(statement []byte) ([]byte, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, statement); err != nil {
		return nil, fmt.Errorf("the statement is not valid JSON: %w", err)
	}
	return append([]byte("rehearsal attestation v1\n"), compact.Bytes()...), nil
}

// Attest returns the contents of a signed attestation file for s.
func Attest(key ed25519.PrivateKey, s Statement) ([]byte, error) {
	s.Type = StatementType
	if s.Commits == nil {
		s.Commits = []string{}
	}
	statement, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode the statement: %w", err)
	}
	msg, err := signedBytes(statement)
	if err != nil {
		return nil, err
	}
	pub, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("the signing key has no Ed25519 public key")
	}
	file, err := json.MarshalIndent(attestation{
		Statement: statement,
		PublicKey: KeyText(pub),
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, msg)),
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode the attestation: %w", err)
	}
	return append(file, '\n'), nil
}

// VerifyAttestation checks an attestation file against the trusted key
// and returns what it states. It needs nothing but the file and the key.
func VerifyAttestation(file []byte, trusted ed25519.PublicKey) (Statement, error) {
	var a attestation
	dec := json.NewDecoder(bytes.NewReader(file))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return Statement{}, fmt.Errorf("the attestation is not readable: %w", err)
	}
	if a.PublicKey != KeyText(trusted) {
		return Statement{}, fmt.Errorf("the attestation was signed with a different key from the one given (%s)", Fingerprint(trusted))
	}
	msg, err := signedBytes(a.Statement)
	if err != nil {
		return Statement{}, err
	}
	sig, err := base64.StdEncoding.DecodeString(a.Signature)
	if err != nil || !ed25519.Verify(trusted, msg, sig) {
		return Statement{}, errors.New("the attestation's signature does not match its contents: it has been altered, or was not signed with this key")
	}
	var s Statement
	if err := json.Unmarshal(a.Statement, &s); err != nil {
		return Statement{}, fmt.Errorf("the statement is not readable: %w", err)
	}
	if s.Type != StatementType {
		return Statement{}, fmt.Errorf("the statement is of type %q, and this version reads %q", s.Type, StatementType)
	}
	return s, nil
}
