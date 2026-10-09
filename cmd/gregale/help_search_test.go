package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestHelpSearchTaskRanking(t *testing.T) {
	for _, tc := range []struct{ query, command, example string }{
		{"restart after changing secrets", "gregale app <slug> restart", "gregale app my-api restart --fresh --wait"},
		{"compare environments", "gregale projects environments diff", "gregale projects environments diff my-project --from staging --to production"},
		{"reduce costs", "gregale app <slug> costs", "gregale app my-api costs"},
		{"REDUCE spending", "gregale app <slug> costs", "gregale app my-api costs"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			results := searchHelpCommands(tc.query, customerCliCommands())
			if len(results) == 0 || results[0].Command != tc.command {
				t.Fatalf("first result for %q: %+v", tc.query, results)
			}
			if !containsHelpToken(results[0].Examples, tc.example) {
				t.Errorf("missing example %q: %+v", tc.example, results[0])
			}
			if !reflect.DeepEqual(results, searchHelpCommands(tc.query, customerCliCommands())) {
				t.Fatal("search order is not deterministic")
			}
		})
	}
}

func TestHelpSearchUsesManifestAliasesFlagsAndNestedSyntax(t *testing.T) {
	commands := []cliCommand{{Name: "apps", DocSlug: "apps", Subcommands: []cliSub{{
		Name: "tcp", Positionals: []string{"<slug>"}, SubcommandsAfterPositionals: true,
		Subcommands: []cliSub{{Name: "add", Aliases: []string{"attach"}, Short: "Add listener", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "port", Short: "socket port", Value: "N", Req: true}}}},
	}}}}
	for _, query := range []string{"attach", "socket port"} {
		results := searchHelpCommands(query, commands)
		if len(results) != 1 || results[0].Command != "gregale apps tcp <slug> add" || results[0].Usage != "gregale apps tcp <slug> add --port <N> <name>" {
			t.Fatalf("search %q lost nested syntax: %+v", query, results)
		}
	}
}

func TestHelpSearchLocalDispatchAndJSON(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	out, stderr, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{
		{"help", "--search", "restart after changing secrets"},
		{"help", "--search=restart after changing secrets"},
		{"--profile", "missing-search-profile", "help", "--search", "restart after changing secrets"},
	} {
		out.Reset()
		if code := run(args); code != 0 || !strings.Contains(out.String(), "Example: gregale app my-api restart --fresh --wait") {
			t.Fatalf("run(%v): code=%d out=%s err=%s", args, code, out.String(), stderr())
		}
	}
	for _, query := range []string{"reduce costs", "no-such-customer-task-xyz"} {
		out.Reset()
		if code := run([]string{"help", "--search", query, "--json"}); code != 0 {
			t.Fatalf("JSON search code=%d stderr=%s", code, stderr())
		}
		var response struct {
			Query   string             `json:"query"`
			Results []helpSearchResult `json:"results"`
		}
		if err := json.Unmarshal(out.Bytes(), &response); err != nil || response.Query != query || response.Results == nil {
			t.Fatalf("invalid JSON response: %s, %v", out.String(), err)
		}
		if query == "reduce costs" && (len(response.Results) == 0 || response.Results[0].Command != "gregale app <slug> costs") {
			t.Fatalf("unexpected JSON results: %+v", response.Results)
		}
		if strings.Contains(out.String(), `"score"`) || strings.Contains(out.String(), `"keywords"`) {
			t.Fatal("search internals leaked into JSON")
		}
	}
}

func TestHelpSearchAudienceAndNoMatches(t *testing.T) {
	resetJSONOut(t)
	out, _, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{{"help", "--all", "--search", "manual rollout recovery"}, {"help", "--search=manual rollout recovery", "--all"}} {
		out.Reset()
		if code := run(args); code != 0 || !strings.Contains(out.String(), "gregale rollouts") {
			t.Fatalf("advanced search: %d %s", code, out.String())
		}
	}
	out.Reset()
	if code := run([]string{"help", "--search", "manual rollout recovery"}); code != 0 || strings.Contains(out.String(), "gregale rollouts") {
		t.Fatalf("customer search exposed operator results: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"help", "--search", "xyznonexistenttask"}); code != 0 || !strings.Contains(out.String(), "No commands match") {
		t.Fatalf("no-match result: %d %s", code, out.String())
	}
	results := searchHelpCommands("app", cliCommands)
	if len(results) != helpSearchLimit {
		t.Fatalf("broad search not bounded: %d", len(results))
	}
}

func TestHelpSearchRejectsInvalidQueries(t *testing.T) {
	resetJSONOut(t)
	_, _, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{
		{"--search"}, {"--search", "--all"}, {"--search="}, {"--search", "   "},
		{"--search", "???"}, {"--search", "how do I"}, {"--search", strings.Repeat("a", 257)},
		{"--search", "reduce", "costs"}, {"--search", "cost", "--unknown"},
		{"--search", "cost", "--search", "logs"}, {"--search", "cost", "--all", "--all"},
	} {
		if code := run(append([]string{"help"}, args...)); code != 1 {
			t.Errorf("run help %v returned %d, want 1", args, code)
		}
	}
}
