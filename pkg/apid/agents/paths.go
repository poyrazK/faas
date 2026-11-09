package agents

import "strings"

// IsAgentPath matches /v1/agents, /v1/agent-runs, /v1/agent-callbacks,
// /v1/agent-tools, /v1/sandboxes, /v1/sandbox-snapshots, /v1/sandbox-templates
// and everything below
// them: the routes of the
// agents capability (ADR-20261027090010, ADR-20261030125501 §3 and §4,
// ADR-20261027090013).
func IsAgentPath(path string) bool {
	for _, root := range []string{"/v1/agents", "/v1/agent-runs", "/v1/agent-callbacks", ToolsetPath, "/v1/sandboxes", "/v1/sandbox-snapshots", "/v1/sandbox-templates"} {
		if rest, ok := strings.CutPrefix(path, root); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
			return true
		}
	}
	return false
}
