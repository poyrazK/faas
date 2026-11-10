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
// Includes structured output envelopes and the five read-only saved-plan and
// action-recovery tools, and gregale_wait_for_approval, which an agent key needs
// the moment a request is answered approval_required (ADR-20261104140058).
// The fully consented hosted catalog is about 67 KB.
const coreCatalogBudget = 68 << 10

func TestReadOnlyCoreCatalogMatchesStdioContract(t *testing.T) {
	backend := httptest.NewServer(http.NotFoundHandler())
	defer backend.Close()
	noCLI := func(context.Context, []string, []byte) (LocalCLIResult, error) { return LocalCLIResult{}, nil }
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Token: testToken, LocalCLI: noCLI}, "2026-07-28")
	catalog, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Tools) != 24 {
		t.Fatalf("read-only core count=%d names=%v", len(catalog.Tools), toolNamesOf(catalog.Tools))
	}
	for _, tool := range catalog.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("read-only core exposes mutation %s", tool.Name)
		}
	}
	for _, name := range []string{"gregale_get_saved_plan", "gregale_check_plan", "gregale_list_actions", "gregale_get_action", "gregale_get_action_outcome"} {
		if !slices.ContainsFunc(catalog.Tools, func(tool *mcp.Tool) bool { return tool.Name == name }) {
			t.Errorf("default read-only core is missing %s", name)
		}
	}
}

func TestCoreRecoveryToolsOnLocalAndHostedConnections(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		catalog := connectionCatalog(t, []string{ToolsetCore}, hosted)
		for _, name := range []string{"gregale_get_saved_plan", "gregale_check_plan", "gregale_list_actions", "gregale_get_action", "gregale_get_action_outcome"} {
			i := slices.IndexFunc(catalog, func(tool *mcp.Tool) bool { return tool.Name == name })
			if i < 0 || catalog[i].Annotations == nil || !catalog[i].Annotations.ReadOnlyHint {
				t.Errorf("hosted=%v: %s is missing or not read-only", hosted, name)
			}
		}
	}
}

func TestProvisionIncludesReadOnlyActionHistory(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		catalog := connectionCatalog(t, []string{ToolsetProvision}, hosted)
		for _, name := range []string{"gregale_list_actions", "gregale_get_action"} {
			i := slices.IndexFunc(catalog, func(tool *mcp.Tool) bool { return tool.Name == name })
			if i < 0 || catalog[i].Annotations == nil || !catalog[i].Annotations.ReadOnlyHint {
				t.Errorf("hosted=%v: %s is missing or not read-only", hosted, name)
			}
		}
	}
}

// toolsetCatalog lists the tools of a local connection and of a fully
// consented hosted grant (ADR-20261026195952) together: some tools exist on
// only one of them.
func toolsetCatalog(t *testing.T, toolsets []string) []*mcp.Tool {
	t.Helper()
	if toolsets == nil {
		toolsets = []string{ToolsetAll}
	}
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
		opts.Capabilities, opts.uploads = []string{api.ScopeSecretsRequest, api.ScopeStorageManage, api.ScopeManagedPostgresManage, api.ScopeEnvWrite, api.ScopeUsageRead}, store
	}
	cs, ctx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
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
	// Request-scoped hosted catalogs filter the SDK's page. Fail before the
	// SDK default page becomes too small to produce a complete focused list.
	if len(toolsetOf) >= mcp.DefaultPageSize {
		t.Fatal("hosted catalog selection needs pagination")
	}
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
	for _, selected := range [][]string{{ToolsetCore}, {ToolsetRelease}, {ToolsetDiagnose}, {ToolsetProvision}, {ToolsetOperate}, {ToolsetCore, ToolsetDiagnose}} {
		var want []string
		for name := range toolsetOf {
			if slices.ContainsFunc(selected, func(set string) bool { return toolsetIncludes(name, set) }) {
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
		all, err := json.Marshal(connectionCatalog(t, []string{ToolsetAll}, hosted))
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
		"":                     {ToolsetCore},
		"all":                  {ToolsetAll},
		"core":                 {"core"},
		" diagnose , core ":    {"core", "diagnose"},
		"core,core,release":    {"core", "release"},
		"provision,core":       {"core", "provision"},
		"core,all":             {ToolsetAll},
		"release,diagnose,all": {ToolsetAll},
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
