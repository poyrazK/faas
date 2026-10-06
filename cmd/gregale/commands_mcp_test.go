package main

import (
	"bufio"
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
	if len(cfg.Auth.ToolScopes) != 3 {
		t.Fatalf("starter must explicitly allow its three harmless tools: %+v", cfg.Auth.ToolScopes)
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
		case "server/discover":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":["%s"],"capabilities":{"tools":{}}}}`, req.ID, mcphosting.ProtocolVersion)
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

func TestMCPCallUsesExplicitInputResponsesFile(t *testing.T) {
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	var output, stderr bytes.Buffer
	osStdout, osStderr, jsonOutput = &output, &stderr, true
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	responses := filepath.Join(t.TempDir(), "responses.json")
	if err := os.WriteFile(responses, []byte(`{"dates":{"action":"accept","content":{"from":"2026-06-01","to":"2026-06-30"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "tools/list":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"report_preview","inputSchema":{"type":"object"}}]}}`, request.ID)
		case "tools/call":
			count := calls.Add(1)
			meta, _ := request.Params["_meta"].(map[string]any)
			caps, _ := meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any)
			if form, ok := caps["elicitation"].(map[string]any); !ok || form["form"] == nil {
				t.Errorf("interactive capability missing: %v", caps)
			}
			if count == 1 {
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"input_required","requestState":"opaque","inputRequests":{"dates":{"method":"elicitation/create","params":{"mode":"form","message":"Choose dates","requestedSchema":{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"}}}}}}}}`, request.ID)
				return
			}
			inputResponses, _ := request.Params["inputResponses"].(map[string]any)
			if inputResponses == nil || request.Params["requestState"] != "opaque" {
				t.Errorf("missing response or opaque state: %v", request.Params)
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","content":[{"type":"text","text":"preview ready"}]}}`, request.ID)
		default:
			t.Errorf("unexpected MCP method %q", request.Method)
		}
	}))
	defer server.Close()
	if code := cmdMCP([]string{"call", "--url", server.URL + "/mcp", "--tool", "report_preview", "--input-responses-file", responses}); code != 0 {
		t.Fatalf("call exit=%d, stdout=%s stderr=%s", code, output.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || calls.Load() != 2 || !strings.Contains(output.String(), "preview ready") || strings.Contains(output.String(), "2026-06") || strings.Contains(output.String(), "opaque") {
		t.Fatalf("output=%s err=%v calls=%d", output.String(), err, calls.Load())
	}
}

func TestMCPInteractivePromptAndTTYGate(t *testing.T) {
	for _, tc := range []struct {
		name, input, action string
		wantContent         bool
	}{
		{name: "accept", input: `{"from":"2026-06-01"}`, action: "accept", wantContent: true},
		{name: "decline", input: "decline", action: "decline"},
		{name: "cancel", input: "cancel", action: "cancel"},
		{name: "eof", input: "", action: "cancel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scanner := bufio.NewScanner(strings.NewReader(tc.input))
			var prompt bytes.Buffer
			response, err := promptMCPInput(scanner, &prompt, mcphosting.InputRequest{Tool: "preview", ID: "dates", Message: "Choose a \x1b[31mrange\x1b[0m", Schema: map[string]any{"type": "object"}})
			if err != nil || response.Action != tc.action || (response.Content != nil) != tc.wantContent || !strings.Contains(prompt.String(), "untrusted") || strings.Contains(prompt.String(), "\x1b") || !strings.Contains(prompt.String(), "Choose a range") {
				t.Fatalf("response=%+v err=%v prompt=%s", response, err, prompt.String())
			}
		})
	}
	if mcpInputIsTerminal(strings.NewReader("response\n")) {
		t.Fatal("a pipe or test reader must not count as interactive terminal input")
	}
}

func TestMCPInteractiveFlagRejectsNonTerminalBeforeCallingServer(t *testing.T) {
	oldOut, oldErr, oldIn, oldJSON := osStdout, osStderr, osStdin, jsonOutput
	var output, stderr bytes.Buffer
	osStdout, osStderr, osStdin, jsonOutput = &output, &stderr, strings.NewReader("{}\n"), false
	t.Cleanup(func() { osStdout, osStderr, osStdin, jsonOutput = oldOut, oldErr, oldIn, oldJSON })
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	if code := cmdMCP([]string{"call", "--url", server.URL + "/mcp", "--tool", "preview", "--interactive"}); code == 0 || requests.Load() != 0 || !strings.Contains(stderr.String(), "requires terminal stdin") {
		t.Fatalf("exit=%d requests=%d stdout=%s stderr=%s", code, requests.Load(), output.String(), stderr.String())
	}
}

func TestMCPHelpAndCompletion(t *testing.T) {
	command, ok := lookupCliCommand("mcp")
	if !ok {
		t.Fatal("MCP missing from customer CLI manifest")
	}
	var reference bytes.Buffer
	renderMarkdownReference(&reference, []cliCommand{command})
	for _, required := range []string{"mcp doctor", "mcp deploy", "mcp lock", "mcp diff", "mcp resources", "mcp resource-read", "mcp prompts", "mcp prompt-get", "--uri", "--prompt", "--before", "--after", "--check", "--strict-catalog", "--force", "--token-env", "--stream-tool", "--arguments-file", "--interactive", "--input-responses-file"} {
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
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "application/json")
		if request.Method == "server/discover" {
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":["%s"],"capabilities":{"tools":{}}}}`, request.ID, mcphosting.ProtocolVersion)
			return
		}
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
