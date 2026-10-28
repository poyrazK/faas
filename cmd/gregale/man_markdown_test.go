package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"strings"
	"testing"
)

// The reference is generated, committed, and checked: a manifest edit
// without a regenerate fails CI with the exact command to run. It renders
// the command tree's names (ADR-20261026145832 step 5).
func TestMarkdownReferenceFresh(t *testing.T) {
	var buf bytes.Buffer
	main.RenderMarkdownReference(&buf, main.CliTreeManifest(main.VisibleCliCommands()))
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
	main.RenderMarkdownReference(&buf, main.CliTreeManifest(main.VisibleCliCommands()))
	for _, action := range []string{"rebind", "unbind"} {
		if !strings.Contains(buf.String(), "#### environment gitops "+action+"\n") {
			t.Errorf("missing nested GitOps %s reference", action)
		}
	}
	if !strings.Contains(buf.String(), "--expected-generation <N>") {
		t.Error("binding lifecycle reference omits generation fence")
	}
}

func TestMarkdownReferenceOmitsAdvancedCommands(t *testing.T) {
	var buf bytes.Buffer
	main.RenderMarkdownReference(&buf, main.CliTreeManifest(main.VisibleCliCommands()))
	out := buf.String()
	for _, command := range []string{"admin", "mail", "postgres", "rollouts", "github-webhook-secret", "tenant-surfaces", "entities"} {
		if strings.Contains(out, "## "+command+"\n") {
			t.Errorf("public Markdown reference exposes advanced command %q", command)
		}
	}
}

func TestMarkdownReferenceShape(t *testing.T) {
	var buf bytes.Buffer
	main.RenderMarkdownReference(&buf, []main.CliCommand{{
		Name:        "plan",
		Short:       "Change the subscription plan.",
		Positionals: []string{"<plan>"},
		ClosedSet:   []string{"free", "hobby"},
		Flags: []main.CliFlag{
			{Name: "json", Short: "machine output"},
			{Name: "yes", Short: "explicit confirmation", Req: true, Bool: true},
		},
		Subcommands: []main.CliSub{{
			Name:  "show",
			Short: "Print the plan.",
			Flags: []main.CliFlag{{Name: "org", Short: "org slug", Req: true, Value: "slug"}},
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
	main.RenderMarkdownReference(&buf, []main.CliCommand{{
		Name: "bindings",
		Subcommands: []main.CliSub{{
			Name: "object-storage",
			Subcommands: []main.CliSub{{
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
	main.RenderMarkdownReference(&buf, []main.CliCommand{{
		Name:  "tail",
		Short: "Filter by <slug> | <owner>.",
		Flags: []main.CliFlag{
			{Name: "app", Short: "app <slug>", Value: "slug"},
			{Name: "include-stateless", Short: "boolean switch"},
		},
		Subcommands: []main.CliSub{{Name: "show", Short: "Show <id>"}},
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

// TestMarkdownReferenceListsAppsRmConfirmation pins H10-117: `apps rm`
// refused in non-interactive mode without --yes while its help listed no
// flags at all.
func TestMarkdownReferenceListsAppsRmConfirmation(t *testing.T) {
	var buf bytes.Buffer
	main.RenderMarkdownReference(&buf, main.CliTreeManifest(main.VisibleCliCommands()))
	if !strings.Contains(buf.String(), "`gregale app delete [--dry-run] [--yes] [--quiet] [-q] <slug>`") {
		t.Error("app delete reference omits its confirmation flags")
	}
}

// The published reference documents the command tree: nouns and their tree
// verbs, the app after an `app` verb, and none of today's renamed commands.
func TestMarkdownReferenceSpeaksTheCommandTree(t *testing.T) {
	var buf bytes.Buffer
	main.RenderMarkdownReference(&buf, main.CliTreeManifest(main.VisibleCliCommands()))
	out := buf.String()
	for _, want := range []string{
		"## cron\n", "### cron get\n", "### env set\n", "#### release attempt cancel\n",
		"#### mcp client call\n", "#### bucket multipart get\n", "`gregale app egress-allowlist add [<app>] <cidr>`",
		"#### app listener create\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("reference omits %q", want)
		}
	}
	for _, renamed := range []string{"## crons\n", "## secrets\n", "## deployments\n", "## apps\n", "### cron info\n", "### app list list\n", "#### app listener rm\n"} {
		if strings.Contains(out, renamed) {
			t.Errorf("reference documents today's name %q instead of the tree's", strings.TrimSpace(renamed))
		}
	}
}
