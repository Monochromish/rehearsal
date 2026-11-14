package standin

import (
	"strings"
	"testing"

	"rehearsal/internal/events"
)

func TestClassify(t *testing.T) {
	type want struct {
		hold, readOnly                    bool
		force, deletes, publishes, answer bool
		access, minor                     bool
	}
	held := want{hold: true}
	passed := want{}
	readOnly := want{readOnly: true}

	tests := []struct {
		line string
		want want
	}{
		// git: push in any form is held, everything else passes
		{"git push", held},
		{"git push origin main", held},
		{"git -C /some/dir push origin main", held},
		{"git -c http.extraHeader=x --no-pager push", held},
		{"git push -u origin feature", held},
		{"git push --force origin main", want{hold: true, force: true}},
		{"git push -f", want{hold: true, force: true}},
		{"git push -fu origin main", want{hold: true, force: true}},
		{"git push --force-with-lease=main:abc origin main", want{hold: true, force: true}},
		{"git push origin +main", want{hold: true, force: true}},
		{"git push origin +refs/heads/a:refs/heads/b", want{hold: true, force: true}},
		{"git push origin --delete old-branch", want{hold: true, deletes: true}},
		{"git push origin :old-branch", want{hold: true, deletes: true}},
		{"git push --mirror backup", want{hold: true, force: true, deletes: true}},
		{"git push --all origin", held},
		{"git push --tags", held},
		{"git push -o ci.skip origin main", held},
		{"git send-pack origin main", held},
		{"git push --dry-run origin main", readOnly},
		{"git push -n", readOnly},
		{"git status", readOnly},
		{"git fetch origin", readOnly},
		{"git pull --rebase", readOnly},
		{"git clone https://example.test/repo.git", readOnly},
		{"git commit -m push", readOnly},
		{"git log --grep push", readOnly},
		{"git -C push status", readOnly},
		{"git stash push", readOnly},
		{"git", readOnly},

		// npm: only publish is held
		{"npm publish", want{hold: true, publishes: true}},
		{"npm publish ./dist --access public", want{hold: true, publishes: true}},
		{"npm --registry https://npm.example.test publish", want{hold: true, publishes: true}},
		{"npm publish --dry-run", passed},
		{"npm install", passed},
		{"npm run publish", passed},
		{"npm view left-pad", passed},
		{"npm", passed},

		// gh: commands that create or change something are held
		{"gh pr create --title x --body y", want{hold: true, answer: true, minor: true}},
		{"gh pr merge 12 --squash", held},
		{"gh pr comment 12 --body hi", want{hold: true, minor: true}},
		{"gh -R ada/repo issue create --title t", want{hold: true, answer: true, minor: true}},
		{"gh issue comment 3 --body thanks", want{hold: true, minor: true}},
		{"gh issue close 3", held},
		{"gh issue delete 3", held},
		{"gh issue edit 3 --add-label bug", want{hold: true, minor: true}},
		{"gh pr edit 12 --add-label=bug --add-label ui", want{hold: true, minor: true}},
		{"gh pr edit 12 --add-label bug --title New", held},
		{"gh issue edit 3 --body rewritten", held},
		{"gh label create bug", want{hold: true, minor: true}},
		{"gh label delete bug", held},
		{"gh release create v1.0.0", held},
		{"gh release create v1.0.0 --draft", held},
		{"gh release delete v1.0.0", want{hold: true, deletes: true}},
		{"gh repo delete ada/repo --yes", want{hold: true, deletes: true}},
		{"gh repo create ada/new --public", want{hold: true, access: true}},
		{"gh repo create ada/new --private", held},
		{"gh repo edit ada/repo --visibility public", want{hold: true, access: true}},
		{"gh repo edit ada/repo --visibility=private", want{hold: true, access: true}},
		{"gh repo edit ada/repo --description New", held},
		{"gh api -X PUT /repos/ada/repo/collaborators/eve", want{hold: true, access: true}},
		{"gh api -X PUT /repos/ada/repo/branches/main/protection --input rules.json", want{hold: true, access: true}},
		{"gh api -X DELETE /repos/ada/repo/branches/main/protection", want{hold: true, deletes: true, access: true}},
		{"gh api /repos/ada/repo/collaborators", readOnly},
		{"gh gist create notes.txt", want{hold: true, answer: true}},
		{"gh secret set TOKEN", held},
		{"gh workflow run ci.yml", held},
		{"gh api -X POST /repos/ada/repo/issues -f title=t", held},
		{"gh api --method=PATCH /repos/ada/repo", held},
		{"gh api -XDELETE /repos/ada/repo", want{hold: true, deletes: true}},
		{"gh api /repos/ada/repo/issues -f title=t", held},
		{"gh api graphql -f query=mutation{addStar(input:{}){clientMutationId}}", held},
		{"gh api graphql -F query=@mutation.graphql", held},

		// gh: read-only commands pass
		{"gh pr list", readOnly},
		{"gh pr view 12 --json title", readOnly},
		{"gh pr checks", readOnly},
		{"gh issue list --state open", readOnly},
		{"gh repo view ada/repo", readOnly},
		{"gh release list", readOnly},
		{"gh run view 99 --log", readOnly},
		{"gh search issues crash", readOnly},
		{"gh api /user", readOnly},
		{"gh api -X GET /repos/ada/repo/pulls", readOnly},
		{"gh api graphql -f query={viewer{login}}", readOnly},
		{"gh api graphql -f query=query{viewer{login}}", readOnly},

		// gh: not known either way, so left to the proxy
		{"gh pr checkout 12", passed},
		{"gh auth status", passed},
		{"gh extension install ada/gh-thing", passed},
		{"gh", passed},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			fields := strings.Fields(tt.line)
			p := Classify(fields[0], fields[1:])
			got := want{p.Hold, p.ReadOnly, p.Force, p.Deletes, p.Publishes, p.NeedsRealAnswer, p.ChangesAccess, p.Minor}
			if got != tt.want {
				t.Errorf("Classify(%q)\n got %+v\nwant %+v", tt.line, got, tt.want)
			}
		})
	}
}

func TestClassifyKinds(t *testing.T) {
	tests := []struct {
		line string
		kind events.ActionKind
	}{
		{"git push", events.ActionGitPush},
		{"npm publish", events.ActionNpmPublish},
		{"gh pr create", events.ActionGh},
		{"gh api -X POST /x", events.ActionGh},
	}
	for _, tt := range tests {
		fields := strings.Fields(tt.line)
		if got := Classify(fields[0], fields[1:]).Kind; got != tt.kind {
			t.Errorf("Classify(%q).Kind = %q, want %q", tt.line, got, tt.kind)
		}
	}
}

func TestParsePush(t *testing.T) {
	tests := []struct {
		line     string
		remote   string
		refspecs string
	}{
		{"", "", ""},
		{"origin", "origin", ""},
		{"origin main", "origin", "main"},
		{"-u origin feature", "origin", "feature"},
		{"--force origin main dev", "origin", "main dev"},
		{"-o ci.skip origin main", "origin", "main"},
		{"--repo ignored origin main", "origin", "main"},
		{"origin HEAD:refs/heads/review", "origin", "HEAD:refs/heads/review"},
		{"-- origin -weird", "origin", "-weird"},
	}
	for _, tt := range tests {
		s := parsePush(strings.Fields(tt.line))
		if s.remote != tt.remote || strings.Join(s.refspecs, " ") != tt.refspecs {
			t.Errorf("parsePush(%q) = remote %q refspecs %q, want %q and %q", tt.line, s.remote, s.refspecs, tt.remote, tt.refspecs)
		}
	}
}
