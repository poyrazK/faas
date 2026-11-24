// adr: 20261123180000

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/mcpmanagement"
)

// `gregale guide "<text>"` answers a task in words with the commands that
// do it and the docs sections that explain it (ADR-20261123180000). Agents
// spent 38% of their gregale commands on help lookups in the 2026-11-23
// eval; one search replaces a `help --all | grep` loop. Commands come from
// the manifest and the guide pages, as this account's capability view shows
// them; docs come from the index shipped in this binary merged with the
// live site.

const (
	guideSearchCommands = 3
	guideSearchDocs     = 3
	guideSearchTimeout  = 5 * time.Second
	guideSnippetMax     = 160

	// Entry weights: a guide step is written for the task an agent names; a
	// top-level command or Common verb is what most tasks need; a deeper
	// verb or a flag rarely is.
	guideWeightStep       = 2.0
	guideWeightCommand    = 1.4
	guideWeightVerb       = 1.0
	guideWeightCommonFlag = 0.8
	guideWeightFlag       = 0.35
)

// guideSynonyms adds the CLI's words to the words agents use for the same
// thing. Each entry comes from an eval transcript or a guide step;
// TestGuideSearchAnswersAgentQuestions keeps them honest.
var guideSynonyms = map[string]string{
	"public":             "anonymous no-require-authn",
	"private":            "require-authn",
	"protect":            "bearer token require-authn",
	"require an api key": "bearer token require-authn",
	"env var":            "env",
	"environment":        "env",
	"variable":           "env",
	"secret":             "secrets env",
	"migration":          "migrate",
	"migrations":         "migrate",
	"roll back":          "rollback",
	"revert":             "rollback",
	"outbound":           "egress",
	"logs":               "logs",
	"cache":              "redis",
	"redis":              "redis",
}

// expandGuideQuery appends the CLI's words for the query's words.
func expandGuideQuery(query string) string {
	lower := " " + strings.ToLower(query) + " "
	extra := []string{}
	for phrase, words := range guideSynonyms {
		if strings.Contains(lower, " "+phrase+" ") {
			extra = append(extra, words)
		}
	}
	if len(extra) == 0 {
		return query
	}
	sort.Strings(extra)
	return query + " " + strings.Join(extra, " ")
}

type guideCommandHit struct {
	Command string `json:"command"`
	Summary string `json:"summary"`
	Example string `json:"example,omitempty"`
}

// guideDocsHit is one docs section. A preview page has no URL, since the
// site does not serve it; Page names it for the MCP gregale_get_docs_page.
type guideDocsHit struct {
	Title   string `json:"title"`
	Page    string `json:"page"`
	URL     string `json:"url,omitempty"`
	Preview bool   `json:"preview,omitempty"`
	Snippet string `json:"snippet"`
}

type guideSearchResult struct {
	Query    string            `json:"query"`
	Commands []guideCommandHit `json:"commands"`
	Docs     []guideDocsHit    `json:"docs"`
	Next     string            `json:"next,omitempty"`
}

