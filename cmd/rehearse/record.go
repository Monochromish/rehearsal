package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"rehearsal/internal/events"
	"rehearsal/internal/ledger"
	"rehearsal/internal/record"
)

const attestationFile = "rehearsal-attestation.json"

// recordCommands are the commands that work on a finished session's
// record: checking it, exporting it and attesting to it.
func recordCommands() []*cobra.Command {
	verify := &cobra.Command{
		Use:   "verify [session]",
		Short: "Check that a session's record has not been changed since it was signed",
		Long: "Recomputes the chain of hashes over the session's event log and checks the\n" +
			"signed checkpoint against it. With no session named, the most recent one is checked.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return verifySession(cmd.OutOrStdout(), first(args))
		},
	}

	var format string
	export := &cobra.Command{
		Use:           "export [session]",
		Short:         "Print a session's events, one JSON object per line",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "jsonl" {
				return fmt.Errorf("--format %q is not supported. The only format is jsonl", format)
			}
			return exportSession(cmd.OutOrStdout(), first(args))
		},
	}
	export.Flags().StringVar(&format, "format", "jsonl", "output format (jsonl)")

	var out string
	attest := &cobra.Command{
		Use:           "attest [session]",
		Short:         "Write a signed statement about a session, for a pull request to carry",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return attestSession(cmd.OutOrStdout(), first(args), out)
		},
	}
	attest.Flags().StringVarP(&out, "output", "o", attestationFile, "file to write the attestation to")

	var key string
	verifyAttestation := &cobra.Command{
		Use:   "verify-attestation <file> --key <public key>",
		Short: "Check a signed attestation against a public key, with nothing else needed",
		Long: "Checks that the attestation was signed with the given public key and has not\n" +
			"been altered. The key is a file (signing-key.pub) or its base64 text.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if key == "" {
				return errors.New("--key is required: an attestation checked against the key it names itself proves nothing")
			}
			return verifyAttestationFile(cmd.OutOrStdout(), args[0], key)
		},
	}
	verifyAttestation.Flags().StringVar(&key, "key", "", "public key to trust: a file, or the key as base64")

	return []*cobra.Command{verify, export, attest, verifyAttestation}
}

func first(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// findSession returns the folder of the session called name: a folder
// path, a name under the state directory, or the most recent session when
// name is empty.
func findSession(name string) (string, error) {
	if info, err := os.Stat(name); name != "" && err == nil && info.IsDir() {
		return filepath.Abs(name)
	}
	state, err := stateDir()
	if err != nil {
		return "", err
	}
	sessions := filepath.Join(state, "sessions")
	if name != "" {
		dir := filepath.Join(sessions, name)
		if _, err := os.Stat(dir); err != nil {
			return "", fmt.Errorf("there is no session called %q in %s", name, sessions)
		}
		return dir, nil
	}
	entries, err := os.ReadDir(sessions)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("list the sessions: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no session has been recorded yet in %s", sessions)
	}
	// session names start with the time they began
	return filepath.Join(sessions, slices.Max(names)), nil
}

// openSession opens a finished session's log and checks its record
// against this user's own key.
func openSession(name string) (dir string, log *events.Log, verdict record.Verdict, err error) {
	if dir, err = findSession(name); err != nil {
		return "", nil, verdict, err
	}
	if log, err = events.OpenExisting(filepath.Join(dir, "events.db")); err != nil {
		return "", nil, verdict, err
	}
	trusted, err := ownPublicKey()
	if err == nil {
		verdict, err = record.Verify(log, dir, trusted)
	}
	if err != nil {
		return "", nil, verdict, errors.Join(err, log.Close())
	}
	return dir, log, verdict, nil
}

func ownPublicKey() (ed25519.PublicKey, error) {
	keyDir, err := record.KeyDir()
	if err != nil {
		return nil, err
	}
	key, err := record.OwnPublicKey(keyDir)
	if err != nil {
		return nil, fmt.Errorf("%w. A key is made the first time a session is recorded, in %s", err, keyDir)
	}
	return key, nil
}

// refusal is the error for a record that was read and does not hold up.
func refusal(v record.Verdict) error {
	if v.BadEntry > 0 {
		return fmt.Errorf("record does not verify, starting at entry %d: %s", v.BadEntry, v.Problem)
	}
	return fmt.Errorf("record does not verify: %s", v.Problem)
}

func verifySession(w io.Writer, name string) (err error) {
	dir, log, verdict, err := openSession(name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, log.Close()) }()
	if !verdict.OK() {
		return refusal(verdict)
	}
	fmt.Fprintf(w, "Record verified: session %s, %d entries, signed %s.\nFinal hash: %s\n",
		filepath.Base(dir), verdict.Entries, verdict.SignedAt.Format(time.RFC3339), verdict.FinalHash)
	return nil
}

