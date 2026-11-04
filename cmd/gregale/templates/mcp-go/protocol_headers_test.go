package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func protocolTestRequest(t *testing.T, legacy bool, body string, headers http.Header) *httptest.ResponseRecorder {
	t.Helper()
	config, err := loadMCPConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.Legacy = legacy
	config.Auth = json.RawMessage(`{"mode":"open"}`)
	auth, err := newAuthenticator(config)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, config.Endpoint, strings.NewReader(body))
	request.Header = headers.Clone()
	request.Header.Set("Content-Type", "application/json")
	result := httptest.NewRecorder()
	handleHTTP(config, auth, nil, result, request)
	return result
}

func TestLegacySettingControlsNegotiationDiscoveryAndNotifications(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, version := range []string{legacyProtocol, currentProtocol} {
			headers := http.Header{}
			headers.Set("MCP-Protocol-Version", version)
			headers.Set("MCP-Method", "initialize")
			body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + version + `"}}`
			want := 200
			if !legacy && version == legacyProtocol {
				want = 400
			}
			if result := protocolTestRequest(t, legacy, body, headers); result.Code != want {
				t.Fatalf("legacy=%v version=%s: %d %s", legacy, version, result.Code, result.Body)
			}
		}
		// A modern HTTP header cannot bypass a disabled legacy initialize body.
		headers := http.Header{}
		headers.Set("MCP-Protocol-Version", currentProtocol)
		headers.Set("MCP-Method", "initialize")
		result := protocolTestRequest(t, legacy, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`, headers)
		if (result.Code == 200) != legacy {
			t.Fatalf("initialize bypass: %d %s", result.Code, result.Body)
		}
		headers.Set("MCP-Method", "server/discover")
		result = protocolTestRequest(t, legacy, `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`, headers)
		if result.Code != 200 || strings.Contains(result.Body.String(), legacyProtocol) != legacy {
			t.Fatalf("discovery legacy=%v: %d %s", legacy, result.Code, result.Body)
		}
		headers.Set("MCP-Protocol-Version", legacyProtocol)
		headers.Del("MCP-Method")
		result = protocolTestRequest(t, legacy, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, headers)
		if (result.Code == 202) != legacy {
			t.Fatalf("notification bypass: %d %s", result.Code, result.Body)
		}
	}
}

func TestModernRequestHeadersMustMatchBeforeToolExecution(t *testing.T) {
	calls := 0
	definition := map[string]any{"name": "header_probe", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"region": map[string]any{"type": "string", "x-mcp-header": "region"}}}}
	if err := RegisterMCPTool(definition, func(context.Context, map[string]any, *MCPRequestContext) (map[string]any, error) {
		calls++
		return map[string]any{"content": []any{}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	defer RemoveMCPTool("header_probe")
	for _, tc := range []struct {
		name, method, tool, param, meta string
		duplicate                       bool
		want                            int
	}{
		{"valid", "tools/call", "header_probe", "us-east", "2026-07-28", false, 200},
		{"missing method", "", "header_probe", "us-east", "2026-07-28", false, 400},
		{"wrong method", "tools/list", "header_probe", "us-east", "2026-07-28", false, 400},
		{"duplicate method", "tools/call", "header_probe", "us-east", "2026-07-28", true, 400},
		{"missing name", "tools/call", "", "us-east", "2026-07-28", false, 400},
		{"wrong name", "tools/call", "greet", "us-east", "2026-07-28", false, 400},
		{"missing annotation", "tools/call", "header_probe", "", "2026-07-28", false, 400},
		{"wrong annotation", "tools/call", "header_probe", "eu-west", "2026-07-28", false, 400},
		{"invalid base64", "tools/call", "header_probe", "=?base64?!!!?=", "2026-07-28", false, 400},
		{"encoded annotation", "tools/call", "header_probe", "=?base64?dXMtZWFzdA==?=", "2026-07-28", false, 200},
		{"wrong metadata version", "tools/call", "header_probe", "us-east", "2025-11-25", false, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			headers := http.Header{}
			headers.Set("MCP-Protocol-Version", currentProtocol)
			if tc.method != "" {
				headers.Set("MCP-Method", tc.method)
			}
			if tc.duplicate {
				headers.Add("MCP-Method", tc.method)
			}
			if tc.tool != "" {
				headers.Set("MCP-Name", tc.tool)
			}
			if tc.param != "" {
				headers.Set("MCP-Param-Region", tc.param)
			}
			body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"header_probe","arguments":{"region":"us-east"},"_meta":{"io.modelcontextprotocol/protocolVersion":"` + tc.meta + `"}}}`
			result := protocolTestRequest(t, true, body, headers)
			if result.Code != tc.want {
				t.Fatalf("status=%d body=%s", result.Code, result.Body)
			}
			if tc.want == 400 {
				var response rpcResponse
				_ = json.Unmarshal(result.Body.Bytes(), &response)
				if response.Error == nil || response.Error.Code != -32020 || calls != before {
					t.Fatalf("invalid request reached tool: calls=%d body=%s", calls, result.Body)
				}
			} else if calls != before+1 {
				t.Fatal("valid request did not execute")
			}
		})
	}
	// Notifications do not need method/name headers.
	headers := http.Header{}
	headers.Set("MCP-Protocol-Version", currentProtocol)
	if result := protocolTestRequest(t, true, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, headers); result.Code != 202 {
		t.Fatalf("notification: %d %s", result.Code, result.Body)
	}
}

func TestModernResourceRoutingAcceptsEncodedURI(t *testing.T) {
	headers := http.Header{}
	headers.Set("MCP-Protocol-Version", currentProtocol)
	headers.Set("MCP-Method", "resources/read")
	headers.Set("MCP-Name", "=?base64?Z3JlZXRpbmc6Ly93ZWxjb21l?=")
	response := protocolTestRequest(t, true, `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"greeting://welcome"}}`, headers)
	if response.Code != 200 {
		t.Fatalf("encoded resource URI rejected: %d %s", response.Code, response.Body)
	}
}
