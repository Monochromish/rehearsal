package standin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"rehearsal/internal/events"
	"rehearsal/internal/hold"
)

// PackageManagers are the programs whose installs are checked.
var PackageManagers = []string{"npm", "pnpm", "yarn", "pip", "pip3", "uv"}

const (
	// a version published more recently than this is high risk
	newVersion = 7 * 24 * time.Hour
	// the most of a registry's answer that is read
	maxMetadata = 32 << 20

	defaultNpmRegistry = "https://registry.npmjs.org"
	defaultPyPI        = "https://pypi.org"
)

// pkg is one package named on an install command line.
type pkg struct {
	name    string
	version string // empty for "whatever is latest"
}

// installs picks the packages out of an install command. ok is false for
// any other command, and for an install that names no package, which
// installs what the project's own files already list.
func installs(name string, args []string) (specs []pkg, python bool, ok bool) {
	words := positional(args)
	var verbs []string
	switch name {
	case "npm":
		verbs = []string{"install", "i", "add", "in", "ins", "inst", "insta", "instal", "isnt", "isnta", "isntal", "isntall"}
	case "pnpm":
		verbs = []string{"add", "install", "i"}
	case "yarn":
		verbs = []string{"add"}
	case "pip", "pip3":
		python, verbs = true, []string{"install"}
	case "uv":
		python = true
		if len(words) > 1 && words[0] == "pip" {
			words = words[1:]
			verbs = []string{"install"}
		} else {
			verbs = []string{"add"}
		}
	}
	if len(words) < 2 || !slices.Contains(verbs, words[0]) {
		return nil, python, false
	}
	for _, w := range words[1:] {
		if p, registry := parseSpec(w, python); registry {
			specs = append(specs, p)
		}
	}
	return specs, python, len(specs) > 0
}

// options of the package managers that take a separate value, so that the
// value is not mistaken for a package
var valueOptions = []string{
	"-r", "--requirement", "-c", "--constraint", "-e", "--editable", "-t", "--target", "-i", "--index-url",
	"--extra-index-url", "-f", "--find-links", "--prefix", "--registry", "--tag", "-w", "--workspace", "--filter",
	"-C", "--dir", "--cwd", "--python", "-p", "--group", "--optional", "--index", "--default-index", "--cache-dir",
}

func positional(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case slices.Contains(valueOptions, a):
			i++
		case strings.HasPrefix(a, "-"):
		default:
			out = append(out, a)
		}
	}
	return out
}

var pythonSpec = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)(?:\[[^\]]*\])?(?:==([A-Za-z0-9.!+_-]+))?`)

// parseSpec reads one package word. registry is false for anything that
// does not come from the registry by name: a path, an archive, an address.
func parseSpec(word string, python bool) (p pkg, registry bool) {
	if word == "" || strings.ContainsAny(word[:1], "./~") || strings.Contains(word, ":") ||
		strings.HasSuffix(word, ".tgz") || strings.HasSuffix(word, ".tar.gz") || strings.HasSuffix(word, ".whl") {
		return pkg{}, false
	}
	if python {
		m := pythonSpec.FindStringSubmatch(word)
		if m == nil {
			return pkg{}, false
		}
		return pkg{name: strings.ToLower(m[1]), version: m[2]}, true
	}
	name, version := word, ""
	// a scoped name starts with @, so the version's @ is a later one
	if at := strings.LastIndex(word, "@"); at > 0 {
		name, version = word[:at], word[at+1:]
	}
	if strings.ContainsAny(version, "^~><*| ") || version == "latest" {
		version = "" // a range or a tag: the registry decides which version
	}
	return pkg{name: name, version: version}, name != ""
}

// lockfiles each package manager writes, and how a package's name appears
// in one. A package found there was installed before, and is not new.
func locked(dir string, p pkg, python bool) bool {
	type lock struct {
		file    string
		pattern string
	}
	name := regexp.QuoteMeta(p.name)
	locks := []lock{
		{"package-lock.json", `"node_modules/` + name + `"`},
		{"pnpm-lock.yaml", `(?m)^\s+'?/?` + name + `[@:']`},
		{"yarn.lock", `(?m)^"?` + name + `@`},
	}
	if python {
		// names compare without regard to case, or to - versus _ versus .
		loose := strings.NewReplacer(`-`, `[-_.]`, `_`, `[-_.]`, `\.`, `[-_.]`).Replace(name)
		locks = []lock{
			{"requirements.txt", `(?im)^` + loose + `\s*(?:[=<>~!\[;]|$)`},
			{"uv.lock", `(?im)^name = "` + loose + `"`},
			{"poetry.lock", `(?im)^name = "` + loose + `"`},
			{"Pipfile.lock", `(?i)"` + loose + `":`},
		}
	}
	for _, l := range locks {
		data, err := os.ReadFile(filepath.Join(dir, l.file))
		if err != nil {
			continue // no such lockfile here
		}
		if re, err := regexp.Compile(l.pattern); err == nil && re.Match(data) {
			return true
		}
	}
	return false
}

