package investigate

import (
	"fmt"
	"strings"
)

// ReportFile is the name the written report is given.
const ReportFile = "rehearsal-investigation.md"

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// code writes s as inline code, whatever backticks it holds.
func code(s string) string {
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

// rawEntry writes one entry as it was stored, with the part that
// supports the statement named beneath it.
func (c *Case) rawEntry(b *strings.Builder, r Ref) {
	e, ok := c.Entry(r)
	if !ok {
		fmt.Fprintf(b, "- %s: this entry is not in the record.\n", r.Cite())
		return
	}
	fmt.Fprintf(b, "- %s, %s, `%s`, hash `%s`\n\n", r.Cite(), e.Time, e.Type, e.Hash)
	fmt.Fprintf(b, "  ```\n  %s\n  ```\n", strings.ReplaceAll(e.Data, "\n", "\n  "))
	if r.Mark != "" {
		fmt.Fprintf(b, "  Supporting part: %s\n", code(r.Mark))
	}
	if e.Problem != "" {
		fmt.Fprintf(b, "  **Cannot be relied on:** %s.\n", e.Problem)
	}
}

func narrative(b *strings.Builder, n *Narrative, c *Case) {
	if n == nil {
		return
	}
	fmt.Fprintf(b, "**Written by a model** (%s). Nothing in this paragraph is evidence. Each sentence cites the entries it was written from.\n\n", n.Model)
	if n.Note != "" {
		fmt.Fprintf(b, "%s\n\n", n.Note)
	}
	for _, s := range n.Sentences {
		fmt.Fprintf(b, "%s\n", s.Text)
	}
	if len(n.Sentences) > 0 {
		b.WriteString("\n")
	}
	if n.Removed > 0 {
		fmt.Fprintf(b, "%s the model wrote %s removed for citing nothing, or something it was not given.\n\n", plural(n.Removed, "sentence", "sentences"), map[bool]string{true: "was", false: "were"}[n.Removed == 1])
	}
	fmt.Fprintf(b, "Entries the model was given: %s\n\n", cites(n.Given))
}

// Markdown is the case as a report a person can read top to bottom.
func (c *Case) Markdown() string {
	var b strings.Builder
	b.WriteString("# Investigation\n\n")
	fmt.Fprintf(&b, "Rehearsal %s, rule set %s. %s read, %s.\n\n", c.ToolVersion, c.RuleSet,
		plural(len(c.Records), "record", "records"), plural(len(c.Findings), "finding", "findings"))
	b.WriteString("Three kinds of statement appear below and are always labelled. **Observed** is read directly from one entry. **Derived** is a named rule applied to several entries. **Written by a model** is an account in prose; it can be skipped without losing a fact.\n\n")

	b.WriteString("## Custody\n\n")
	for _, cu := range c.Records {
		state := "intact"
		if !cu.Verified {
			state = "NOT VERIFIED"
		}
		fmt.Fprintf(&b, "### %s: %s (%s)\n\n", cu.Label, cu.Session, state)
		fmt.Fprintf(&b, "- Read from: %s, %s\n", cu.Kind, code(cu.Source))
		fmt.Fprintf(&b, "- Chain: %s.\n", cu.Chain)
		fmt.Fprintf(&b, "- Signature: %s.\n", cu.Signature)
		fmt.Fprintf(&b, "- To check this yourself: %s\n\n", code(cu.Command))
	}

	b.WriteString("## What the record cannot show\n\n")
	for _, l := range c.Limits {
		fmt.Fprintf(&b, "- %s\n", l)
	}

	if len(c.Truth) > 0 {
		b.WriteString("\n## Said against observed\n\n")
		b.WriteString("What the agent said in its last message, or was told when an action was held, beside what the record shows. The first entries cited are what was said or told, the rest what was observed.\n\n")
		for _, s := range c.Truth {
			note := ""
			if i := c.session(s.Refs[0].Label); i >= 0 && !c.records[i].Custody.Verified {
				note = " **From an unverified record.**"
			}
			fmt.Fprintf(&b, "- **%s.** %s: %s %s%s\n\n", s.Kind, s.Refs[0].Label, s.Text, cites(s.Refs), note)
			for _, r := range s.Refs {
				c.rawEntry(&b, r)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n## Findings\n\n")
	if len(c.Findings) == 0 {
		b.WriteString("The rules found nothing to report in what was read.\n")
	}
	if c.ModelNote != "" {
		fmt.Fprintf(&b, "%s\n\n", c.ModelNote)
	}
	if c.Account != nil {
		narrative(&b, c.Account, c)
	}
	if len(c.Records) > 1 {
		b.WriteString("| Session |")
		for _, k := range c.Kinds {
			fmt.Fprintf(&b, " %s |", k.Title)
		}
		b.WriteString("\n|---|" + strings.Repeat("---|", len(c.Kinds)) + "\n")
		for _, row := range c.Overview {
			fmt.Fprintf(&b, "| %s |", row.Label)
			for _, cell := range row.Cells {
				fmt.Fprintf(&b, " %s |", strings.Join(cell, " "))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	for _, f := range c.Findings {
		fmt.Fprintf(&b, "### %s. %s\n\n", f.ID, f.Title)
		fmt.Fprintf(&b, "Kind: %s. Sessions: %s.\n\n", f.Kind, strings.Join(f.Sessions, ", "))
		for _, note := range []string{f.UnverifiedNote, f.RecordNote} {
			if note != "" {
				fmt.Fprintf(&b, "> **%s**\n\n", note)
			}
		}
		b.WriteString("Chain: ")
		for i, s := range f.Chain {
			if i > 0 {
				b.WriteString(" → ")
			}
			if s.Rule != "" {
				fmt.Fprintf(&b, "(rule %s) ", s.Rule)
			}
			fmt.Fprintf(&b, "%s %s", s.Label, cites(s.Refs))
		}
		b.WriteString("\n\n")
		for _, s := range f.Statements {
			kind := s.Kind
			if s.Rule != "" {
				kind += ", rule " + s.Rule
			}
			fmt.Fprintf(&b, "- **%s.** %s %s\n", kind, s.Text, cites(s.Refs))
		}
		b.WriteString("\n")
		for _, x := range f.Extracts {
			fmt.Fprintf(&b, "Extract from what the agent read (%s, a reply from %s)", x.Ref.Cite(), x.Source)
			if x.Hidden != "" {
				fmt.Fprintf(&b, ", hidden in %s", x.Hidden)
			}
			fmt.Fprintf(&b, ":\n\n> %s\n\n", x.Text)
			if x.Marked != "" {
				fmt.Fprintf(&b, "The part that matters: %s\n\n", code(x.Marked))
			}
		}
		if p := f.Proof; p != nil {
			b.WriteString("Proof that the encoded text was the watched value:\n\n")
			fmt.Fprintf(&b, "- Encoded text as it appeared: %s\n- Encoding: %s\n", code(p.Encoded), p.Encoding)
			if p.StoredHash != "" {
				fmt.Fprintf(&b, "- Hash stored for %q: `%s`\n", p.Value, p.StoredHash)
			}
			if p.DecodedHash != "" {
				fmt.Fprintf(&b, "- Hash of the decoded text: `%s`\n", p.DecodedHash)
			}
			fmt.Fprintf(&b, "- %s %s\n\n", p.Text, cites(p.Refs))
		}
		b.WriteString("Evidence, as stored:\n\n")
		for _, r := range f.Refs {
			c.rawEntry(&b, r)
		}
		b.WriteString("\nHow this was found:\n\n")
		for i, h := range f.How {
			fmt.Fprintf(&b, "%d. %s. %s %s\n", i+1, h.Stage, h.What, cites(h.Refs))
		}
		b.WriteString("\n")
		narrative(&b, f.Narrative, c)
	}
	return b.String()
}

// AnswerText is an answer as plain text for a terminal.
func (c *Case) AnswerText(a Answer) string {
	var b strings.Builder
	switch {
	case a.Text != "":
		fmt.Fprintf(&b, "%s\n", a.Text)
	default:
		fmt.Fprintf(&b, "Written by a model (%s) from the entries below. Each sentence cites the entries it rests on.\n\n", a.Model)
		for _, s := range a.Sentences {
			fmt.Fprintf(&b, "%s\n", s.Text)
		}
		if a.Removed > 0 {
			fmt.Fprintf(&b, "\n%s removed for citing nothing, or something the model was not given.\n", plural(a.Removed, "sentence was", "sentences were"))
		}
	}
	if len(a.Entries) > 0 {
		b.WriteString("\nEntries retrieved for the question, as stored:\n\n")
		for i := range a.Entries {
			e := &a.Entries[i]
			fmt.Fprintf(&b, "%s %s %s hash %s\n  %s\n", ref(e).Cite(), e.Time, e.Type, e.Hash, e.Data)
			if e.Problem != "" {
				fmt.Fprintf(&b, "  Cannot be relied on: %s.\n", e.Problem)
			}
		}
	}
	return b.String()
}
