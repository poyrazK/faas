// adr: 1237
package mcpmanagement

import "testing"

// OpenAI's plugin review requires an explicit openWorldHint on every tool:
// true only for tools that reach public or open-ended destinations.
func TestEveryToolHasAnExplicitOpenWorldHint(t *testing.T) {
	listed := map[string]bool{}
	for _, tool := range toolsetCatalog(t, nil) {
		listed[tool.Name] = true
		if tool.Annotations == nil || tool.Annotations.OpenWorldHint == nil {
			t.Errorf("%s: missing openWorldHint", tool.Name)
			continue
		}
		if got, want := *tool.Annotations.OpenWorldHint, openWorldTools[tool.Name]; got != want {
			t.Errorf("%s: openWorldHint = %v, want %v", tool.Name, got, want)
		}
	}
	for name := range openWorldTools {
		if !listed[name] {
			t.Errorf("openWorldTools names %s, which no catalog lists", name)
		}
	}
}

func TestToolOpenWorld(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"gregale_test_notification_channel", true},
		{"gregale_check_domain", true},
		{"gregale_invoke_tool", true},
		{"gregale_list_apps", false},
		{"gregale_search_docs", false},
		{"gregale_verify_application_response", false},
		{"gregale_plan_source_deploy", false},
		{findToolsName, false},
	} {
		if got := toolOpenWorld(tc.name); got == nil || *got != tc.want {
			t.Errorf("toolOpenWorld(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
