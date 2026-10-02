package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/secretscan"
)

// adr: 426 — customer CLI journey from scaffold through discovery, call and config.
func TestMCPCLIJourney(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout = &output
	jsonOutput = true
	t.Cleanup(func() { osStdout = oldOut; jsonOutput = oldJSON })
	dir := filepath.Join(t.TempDir(), "my-mcp")
	if code := cmdMCP([]string{"init", "--path", dir}); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	cfg, err := mcphosting.Load(dir)
	if err != nil || cfg.Auth.Mode != "open" {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
	lock, err := os.ReadFile(filepath.Join(dir, "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if findings := secretscan.ScanFile("package-lock.json", lock); len(findings) != 0 {
		t.Fatalf("reproducible starter rejected by source scanner: %+v", findings)
	}
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected operator credential forwarded")
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
		case "tools/list":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"add","inputSchema":{"type":"object"}}]}}`, req.ID)
		case "tools/call":
			calls.Add(1)
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"12"}],"structuredContent":{"sum":12}}}`, req.ID)
		}
	}))
	defer s.Close()
	for _, command := range [][]string{
		{"tools", "--url", s.URL + "/mcp"},
		{"doctor", "--url", s.URL + "/mcp"},
		{"call", "--url", s.URL + "/mcp", "--tool", "add", "--arguments", `{"a":7,"b":5}`},
		{"config", "--url", s.URL + "/mcp", "--name", "my-mcp"},
	} {
		output.Reset()
		if code := cmdMCP(command); code != 0 {
			t.Fatalf("%v exit=%d output=%s", command, code, output.String())
		}
		var result map[string]any
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatalf("invalid CLI receipt: %v; %s", err, output.String())
		}
		if command[0] == "doctor" && result["ok"] != true {
			t.Fatalf("doctor=%v", result)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("CLI executed %d tools; discovery/doctor/config must not invoke tools", calls.Load())
	}
}

func TestMCPArgumentsAndPreflight(t *testing.T) {
	for _, value := range []string{"null", "[]", `{} {}`, strings.Repeat(" ", 1<<20) + "{}"} {
		if _, err := mcpArguments(value, ""); err == nil {
			t.Errorf("accepted arguments %q", value[:min(len(value), 30)])
		}
	}
	if _, err := mcpArguments(`{}`, "file.json"); err == nil {
		t.Fatal("mixed arguments accepted")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, mcphosting.ConfigFile), []byte(`{"version":1,"endpoint":"/mcp","transport":"stdio","mode":"stateless","auth":{"mode":"open"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := cmdMCPDeploy([]string{"--path", dir, "--name", "my-mcp"}); code == 0 {
		t.Fatal("invalid transport reached deployment")
	}
	if _, err := mcpToken("MCP_NONEXISTENT_CLIENT_TOKEN"); err == nil {
		t.Fatal("missing token variable accepted")
	}
}

func TestMCPHelpAndCompletion(t *testing.T) {
	command, ok := lookupCliCommand("mcp")
	if !ok {
		t.Fatal("MCP missing from customer CLI manifest")
	}
	var reference bytes.Buffer
	renderMarkdownReference(&reference, []cliCommand{command})
	for _, required := range []string{"mcp doctor", "mcp deploy", "mcp lock", "mcp diff", "--before", "--after", "--check", "--force", "--token-env", "--stream-tool", "--arguments-file"} {
		if !strings.Contains(reference.String(), required) {
			t.Errorf("MCP help omitted %q", required)
		}
	}
	if cliHelpGroup(command) != "API" {
		t.Fatal("MCP not discoverable with API commands")
	}
}

func TestMCPDiscoveryReportsRejectedTools(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			w.WriteHeader(403)
			return
		}
		var request struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"bad","inputSchema":{"properties":{"x":{"type":"number","x-mcp-header":"X"}}}},{"name":"good","inputSchema":{"type":"object"}}]}}`, request.ID)
	}))
	defer s.Close()
	for _, command := range []string{"tools", "doctor"} {
		output.Reset()
		if code := cmdMCP([]string{command, "--url", s.URL + "/mcp"}); code != 0 {
			t.Fatalf("%s exit=%d: %s", command, code, output.String())
		}
		if !strings.Contains(output.String(), `"rejected_tools"`) || !strings.Contains(output.String(), `"good"`) || !strings.Contains(output.String(), `"reason"`) {
			t.Fatalf("incomplete %s receipt: %s", command, output.String())
		}
	}
}

// adr: 426 — failed protocol/auth verification closes public ingress.
func TestMCPDeploymentVerificationFailureEnablesMaintenance(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout = &output
	jsonOutput = true
	t.Cleanup(func() { osStdout = oldOut; jsonOutput = oldJSON })
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }))
	defer mcp.Close()
	var maintenance atomic.Bool
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer operator-only" {
			t.Error("control plane missing its own account credential")
		}
		if r.Method == http.MethodPatch {
			var update api.UpdateAppRequest
			_ = json.NewDecoder(r.Body).Decode(&update)
			if update.MaintenanceMode != nil {
				maintenance.Store(*update.MaintenanceMode)
			}
		}
		_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "my-mcp", URL: mcp.URL})
	}))
	defer control.Close()
	cfg := mcphosting.Config{Endpoint: "/mcp", Auth: mcphosting.AuthConfig{Mode: "open"}}
	if code := finishMCPDeploy(context.Background(), NewClient(control.URL, "operator-only"), "my-mcp", cfg, ""); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	if !maintenance.Load() || !strings.Contains(output.String(), "maintenance_enabled") {
		t.Fatalf("failed deployment remains open: %s", output.String())
	}
}
