package record

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"rehearsal/internal/events"
)

const checkpointFile = "checkpoint.json"

// Checkpoint is the signed statement that a session's log ended with a
// given number of entries and a given final hash.
type Checkpoint struct {
	Version   int       `json:"version"`
	Session   string    `json:"session"`
	Entries   int64     `json:"entries"`
	FinalHash string    `json:"final_hash"`
	SignedAt  time.Time `json:"signed_at"`
	PublicKey string    `json:"public_key"`
	Signature string    `json:"signature"`
}

// message is what the signature covers.
func (c Checkpoint) message() []byte {
	return fmt.Appendf(nil, "rehearsal checkpoint v%d\n%s\n%d\n%s\n%s",
		c.Version, c.Session, c.Entries, c.FinalHash, c.SignedAt.Format(time.RFC3339))
}

// SignCheckpoint signs the end of a session's log.
func SignCheckpoint(key ed25519.PrivateKey, session string, entries int64, finalHash string, now time.Time) Checkpoint {
	c := Checkpoint{
		Version: 1, Session: session, Entries: entries, FinalHash: finalHash,
		SignedAt: now.UTC().Truncate(time.Second),
	}
	if pub, ok := key.Public().(ed25519.PublicKey); ok {
		c.PublicKey = KeyText(pub)
	}
	c.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, c.message()))
	return c
}

// check verifies the signature, and that it was made with the trusted
// key and not merely with whatever key the file names.
func (c Checkpoint) check(trusted ed25519.PublicKey) error {
	if c.PublicKey != KeyText(trusted) {
		return fmt.Errorf("the checkpoint was signed with a different key from the one being checked against (%s)", Fingerprint(trusted))
	}
	sig, err := base64.StdEncoding.DecodeString(c.Signature)
	if err != nil || !ed25519.Verify(trusted, c.message(), sig) {
		return errors.New("the checkpoint's signature does not match its contents")
	}
	return nil
}

// WriteCheckpoint saves the checkpoint in the session's folder.
func WriteCheckpoint(sessionDir string, c Checkpoint) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the checkpoint: %w", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, checkpointFile), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write the checkpoint: %w", err)
	}
	return nil
}

// ReadCheckpoint returns fs.ErrNotExist when the session has none.
func ReadCheckpoint(sessionDir string) (Checkpoint, error) {
	return readCheckpoint(filepath.Join(sessionDir, checkpointFile))
}

func readCheckpoint(path string) (Checkpoint, error) {
	var c Checkpoint
	data, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("read the checkpoint: %w", err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("the checkpoint is not readable: %w", err)
	}
	return c, nil
}

// Verdict is the outcome of checking a session's record.
type Verdict struct {
	Entries   int64
	FinalHash string
	SignedAt  time.Time
	// empty when the record verifies
	Problem string
	// the first entry that does not match, when that can be told
	BadEntry int64
}

// OK reports whether the record verified.
func (v Verdict) OK() bool { return v.Problem == "" }

// Verify recomputes the log's chain and checks the checkpoint against it
// and against the trusted key. An error means the record could not be
// read; a record that was read and does not hold up comes back as a
// Verdict with a Problem.
func Verify(log *events.Log, sessionDir string, trusted ed25519.PublicKey) (Verdict, error) {
	chain, err := log.Check()
	if err != nil {
		return Verdict{}, err
	}
	return VerifyChain(chain, filepath.Join(sessionDir, checkpointFile), filepath.Base(sessionDir), trusted), nil
}

// VerifyChain checks a chain that has already been recomputed against the
// checkpoint file at path, for the session of the given name. It is what
// Verify does once the log is read, for a record that is not in a log: an
// exported one.
func VerifyChain(chain events.Chain, path, session string, trusted ed25519.PublicKey) Verdict {
	v := Verdict{Entries: chain.Entries, FinalHash: chain.Head}
	if !chain.OK() {
		v.Problem, v.BadEntry = chain.Problem, chain.BadEntry
		return v
	}
	c, err := readCheckpoint(path)
	if errors.Is(err, fs.ErrNotExist) {
		v.Problem = "there is no signed checkpoint, so entries missing from the end could not be noticed. The session did not finish cleanly, or was recorded without a signing key"
		return v
	}
	if err != nil {
		v.Problem = err.Error()
		return v
	}
	v.SignedAt = c.SignedAt
	if err := c.check(trusted); err != nil {
		v.Problem = err.Error()
		return v
	}
	switch {
	case c.Session != session:
		v.Problem = fmt.Sprintf("the checkpoint belongs to another session (%s)", c.Session)
	case chain.Entries < c.Entries:
		v.BadEntry = chain.Entries + 1
		v.Problem = fmt.Sprintf("entry %d is missing: the signed checkpoint covers %d entries and the log has %d", v.BadEntry, c.Entries, chain.Entries)
	case chain.Entries > c.Entries:
		v.BadEntry = c.Entries + 1
		v.Problem = fmt.Sprintf("entry %d was added after the record was signed", v.BadEntry)
	case chain.Head != c.FinalHash:
		v.Problem = "the log is consistent with itself but is not the one that was signed. It has been rewritten, and which entry changed first cannot be told"
	}
	return v
}
