package main_test

import (
	"slices"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Cost-control review 2026-11-19 (N11): approvals and agent budgets apply
// only to keys with the agent scope, so `key create --agent` adds it.
func TestAgentKeyScopes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scopes []string
		agent  bool
		want   []string
	}{
		{"a plain key keeps its scopes", []string{"apps:read"}, false, []string{"apps:read"}},
		{"a plain key without scopes stays admin", nil, false, nil},
		{"--agent alone inspects and deploys", nil, true, []string{"agent", "apps:read", "deploy:write"}},
		{"--agent adds the agent scope", []string{"apps:read", "env:write"}, true, []string{"apps:read", "env:write", "agent"}},
		{"--agent keeps an agent scope once", []string{"agent", "deploy:write"}, true, []string{"agent", "deploy:write"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := main.AgentKeyScopes(tc.scopes, tc.agent); !slices.Equal(got, tc.want) {
				t.Fatalf("AgentKeyScopes(%v, %v) = %v, want %v", tc.scopes, tc.agent, got, tc.want)
			}
		})
	}
}
