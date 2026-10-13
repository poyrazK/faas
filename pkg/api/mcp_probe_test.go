package api

import "testing"

func TestMCPProbeRPCAllowlist(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		method string
		valid  bool
	}{
		{name: "initialize", body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28"}}`, method: "initialize", valid: true},
		{name: "initialized notification", body: `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`, method: "notifications/initialized", valid: true},
		{name: "catalog list", body: `{"jsonrpc":"2.0","id":"list-1","method":"tools/list","params":{}}`, method: "tools/list", valid: true},
		{name: "tool invocation rejected", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete"}}`},
		{name: "resource reads rejected", body: `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"secret://value"}}`},
		{name: "prompt rendering rejected", body: `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"private"}}`},
		{name: "task access rejected", body: `{"jsonrpc":"2.0","id":1,"method":"tasks/get","params":{"taskId":"task-1"}}`},
		{name: "request ID required", body: `{"jsonrpc":"2.0","method":"tools/list"}`},
		{name: "notification ID rejected", body: `{"jsonrpc":"2.0","id":1,"method":"notifications/initialized"}`},
		{name: "array parameters rejected", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":[]}`},
		{name: "null parameters rejected", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":null}`},
		{name: "duplicate method rejected", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","method":"tools/list"}`},
		{name: "duplicate nested keys rejected", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"cursor":"a","cursor":"b"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method, valid := MCPProbeRPCMethod([]byte(tc.body))
			if valid != tc.valid || valid && method != tc.method {
				t.Fatalf("MCPProbeRPCMethod() = %q, %t; want %q, %t", method, valid, tc.method, tc.valid)
			}
		})
	}
}

func TestMCPProbeRPCVersionValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		version string
		valid   bool
	}{
		{name: "matching initialize", body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28"}}`, version: MCPProtocolVersion, valid: true},
		{name: "mismatched initialize", body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`, version: MCPProtocolVersion},
		{name: "legacy catalog request", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, version: MCPLegacyProtocolVersion, valid: true},
		{name: "modern discovery rejected on legacy", body: `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}}`, version: MCPLegacyProtocolVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MCPProbeRPCMatchesVersion([]byte(tc.body), tc.version); got != tc.valid {
				t.Fatalf("MCPProbeRPCMatchesVersion() = %t, want %t", got, tc.valid)
			}
		})
	}
}

func TestMCPProbeHostAndEndpointValidation(t *testing.T) {
	for _, host := range []string{"mcp.example.test", "mcp.example.test:443", "[::1]:8080"} {
		if !ValidMCPProbeHost(host) {
			t.Errorf("ValidMCPProbeHost(%q) = false", host)
		}
	}
	for _, host := range []string{"", "https://mcp.example.test", "user@mcp.example.test", "mcp.example.test/path", "mcp.example.test?token=x", "mcp.example.test#fragment", "mcp.example.test\r\nInjected: true"} {
		if ValidMCPProbeHost(host) {
			t.Errorf("ValidMCPProbeHost(%q) = true", host)
		}
	}
	for _, path := range []string{"/", "/mcp", "/v1/mcp/"} {
		if !ValidMCPProbeEndpointPath(path) {
			t.Errorf("ValidMCPProbeEndpointPath(%q) = false", path)
		}
	}
	for _, path := range []string{"", "//mcp", "/mcp//other", "/../admin", "/%2e%2e/admin", "https://mcp.example.test/mcp", "/mcp?token=x"} {
		if ValidMCPProbeEndpointPath(path) {
			t.Errorf("ValidMCPProbeEndpointPath(%q) = true", path)
		}
	}
}

func TestMCPProbeTokenValidation(t *testing.T) {
	for _, token := range []string{"", "opaque-token_123", "a/b+c=="} {
		if !ValidMCPProbeToken(token) {
			t.Errorf("ValidMCPProbeToken(%q) = false", token)
		}
	}
	for _, token := range []string{"has space", "bad\r\ntoken", "contains:colon"} {
		if ValidMCPProbeToken(token) {
			t.Errorf("ValidMCPProbeToken(%q) = true", token)
		}
	}
}
