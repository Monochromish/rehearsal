package replay

import (
	"strings"
	"testing"
)

func TestParseRejectsWhatCannotBePlayed(t *testing.T) {
	step := `{"request":{"method":"GET","url":"https://docs.example.org/x"}}`
	tests := []struct{ name, recording, want string }{
		{"a good one", `{"version":1,"name":"ok","steps":[` + step + `]}`, ""},
		{"not JSON", `{"version":1,`, "not a readable recording"},
		{"an unknown field", `{"version":1,"name":"x","steps":[` + step + `],"extra":true}`, "not a readable recording"},
		{"another version", `{"version":2,"name":"x","steps":[` + step + `]}`, "version 2"},
		{"no name", `{"version":1,"steps":[` + step + `]}`, "no name, or no steps"},
		{"no steps", `{"version":1,"name":"x","steps":[]}`, "no name, or no steps"},
		{"an empty step", `{"version":1,"name":"x","steps":[{}]}`, "step 1 must be exactly one of"},
		{"a step that is two things", `{"version":1,"name":"x","steps":[{"request":{"method":"GET","url":"https://a.example/"},"file":{"path":"a","change":"added","files":1,"bytes_before":0,"bytes_after":1}}]}`, "step 1 must be exactly one of"},
		{"a query string, which could hold anything", `{"version":1,"name":"x","steps":[{"request":{"method":"GET","url":"https://a.example/x?token=abc"}}]}`, "no query string"},
		{"a request with no method", `{"version":1,"name":"x","steps":[{"request":{"url":"https://a.example/x"}}]}`, "needs a method"},
		{"an address with no scheme", `{"version":1,"name":"x","steps":[{"request":{"method":"GET","url":"a.example/x"}}]}`, "needs a method and an address"},
		{"a command of no known kind", `{"version":1,"name":"x","steps":[{"command":{"kind":"ssh","args":["x"],"destination":"y"}}]}`, `"ssh" is not a kind of command`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.recording))
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("Parse = %v, want it accepted", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("Parse = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}
