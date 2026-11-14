package redact

import (
	"slices"
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	const canary = "sk-rehearsal-canary-abcdefghijklmnopqrstuvwxyz"
	tests := []struct {
		name   string
		in     string
		gone   []string // must not survive
		stays  []string // must survive
		intact bool     // output must equal input
	}{
		{"password in a URL", "https://ada:hunter2secret@github.com/ada/repo.git", []string{"hunter2secret", "ada:"}, []string{"github.com/ada/repo.git"}, false},
		{"token-only URL", "https://ghp_abcdefghijklmnopqrstuvwx@github.com/a/b", []string{"ghp_abcdefghijklmnopqrstuvwx"}, []string{"github.com/a/b"}, false},
		{"bearer header", "Authorization: Bearer abc.def-123456789", []string{"abc.def-123456789"}, []string{"Authorization"}, false},
		{"basic header", "authorization: Basic dXNlcjpwYXNzd29yZA==", []string{"dXNlcjpwYXNzd29yZA"}, nil, false},
		{"json api key", `{"name":"demo","api_key":"zz-top-secret-value"}`, []string{"zz-top-secret-value"}, []string{`"name":"demo"`}, false},
		{"form field", "user=ada&access_token=tok123456&page=2", []string{"tok123456"}, []string{"user=ada", "page=2"}, false},
		{"flag with equals", "--password=opensesame", []string{"opensesame"}, []string{"--password="}, false},
		{"github token alone", "see ghp_0123456789abcdefghijABCDEFGHIJ0123 here", []string{"ghp_0123456789"}, []string{"see", "here"}, false},
		{"openai-style key alone", "key is sk-proj-0123456789abcdefghij", []string{"sk-proj-0123456789abcdefghij"}, nil, false},
		{"aws key id", "AKIAIOSFODNN7EXAMPLE", []string{"AKIAIOSFODNN7EXAMPLE"}, nil, false},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N", []string{"eyJzdWIiOiIxMjM0NTY3ODkwIn0"}, nil, false},
		{"our canary key is kept", "key=" + canary, nil, []string{canary}, true},
		{"canary kept, real key beside it masked", canary + " and sk-real-0123456789abcdefgh", []string{"sk-real-0123456789abcdefgh"}, []string{canary}, false},
		{"plain command", "git push origin main", nil, nil, true},
		{"ssh remote", "git@github.com:ada/repo.git", nil, nil, true},
		{"ordinary prose", "The quick brown fox pushes to main.", nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Text(tt.in, canary)
			for _, s := range tt.gone {
				if strings.Contains(got, s) {
					t.Errorf("Text(%q) = %q, still contains %q", tt.in, got, s)
				}
			}
			for _, s := range tt.stays {
				if !strings.Contains(got, s) {
					t.Errorf("Text(%q) = %q, lost %q", tt.in, got, s)
				}
			}
			if tt.intact && got != tt.in {
				t.Errorf("Text(%q) = %q, want it unchanged", tt.in, got)
			}
			if !tt.intact && !strings.Contains(got, Hidden) {
				t.Errorf("Text(%q) = %q, want a %s marker", tt.in, got, Hidden)
			}
		})
	}
}

func TestArgs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"value after a secret flag", []string{"publish", "--otp", "123456"}, []string{"publish", "--otp", Hidden}},
		{"value after --token", []string{"api", "--auth-token", "abc", "/user"}, []string{"api", "--auth-token", Hidden, "/user"}},
		{"header argument", []string{"-H", "Authorization: Bearer abcdefgh12345"}, []string{"-H", "Authorization: " + Hidden + " " + Hidden}},
		{"nothing secret", []string{"push", "--force", "origin", "main"}, []string{"push", "--force", "origin", "main"}},
		{"remote with password", []string{"push", "https://u:p4ssw0rd@host.test/r.git"}, []string{"push", "https://" + Hidden + "@host.test/r.git"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Args(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("Args(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
