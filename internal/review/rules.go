// Package review describes each held action in plain words and rates its
// risk, using a local model when one answers and fixed rules when not.
package review

import (
	"fmt"
	"net"
	"path"
	"slices"
	"strings"

	"rehearsal/internal/events"
)

type Verdict struct {
	Summary string
	Risk    events.Risk
	Concern string
	Source  events.ReviewSource
	// why the model's answer was not used
	Note string
}

func rank(r events.Risk) int {
	switch r {
	case events.RiskLow:
		return 1
	case events.RiskMedium:
		return 2
	case events.RiskHigh:
		return 3
	}
	return 0
}

// Floor is the rules' own rating of an action, and the lowest it can be
// given. The model may rate an action higher than this, never lower.
//
// High: anything that overwrites, deletes or publishes, changes who has
// access, carries one of the user's details, or pushes files that look
// like secrets. Low: the few kinds marked Minor. Everything else is medium.
func Floor(a events.ActionHeld) events.Risk {
	switch {
	case a.Force, a.Deletes, a.Publishes, a.ChangesAccess, len(a.IdentifierIDs) > 0, len(a.SecretFiles) > 0:
		return events.RiskHigh
	case a.Minor:
		return events.RiskLow
	}
	return events.RiskMedium
}

var (
	privateKeyNames = []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"}
	secretExts      = []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".ppk"}
	secretWords     = []string{"credential", "credentials", "secret", "secrets"}
)

// SecretLooking picks out the file names that suggest a secret: .env
// files, private keys, key and certificate stores, and anything named
// after credentials or secrets. It goes by the name alone.
func SecretLooking(paths []string) []string {
	var found []string
	for _, p := range paths {
		name := strings.ToLower(path.Base(p))
		words := strings.FieldsFunc(strings.TrimSuffix(name, path.Ext(name)), func(r rune) bool {
			return r < 'a' || r > 'z'
		})
		switch {
		case name == ".env" || strings.HasPrefix(name, ".env."),
			slices.Contains(privateKeyNames, name),
			slices.Contains(secretExts, path.Ext(name)),
			slices.ContainsFunc(words, func(w string) bool { return slices.Contains(secretWords, w) }):
			found = append(found, p)
		}
	}
	return found
}

// ByRules describes an action without a model. Its risk is the floor.
func ByRules(a events.ActionHeld) Verdict {
	v := Verdict{Risk: Floor(a), Source: events.ReviewByRules}
	dest := a.Destination
	if dest == "" {
		dest = "another server"
	}
	switch a.Kind {
	case events.ActionGitPush:
		what := "this project's saved changes"
		if a.Subject != "" {
			what += " (" + a.Subject + ")"
		}
		switch {
		case a.Force:
			v.Summary = fmt.Sprintf("Replace what is stored at %s with %s, overwriting what is there.", dest, what)
			v.Concern = "Work other people have already uploaded there can be erased and may not be recoverable."
		case a.Deletes:
			v.Summary = fmt.Sprintf("Delete part of the shared copy at %s (%s).", dest, a.Subject)
			v.Concern = "Whatever exists only in the deleted part is lost for everyone who uses that copy."
		default:
			v.Summary = fmt.Sprintf("Upload %s to the shared copy at %s.", what, dest)
			v.Concern = "Everyone who uses that copy gets these changes, and automated systems there may act on them."
		}
	case events.ActionNpmPublish:
		what := "this package"
		if a.Subject != "" {
			what = a.Subject
		}
		v.Summary = fmt.Sprintf("Publish %s to %s, where anyone can download it.", what, dest)
		v.Concern = "A published version is public at once and usually cannot be fully taken back."
	case events.ActionGh:
		v.Summary = fmt.Sprintf("Run the GitHub command %q on %s.", a.Subject, dest)
		switch {
		case a.Deletes:
			v.Concern = "What is deleted on GitHub may not be recoverable."
		case a.Publishes:
			v.Concern = "A release is visible to everyone who can see the repository."
		default:
			v.Concern = "This changes something on GitHub that other people can see."
		}
	case events.ActionHTTP:
		if a.Deletes {
			v.Summary = fmt.Sprintf("Ask %s to delete something (%s).", dest, a.Path)
		} else {
			v.Summary = fmt.Sprintf("Send %d bytes of data to %s (%s %s).", a.RequestBytes, dest, a.Method, a.Path)
		}
		v.Concern = "The server will act on this request, and that may not be possible to undo."
		if isThisMachine(a.Host) {
			v.Concern = "It goes to a program on this computer, not out to the internet, but that program will still act on it."
		}
	}
	if a.ChangesAccess {
		v.Concern = "It changes who can see or change the repository, or removes a safeguard on it."
	}
	if len(a.IdentifierIDs) > 0 {
		v.Concern = "It carries details of yours to a server that is not the AI service the agent is meant to talk to."
	}
	if len(a.SecretFiles) > 0 {
		v.Concern = fmt.Sprintf("It includes files that look like passwords or keys (%s), which would then be readable by everyone with access to that copy.", strings.Join(a.SecretFiles, ", "))
	}
	return v
}

func isThisMachine(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport // no port
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
