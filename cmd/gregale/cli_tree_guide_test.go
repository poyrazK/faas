package main_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

const (
	cliTreeGuidePath  = "../../docs/cli-command-tree.md"
	cliTreeGuideBegin = "<!-- BEGIN GENERATED: earlier names (TestCliTreeMigrationGuideFresh) -->\n"
	cliTreeGuideEnd   = "<!-- END GENERATED -->"
)

// renderCliTreeGuide lists every earlier name of a released customer
// command with the tree's name for it: top-level commands, the paths the
// tree renamed, and the flag spellings one canonical flag replaces.
func renderCliTreeGuide() string {
	released := map[string]bool{}
	for _, command := range main.VisibleCliCommands() {
		if command.Audience == main.CliAudienceCustomer {
			released[command.Name] = true
		}
	}
	var b strings.Builder
	b.WriteString("\n## Earlier commands\n\n| Earlier | Now |\n|---|---|\n")
	var commands []string
	for name := range released {
		if main.CliTreeEarlierName(name) {
			commands = append(commands, name)
		}
	}
	slices.Sort(commands)
	for _, name := range commands {
		now := (*main.CliTreeFolds)[name]
		if now == "" {
			now = strings.Join(cliTreeGuideCommandPaths(name), "`, `gregale ")
		}
		fmt.Fprintf(&b, "| `gregale %s` | `gregale %s` |\n", name, now)
	}
	b.WriteString("\n## Earlier paths\n\n| Earlier | Now |\n|---|---|\n")
	var rows []string
	for _, alias := range *main.CliTreeAliases {
		old := strings.Fields(alias.Old)
		if alias.New == alias.Old || strings.HasPrefix(alias.New, "admin ") || len(old) < 2 || !released[old[0]] {
			continue
		}
		rows = append(rows, fmt.Sprintf("| `gregale %s` | `gregale %s` |\n", alias.Old, alias.New))
	}
	for typed, now := range *main.CliTreeFolds {
		if strings.Contains(typed, " ") && released[strings.Fields(typed)[0]] {
			rows = append(rows, fmt.Sprintf("| `gregale %s` | `gregale %s` |\n", typed, now))
		}
	}
	slices.Sort(rows)
	rows = slices.Compact(rows)
	for _, row := range rows {
		b.WriteString(row)
	}
	b.WriteString("\n## Earlier flags\n\n| Earlier | Now |\n|---|---|\n")
	for _, synonym := range *main.CliTreeFlagSynonyms {
		fmt.Fprintf(&b, "| `--%s` | `--%s` |\n", strings.Join(synonym.Old, "`, `--"), synonym.Canonical)
	}
	b.WriteString("\nA command keeps its earlier flag where the new spelling could mean something else, such as `--from` naming a source environment rather than a time.\n\n")
	return b.String()
}

// cliTreeGuideCommandPaths returns the customer paths that run a top-level
// command as a whole, or else the nouns holding its verbs.
func cliTreeGuideCommandPaths(name string) []string {
	var paths []string
	for _, alias := range *main.CliTreeAliases {
		if alias.Old == name && !strings.HasPrefix(alias.New, "admin ") && !slices.Contains(paths, alias.New) {
			paths = append(paths, alias.New)
		}
	}
	if len(paths) == 0 {
		return main.CliTreeNounsHolding(name)
	}
	return paths
}

// docs/cli-command-tree.md maps every earlier name to the tree's
// (ADR-20261026145832 step 5). Rewrite its tables with
// GREGALE_UPDATE_CLI_TREE_GUIDE=1.
func TestCliTreeMigrationGuideFresh(t *testing.T) {
	page, err := os.ReadFile(cliTreeGuidePath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(page)
	start := strings.Index(text, cliTreeGuideBegin)
	end := strings.Index(text, cliTreeGuideEnd)
	if start < 0 || end < start {
		t.Fatalf("%s lacks its generated section markers", cliTreeGuidePath)
	}
	want := text[:start+len(cliTreeGuideBegin)] + renderCliTreeGuide() + text[end:]
	if want == text {
		return
	}
	if os.Getenv("GREGALE_UPDATE_CLI_TREE_GUIDE") == "1" {
		if err := os.WriteFile(cliTreeGuidePath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Errorf("%s is stale; rewrite it with GREGALE_UPDATE_CLI_TREE_GUIDE=1", cliTreeGuidePath)
}