// exportLine is one event as a log system would take it. The field names
// are part of the format: see docs/attestation.md.
type exportLine struct {
	Session string          `json:"session"`
	Seq     int64           `json:"seq"`
	Time    string          `json:"time"`
	Type    events.Type     `json:"type"`
	Data    json.RawMessage `json:"data"`
	Prev    string          `json:"prev"`
	Hash    string          `json:"hash"`
}

func exportSession(w io.Writer, name string) (err error) {
	dir, err := findSession(name)
	if err != nil {
		return err
	}
	log, err := events.OpenExisting(filepath.Join(dir, "events.db"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, log.Close()) }()
	evs, err := log.Since(0)
	if err != nil {
		return err
	}
	out := bufio.NewWriter(w)
	enc := json.NewEncoder(out)
	for _, ev := range evs {
		data, err := exportData(ev)
		if err != nil {
			return err
		}
		line := exportLine{
			Session: filepath.Base(dir), Seq: ev.Seq, Time: ev.Time.Format(time.RFC3339Nano),
			Type: ev.Type, Data: data, Prev: ev.Prev, Hash: ev.Hash,
		}
		if err := enc.Encode(line); err != nil {
			return fmt.Errorf("write event %d: %w", ev.Seq, err)
		}
	}
	return out.Flush()
}

// exportData is an event's data as it leaves the machine. The log never
// holds bodies, headers or secret values. The one thing it does hold that
// is the user's own is the values being watched for, in the first event,
// and those are left out here: the label says what each one was.
func exportData(ev events.Event) (json.RawMessage, error) {
	if ev.Type != events.TypeSessionStarted {
		return ev.Data, nil
	}
	var started events.SessionStarted
	if err := json.Unmarshal(ev.Data, &started); err != nil {
		return nil, fmt.Errorf("event %d is not readable: %w", ev.Seq, err)
	}
	for i := range started.Identifiers {
		if started.Identifiers[i].Kind == events.KindReal {
			started.Identifiers[i].Value = ""
		}
	}
	return json.Marshal(started)
}

func attestSession(w io.Writer, name, out string) (err error) {
	dir, log, verdict, err := openSession(name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, log.Close()) }()
	if !verdict.OK() {
		return fmt.Errorf("not attesting to this session: %w", refusal(verdict))
	}
	l, err := ledger.FromLog(log)
	if err != nil {
		return err
	}
	statement := record.Statement{
		Session:     filepath.Base(dir),
		ToolVersion: l.ToolVersion,
		StartedAt:   l.StartedAt.UTC(),
		EndedAt:     l.EndedAt,
		Chain:       record.ChainEnd{Entries: verdict.Entries, FinalHash: verdict.FinalHash},
	}
	if l.Commits != nil {
		statement.CommitsKnown, statement.Commits, statement.CommitsOmitted = l.Commits.Known, l.Commits.Commits, l.Commits.Omitted
	}
	statement.Actions.Held = len(l.Actions)
	for _, a := range l.Actions {
		switch a.Decision {
		case ledger.Approved:
			statement.Actions.Approved++
		case ledger.Refused:
			statement.Actions.Refused++
		}
	}

	keyDir, err := record.KeyDir()
	if err != nil {
		return err
	}
	key, err := record.LoadOrCreateKey(keyDir)
	if err != nil {
		return err
	}
	file, err := record.Attest(key, statement)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, file, 0o644); err != nil {
		return fmt.Errorf("write the attestation: %w", err)
	}
	fmt.Fprintf(w, "Wrote %s for session %s: %d commits, %d held, %d approved, %d refused.\nCheck it anywhere with: rehearse verify-attestation %s --key %s\n",
		out, statement.Session, len(statement.Commits), statement.Actions.Held, statement.Actions.Approved, statement.Actions.Refused,
		out, filepath.Join(keyDir, "signing-key.pub"))
	return nil
}

func verifyAttestationFile(w io.Writer, path, keyText string) error {
	trusted, err := record.ParsePublicKey(keyText)
	if err != nil {
		return err
	}
	file, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the attestation: %w", err)
	}
	s, err := record.VerifyAttestation(file, trusted)
	if err != nil {
		return err
	}
	commits := fmt.Sprintf("%d", len(s.Commits)+s.CommitsOmitted)
	if !s.CommitsKnown {
		commits = "not available"
	}
	policy := s.PolicyHash
	if policy == "" {
		policy = "none"
	}
	fmt.Fprintf(w, "Attestation verified, signed with key %s.\nSession %s, recorded by rehearse %s.\nCommits made in the session: %s\n",
		record.Fingerprint(trusted), s.Session, s.ToolVersion, commits)
	for _, c := range s.Commits {
		fmt.Fprintf(w, "  %s\n", c)
	}
	fmt.Fprintf(w, "Actions held: %d, approved: %d, refused: %d\nPolicy: %s\nRecord: %d entries, final hash %s\n",
		s.Actions.Held, s.Actions.Approved, s.Actions.Refused, policy, s.Chain.Entries, s.Chain.FinalHash)
	return nil
}
