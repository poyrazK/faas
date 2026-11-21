package journeys

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHarnessMCPToolJourneyConfiguration(t *testing.T) {
	if _, err := loadTarget(func(k string) string {
		if k == "GREGALE_E2E_MCP_TOOL_LIFECYCLE" {
			return "1"
		}
		return ""
	}); err == nil {
		t.Fatal("enabled preview skipped without an explicit API")
	}
	for _, tc := range []struct {
		name  string
		env   map[string]string
		valid bool
	}{
		{"off", nil, true},
		{"hosted publisher", map[string]string{"GREGALE_E2E_TIER": "full", "GREGALE_E2E_TOKEN": "synthetic-owner", "GREGALE_E2E_MCP_HOSTED_TOOL_LIFECYCLE": "1"}, true},
		{"invalid hosted opt-in", map[string]string{"GREGALE_E2E_MCP_HOSTED_TOOL_LIFECYCLE": "yes"}, false},
		{"full owner", map[string]string{"GREGALE_E2E_TIER": "full", "GREGALE_E2E_TOKEN": "synthetic-owner", "GREGALE_E2E_MCP_TOOL_LIFECYCLE": "1"}, true},
		{"no token", map[string]string{"GREGALE_E2E_TIER": "full", "GREGALE_E2E_MCP_TOOL_LIFECYCLE": "1"}, false},
		{"login only", map[string]string{"GREGALE_E2E_TIER": "full", "GREGALE_E2E_AUTH": "cli-login", "GREGALE_E2E_MCP_TOOL_LIFECYCLE": "1"}, false},
		{"smoke", map[string]string{"GREGALE_E2E_TOKEN": "synthetic-owner", "GREGALE_E2E_MCP_TOOL_LIFECYCLE": "1"}, false},
		{"invalid opt in", map[string]string{"GREGALE_E2E_MCP_TOOL_LIFECYCLE": "yes"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			get := func(k string) string {
				if k == "GREGALE_E2E_API" {
					return "https://api.staging.example"
				}
				return tc.env[k]
			}
			tg, err := loadTarget(get)
			if (err == nil) != tc.valid {
				t.Fatalf("configuration valid=%v, error=%v", tc.valid, err)
			}
			if err == nil && tg.mcpURL != "https://api.staging.example/mcp" {
				t.Fatal("bridge did not inherit the exact selected API")
			}
		})
	}
	for _, endpoint := range []string{"http://remote.example/mcp", "https://user:password@bridge.example/mcp", "https://bridge.example/mcp?token=secret", "https://bridge.example/mcp#part", "https://bridge.example/other", "file:///tmp/mcp"} {
		tg := &target{api: "https://api.staging.example", tier: tierFull, token: "owner"}
		err := loadMCPToolJourney(tg, func(k string) string {
			if k == "GREGALE_E2E_MCP_TOOL_LIFECYCLE" {
				return "1"
			}
			if k == "GREGALE_E2E_MCP_URL" {
				return endpoint
			}
			return ""
		})
		if err == nil {
			t.Errorf("unsafe bridge accepted: %s", endpoint)
		}
	}
	tg := &target{api: "http://api.remote.example", tier: tierFull, token: "owner"}
	if err := loadMCPToolJourney(tg, func(k string) string {
		if k == "GREGALE_E2E_MCP_TOOL_LIFECYCLE" {
			return "1"
		}
		if k == "GREGALE_E2E_MCP_URL" {
			return "https://bridge.example/mcp"
		}
		return ""
	}); err == nil {
		t.Fatal("owner token could travel to a remote plaintext API")
	}
}