// facts is what a registry says about one version of a package.
type facts struct {
	known     bool // false when the registry could not be asked or understood
	exists    bool
	version   string
	published time.Time // zero when not stated
	scripts   bool      // runs code of its own when installed
	why       string    // when not known, the reason
}

// lookup asks the registry about p. The request goes out through the
// proxy like any other the agent's tools make, so it is in the ledger.
func lookup(ctx context.Context, p pkg, python bool) facts {
	if python {
		return lookupPyPI(ctx, p)
	}
	return lookupNpm(ctx, p)
}

// viaProxy sends every request through the proxy named in the
// environment, which under rehearse is the Rehearsal proxy. The standard
// client skips the proxy for addresses on this machine, and a registry on
// this machine should be in the ledger like any other.
var viaProxy = &http.Client{Transport: &http.Transport{
	Proxy: func(req *http.Request) (*url.URL, error) {
		names := []string{"HTTPS_PROXY", "https_proxy"}
		if req.URL.Scheme == "http" {
			names = []string{"HTTP_PROXY", "http_proxy"}
		}
		for _, name := range names {
			if v := os.Getenv(name); v != "" {
				return url.Parse(v)
			}
		}
		return nil, nil // no proxy set: go direct
	},
}}

func fetch(ctx context.Context, url string, into any) (found bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := viaProxy.Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close() // read below; nothing was written to it
	if res.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if res.StatusCode != http.StatusOK {
		return false, fmt.Errorf("the registry answered %s", res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxMetadata+1))
	if err != nil {
		return false, err
	}
	if len(data) > maxMetadata {
		return false, errors.New("the registry's answer is too large to read")
	}
	return true, json.Unmarshal(data, into)
}

// registryOption is the registry named on the command line, if one is.
func registryOption(args []string) string {
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, "--registry="); ok {
			return v
		}
		if a == "--registry" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func npmRegistry() string {
	for _, k := range []string{"npm_config_registry", "NPM_CONFIG_REGISTRY"} {
		if v := os.Getenv(k); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return defaultNpmRegistry
}

func lookupNpm(ctx context.Context, p pkg) facts {
	var doc struct {
		DistTags map[string]string    `json:"dist-tags"`
		Time     map[string]time.Time `json:"time"`
		Versions map[string]struct {
			Scripts          map[string]string `json:"scripts"`
			HasInstallScript bool              `json:"hasInstallScript"`
		} `json:"versions"`
	}
	found, err := fetch(ctx, npmRegistry()+"/"+strings.ReplaceAll(p.name, "/", "%2f"), &doc)
	if err != nil {
		return facts{why: err.Error()}
	}
	if !found {
		return facts{known: true}
	}
	f := facts{known: true, version: p.version}
	if f.version == "" {
		f.version = doc.DistTags["latest"]
	}
	v, ok := doc.Versions[f.version]
	if !ok {
		return f // the package is there and this version of it is not
	}
	f.exists = true
	f.published = doc.Time[f.version]
	f.scripts = v.HasInstallScript
	for _, hook := range []string{"preinstall", "install", "postinstall"} {
		if v.Scripts[hook] != "" {
			f.scripts = true
		}
	}
	return f
}

// pypiBase turns the index address pip and uv are given, which ends in
// /simple, into the address the JSON pages are under.
func pypiBase() string {
	for _, k := range []string{"UV_DEFAULT_INDEX", "UV_INDEX_URL", "PIP_INDEX_URL"} {
		if v := os.Getenv(k); v != "" {
			return strings.TrimSuffix(strings.TrimRight(v, "/"), "/simple")
		}
	}
	return defaultPyPI
}

func lookupPyPI(ctx context.Context, p pkg) facts {
	var doc struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
		Releases map[string][]struct {
			PackageType string    `json:"packagetype"`
			Uploaded    time.Time `json:"upload_time_iso_8601"`
		} `json:"releases"`
	}
	found, err := fetch(ctx, pypiBase()+"/pypi/"+p.name+"/json", &doc)
	if err != nil {
		return facts{why: err.Error()}
	}
	if !found {
		return facts{known: true}
	}
	f := facts{known: true, version: p.version}
	if f.version == "" {
		f.version = doc.Info.Version
	}
	files, ok := doc.Releases[f.version]
	if !ok || len(files) == 0 {
		return f
	}
	f.exists = true
	// With no ready-built file, installing runs the package's own build
	// code on this machine.
	f.scripts = true
	for _, file := range files {
		if f.published.IsZero() || file.Uploaded.Before(f.published) {
			f.published = file.Uploaded
		}
		if file.PackageType == "bdist_wheel" {
			f.scripts = false
		}
	}
	return f
}

