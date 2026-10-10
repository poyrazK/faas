package main

// adr: 644 — verified promotion and gateway resource-policy acceptance.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPCandidatePromotion(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		valid, preview, first, drift bool
		want                         int
	}{
		{"valid candidate", true, true, false, false, 0},
		{"first deployment", true, true, true, false, 0},
		{"failed doctor preserves serving", false, true, false, false, 1},
		{"missing preview fails closed", true, false, false, false, 1},
		{"catalog expansion blocks promotion", true, true, false, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			var output bytes.Buffer
			osStdout, osStderr, jsonOutput = &output, &output, true
			t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
			calls, promotions, updates := 0, 0, 0
			candidate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("operator key forwarded to candidate")
				}
				if !tc.valid {
					w.WriteHeader(404)
					return
				}
				if r.Header.Get("Origin") != "" {
					w.WriteHeader(403)
					return
				}
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				w.Header().Set("Content-Type", "application/json")
				switch req.Method {
				case "server/discover":
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":["%s"],"capabilities":{"tools":{}}}}`, req.ID, mcphosting.ProtocolVersion)
				case "tools/list":
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"add","inputSchema":{"type":"object"}}]}}`, req.ID)
				default:
					calls++
					t.Errorf("unexpected tool execution: %s", req.Method)
				}
			}))
			defer candidate.Close()
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer operator-only" {
					t.Error("missing control credential")
				}
				switch r.URL.Path {
				case "/v1/apps/my-mcp":
					if r.Method == "PATCH" {
						updates++
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app", Slug: "my-mcp", URL: "https://my-mcp.example", StreamingEnabled: true, PublicAuth: api.PublicAuthStatus{Mode: api.AppPublicAuthModeOpen}})
				case "/v1/deployments/candidate":
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "candidate", AppID: "app", Status: statusLive})
				case "/v1/apps/my-mcp/deployments":
					deps := []api.DeploymentResponse{{ID: "candidate", AppID: "app", Status: statusLive}}
					if !tc.first {
						deps = append(deps, api.DeploymentResponse{ID: "stable", AppID: "app", Status: statusLive, TrafficPercent: 100})
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: deps})
				case "/v1/deployments/candidate/url":
					_ = json.NewEncoder(w).Encode(api.DeploymentPreviewURL{URL: candidate.URL, Alive: tc.preview})
				case "/v1/deployments/candidate/traffic":
					var req api.UpdateDeploymentTrafficRequest
					_ = json.NewDecoder(r.Body).Decode(&req)
					expected := "stable"
					if tc.first {
						expected = ""
					}
					if req.ExpectedServingDeploymentID == nil || *req.ExpectedServingDeploymentID != expected || req.TrafficPercent != 100 {
						t.Errorf("unguarded promotion: %+v", req)
					}
					promotions++
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "candidate", TrafficPercent: 100})
				default:
					t.Errorf("unexpected control request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer control.Close()
			var roles []mcpReleaseRole
			if tc.drift {
				baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{}})
				roles = []mcpReleaseRole{{Name: "reader", contract: baseline}}
			}
			cfg := mcphosting.Config{Endpoint: "/mcp", Auth: mcphosting.AuthConfig{Mode: "open"}}
			if got := finishMCPDeploy(context.Background(), NewClient(control.URL, "operator-only"), "my-mcp", "candidate", cfg, "", roles); got != tc.want {
				t.Fatalf("exit=%d want=%d output=%s", got, tc.want, output.String())
			}
			if calls != 0 || (tc.want != 0 && promotions != 0) || (!tc.first && updates != 0) {
				t.Fatalf("calls=%d promotions=%d app mutations=%d", calls, promotions, updates)
			}
		})
	}
}

func TestMCPReleasePolicyPreflight(t *testing.T) {
	dir := t.TempDir()
	baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{}})
	body, _ := mcphosting.MarshalContract(baseline)
	if err := os.WriteFile(filepath.Join(dir, "reader.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_READER_TOKEN", "reader-secret")
	policy := filepath.Join(dir, "release.json")
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"version":1,"roles":[{"name":"reader","token_env":"MCP_READER_TOKEN","baseline":"reader.json"}]}`, true},
		{`{"version":1,"roles":[{"name":"reader","baseline":"../reader.json"}]}`, false},
		{`{"version":1,"roles":[{"name":"reader","baseline":"reader.json"},{"name":"reader","baseline":"reader.json"}]}`, false},
		{`{"version":1,"roles":[{"name":"reader","token":"secret","baseline":"reader.json"}]}`, false},
	} {
		if err := os.WriteFile(policy, []byte(tc.body), 0600); err != nil {
			t.Fatal(err)
		}
		roles, err := loadMCPReleasePolicy(policy)
		if (err == nil) != tc.valid {
			t.Fatalf("roles=%v err=%v", roles, err)
		}
	}
}

func TestMCPTaskDefaultsMatchAPI(t *testing.T) {
	body, err := os.ReadFile("templates/mcp-node/task-limits.json")
	if err != nil {
		t.Fatal(err)
	}
	var defaults struct {
		MaxRunning             int
		MaxRunningPerOwner     int
		MaxOutstanding         int
		MaxOutstandingPerOwner int
	}
	if err := json.Unmarshal(body, &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.MaxRunning != api.MCPTaskDefaultMaxRunning || defaults.MaxRunningPerOwner != api.MCPTaskDefaultMaxRunningPerOwner || defaults.MaxOutstanding != api.MCPTaskDefaultMaxOutstanding || defaults.MaxOutstandingPerOwner != api.MCPTaskDefaultMaxOutstandingPerOwner {
		t.Fatalf("starter defaults drifted: %+v", defaults)
	}
}
