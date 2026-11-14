package standin

import (
	"fmt"
	"slices"
	"strings"

	"rehearsal/internal/events"
)

type pushFactSheet struct {
	destination string
	// the name the remote is configured under, when it has one
	remote  string
	subject string
	// each local ref being pushed and what it points at now, so the push
	// can be refused later if the ref has moved
	refs []events.RefState
	// files the pushed commits add or change, and those commits' first lines
	files   []string
	commits []string
	// pushes only new branches, none of them the remote's default branch
	minor  bool
	stdout string
	stderr string
}

// The most commits looked through for file names and messages. A first
// push of a long history would otherwise list everything ever committed.
const maxCommitsRead = "500"

// pushed lists what the commits between old and tip add or change, and
// their first lines. With no old commit known, it takes the commits no
// remote-tracking branch has yet.
func (g gitRunner) pushed(old, tip string) (files, subjects []string) {
	args := []string{"-c", "core.quotePath=false", "log", "-n", maxCommitsRead, "--format=%x00%s", "--name-only", "--diff-filter=AMR"}
	if old != "" {
		args = append(args, old+".."+tip)
	} else {
		args = append(args, tip, "--not", "--remotes")
	}
	out, _ := g.out(args...)
	for _, line := range strings.Split(out, "\n") {
		if subject, ok := strings.CutPrefix(line, "\x00"); ok {
			subjects = append(subjects, subject)
		} else if line != "" {
			files = append(files, line)
		}
	}
	return files, subjects
}

type refUpdate struct {
	src, dst string
	full     string // full local ref name, "" for a deletion
	commit   string
	tag      bool
}