// RatePackage rates a package from facts supplied by the caller, as an
// install would after asking the registry. It is for replaying a recorded
// install, where there is no registry to ask. A zero published time means
// the registry did not say.
func RatePackage(name, version string, known, exists bool, published time.Time, scripts bool, now time.Time) (events.Risk, string) {
	return rate(pkg{name: name, version: version}, facts{known: known, exists: exists, version: version, published: published, scripts: scripts, why: "it was not reachable"}, now)
}

// rate turns the facts into a rating and a sentence saying why.
func rate(p pkg, f facts, now time.Time) (events.Risk, string) {
	what := p.name
	if f.version != "" {
		what += " " + f.version
	}
	switch {
	case !f.known:
		return events.RiskMedium, fmt.Sprintf("%s: the registry could not be asked about it (%s)", what, f.why)
	case !f.exists && f.version != "" && p.version != "":
		return events.RiskHigh, fmt.Sprintf("%s: the registry has no such version", what)
	case !f.exists:
		return events.RiskHigh, fmt.Sprintf("%s: the registry has no package of that name", p.name)
	}
	risk := events.RiskLow
	var parts []string
	switch age := now.Sub(f.published); {
	case f.published.IsZero():
		risk = events.RiskMedium
		parts = append(parts, "the registry does not say when it was published")
	case age < newVersion:
		risk = events.RiskHigh
		parts = append(parts, "published "+ago(age))
	default:
		parts = append(parts, "published "+ago(age))
	}
	if f.scripts {
		risk = events.RiskHigh
		parts = append(parts, "runs a script when installed")
	}
	return risk, what + ": " + strings.Join(parts, "; ")
}

// gate checks the packages an install would add that the project has not
// installed before. If the registry's facts make any of them high risk the
// install is stopped here, with the reason, and the agent sees it fail.
// Otherwise the real command runs.
//
// The agent needs the real result of an install to carry on, so an install
// is never held with a made-up success the way a push is.
func gate(real, name string, args []string, specs []pkg, python bool) int {
	workDir, err := os.Getwd()
	if err != nil {
		return refuse(name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// ask the registry the install itself would use
	if named := registryOption(args); named != "" && !python {
		if err := os.Setenv("npm_config_registry", named); err != nil {
			return refuse(name, err)
		}
	}
	var checked []events.PackageChecked
	stop := false
	for _, p := range specs {
		if locked(workDir, p, python) {
			continue
		}
		f := lookup(ctx, p, python)
		risk, words := rate(p, f, time.Now())
		version := f.version
		if version == "" {
			version = p.version
		}
		checked = append(checked, events.PackageChecked{Manager: name, Name: p.name, Version: version, Risk: risk, Facts: words})
		stop = stop || risk == events.RiskHigh
	}
	if len(checked) == 0 {
		return pass(real, name, args, false)
	}
	for i := range checked {
		checked[i].Blocked = checked[i].Risk == events.RiskHigh
	}
	socket := os.Getenv(EnvSocket)
	if socket == "" {
		return refuse(name, errors.New(EnvSocket+" is not set"))
	}
	if err := hold.ReportPackages(ctx, socket, checked); err != nil {
		return refuse(name, err)
	}
	if !stop {
		return pass(real, name, args, false)
	}
	fmt.Fprintf(os.Stderr, "rehearse: this %s install was stopped before it ran, because of what the registry says about a package it would add:\n", name)
	for _, c := range checked {
		if c.Blocked {
			fmt.Fprintf(os.Stderr, "  %s\n", c.Facts)
		}
	}
	fmt.Fprintln(os.Stderr, "Nothing was installed. If this is the package that was meant, the user can install it themselves, outside Rehearsal.")
	return 1
}

func ago(d time.Duration) string {
	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit + " ago"
		}
		return fmt.Sprintf("%d %ss ago", n, unit)
	}
	switch {
	case d < 0:
		return "at a time in the future"
	case d < time.Hour:
		return "less than an hour ago"
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour")
	case d < 60*24*time.Hour:
		return plural(int(d.Hours()/24), "day")
	case d < 2*365*24*time.Hour:
		return plural(int(d.Hours()/24/30), "month")
	}
	return plural(int(d.Hours()/24/365), "year")
}
