package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// The full write catalog is larger than clients that cap tools at 128 accept.
// Above the cap the server lists core plus gregale_find_tools, and a search
// adds its matches to the listing.
func TestFindToolsAddsDeferredTools(t *testing.T) {
	backend := httptest.NewServer(http.NotFoundHandler())
	defer backend.Close()
	cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, Toolsets: []string{ToolsetAll}, MaxListedTools: 40}, "2026-07-28")
	names := func() []string {
		catalog, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		return toolNamesOf(catalog.Tools)
	}
	listed := names()
	if len(listed) > 40 || !slices.Contains(listed, findToolsName) || !slices.Contains(listed, "gregale_plan_promotion") || slices.Contains(listed, "gregale_get_postgres_schema") {
		t.Fatalf("deferred catalog=%v", listed)
	}
	out := callTool(t, ctx, cs, findToolsName, map[string]any{"query": "postgres schema", "limit": 3}, false)
	var found []foundTool
	if err := json.Unmarshal(out["tools"], &found); err != nil || len(found) == 0 || found[0].Name != "gregale_get_postgres_schema" {
		t.Fatalf("found=%s", out)
	}
	if !slices.Contains(names(), "gregale_get_postgres_schema") {
		t.Fatal("found tool is not listed")
	}
	// A found tool runs through the normal handler (here the backend 404s).
	callTool(t, ctx, cs, "gregale_get_postgres_schema", map[string]any{"app": "alpha", "database": "main"}, true)

	// Searches past the cap drop the oldest found tools from the listing.
	for _, q := range []string{"cron", "domain", "bucket", "rollout", "flag", "budget", "experiment", "schema", "worker", "trace"} {
		callTool(t, ctx, cs, findToolsName, map[string]any{"query": q, "limit": 20}, false)
	}
	if got := names(); len(got) > 40 {
		t.Fatalf("listing grew past the cap: %d", len(got))
	}
	out = callTool(t, ctx, cs, findToolsName, map[string]any{"query": "zzzz-nothing"}, false)
	if !strings.Contains(string(out["note"]), "No unlisted tool") {
		t.Fatalf("empty search=%s", out)
	}
}

func TestSmallCatalogListsEverything(t *testing.T) {
	backend := httptest.NewServer(http.NotFoundHandler())
	defer backend.Close()
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Token: testToken, MaxListedTools: DefaultMaxListedTools}, "2026-07-28")
	catalog, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if names := toolNamesOf(catalog.Tools); slices.Contains(names, findToolsName) || len(names) != 29 {
		t.Fatalf("core catalog=%v", names)
	}
}
