package api

import (
	"github.com/google/uuid"
	"testing"
)

func TestMCPEnvironmentOAuthSelection(t *testing.T) {
	ref := MCPReadinessOAuthConnection{Role: "staging-reader", Revision: uuid.NewString()}
	c := MCPReadinessMonitorConfig{IntervalSeconds: 60, FailureThreshold: 1, OAuthConnections: map[string]MCPReadinessOAuthConnection{"staging": ref}}
	if err := ValidateMCPReadinessMonitor(c); err != nil {
		t.Fatal(err)
	}
	if c.OAuthForScope("") != nil || c.OAuthForScope("production") != nil {
		t.Fatal("missing environment fell back")
	}
	if selected := c.OAuthForScope("staging"); selected == nil || selected.Scope != "staging" || selected.Role != ref.Role {
		t.Fatal(selected)
	}
	for _, mutate := range []func(*MCPReadinessMonitorConfig){
		func(c *MCPReadinessMonitorConfig) { c.SecretRef = "secret:TOKEN" },
		func(c *MCPReadinessMonitorConfig) { c.OAuthConnection = &ref },
		func(c *MCPReadinessMonitorConfig) {
			c.OAuthConnections = map[string]MCPReadinessOAuthConnection{"__all__": ref}
		},
		func(c *MCPReadinessMonitorConfig) {
			bad := ref
			bad.Scope = "default"
			c.OAuthConnections = map[string]MCPReadinessOAuthConnection{"staging": bad}
		},
	} {
		bad := c
		mutate(&bad)
		if ValidateMCPReadinessMonitor(bad) == nil {
			t.Fatal("invalid binding accepted", bad)
		}
	}
	legacy := MCPReadinessMonitorConfig{OAuthConnection: &ref}
	if legacy.OAuthForScope("") == nil || legacy.OAuthForScope("staging") != nil {
		t.Fatal("legacy binding escaped default")
	}
}