// guideCommandEntries are the searchable commands: every discoverable
// command and verb, then every guide step, which carries the task words a
// person would use.
func guideCommandEntries(ctx context.Context) ([]mcpmanagement.SearchEntry, map[string]guideCommandHit) {
	var entries []mcpmanagement.SearchEntry
	hits := map[string]guideCommandHit{}
	add := func(hit guideCommandHit, body string, weight float64) {
		id := fmt.Sprintf("c%d", len(entries))
		hits[id] = hit
		entries = append(entries, mcpmanagement.SearchEntry{ID: id, Title: hit.Command, Summary: hit.Summary, Body: body, Weight: weight})
	}
	// A flag is often the answer ("make it public" is --no-require-authn), so
	// each flag is an entry of its own, shown on its command. There are
	// thousands, with terse descriptions, so only Common flags count fully.
	addFlags := func(command, context string, flags []cliFlag) {
		for _, f := range flags {
			if f.Short == "" || f.Unreleased {
				continue
			}
			weight := guideWeightFlag
			if f.Common {
				weight = guideWeightCommonFlag
			}
			add(guideCommandHit{Command: command + " " + mdFlagLabel(f), Summary: f.Short}, f.Name+"\n"+f.Short+"\n"+context, weight)
		}
	}
	// The tree's names, as help and completion list them: today's table
	// without the commands the tree renamed left domain, webhook, key, flag,
	// alert, project and more out of every search (prod hunt r33).
	for _, c := range cliTreeManifest(discoverableCliCommandsContext(ctx)) {
		if len(c.Subcommands) == 0 {
			command := strings.Join(append([]string{"gregale", c.Name}, c.Positionals...), " ")
			add(guideCommandHit{Command: command, Summary: c.Short, Example: first(c.Examples)},
				strings.Join(append(append([]string{c.Name, c.Short}, c.HelpNotes...), c.Examples...), "\n"), guideWeightCommand)
			addFlags(command, c.Short, c.Flags)
			continue
		}
		addFlags("gregale "+c.Name, c.Short, c.Flags)
		var walk func(names []string, subs []cliSub, depth int)
		walk = func(names []string, subs []cliSub, depth int) {
			for _, s := range subs {
				path := append(append([]string{}, names...), s.Name)
				command := mdSubSynopsis(c, path, s.Positionals, nil)
				weight := guideWeightVerb
				if s.Common && depth == 0 {
					weight = guideWeightCommand
				}
				add(guideCommandHit{Command: command, Summary: s.Short, Example: first(s.Examples)},
					strings.Join(append(append([]string{c.Name + " " + strings.Join(path, " "), s.Short, c.Short}, s.Notes...), s.Examples...), "\n"), weight)
				addFlags(command, s.Short, s.Flags)
				walk(path, s.Subcommands, depth+1)
			}
		}
		walk(nil, c.Subcommands, 0)
	}
	for _, page := range guidePages {
		for _, step := range page.Steps {
			if step.Command != "" {
				add(guideCommandHit{Command: step.Command, Summary: step.Do}, step.Do+"\n"+page.Title+"\n"+page.Summary, guideWeightStep)
			}
		}
	}
	return entries, hits
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// guideDocsCacheDir keeps the live site's agent files; empty when the
// user has no cache directory, which searches the shipped index alone.
func guideDocsCacheDir() string {
	if os.Getenv("GREGALE_DOCS_OFFLINE") == "1" {
		return ""
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "gregale", "docs")
}

// searchGuideCommands ranks the commands for a question; the network is
// read only for the capability view, bounded by ctx.
func searchGuideCommands(ctx context.Context, query string) []guideCommandHit {
	hits := []guideCommandHit{}
	entries, byID := guideCommandEntries(ctx)
	seen := map[string]bool{}
	for _, id := range mcpmanagement.NewSearchIndex(entries).Search(expandGuideQuery(query), guideSearchCommands*3) {
		hit := byID[id]
		key := strings.Join(strings.Fields(hit.Command), " ")
		if seen[key] {
			continue
		}
		seen[key] = true
		hits = append(hits, hit)
		if len(hits) == guideSearchCommands {
			break
		}
	}
	return hits
}

func searchGuide(ctx context.Context, query string) guideSearchResult {
	result := guideSearchResult{Query: query, Commands: searchGuideCommands(ctx, query), Docs: []guideDocsHit{}}
	view := discoveryCapabilityViewContext(ctx)
	docs, err := mcpmanagement.SearchLocalDocs(ctx, query, guideSearchDocs, mcpmanagement.LocalDocsOptions{
		CacheDir: guideDocsCacheDir(),
		Visible: func(capabilities []string) bool {
			for _, id := range capabilities {
				if !view.hides(id) {
					return true
				}
			}
			return false
		},
	})
	if err == nil {
		for _, d := range docs {
			title := d.Title
			if d.Heading != "" {
				title += " › " + d.Heading
			}
			result.Docs = append(result.Docs, guideDocsHit{Title: title, Page: d.Page, URL: d.URL, Preview: d.Preview, Snippet: clip(d.Snippet, guideSnippetMax)})
		}
	}
	switch {
	case len(result.Commands) == 0 && len(result.Docs) == 0:
		result.Next = "Nothing matched; try other words, or run `gregale guide` for the task list."
	case len(result.Commands) > 0:
		result.Next = "Run `gregale <command> --help` for its flags."
	}
	return result
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndexByte(s[:n], ' ')
	if cut < n/2 {
		// No word break: cut at a character boundary, never inside one.
		cut = n
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
	}
	return strings.TrimRight(s[:cut], " ,.;:") + "…"
}

func renderGuideSearch(w io.Writer, r guideSearchResult) {
	if len(r.Commands) > 0 {
		_, _ = fmt.Fprintln(w, "Commands:")
		for _, c := range r.Commands {
			_, _ = fmt.Fprintf(w, "  %s\n      %s\n", c.Command, c.Summary)
			if c.Example != "" && c.Example != c.Command {
				_, _ = fmt.Fprintf(w, "      e.g. %s\n", c.Example)
			}
		}
	}
	if len(r.Docs) > 0 {
		if len(r.Commands) > 0 {
			_, _ = fmt.Fprintln(w)
		}
		_, _ = fmt.Fprintln(w, "Docs:")
		for _, d := range r.Docs {
			where := d.URL
			if where == "" {
				where = "preview docs, not on the site yet: read with the MCP tool gregale_get_docs_page page=" + d.Page
			}
			_, _ = fmt.Fprintf(w, "  %s\n      %s\n      %s\n", d.Title, where, d.Snippet)
		}
	}
	if r.Next != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", r.Next)
	}
}

// cmdGuideSearch runs a free-text guide search.
func cmdGuideSearch(query string) int {
	ctx, cancel := context.WithTimeout(context.Background(), guideSearchTimeout)
	defer cancel()
	result := searchGuide(ctx, query)
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderGuideSearch(osStdout, result)
	return 0
}