// pushFacts works out, without touching the network, what a push would
// send and where, and writes the output git prints for a push like it.
// The error is the one git gives for a source ref that does not exist.
func pushFacts(g gitRunner, spec pushSpec) (pushFactSheet, error) {
	var facts pushFactSheet
	current, _ := g.out("symbolic-ref", "-q", "--short", "HEAD")

	remote := spec.remote
	if remote == "" {
		for _, key := range []string{"branch." + current + ".pushRemote", "remote.pushDefault", "branch." + current + ".remote"} {
			if v, ok := g.out("config", "--get", key); ok && v != "" {
				remote = v
				break
			}
		}
		if remote == "" {
			remote = "origin"
		}
	}
	facts.destination = remote // a URL or path given directly has no configured name
	if url, ok := g.out("remote", "get-url", "--push", remote); ok && url != "" {
		facts.destination, facts.remote = url, remote
	}

	var updates []refUpdate
	listed := func(prefix string) {
		out, _ := g.out("for-each-ref", "--format=%(refname) %(objectname)", prefix)
		for _, line := range strings.Split(out, "\n") {
			if full, commit, ok := strings.Cut(line, " "); ok {
				short := strings.TrimPrefix(strings.TrimPrefix(full, "refs/heads/"), "refs/tags/")
				updates = append(updates, refUpdate{src: short, dst: short, full: full, commit: commit, tag: prefix == "refs/tags"})
			}
		}
	}
	switch {
	case spec.all || spec.mirror:
		listed("refs/heads")
		if spec.mirror {
			listed("refs/tags")
		}
	case len(spec.refspecs) == 0:
		name := "HEAD" // detached
		if current != "" {
			name = current
		}
		u, err := resolve(g, name, name, current)
		if err != nil {
			return facts, err
		}
		updates = append(updates, u)
	default:
		for _, refspec := range spec.refspecs {
			src, dst, hasDst := strings.Cut(strings.TrimPrefix(refspec, "+"), ":")
			if !hasDst {
				dst = src
			}
			if src == "" || (spec.deletes && !hasDst) {
				updates = append(updates, refUpdate{dst: shortRef(dst)})
				continue
			}
			u, err := resolve(g, src, dst, current)
			if err != nil {
				return facts, err
			}
			updates = append(updates, u)
		}
	}
	if spec.tags {
		listed("refs/tags")
	}

	// The remote's default branch, as far as this repository knows. A
	// remote that was added but never fetched has none recorded, and then
	// the two usual names are taken to be it.
	defaults := []string{"main", "master"}
	if head, ok := g.out("symbolic-ref", "-q", "--short", "refs/remotes/"+remote+"/HEAD"); ok && head != "" {
		defaults = []string{strings.TrimPrefix(head, remote+"/")}
	}

	var lines, names []string
	tracked := ""
	// minor until shown otherwise: only new branches, none of them the default
	facts.minor = len(updates) > 0 && !spec.all && !spec.mirror && !spec.tags
	for _, u := range updates {
		if u.full == "" {
			lines = append(lines, fmt.Sprintf(" - [deleted]         %s", u.dst))
			names = append(names, u.dst+" (delete)")
			facts.minor = false
			continue
		}
		facts.refs = append(facts.refs, events.RefState{Ref: u.full, Commit: u.commit})
		names = append(names, u.dst)
		if u.tag {
			lines = append(lines, fmt.Sprintf(" * [new tag]         %s -> %s", u.src, u.dst))
			facts.minor = false
			continue
		}
		if tracked == "" {
			tracked = u.dst
		}
		old, _ := g.out("rev-parse", "--verify", "-q", "refs/remotes/"+remote+"/"+u.dst)
		if old != "" || slices.Contains(defaults, u.dst) {
			facts.minor = false
		}
		files, subjects := g.pushed(old, u.commit)
		for _, f := range files {
			if !slices.Contains(facts.files, f) {
				facts.files = append(facts.files, f)
			}
		}
		for _, s := range subjects {
			if !slices.Contains(facts.commits, s) {
				facts.commits = append(facts.commits, s)
			}
		}
		switch {
		case old == "":
			lines = append(lines, fmt.Sprintf(" * [new branch]      %s -> %s", u.src, u.dst))
		case old == u.commit:
		case spec.force:
			lines = append(lines, fmt.Sprintf(" + %s...%s %s -> %s (forced update)", short(old), short(u.commit), u.src, u.dst))
		default:
			lines = append(lines, fmt.Sprintf("   %s..%s  %s -> %s", short(old), short(u.commit), u.src, u.dst))
		}
	}

	switch {
	case spec.mirror:
		facts.subject = "every branch and tag"
	case spec.all:
		facts.subject = "every branch"
	case len(names) > 3:
		facts.subject = fmt.Sprintf("%s and %d more", strings.Join(names[:3], ", "), len(names)-3)
	default:
		facts.subject = strings.Join(names, ", ")
	}
	if len(lines) == 0 {
		facts.stderr = "Everything up-to-date\n"
	} else {
		facts.stderr = "To " + facts.destination + "\n" + strings.Join(lines, "\n") + "\n"
	}
	if spec.setUpstream && tracked != "" && current != "" {
		facts.stdout = fmt.Sprintf("branch '%s' set up to track '%s/%s'.\n", current, remote, tracked)
	}
	return facts, nil
}

func resolve(g gitRunner, src, dst, current string) (refUpdate, error) {
	full, _ := g.out("rev-parse", "--symbolic-full-name", src)
	if full == "" {
		full = src // a bare commit ID has no ref name
	}
	commit, ok := g.out("rev-parse", "--verify", "-q", full)
	if !ok || commit == "" {
		return refUpdate{}, fmt.Errorf("error: src refspec %s does not match any\nerror: failed to push some refs", src)
	}
	if dst == "HEAD" {
		dst = current
	}
	if src == "HEAD" && current != "" {
		src = current
	}
	return refUpdate{src: shortRef(src), dst: shortRef(dst), full: full, commit: commit, tag: strings.HasPrefix(full, "refs/tags/")}, nil
}

func shortRef(ref string) string {
	return strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/tags/")
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