func TestHarnessMCPToolDescriptorRejectsUnboundEvidence(t *testing.T) {
	version, app, deployment, grant := "version", "app-id", "deployment-id", "grant-id"
	catalog := []byte(`{"version":2,"tools":[{"name":"journey_echo"}],"capabilities":["tools"]}`)
	base := func() map[string]any {
		return map[string]any{
			"version": version, "app_id": app, "deployment_id": deployment, "access_mode": "open", "execution_authorized": false, "catalog_projection": "single_tool",
			"catalog_contract_sha256": toolJourneyDigest(catalog), "published_catalog_contract_sha256": strings.Repeat("a", 64),
			"grants": []any{map[string]any{"grant_id": grant, "allow_invocation": true, "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)}},
		}
	}
	if err := checkToolJourneyDescriptor(base(), catalog, version, app, deployment, grant, "journey_echo"); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"version", "app_id", "deployment_id", "access_mode", "catalog_contract_sha256", "catalog_projection", "execution_authorized", "grants"} {
		doc := base()
		delete(doc, field)
		if checkToolJourneyDescriptor(doc, catalog, version, app, deployment, grant, "journey_echo") == nil {
			t.Errorf("missing %s accepted", field)
		}
	}
	for _, extra := range []string{`,"tools":[{"name":"journey_echo"},{"name":"hidden"}]`, `,"resource_templates":[{"uriTemplate":"private://{id}"}]`, `,"prompts":[{"name":"private"}]`, `,"resources":[{"uri":"private://data"}]`} {
		// Last duplicate tools key deliberately exercises the decoder's effective value.
		raw := append(append([]byte{}, catalog[:len(catalog)-1]...), []byte(extra+"}")...)
		doc := base()
		doc["catalog_contract_sha256"] = toolJourneyDigest(raw)
		if checkToolJourneyDescriptor(doc, raw, version, app, deployment, grant, "journey_echo") == nil {
			t.Error("ungranted catalog data accepted")
		}
	}
	for _, allowed := range []bool{false, true} {
		doc := base()
		doc["grants"] = []any{map[string]any{"grant_id": grant, "allow_invocation": allowed, "expires_at": time.Now().Add(-time.Minute).Format(time.RFC3339)}}
		if checkToolJourneyDescriptor(doc, catalog, version, app, deployment, grant, "journey_echo") == nil {
			t.Error("expired or discovery-only grant accepted")
		}
	}
}

func TestHarnessMCPToolResultRequiresCompleteFirstCall(t *testing.T) {
	raw := []byte(`{"content":[],"structuredContent":{"nonce":"e2ej-test","count":1}}`)
	doc := map[string]any{"status": "completed", "effects_may_have_occurred": true, "outcome": map[string]any{"status": "completed", "result_sha256": toolJourneyDigest(raw), "result_bytes": float64(len(raw))}}
	if err := decodeToolJourneyResult(doc, raw, "e2ej-test"); err != nil {
		t.Fatal(err)
	}
	if decodeToolJourneyResult(doc, raw[:len(raw)-1], "e2ej-test") == nil {
		t.Fatal("truncated result accepted")
	}
	if decodeToolJourneyResult(doc, raw, "e2ej-another") == nil {
		t.Fatal("another invocation's nonce accepted")
	}
	for _, count := range []int{0, 2} {
		changed := []byte(strings.Replace(string(raw), `"count":1`, `"count":`+strconv.Itoa(count), 1))
		doc["outcome"] = map[string]any{"status": "completed", "result_sha256": toolJourneyDigest(changed), "result_bytes": float64(len(changed))}
		if decodeToolJourneyResult(doc, changed, "e2ej-test") == nil {
			t.Error("missing or duplicate application call accepted")
		}
	}
}

func TestHarnessMCPToolLostReplyForwardsOnlyOneMatchingRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"request_id":"retained"`)) {
			t.Error("request changed in transit")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"status":"queued"}}}`))
	}))
	defer server.Close()
	loss := &toolJourneyLostReply{base: http.DefaultTransport, requestID: "retained"}
	client := &http.Client{Transport: loss}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"gregale_invoke_tool","arguments":{"request_id":"retained"}}}`
	resp, err := client.Post(server.URL, "application/json", strings.NewReader(body))
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || calls.Load() != 1 || !loss.dropped.Load() {
		t.Fatal("successful start reply was not lost after exactly one forward")
	}
	resp, err = client.Post(server.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if calls.Load() != 2 {
		t.Fatal("injection retried or suppressed the explicit second request")
	}
}

// With a supplied locally built CLI this exercises real stdio discovery,
// scaffold bytes and source adaptation without any platform network request.
func TestHarnessMCPToolSourceWithCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI to a matching local CLI")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("scaffold contacted a native API")
		w.WriteHeader(500)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	j := &mcpToolJourney{root: t, ctx: ctx, tg: &target{cli: cli, home: t.TempDir(), api: server.URL, token: "fp_test_0123456789abcdef0123456789abcdef", runID: "hk3x9a"}}
	j.scaffold(t)
	fixture, err := os.ReadFile(filepath.Join(j.path, "gregale-mcp.tests.json"))
	var suite map[string]any
	if err != nil || json.Unmarshal(fixture, &suite) != nil || str(suite, "cases.0.target") != "journey_echo" {
		t.Fatal("scaffold omitted its reviewed behavioral fixture")
	}
	for _, name := range []string{"server.js", "journey-tools.mjs"} {
		out, err := exec.Command("node", "--check", filepath.Join(j.path, name)).CombinedOutput()
		if err != nil {
			t.Fatalf("generated %s syntax: %s", name, out)
		}
	}
}

func TestHarnessMCPToolRunnerRequiresExplicitTargetAndToken(t *testing.T) {
	for _, args := range [][]string{
		{"--mcp-private-tool-lifecycle"},
		{"--mcp-private-tool-lifecycle", "--tier", "full"},
		{"--mcp-private-tool-lifecycle", "--tier", "full", "--api", "https://api.staging.example"},
		{"--mcp-hosted-tool-lifecycle"},
		{"--mcp-hosted-tool-lifecycle", "--tier", "full", "--api", "https://api.staging.example"},
		{"--mcp-tool-lifecycle"},
		{"--mcp-tool-lifecycle", "--tier", "full"},
		{"--mcp-tool-lifecycle", "--tier", "full", "--api", "https://api.staging.example"},
	} {
		cmd := exec.Command("bash", append([]string{filepath.Join("..", "..", "scripts", "e2e-journeys.sh")}, args...)...)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "GREGALE_E2E_") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		out, err := cmd.CombinedOutput()
		if err == nil || cmd.ProcessState.ExitCode() != 2 || !strings.Contains(string(out), "requires") {
			t.Fatalf("preview runner bypassed preflight: %v %s", err, out)
		}
	}
}
