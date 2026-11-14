package standin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"rehearsal/internal/hold"
)

// Environment a stand-in reads. rehearse sets these for the wrapped agent.
const (
	EnvSocket    = "REHEARSAL_SOCKET"
	EnvShimDir   = "REHEARSAL_SHIM_DIR"
	EnvPassProxy = "REHEARSAL_PASS_PROXY"
)

// Names are the programs that get a stand-in.
var Names = []string{"git", "npm", "gh"}

const maxStdin = 10 << 20

// Main runs as the stand-in for name and returns its exit code. For a
// command that is not held it never returns: the real program takes over
// this process.
func Main(name string, args []string) int {
	real, err := findReal(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: command not found\n", name)
		return 127
	}
	plan := Classify(name, args)
	if name == "git" && !plan.Hold {
		plan = followAlias(real, plan, args)
	}
	if !plan.Hold {
		return pass(real, name, args, plan.ReadOnly)
	}

	workDir, err := os.Getwd()
	if err != nil {
		return refuse(name, err)
	}
	c := hold.Command{
		Kind: plan.Kind, Program: name, Args: args, WorkDir: workDir,
		Force: plan.Force, Deletes: plan.Deletes, Publishes: plan.Publishes,
		ChangesAccess: plan.ChangesAccess, Minor: plan.Minor, NeedsRealAnswer: plan.NeedsRealAnswer,
	}
	var stdout, stderr string
	switch name {
	case "git":
		g := gitRunner{real: real, globals: plan.Globals}
		facts, err := pushFacts(g, plan.Push)
		if err != nil {
			// what the real git says when asked to push something that is not there
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		c.GitGlobals, c.Destination, c.Subject, c.Refs = plan.Globals, facts.destination, facts.subject, facts.refs
		c.Files, c.Commits, c.Minor = facts.files, facts.commits, facts.minor
		stdout, stderr = facts.stdout, facts.stderr
	case "npm":
		c.Destination, c.Subject = npmFacts(real, args, workDir)
		if c.Subject != "" {
			stdout = "+ " + c.Subject + "\n"
		}
	case "gh":
		c.Destination, c.Subject = ghFacts(args)
		if readsStdin(args) {
			if c.Stdin, err = io.ReadAll(io.LimitReader(os.Stdin, maxStdin)); err != nil {
				return refuse(name, fmt.Errorf("read standard input: %w", err))
			}
		}
	}

	socket := os.Getenv(EnvSocket)
	if socket == "" {
		return refuse(name, errors.New(EnvSocket+" is not set"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := hold.Report(ctx, socket, c); err != nil {
		return refuse(name, err)
	}
	fmt.Fprint(os.Stdout, stdout)
	fmt.Fprint(os.Stderr, stderr)
	return 0
}

// refuse is for a command that should be held but cannot be. It is not
// run for real either.
func refuse(name string, err error) int {
	fmt.Fprintf(os.Stderr, "rehearse: could not hold this %s command, so it was not run: %v\n", name, err)
	return 1
}

// findReal looks along PATH for the program the stand-in replaces,
// skipping the folder the stand-ins live in.
func findReal(name string) (string, error) {
	shimDir := filepath.Clean(os.Getenv(EnvShimDir))
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || filepath.Clean(dir) == shimDir {
			continue
		}
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s is not installed", name)
}

func pass(real, name string, args []string, readOnly bool) int {
	env := os.Environ()
	if proxy := os.Getenv(EnvPassProxy); readOnly && proxy != "" {
		for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			env = append(env, k+"="+proxy)
		}
	}
	err := syscall.Exec(real, append([]string{name}, args...), env)
	fmt.Fprintf(os.Stderr, "rehearse: could not start the real %s: %v\n", name, err)
	return 126
}

// Subcommands git already has cannot be aliased, so only other words are
// worth a lookup. The list only saves time: a word missing from it costs
// one extra call to git.
var gitBuiltins = strings.Fields(`add am apply bisect blame branch checkout cherry-pick clean clone commit
	config describe diff fetch for-each-ref grep init log ls-files ls-remote merge mv pull rebase reflog
	remote reset restore rev-list rev-parse rm show stash status submodule switch symbolic-ref tag
	update-index update-ref version worktree`)

// followAlias holds "git p" when the user has aliased p to push.
func followAlias(real string, plan Plan, args []string) Plan {
	globals, sub, rest := splitGit(args)
	if sub == "" || slices.Contains(gitBuiltins, sub) {
		return plan
	}
	g := gitRunner{real: real, globals: globals}
	alias, ok := g.out("config", "--get", "alias."+sub)
	if !ok || alias == "" || strings.HasPrefix(alias, "!") {
		// a "!" alias runs a shell command, and any git push inside it
		// comes back through this stand-in
		return plan
	}
	expanded := slices.Concat(globals, strings.Fields(alias), rest)
	if p := classifyGit(expanded); p.Hold {
		return p
	}
	return plan
}

type gitRunner struct {
	real    string
	globals []string
}

// out runs a git query. These are all "is it set" or "does it exist"
// questions, where git answers no by failing, so a failure means no.
func (g gitRunner) out(args ...string) (string, bool) {
	out, err := exec.Command(g.real, slices.Concat(g.globals, args)...).Output()
	return strings.TrimSpace(string(out)), err == nil
}

func npmFacts(real string, args []string, workDir string) (destination, subject string) {
	dir := workDir
	afterPublish := false
	for i, a := range args {
		switch {
		case a == "--registry" && i+1 < len(args):
			destination = args[i+1]
		case strings.HasPrefix(a, "--registry="):
			destination = strings.TrimPrefix(a, "--registry=")
		case a == "publish":
			afterPublish = true
		case afterPublish && !strings.HasPrefix(a, "-"):
			if info, err := os.Stat(a); err == nil && info.IsDir() {
				dir = a
			}
		}
	}
	if destination == "" {
		// npm decides this from several config files, so ask it
		out, err := exec.Command(real, "config", "get", "registry").Output()
		if destination = strings.TrimSpace(string(out)); err != nil || destination == "" {
			destination = "the npm registry"
		}
	}
	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	// A missing or broken package.json only costs the description: the
	// real npm reports it properly if the publish is applied.
	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil && json.Unmarshal(data, &pkg) == nil && pkg.Name != "" {
		subject = pkg.Name + "@" + pkg.Version
	}
	return destination, subject
}

func ghFacts(args []string) (destination, subject string) {
	destination = "GitHub, for the repository in this folder"
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case (a == "-R" || a == "--repo") && i+1 < len(args):
			destination = "GitHub repository " + args[i+1]
			i++
		case strings.HasPrefix(a, "--repo="):
			destination = "GitHub repository " + strings.TrimPrefix(a, "--repo=")
		case !strings.HasPrefix(a, "-") && len(words) < 2:
			words = append(words, a)
		}
	}
	return destination, strings.Join(words, " ")
}

// readsStdin reports whether gh was told to take input from standard
// input ("--body-file -", "--input -", "-F field=@-"). Reading it without
// being told to could wait forever on a pipe nobody closes.
func readsStdin(args []string) bool {
	for _, a := range args {
		if a == "-" || strings.HasSuffix(a, "=@-") {
			return true
		}
	}
	return false
}
