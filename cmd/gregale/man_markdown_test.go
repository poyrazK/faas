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
	renderMarkdownReference(&buf, customerCliCommands())
	want, err := os.ReadFile("../../docs/cli-reference.md")
	if err != nil {
		t.Fatalf("docs/cli-reference.md missing: %v — run `go run ./cmd/gregale man --markdown > docs/cli-reference.md`", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("docs/cli-reference.md is stale — run `go run ./cmd/gregale man --markdown > docs/cli-reference.md`")
	}
}

func TestMarkdownReferenceIncludesGitOpsBindingLifecycle(t *testing.T) {
	var buf bytes.Buffer
	renderMarkdownReference(&buf, customerCliCommands())
	for _, action := range []string{"rebind", "unbind"} {
		if !strings.Contains(buf.String(), "##### projects environments gitops "+action+"\n") {
			t.Errorf("missing nested GitOps %s reference", action)
		}
	}
	if !strings.Contains(buf.String(), "--expected-generation <N>") {
		t.Error("binding lifecycle reference omits generation fence")
	}
}

func TestMarkdownReferenceOmitsAdvancedCommands(t *testing.T) {
	var buf bytes.Buffer
	renderMarkdownReference(&buf, customerCliCommands())
	out := buf.String()
	for _, command := range []string{"admin", "mail", "postgres", "rollouts", "github-webhook-secret", "tenant-surfaces"} {
		if strings.Contains(out, "## "+command+"\n") {
			t.Errorf("public Markdown reference exposes advanced command %q", command)
		}
	}
}

func TestMarkdownReferenceShape(t *testing.T) {
	var buf bytes.Buffer
	renderMarkdownReference(&buf, []cliCommand{{
		Name:        "plan",
		Short:       "Change the subscription plan.",
		Positionals: []string{"<plan>"},
		ClosedSet:   []string{"free", "hobby"},
		Flags: []cliFlag{
			{Name: "json", Short: "machine output"},
			{Name: "yes", Short: "explicit confirmation", Req: true, Bool: true},
		},
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
		"`gregale plan [<subcommand>] <plan> [--json] --yes`",
		"`free` · `hobby`",
		"| `--json` | machine output |  |",
		"| `--yes` | explicit confirmation | required |",
		"### plan show",
		"| `--org <slug>` | org slug | required |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMarkdownReferenceNestedSubcommandSynopsisIncludesPositionals(t *testing.T) {
	var buf bytes.Buffer
	renderMarkdownReference(&buf, []cliCommand{{
		Name: "bindings",
		Subcommands: []cliSub{{
			Name: "object-storage",
			Subcommands: []cliSub{{
				Name:        "rotate",
				Short:       "Rotate one storage binding",
				Positionals: []string{"<app>", "<bucket>", "<binding-id>"},
			}},
		}},
	}})
	if !strings.Contains(buf.String(), "`gregale bindings object-storage rotate <app> <bucket> <binding-id>`") {
		t.Fatalf("nested command synopsis missing positionals:\n%s", buf.String())
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
