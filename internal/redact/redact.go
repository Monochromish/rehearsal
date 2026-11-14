// Package redact masks anything that looks like a credential before it is
// stored for display or sent to the reviewer.
package redact

import (
	"regexp"
	"strings"
)

const Hidden = "[hidden]"

// Order matters: "Authorization: Bearer abc" must lose "abc" to the scheme
// rule before the key/value rule takes "Bearer" for the value.
var rules = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(://)[^/\s:@]+(?::[^/\s@]*)?@`), "${1}" + Hidden + "@"},
	{regexp.MustCompile(`(?i)\b(bearer|basic|token)\s+[A-Za-z0-9._~+/=-]{8,}`), "${1} " + Hidden},
	{regexp.MustCompile(`(?i)([\w.-]*(?:token|secret|passw(?:or)?d|api[_-]?key|access[_-]?key|private[_-]?key|credential|authorization|cookie|otp)[\w.-]*["']?\s*[=:]\s*["']?)[^\s"'&,;}]+`), "${1}" + Hidden},
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{16,}|xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|npm_[A-Za-z0-9]{20,}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,})`), Hidden},
}

var secretFlag = regexp.MustCompile(`(?i)^--?[\w-]*(?:token|secret|password|otp|api-?key|auth)[\w-]*$`)

// Text masks credentials in s. A match containing one of keep is left
// alone: those are our own planted canaries, which may be shown.
func Text(s string, keep ...string) string {
	for _, rule := range rules {
		s = rule.re.ReplaceAllStringFunc(s, func(match string) string {
			for _, k := range keep {
				if k != "" && strings.Contains(match, k) {
					return match
				}
			}
			return rule.re.ReplaceAllString(match, rule.repl)
		})
	}
	return s
}

// Args masks a command line: each argument as Text does, and also the
// argument after a flag such as --token.
func Args(args []string, keep ...string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		if i > 0 && secretFlag.MatchString(args[i-1]) {
			out[i] = Hidden
			continue
		}
		out[i] = Text(arg, keep...)
	}
	return out
}
