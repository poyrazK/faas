package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// The reference is generated, committed, and checked: a manifest edit
// without a regenerate fails CI with the exact command to run.
func TestMarkdownReferenceFresh(t *testing.T) {
	var buf bytes.Buffer
	renderMarkdownReference(&buf, cliCommands)
	want, err := os.ReadFile("../../docs/cli-reference.md")
	if err != nil {
		t.Fatalf("docs/cli-reference.md missing: %v — run `go run ./cmd/gregale man --markdown > docs/cli-reference.md`", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("docs/cli-reference.md is stale — run `go run ./cmd/gregale man --markdown > docs/cli-reference.md`")
	}
}

func TestMarkdownReferenceShape(t *testing.T) {
	var buf bytes.Buffer
	renderMarkdownReference(&buf, []cliCommand{{
		Name:        "plan",
		Short:       "Change the subscription plan.",
		Positionals: []string{"<plan>"},
		ClosedSet:   []string{"free", "hobby"},
		Flags:       []cliFlag{{Name: "json", Short: "machine output"}},
		Subcommands: []cliSub{{
			Name:  "show",
			Short: "Print the plan.",
			Flags: []cliFlag{{Name: "org", Short: "org slug", Req: true, Value: "slug"}},
		}},
	}})
	out := buf.String()
	for _, want := range []string{
		"# gregale CLI reference",
		"## plan",
		"`gregale plan [<subcommand>] <plan> [--json]`",
		"`free` · `hobby`",
		"| `--json` | machine output |  |",
		"### plan show",
		"| `--org <slug>` | org slug | required |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMarkdownReferenceEscapesPlaceholdersAndFlagValues(t *testing.T) {
	var buf bytes.Buffer
	renderMarkdownReference(&buf, []cliCommand{{
		Name:  "tail",
		Short: "Filter by <slug> | <owner>.",
		Flags: []cliFlag{
			{Name: "app", Short: "app <slug>", Value: "slug"},
			{Name: "include-stateless", Short: "boolean switch"},
		},
		Subcommands: []cliSub{{Name: "show", Short: "Show <id>"}},
	}})
	out := buf.String()
	for _, want := range []string{
		"Filter by &lt;slug&gt; \\| &lt;owner&gt;.",
		"`gregale tail [<subcommand>] [--app <slug>] [--include-stateless]`",
		"| `--app <slug>` | app &lt;slug&gt; |  |",
		"Show &lt;id&gt;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
