package agents

import "strings"

// IsAgentPath matches /v1/agents, /v1/agent-runs and everything below them:
// the routes of the agents capability (ADR-20261027090010).
func IsAgentPath(path string) bool {
	for _, root := range []string{"/v1/agents", "/v1/agent-runs"} {
		if rest, ok := strings.CutPrefix(path, root); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
			return true
		}
	}
	return false
}
