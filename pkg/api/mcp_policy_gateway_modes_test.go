package api

// adr: 20261122194324 — consumer-key MCP policies.

import "testing"

func TestConsumerKeyMCPScopes(t *testing.T) {
	for scope, want := range map[string]bool{
		"mcp:tools": true, "mcp:a/b.c-d_e:f": true, "mcp:": false, "mcp:a b": false, `mcp:a"b`: false,
		`mcp:a\b`: false, "mcp:é": false, "tools": false, "read": false,
	} {
		if got := ValidConsumerKeyMCPScope(scope); got != want {
			t.Errorf("ValidConsumerKeyMCPScope(%q) = %v", scope, got)
		}
	}
	long := "mcp:"
	for len(long) < ConsumerKeyMCPScopeMaxBytes+1 {
		long += "a"
	}
	if ValidConsumerKeyMCPScope(long) || !ValidConsumerKeyMCPScope(long[:ConsumerKeyMCPScopeMaxBytes]) {
		t.Error("the 128-byte bound is wrong")
	}
	got := ConsumerKeyMCPScopes([]string{"read", "mcp:tools", "mcp:", "mcp:admin"})
	if len(got) != 2 || got[0] != "tools" || got[1] != "admin" {
		t.Fatalf("ConsumerKeyMCPScopes = %v", got)
	}
}

func TestMCPPolicyCredentialAndToolLimits(t *testing.T) {
	base := func() *MCPResourcePolicy { return &MCPResourcePolicy{Resource: "https://app.example/mcp"} }
	for _, tc := range []struct {
		name   string
		change func(*MCPResourcePolicy)
		ok     bool
	}{
		{"consumer key", func(p *MCPResourcePolicy) { p.Credential = MCPCredentialConsumerKey }, true},
		{"unknown credential", func(p *MCPResourcePolicy) { p.Credential = "basic" }, false},
		{"tool limit", func(p *MCPResourcePolicy) {
			p.ToolLimits = map[string]MCPToolLimit{"search": {Requests: 5, WindowSeconds: 60}}
		}, true},
		{"zero requests", func(p *MCPResourcePolicy) { p.ToolLimits = map[string]MCPToolLimit{"search": {WindowSeconds: 60}} }, false},
		{"window too long", func(p *MCPResourcePolicy) {
			p.ToolLimits = map[string]MCPToolLimit{"search": {Requests: 1, WindowSeconds: 3601}}
		}, false},
		{"empty tool name", func(p *MCPResourcePolicy) {
			p.ToolLimits = map[string]MCPToolLimit{"": {Requests: 1, WindowSeconds: 1}}
		}, false},
	} {
		p := base()
		tc.change(p)
		if err := p.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: Validate() = %v", tc.name, err)
		}
	}
	if !(&MCPResourcePolicy{Credential: MCPCredentialConsumerKey}).ConsumerKey() || (&MCPResourcePolicy{}).ConsumerKey() || (*MCPResourcePolicy)(nil).ConsumerKey() {
		t.Error("ConsumerKey() is wrong")
	}
}
