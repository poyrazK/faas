package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// cliTreeDocs are the customer-facing pages that spell commands besides the
// docs site (docs/customer-pages.json): the starter templates and their
// examples, the AGENTS.md template and the Claude Code plugin. Specs, ADRs, ops docs and
// runbooks are records of their time and keep their spelling.
var cliTreeDocs = []string{
	"templates/*.tmpl",
	"templates/*/README.md",
	"../../examples/*/README.md",
	"../../packaging/claude-plugin/README.md",
	"../../packaging/claude-plugin/skills/*/SKILL.md",
}

// cliTreeDocsGenerated are rendered from the manifest, not edited.
var cliTreeDocsGenerated = []string{"../../docs/cli-reference.md", "../../docs/cli-command-tree.md"}

// customerDocPages returns the docs site's page sources.
func customerDocPages(t *testing.T, dir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "../../docs/customer-pages.json"))
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Routes []struct {
			Source string `json:"source"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	var pages []string
	for _, route := range catalog.Routes {
		if page := filepath.Join(dir, "../..", route.Source); route.Source != "" && !slices.Contains(pages, page) {
			pages = append(pages, page)
		}
	}
	return pages
}

// cliTreeCommandWords matches `gregale`, the lowercase words after it (the
// command path and the positional values that may follow it) and the flag
// after those, which can change what the words mean (`app api --ram 512`).
var cliTreeCommandWords = regexp.MustCompile(`\bgregale((?: [a-z0-9][a-z0-9._/-]*)+)( --?[a-z][a-z0-9-]*)?`)

// cliTreeCommandFlags matches the flags of one command, up to the next
// shell operator.
var cliTreeCommandFlags = regexp.MustCompile(`^[^|;&)]*`)

// cliTreeFlagToken matches a long flag and what ends it.
var cliTreeFlagToken = regexp.MustCompile(`--([a-z][a-z0-9-]*)([= ]|$)`)

// spellDocLine rewrites the commands inside the code spans of line, or the
// whole line in a code block, in the tree's names: the command path, and
// each flag that has a canonical spelling (ADR-20261026145832 step 4). A
// spelling that would move the flag after the words is left alone: the
// rest of the line follows that flag.
func spellDocLine(line string, inBlock bool) string {
	if inBlock {
		return spellDocCode(line)
	}
	parts := strings.Split(line, "`")
	for i := 1; i < len(parts); i += 2 {
		parts[i] = spellDocCode(parts[i])
	}
	return strings.Join(parts, "`")
}

// spellDocCode respells every gregale command in a piece of code.
func spellDocCode(code string) string {
	var out strings.Builder
	for {
		loc := cliTreeCommandWords.FindStringSubmatchIndex(code)
		if loc == nil {
			out.WriteString(code)
			return out.String()
		}
		out.WriteString(code[:loc[0]])
		words := strings.Fields(code[loc[2]:loc[3]])
		head := "gregale " + strings.Join(words, " ")
		lookup := words
		if loc[4] >= 0 {
			lookup = append(append([]string{}, words...), strings.TrimSpace(code[loc[4]:loc[5]]))
		}
		if spelled, ok := main.CliTreeSpelling(lookup); ok {
			if loc[4] < 0 {
				head = "gregale " + strings.Join(spelled, " ")
			} else if spelled[len(spelled)-1] == lookup[len(lookup)-1] {
				head = "gregale " + strings.Join(spelled[:len(spelled)-1], " ")
			}
		}
		rest := code[loc[3]:]
		flags := cliTreeCommandFlags.FindString(rest)
		flags = cliTreeFlagToken.ReplaceAllStringFunc(flags, func(token string) string {
			name := cliTreeFlagToken.FindStringSubmatch(token)
			if canonical, ok := main.CliTreeCanonicalFlag(words, name[1]); ok {
				return "--" + canonical + name[2]
			}
			return token
		})
		out.WriteString(head + flags)
		code = rest[len(cliTreeCommandFlags.FindString(rest)):]
	}
}

// spellDoc rewrites a page's commands in the tree's names.
func spellDoc(text string) string {
	lines := strings.Split(text, "\n")
	inBlock := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inBlock = !inBlock
			continue
		}
		lines[i] = spellDocLine(line, inBlock)
	}
	return strings.Join(lines, "\n")
}

// Docs, the AGENTS.md template and the Claude Code plugin spell commands in
// the command tree's names (ADR-20261026145832 step 5). Each rewrite is a
// tree path that runs exactly today's command. Rewrite the pages with
// GREGALE_UPDATE_CLI_TREE_DOCS=1.
func TestDocsSpellCommandsInTheTree(t *testing.T) {
	enableEveryCLIFeature(t)
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir()) // no linked checkout may resolve an app
	update := os.Getenv("GREGALE_UPDATE_CLI_TREE_DOCS") == "1"
	stale := 0
	groups := [][]string{customerDocPages(t, dir)}
	for _, pattern := range cliTreeDocs {
		paths, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, paths)
	}
	for _, paths := range groups {
		for _, path := range paths {
			if slices.ContainsFunc(cliTreeDocsGenerated, func(generated string) bool { return path == filepath.Join(dir, generated) }) {
				continue
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			after := spellDoc(string(before))
			if after == string(before) {
				continue
			}
			stale++
			if update {
				if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			t.Errorf("%s spells commands by today's names; rewrite with GREGALE_UPDATE_CLI_TREE_DOCS=1", path)
		}
	}
	if update {
		t.Logf("rewrote %d pages", stale)
	}
}
