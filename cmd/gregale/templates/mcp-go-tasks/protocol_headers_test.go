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
			// Spec negotiation: an unserved revision gets the server's own
			// revision back, never a refused handshake.
			want := version
			if !legacy && version == legacyProtocol {
				want = currentProtocol
			}
			result := protocolTestRequest(t, legacy, body, headers)
			if result.Code != 200 || negotiatedVersion(t, result) != want {
				t.Fatalf("legacy=%v version=%s: %d %s", legacy, version, result.Code, result.Body)
			}
		}
		// A modern HTTP header cannot bypass a disabled legacy initialize body.
		headers := http.Header{}
		headers.Set("MCP-Protocol-Version", currentProtocol)
		headers.Set("MCP-Method", "initialize")
		result := protocolTestRequest(t, legacy, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`, headers)
		if result.Code != 200 || (negotiatedVersion(t, result) == legacyProtocol) != legacy {
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

func TestKeyedTaskHeadersFailClosed(t *testing.T) {
	const key = "019ab230-1234-7234-9234-123456789abc"
	request := rpcRequest{
		ID: json.RawMessage("1"), Method: "tools/call",
		Params: json.RawMessage(`{"name":"greet","_meta":{"io.gregale/task-submission-key":"` + key + `"}}`),
	}
	for _, tc := range []struct {
		name, version, headerVersion string
		methods                      []string
		valid                        bool
	}{
		{"matched", currentProtocol, currentProtocol, []string{taskSubmissionMethod}, true},
		{"unversioned", currentProtocol, "", []string{taskSubmissionMethod}, false},
		{"legacy", legacyProtocol, legacyProtocol, []string{taskSubmissionMethod}, false},
		{"ordinary marker", currentProtocol, currentProtocol, []string{"tools/call"}, false},
		{"duplicate marker", currentProtocol, currentProtocol, []string{"tools/call", taskSubmissionMethod}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := http.Header{}
			headers.Set("MCP-Protocol-Version", tc.headerVersion)
			headers.Set("MCP-Name", "greet")
			for _, method := range tc.methods {
				headers.Add("MCP-Method", method)
			}
			if err := validateMCPHeaders(headers, request, tc.version); (err == nil) != tc.valid {
				t.Fatalf("validateMCPHeaders() = %v, valid = %t", err, tc.valid)
			}
		})
	}

	// A keyed request for a synchronous tool must not fall back to running it.
	headers := http.Header{}
	headers.Set("MCP-Protocol-Version", currentProtocol)
	headers.Set("MCP-Method", taskSubmissionMethod)
	headers.Set("MCP-Name", "greet")
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"greet","arguments":{"name":"Gregale"},"_meta":{"io.gregale/task-submission-key":"` + key + `"}}}`
	if reply := protocolTestRequest(t, true, body, headers); reply.Code != http.StatusBadRequest {
		t.Fatalf("keyed request fell through to ordinary tool call: %d %s", reply.Code, reply.Body)
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

func negotiatedVersion(t *testing.T, result *httptest.ResponseRecorder) string {
	t.Helper()
	var response struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode initialize: %v %s", err, result.Body)
	}
	if header := result.Header().Get("MCP-Protocol-Version"); header != response.Result.ProtocolVersion {
		t.Fatalf("header %q disagrees with negotiated %q", header, response.Result.ProtocolVersion)
	}
	return response.Result.ProtocolVersion
}

// Clients pinned to 2025-06-18 or 2025-03-26 (SDKs before late 2025) connect
// in legacy mode; unknown revisions are offered the newest legacy revision.
func TestOlderClientRevisionsNegotiate(t *testing.T) {
	for _, tc := range []struct {
		requested, header, want string
	}{
		{"2025-06-18", "", "2025-06-18"},
		{"2025-06-18", "2025-06-18", "2025-06-18"},
		{"2025-03-26", "", "2025-03-26"},
		{"2024-11-05", "", legacyProtocol},
		{"1999-01-01", "1999-01-01", legacyProtocol},
	} {
		headers := http.Header{}
		if tc.header != "" {
			headers.Set("MCP-Protocol-Version", tc.header)
		}
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + tc.requested + `"}}`
		result := protocolTestRequest(t, true, body, headers)
		if result.Code != 200 || negotiatedVersion(t, result) != tc.want {
			t.Fatalf("requested %s: %d %s", tc.requested, result.Code, result.Body)
		}
	}
	for _, header := range []string{"2025-06-18", ""} {
		headers := http.Header{}
		if header != "" {
			headers.Set("MCP-Protocol-Version", header)
		}
		result := protocolTestRequest(t, true, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, headers)
		if result.Code != 200 || !strings.Contains(result.Body.String(), `"add"`) {
			t.Fatalf("tools/list header %q: %d %s", header, result.Code, result.Body)
		}
		want := header
		if want == "" {
			want = "2025-03-26"
		}
		if got := result.Header().Get("MCP-Protocol-Version"); got != want {
			t.Fatalf("tools/list header %q answered as %q", header, got)
		}
	}
}
