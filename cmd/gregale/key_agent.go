package main

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// agentKeyDefaultScopes is what `key create --agent` grants without --scopes:
// inspect and deploy, the agents guide's recommended key, never admin.
var agentKeyDefaultScopes = []string{api.ScopeAgent, "apps:read", "deploy:write"}

// agentKeyScopes adds the agent scope for `key create --agent`, so the key's
// risky requests ask a person and its always-on cost is held to an agent
// budget (cost-control review 2026-11-19, N11). Approvals and agent budgets
// apply only to keys with the agent scope; a plain scoped key a coding agent
// uses has neither.
func agentKeyScopes(scopes []string, agent bool) []string {
	if !agent {
		return scopes
	}
	if len(scopes) == 0 {
		return slices.Clone(agentKeyDefaultScopes)
	}
	if slices.Contains(scopes, api.ScopeAgent) {
		return scopes
	}
	return append(slices.Clone(scopes), api.ScopeAgent)
}
