package apidserver

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Deploy and app audit rows did not say whether a coding agent made them,
// so agent-driven usage could not be counted (ADR-20261112223000
// Amendment 1).
func TestMergeAgentAudit(t *testing.T) {
	acct := state.Account{ID: "acct-1"}
	tests := []struct {
		name string
		ua   string
		key  *state.APIKey
		want map[string]any
	}{
		{"person on the cli", "gregale/0.1.0 (linux/amd64)", &state.APIKey{Label: "cli-login"}, map[string]any{}},
		{"claude code on the cli", "gregale/0.1.0 (linux/amd64; agent=claude-code)", &state.APIKey{Label: "cli-login"}, map[string]any{"actor_agent": "claude-code"}},
		{"unknown agent name", "gregale/0.1.0 (linux/amd64; agent=<script>)", nil, map[string]any{}},
		{"browser session", "Mozilla/5.0 (agent=claude-code)", nil, map[string]any{}},
		{"hosted mcp key", "gregale-mcp/1", &state.APIKey{Label: api.MCPLoginKeyLabel, Scopes: []string{"apps:read"}}, map[string]any{"actor_agent_key": api.AgentConnectionHostedMCP}},
		{"agent key from the cli", "gregale/0.1.0 (linux/amd64; agent=codex)", &state.APIKey{Scopes: []string{api.ScopeAgent}}, map[string]any{"actor_agent": "codex", "actor_agent_key": api.AgentConnectionAgentKey}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/apps", nil)
			r.Header.Set("User-Agent", tt.ua)
			r = r.WithContext(authmw.WithPrincipal(r.Context(), acct, tt.key, nil))
			got := mergeAgentAudit(map[string]any{}, r)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("mergeAgentAudit = %v, want %v", got, tt.want)
			}
		})
	}
}

// A request that skipped the auth chain still reports the CLI's agent.
func TestMergeAgentAuditWithoutPrincipal(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/apps", nil)
	r.Header.Set("User-Agent", "gregale/0.1.0 (darwin/arm64; agent=cursor)")
	got := mergeAgentAudit(nil, r)
	if want := (map[string]any{"actor_agent": "cursor"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeAgentAudit = %v, want %v", got, want)
	}
}
