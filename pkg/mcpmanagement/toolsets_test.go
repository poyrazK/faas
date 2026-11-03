// adr: 20261026152009
package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/onebox-faas/faas/pkg/api"
)

// coreCatalogBudget bounds the tools/list bytes an agent loads with --tools
// core. Raise it deliberately: every byte is paid on every turn.
const coreCatalogBudget = 16 << 10

// toolsetCatalog lists the tools of a local connection and of a fully
// consented hosted grant (ADR-20261026195952) together: some tools exist on
// only one of them.
func toolsetCatalog(t *testing.T, toolsets []string) []*mcp.Tool {
	t.Helper()
	tools := connectionCatalog(t, toolsets, false)
	for _, tool := range connectionCatalog(t, toolsets, true) {
		if !slices.ContainsFunc(tools, func(listed *mcp.Tool) bool { return listed.Name == tool.Name }) {
			tools = append(tools, tool)
		}
	}
	return tools
}

func connectionCatalog(t *testing.T, toolsets []string, hosted bool) []*mcp.Tool {
	t.Helper()
	backend := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(backend.Close)
	noCLI := func(context.Context, []string, []byte) (LocalCLIResult, error) { return LocalCLIResult{}, nil }
	opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, LocalCLI: noCLI, Toolsets: toolsets}
	if hosted {
		store, err := newUploadStore(filepath.Join(t.TempDir(), "uploads"), "https://api.gregale.test/mcp")
		if err != nil {
			t.Fatal(err)
		}
		opts.Environments = &EnvironmentConsent{DefaultWrite: true, Named: map[string]EnvironmentGrant{}}
		opts.Capabilities, opts.uploads = []string{api.ScopeSecretsRequest, api.ScopeStorageManage, api.ScopeManagedPostgresManage}, store
	}
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	var tools []*mcp.Tool
	params := &mcp.ListToolsParams{}
	for {
		page, err := cs.ListTools(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		tools = append(tools, page.Tools...)
		if page.NextCursor == "" {
			return tools
		}
		params.Cursor = page.NextCursor
	}
}

func toolNamesOf(tools []*mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func TestEveryToolHasAToolset(t *testing.T) {
	listed := toolNamesOf(toolsetCatalog(t, nil))
	for _, name := range listed {
		if _, ok := toolsetOf[name]; !ok {
			t.Errorf("%s has no toolset; add it to toolsetOf", name)
		}
	}
	for name := range toolsetOf {
		if !slices.Contains(listed, name) {
			t.Errorf("toolsetOf names %s, which no connection registers", name)
		}
	}
}

func TestToolsetsListOnlySelectedTools(t *testing.T) {
	for _, selected := range [][]string{{ToolsetCore}, {ToolsetRelease}, {ToolsetDiagnose}, {ToolsetCore, ToolsetDiagnose}} {
		var want []string
		for name, set := range toolsetOf {
			if slices.Contains(selected, set) {
				want = append(want, name)
			}
		}
		sort.Strings(want)
		if got := toolNamesOf(toolsetCatalog(t, selected)); !slices.Equal(got, want) {
			t.Errorf("toolsets %v listed %v, want %v", selected, got, want)
		}
	}
}

func TestCoreCatalogStaysSmall(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		core, err := json.Marshal(connectionCatalog(t, []string{ToolsetCore}, hosted))
		if err != nil {
			t.Fatal(err)
		}
		all, err := json.Marshal(connectionCatalog(t, nil, hosted))
		if err != nil {
			t.Fatal(err)
		}
		if len(core) > coreCatalogBudget {
			t.Errorf("hosted=%v: core catalog is %d bytes, over the %d-byte budget", hosted, len(core), coreCatalogBudget)
		}
		t.Logf("hosted=%v catalog bytes: core %d, all %d", hosted, len(core), len(all))
	}
}

func TestParseToolsets(t *testing.T) {
	for spec, want := range map[string][]string{
		"":                     nil,
		"all":                  nil,
		"core":                 {"core"},
		" diagnose , core ":    {"core", "diagnose"},
		"core,core,release":    {"core", "release"},
		"provision,core":       {"core", "provision"},
		"core,all":             nil,
		"release,diagnose,all": nil,
	} {
		got, err := ParseToolsets(spec)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("ParseToolsets(%q) = %v, %v; want %v", spec, got, err, want)
		}
	}
	for _, spec := range []string{"cor", "core,", "core,deploy", "ALL"} {
		if _, err := ParseToolsets(spec); err == nil {
			t.Errorf("ParseToolsets(%q) accepted an unknown toolset", spec)
		}
	}
}

func TestUnlistedToolsCannotBeCalled(t *testing.T) {
	backend := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(backend.Close)
	cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, Token: testToken, Toolsets: []string{ToolsetCore}}, "2026-07-28")
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_get_incident_bundle", Arguments: map[string]any{"app": "alpha"}})
	if err == nil && (result == nil || !result.IsError) {
		t.Fatal("a tool outside the selected toolsets was callable")
	}
}
