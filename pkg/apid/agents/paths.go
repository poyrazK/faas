package agents

import "strings"

// IsAgentPath matches /v1/agents, /v1/agent-runs, /v1/agent-callbacks and
// everything below them: the routes of the agents capability
// (ADR-20261027090010, ADR-20261030125501 §3).
func IsAgentPath(path string) bool {
	for _, root := range []string{"/v1/agents", "/v1/agent-runs", "/v1/agent-callbacks"} {
		if rest, ok := strings.CutPrefix(path, root); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
			return true
		}
	}
	return false
}
