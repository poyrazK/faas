package journeys

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func loadMCPToolJourney(tg *target, getenv func(string) string) error {
	raw := getenv("GREGALE_E2E_MCP_TOOL_LIFECYCLE")
	if raw != "" && raw != "0" && raw != "1" {
		return fmt.Errorf("GREGALE_E2E_MCP_TOOL_LIFECYCLE must be 0 or 1")
	}
	hosted := getenv("GREGALE_E2E_MCP_HOSTED_TOOL_LIFECYCLE")
	if hosted != "" && hosted != "0" && hosted != "1" {
		return fmt.Errorf("GREGALE_E2E_MCP_HOSTED_TOOL_LIFECYCLE must be 0 or 1")
	}
	tg.mcpHostedTools = hosted == "1"
	private := getenv("GREGALE_E2E_MCP_PRIVATE_TOOL_LIFECYCLE")
	if private != "" && private != "0" && private != "1" {
		return fmt.Errorf("GREGALE_E2E_MCP_PRIVATE_TOOL_LIFECYCLE must be 0 or 1")
	}
	tg.mcpPrivateTools = private == "1"
	if tg.mcpPrivateTools && (raw == "1" || tg.mcpHostedTools) {
		return fmt.Errorf("select private or open tool lifecycle acceptance, not both")
	}
	tg.mcpTools = raw == "1" || tg.mcpHostedTools
	tg.mcpURL = strings.TrimRight(getenv("GREGALE_E2E_MCP_URL"), "/")
	if tg.mcpURL == "" {
		tg.mcpURL = tg.api + "/mcp"
	}
	if !tg.mcpTools && !tg.mcpPrivateTools {
		return nil
	}
	if tg.tier != tierFull || tg.useLogin || tg.token == "" {
		return fmt.Errorf("the preview MCP tool journey requires full tier and an explicit owner API token")
	}
	native, err := url.Parse(tg.api)
	if err != nil || native.Host == "" || native.User != nil || native.RawQuery != "" || native.Fragment != "" || !toolJourneySecureURL(native) {
		return fmt.Errorf("the preview owner API requires HTTPS except on loopback and must omit URL credentials, query and fragment")
	}
	u, err := url.Parse(tg.mcpURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !strings.HasSuffix(u.Path, "/mcp") {
		return fmt.Errorf("GREGALE_E2E_MCP_URL must be an exact hosted /mcp URL without credentials, query or fragment")
	}
	if !toolJourneySecureURL(u) {
		return fmt.Errorf("GREGALE_E2E_MCP_URL requires HTTPS except on loopback")
	}
	return nil
}

func toolJourneySecureURL(u *url.URL) bool {
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())
}

// prepareMCPToolSource edits only the newly materialized trusted starter.
// No application code is executed on the journey host.
func prepareMCPToolSource(dir, nonce string) error {
	serverPath := filepath.Join(dir, "server.js")
	server, err := os.ReadFile(serverPath)
	if err != nil {
		return err
	}
	const declaration = "const { app, handler, beginDrain, closeTaskSubscriptions } = createApp(config, { taskRuntime });"
	if bytes.Count(server, []byte(declaration)) != 1 {
		return fmt.Errorf("Node starter entry point changed; review the journey adapter before publishing")
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "mcp-tool-lifecycle", "journey-tools.mjs"))
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "journey-tools.mjs"), fixture, 0o644); err != nil {
		return err
	}
	imports := "import { z } from 'zod';\nimport { installJourneyTools } from './journey-tools.mjs';\n"
	replacement := "const runtime = createApp(config, { taskRuntime });\nconst { app, handler, beginDrain, closeTaskSubscriptions } = runtime;\ninstallJourneyTools(runtime, z);"
	server = append([]byte(imports), bytes.Replace(server, []byte(declaration), []byte(replacement), 1)...)
	if err = os.WriteFile(serverPath, server, 0o644); err != nil {
		return err
	}
	configPath := filepath.Join(dir, "gregale-mcp.json")
	config, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err = json.Unmarshal(config, &doc); err != nil {
		return err
	}
	auth, ok := doc["auth"].(map[string]any)
	if !ok || auth["mode"] != "open" {
		return fmt.Errorf("journey requires the explicit open starter")
	}
	policy, ok := auth["tool_scopes"].(map[string]any)
	if !ok {
		return fmt.Errorf("starter omitted explicit tool policy")
	}
	policy["journey_echo"], policy["journey_wait"] = []string{}, []string{}
	config, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(configPath, config, 0o644); err != nil {
		return err
	}
	manifestPath := filepath.Join(dir, "gregale.yaml")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	// The runtime counter is meaningful only with this explicit ceiling. The
	// journey also verifies the effective native setting after publication.
	manifest = append(manifest, []byte("\nscaling:\n  max_instances: 1\n")...)
	if err = os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		return err
	}
	suite := map[string]any{"version": 1, "cases": []any{map[string]any{
		"name": "records_one_reviewed_call", "kind": "tool", "target": "journey_echo", "arguments": map[string]string{"nonce": nonce},
		"expected_result": map[string]any{"content": []any{map[string]string{"type": "text", "text": "Journey call recorded."}}, "structuredContent": map[string]any{"nonce": nonce, "count": 1}},
	}}}
	data, err := json.Marshal(suite)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "gregale-mcp.tests.json"), data, 0o644)
}

func toolJourneyDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func checkToolJourneyDescriptor(doc map[string]any, catalog []byte, version, appID, deployment, grant, name string) error {
	return checkToolJourneyDescriptorBinding(doc, catalog, version, appID, deployment, grant, name, "open", "")
}

func checkToolJourneyDescriptorBinding(doc map[string]any, catalog []byte, version, appID, deployment, grant, name, mode, binding string) error {
	if str(doc, "version") != version || str(doc, "app_id") != appID || str(doc, "deployment_id") != deployment ||
		str(doc, "access_mode") != mode || doc["execution_authorized"] != false || str(doc, "catalog_projection") != "single_tool" ||
		toolJourneyDigest(catalog) != str(doc, "catalog_contract_sha256") || len(str(doc, "published_catalog_contract_sha256")) != 64 {
		return fmt.Errorf("descriptor changed identity, projection, digest or authority")
	}
	var c struct {
		Version int `json:"version"`
		Tools   []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Resources, Prompts, Extensions []json.RawMessage
		ResourceTemplates              []json.RawMessage `json:"resource_templates"`
		Capabilities                   []string          `json:"capabilities"`
	}
	if json.Unmarshal(catalog, &c) != nil || c.Version != 2 || len(c.Tools) != 1 || c.Tools[0].Name != name ||
		len(c.Resources)+len(c.ResourceTemplates)+len(c.Prompts)+len(c.Extensions) != 0 || len(c.Capabilities) != 1 || c.Capabilities[0] != "tools" {
		return fmt.Errorf("descriptor includes ungranted catalog entries")
	}
	grants, ok := doc["grants"].([]any)
	if !ok {
		return fmt.Errorf("descriptor omitted invocation grants")
	}
	for _, value := range grants {
		g, ok := value.(map[string]any)
		if !ok || str(g, "grant_id") != grant || g["allow_invocation"] != true || str(g, "credential_binding_sha256") != binding {
			continue
		}
		expiry, err := time.Parse(time.RFC3339Nano, str(g, "expires_at"))
		if err == nil && time.Now().Before(expiry) {
			return nil
		}
	}
	return fmt.Errorf("descriptor omitted the exact active invocation grant")
}

func decodeToolJourneyResult(doc map[string]any, raw []byte, nonce string) error {
	if str(doc, "status") != "completed" || str(doc, "outcome.status") != "completed" ||
		doc["effects_may_have_occurred"] != true || toolJourneyDigest(raw) != str(doc, "outcome.result_sha256") || num(doc, "outcome.result_bytes") != float64(len(raw)) {
		return fmt.Errorf("invocation lacks complete bound result evidence")
	}
	var result struct {
		IsError    bool `json:"isError"`
		Structured struct {
			Nonce string `json:"nonce"`
			Count int    `json:"count"`
		} `json:"structuredContent"`
	}
	if json.Unmarshal(raw, &result) != nil || result.IsError || result.Structured.Nonce != nonce || result.Structured.Count != 1 {
		return fmt.Errorf("tool result differs from the exact requested nonce and first call")
	}
	return nil
}

func toolJourneyChunk(t *testing.T, doc map[string]any, field string, offset int) ([]byte, int) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(str(doc, field+"_base64"))
	if err != nil || num(doc, field+"_offset") != float64(offset) {
		t.Fatalf("invalid %s chunk or offset", field)
	}
	next := int(num(doc, "next_"+field+"_offset"))
	if next != -1 && (len(raw) == 0 || next != offset+len(raw)) {
		t.Fatalf("%s pagination did not advance by the returned bytes", field)
	}
	return raw, next
}

// Drop one successful start reply at the client boundary, after forwarding the
// request unchanged. The native intent and app call must survive that loss.
// This injection never retries, changes the target, or affects other RPCs.
type toolJourneyLostReply struct {
	base      http.RoundTripper
	requestID string
	dropped   atomic.Bool
}

func (l *toolJourneyLostReply) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(io.LimitReader(req.Body, 1<<20))
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	var rpc struct {
		Method string `json:"method"`
		Params struct {
			Name      string `json:"name"`
			Arguments struct {
				RequestID string `json:"request_id"`
			} `json:"arguments"`
		} `json:"params"`
	}
	matched := json.Unmarshal(body, &rpc) == nil && rpc.Method == "tools/call" && rpc.Params.Name == "gregale_invoke_tool" && rpc.Params.Arguments.RequestID == l.requestID
	resp, err := l.base.RoundTrip(req)
	if err != nil || !matched || resp.StatusCode != http.StatusOK || !l.dropped.CompareAndSwap(false, true) {
		return resp, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	return nil, errors.New("journey deliberately discarded the invocation reply; inspect the retained request UUID")
}
