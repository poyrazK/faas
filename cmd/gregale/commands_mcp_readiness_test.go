package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPReadinessDeploymentIdentityAndCredentialIsolation(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint(foreign), func(t *testing.T) {
			appID, depID := uuid.NewString(), uuid.NewString()
			probes, previews := 0, 0
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				probes++
				if r.Header.Get("Authorization") != "Bearer MCP_PROBE_TOKEN" {
					t.Error("wrong probe credential")
				}
				var request struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("MCP-Protocol-Version", mcphosting.ProtocolVersion)
				var result any = map[string]any{}
				switch request.Method {
				case "initialize":
					result = map[string]any{"protocolVersion": mcphosting.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}}
				case "notifications/initialized":
					w.WriteHeader(202)
					return
				case "ping":
				case "tools/list":
					result = map[string]any{"tools": []any{}}
				default:
					t.Errorf("unexpected RPC: %s", request.Method)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
			}))
			defer endpoint.Close()
			platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("readiness mutated platform")
				}
				if r.Header.Get("Authorization") != "Bearer PLATFORM_ONLY" {
					t.Error("wrong platform credential")
				}
				switch r.URL.Path {
				case "/v1/apps/test-mcp":
					writeJSONTest(w, api.AppResponse{ID: appID, Slug: "test-mcp"})
				case "/v1/deployments/" + depID:
					owner := appID
					if foreign {
						owner = uuid.NewString()
					}
					writeJSONTest(w, api.DeploymentResponse{ID: depID, AppID: owner, Status: statusLive})
				case "/v1/deployments/" + depID + "/url":
					previews++
					writeJSONTest(w, api.DeploymentPreviewURL{URL: endpoint.URL, Alive: true})
				default:
					t.Errorf("unexpected platform path: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer platform.Close()
			t.Setenv("FAAS_API", platform.URL)
			t.Setenv("FAAS_TOKEN", "PLATFORM_ONLY")
			t.Setenv("MCP_PROBE_TEST_TOKEN", "MCP_PROBE_TOKEN")
			var output bytes.Buffer
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			osStdout, osStderr, jsonOutput = &output, &output, true
			defer func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON }()
			code := cmdMCP([]string{"readiness", "--app", "test-mcp", "--deployment", depID, "--token-env", "MCP_PROBE_TEST_TOKEN"})
			if foreign {
				if code == 0 || probes != 0 || previews != 0 {
					t.Fatal("foreign deployment probed")
				}
				return
			}
			if code != 0 || probes != 4 || previews != 1 {
				t.Fatalf("exit=%d probes=%d previews=%d output=%s", code, probes, previews, output.String())
			}
			var report mcpDeploymentReadiness
			if json.Unmarshal(output.Bytes(), &report) != nil || report.DeploymentID != depID || !report.Readiness.OK {
				t.Fatalf("invalid report: %s", output.String())
			}
			if strings.Contains(output.String(), "PLATFORM_ONLY") || strings.Contains(output.String(), "MCP_PROBE_TOKEN") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestMCPReadinessRecordUsesPlatform(t *testing.T) {
	appID, depID := uuid.NewString(), uuid.NewString()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer PLATFORM_ONLY" {
			t.Error("platform authorization missing")
		}
		switch r.URL.Path {
		case "/v1/apps/test-mcp":
			writeJSONTest(w, api.AppResponse{ID: appID, Slug: "test-mcp"})
		case "/v1/deployments/" + depID:
			writeJSONTest(w, api.DeploymentResponse{ID: depID, AppID: appID, Status: statusLive})
		case "/v1/deployments/" + depID + "/mcp-readiness":
			if r.Method != "POST" || r.Header.Get(api.MCPReadinessTokenHeader) != "PROBE_ONLY" {
				t.Error("wrong platform probe request")
			}
			var request api.MCPReadinessProbeRequest
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.Role != "reader" {
				t.Error("wrong role")
			}
			writeJSONTest(w, api.MCPReadinessEvidence{DeploymentID: depID, Role: "reader", Passed: true})
		default:
			t.Errorf("record mode made a local probe or unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "PLATFORM_ONLY")
	t.Setenv("MCP_RECORDED_TEST_TOKEN", "PROBE_ONLY")
	var output bytes.Buffer
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	osStdout, osStderr, jsonOutput = &output, &output, true
	defer func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON }()
	if code := cmdMCPReadiness([]string{"--app", "test-mcp", "--deployment", depID, "--record", "--role", "reader", "--token-env", "MCP_RECORDED_TEST_TOKEN"}); code != 0 {
		t.Fatalf("record failed: %s", output.String())
	}
	if strings.Contains(output.String(), "PLATFORM_ONLY") || strings.Contains(output.String(), "PROBE_ONLY") {
		t.Fatal("credential leaked")
	}
}
