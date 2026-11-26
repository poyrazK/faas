// adr: 1237
package mcpmanagement

import (
	"strings"
	"testing"
)

// The Claude Connectors Directory rejects a connector whose tools lack a
// title or a readOnlyHint/destructiveHint annotation. Its submission check
// reads annotations.title, not the top-level title, so both carry it.
func TestEveryToolHasATitleAndSafetyHints(t *testing.T) {
	owners := map[string]string{}
	for _, tool := range toolsetCatalog(t, nil) {
		if tool.Title == "" || tool.Title == tool.Name || strings.Contains(tool.Title, "_") {
			t.Errorf("%s: title %q is not human-readable", tool.Name, tool.Title)
		}
		if other, ok := owners[tool.Title]; ok {
			t.Errorf("%s and %s share the title %q", other, tool.Name, tool.Title)
		}
		owners[tool.Title] = tool.Name
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil {
			t.Errorf("%s: missing readOnlyHint/destructiveHint annotations", tool.Name)
			continue
		}
		if tool.Annotations.Title != tool.Title {
			t.Errorf("%s: annotations.title %q, want the tool title %q", tool.Name, tool.Annotations.Title, tool.Title)
		}
	}
}

func TestToolTitle(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"gregale_list_apps", "List apps"},
		{"gregale_get_slo_status", "Get SLO status"},
		{"gregale_generate_tools_from_openapi", "Generate tools from OpenAPI"},
		{"gregale_get_postgres_schema", "Get Postgres schema"},
		{"gregale_list_api_consumers", "List API consumers"},
		{"gregale_context", "Show connection context"},
		{findToolsName, "Find more Gregale tools"},
	} {
		if got := toolTitle(tc.name); got != tc.want {
			t.Errorf("toolTitle(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
