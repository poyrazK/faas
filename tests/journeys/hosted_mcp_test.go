package journeys

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Inventory row 160: the Gregale management MCP server, hosted at
// https://api.<platform>/mcp (ADR-20261026195952). A browser sign-in cannot
// run headless, so the journey connects the way headless agents do, with
// agent keys (ADR-20261029085359): it reads the server's OAuth discovery,
// opens an MCP session with a read-only agent key and lists the account's
// apps through the server, sees a non-agent key refused, finds the
// connection among the account's connected agents and revokes it there
// (ADR-20261030133644). Then it creates an organization and checks its agent
// policy (ADR-20261101225122): a read-only policy refuses an agent key that
// may deploy, an organization agent key lists the organization's apps and
// not the account's, and denying agents revokes that key.

const mcpProtocolVersion = "2026-07-28"

// TestHostedMCP is the hosted management MCP journey (row 160).
func TestHostedMCP(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	j := &hostedMCPJourney{tg: tg, endpoint: strings.TrimRight(tg.api, "/") + "/mcp"}
	s := newStory(t)
	s.step("discovery", j.discovery)
	s.step("probe_app", func(t *testing.T) { j.probe, _ = tg.sharedProbe(t) })
	s.step("agent_key_session", j.agentKeySession)
	s.step("lists_account_apps", j.listsAccountApps)
	s.check("non_agent_key_refused", j.nonAgentKeyRefused)
	s.step("connection_listed", j.connectionListed)
	s.step("connection_revoked", j.connectionRevoked)
	s.step("org_created", j.orgCreated)
	s.step("org_policy_read_only", j.orgPolicyReadOnly)
	s.check("org_policy_refuses_deploy_key", j.orgPolicyRefusesDeployKey)
	s.step("org_key_lists_org_apps", j.orgKeyListsOrgApps)
	s.step("org_connection_listed", j.orgConnectionListed)
	s.step("denying_agents_revokes", j.denyingAgentsRevokes)
}

type hostedMCPJourney struct {
	tg       *target
	endpoint string
	probe    string // the shared probe app: the account's, not the org's
	admin    string // admin key for the connected-agents API
	keyID    string // the account agent key
	session  *mcpSession
	org      string
	orgKeyID string
	orgMCP   *mcpSession
}

var resourceMetadataRe = regexp.MustCompile(`resource_metadata="([^"]+)"`)

// discovery: an unauthenticated call is challenged with the protected
// resource metadata, which names the /mcp resource and an authorization
// server.
func (j *hostedMCPJourney) discovery(t *testing.T) {
	r := send(http.MethodPost, j.endpoint, map[string]string{"Content-Type": "application/json"}, []byte(`{}`))
	if r.err != nil || r.status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST %s = %s, want 401", j.endpoint, r)
	}
	m := resourceMetadataRe.FindStringSubmatch(r.header.Get("WWW-Authenticate"))
	if m == nil {
		t.Fatalf("challenge %q names no resource_metadata", r.header.Get("WWW-Authenticate"))
	}
	meta := jsonBody(t, get(m[1]))
	if str(meta, "resource") != j.endpoint {
		t.Fatalf("protected resource metadata names resource %q, want %q", str(meta, "resource"), j.endpoint)
	}
	if servers, _ := meta["authorization_servers"].([]any); len(servers) == 0 {
		t.Fatalf("protected resource metadata lists no authorization server: %s", compact(meta))
	}
}

// agentKeySession: a read-only agent key opens a session that lists the
// read tools and none of the write tools.
func (j *hostedMCPJourney) agentKeySession(t *testing.T) {
	key, id := j.tg.scopedKeyWithID(t, "mcp-agent", "agent")
	j.keyID = id
	j.session = openMCP(t, j.endpoint, key)
	tools := j.session.toolNames(t)
	if !tools["gregale_list_apps"] {
		t.Fatalf("read-only agent key lists no gregale_list_apps: %v", tools)
	}
	for _, write := range []string{"gregale_plan_domain", "gregale_request_secret", "gregale_plan_bucket"} {
		if tools[write] {
			t.Fatalf("read-only agent key lists write tool %s", write)
		}
	}
}

func (j *hostedMCPJourney) listsAccountApps(t *testing.T) {
	if text := j.session.callTool(t, "gregale_list_apps", map[string]any{}); !strings.Contains(text, j.probe) {
		t.Fatalf("gregale_list_apps does not name the probe app %s: %s", j.probe, tail(text, 400))
	}
}

// nonAgentKeyRefused: a key without the agent scope is refused with a
// pointer to agent keys rather than the OAuth challenge.
func (j *hostedMCPJourney) nonAgentKeyRefused(t *testing.T) {
	key := j.tg.scopedKey(t, "mcp-reader", "apps:read")
	r := mcpPost(j.endpoint, key, "", initializeRequest())
	if r.status != http.StatusForbidden || !strings.Contains(r.header.Get("WWW-Authenticate"), "insufficient_scope") {
		t.Fatalf("apps:read key on %s = %s, want 403 insufficient_scope", j.endpoint, r)
	}
}

func (j *hostedMCPJourney) connectionListed(t *testing.T) {
	j.admin = j.tg.scopedKey(t, "mcp-admin", "admin")
	if !j.listsConnection(t, "/v1/agent-connections", j.keyID) {
		t.Fatalf("GET /v1/agent-connections does not list agent key %s", j.keyID)
	}
}

// connectionRevoked: revoking the connection ends the session at its next
// request.
func (j *hostedMCPJourney) connectionRevoked(t *testing.T) {
	if r := j.tg.apiCall(http.MethodDelete, "/v1/agent-connections/"+j.keyID, nil, j.admin); r.status != http.StatusNoContent {
		t.Fatalf("revoke = %s", r)
	}
	eventually(t, "revoked agent key refused", time.Minute, 5*time.Second, func() (bool, string) {
		r := j.session.post(listToolsRequest())
		return r.status == http.StatusUnauthorized || r.status == http.StatusForbidden, r.String()
	})
}

func (j *hostedMCPJourney) orgCreated(t *testing.T) {
	j.org = j.tg.slug("mcp-org")
	j.tg.ok(t, time.Minute, "org", "create", "--slug", j.org, "--name", "E2E agents "+j.tg.runID)
	t.Cleanup(func() { j.tg.run(t, time.Minute, "org", "delete", j.org, "--yes") })
}

// orgPolicyReadOnly: an owner sets the policy that lets agents in with
// inspection only.
func (j *hostedMCPJourney) orgPolicyReadOnly(t *testing.T) {
	out := object(t, j.tg.ok(t, time.Minute, "org", "agent", "policy", "set", "--org", j.org, "--agents", "allow", "--capabilities", "").stdout)
	if field(out, "agents_allowed") != true || len(asList(field(out, "allowed_scopes"))) != 0 {
		t.Fatalf("policy after read-only set = %s", compact(out))
	}
}

func (j *hostedMCPJourney) orgPolicyRefusesDeployKey(t *testing.T) {
	res := j.tg.run(t, time.Minute, "org", "keys", "add", "--org", j.org, "--label", j.tg.slug("mcp-deployer"), "--scopes", "agent,deploy:write")
	if res.exit == 0 {
		if id := str(object(t, res.stdout), "id"); id != "" {
			j.tg.run(t, time.Minute, "org", "keys", "rm", "--org", j.org, id)
		}
		t.Fatalf("an agent key that may deploy was created under a read-only policy")
	}
	if !strings.Contains(res.problem(), "agent") {
		t.Fatalf("refusal does not name the agent policy: %s", res.problem())
	}
}

// orgKeyListsOrgApps: an organization agent key lists the organization's
// apps, so the account's probe app is not among them.
func (j *hostedMCPJourney) orgKeyListsOrgApps(t *testing.T) {
	res := j.tg.ok(t, time.Minute, "org", "keys", "add", "--org", j.org, "--label", j.tg.slug("mcp-org-agent"), "--scopes", "agent")
	key := apiKeyRe.FindString(string(res.stdout))
	j.orgKeyID = str(object(t, res.stdout), "id")
	if key == "" || j.orgKeyID == "" {
		t.Fatal("org key create printed no key or id")
	}
	t.Cleanup(func() { j.tg.run(t, time.Minute, "org", "keys", "rm", "--org", j.org, j.orgKeyID) })
	j.orgMCP = openMCP(t, j.endpoint, key)
	if text := j.orgMCP.callTool(t, "gregale_list_apps", map[string]any{}); strings.Contains(text, j.probe) {
		t.Fatalf("the organization's agent lists the account's probe app %s: %s", j.probe, tail(text, 400))
	}
}

func (j *hostedMCPJourney) orgConnectionListed(t *testing.T) {
	if !j.listsConnection(t, "/v1/orgs/"+j.org+"/agent-connections", j.orgKeyID) {
		t.Fatalf("the organization's agent connections do not list key %s", j.orgKeyID)
	}
}

// denyingAgentsRevokes: a policy that denies agents revokes the
// organization's agent key at once.
func (j *hostedMCPJourney) denyingAgentsRevokes(t *testing.T) {
	out := object(t, j.tg.ok(t, time.Minute, "org", "agent", "policy", "set", "--org", j.org, "--agents", "deny").stdout)
	if field(out, "agents_allowed") != false || num(out, "revoked") < 1 {
		t.Fatalf("deny = %s, want agents_allowed false and at least one revoked", compact(out))
	}
	eventually(t, "revoked organization agent key refused", time.Minute, 5*time.Second, func() (bool, string) {
		r := j.orgMCP.post(listToolsRequest())
		return r.status == http.StatusUnauthorized || r.status == http.StatusForbidden, r.String()
	})
}

func (j *hostedMCPJourney) listsConnection(t *testing.T, path, id string) bool {
	t.Helper()
	list := jsonBody(t, j.tg.apiCall(http.MethodGet, path, nil, j.admin))
	for _, item := range asList(list["items"]) {
		if str(item, "id") == id {
			return true
		}
	}
	return false
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

// --- a minimal MCP client over streamable HTTP -----------------------

type mcpSession struct {
	endpoint, key, id string
	next              int
}

func initializeRequest() map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "gregale-e2e-journeys", "version": "1"},
	}}
}

func listToolsRequest() map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
}

func mcpPost(endpoint, key, session string, msg map[string]any) httpResult {
	body, _ := json.Marshal(msg)
	header := map[string]string{
		"Authorization":        "Bearer " + key,
		"Content-Type":         "application/json",
		"Accept":               "application/json, text/event-stream",
		"MCP-Protocol-Version": mcpProtocolVersion,
	}
	if session != "" {
		header["Mcp-Session-Id"] = session
	}
	return send(http.MethodPost, endpoint, header, body)
}

// openMCP initializes a session and sends notifications/initialized.
func openMCP(t *testing.T, endpoint, key string) *mcpSession {
	t.Helper()
	r := mcpPost(endpoint, key, "", initializeRequest())
	if r.err != nil || r.status != http.StatusOK {
		t.Fatalf("initialize = %s", r)
	}
	s := &mcpSession{endpoint: endpoint, key: key, id: r.header.Get("Mcp-Session-Id"), next: 1}
	if result := rpcResult(t, r); str(result, "serverInfo.name") == "" {
		t.Fatalf("initialize result names no server: %s", compact(result))
	}
	if n := s.post(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); n.status >= 300 {
		t.Fatalf("notifications/initialized = %s", n)
	}
	return s
}

func (s *mcpSession) post(msg map[string]any) httpResult {
	return mcpPost(s.endpoint, s.key, s.id, msg)
}

func (s *mcpSession) call(t *testing.T, method string, params map[string]any) map[string]any {
	t.Helper()
	s.next++
	r := s.post(map[string]any{"jsonrpc": "2.0", "id": s.next, "method": method, "params": params})
	if r.err != nil || r.status != http.StatusOK {
		t.Fatalf("%s = %s", method, r)
	}
	return rpcResult(t, r)
}

func (s *mcpSession) toolNames(t *testing.T) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, tool := range asList(s.call(t, "tools/list", map[string]any{})["tools"]) {
		names[str(tool, "name")] = true
	}
	return names
}

// callTool returns the text content of a tool result, failing on a tool
// error.
func (s *mcpSession) callTool(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	result := s.call(t, "tools/call", map[string]any{"name": name, "arguments": args})
	var text strings.Builder
	for _, c := range asList(result["content"]) {
		text.WriteString(str(c, "text"))
	}
	if field(result, "isError") == true {
		t.Fatalf("%s failed: %s", name, tail(text.String(), 400))
	}
	if sc, ok := result["structuredContent"]; ok {
		b, _ := json.Marshal(sc)
		text.Write(b)
	}
	return text.String()
}

// rpcResult reads the JSON-RPC response from a JSON or an event-stream
// body and returns its result, failing on a JSON-RPC error.
func rpcResult(t *testing.T, r httpResult) map[string]any {
	t.Helper()
	payload := r.body
	if strings.HasPrefix(r.header.Get("Content-Type"), "text/event-stream") {
		payload = nil
		sc := bufio.NewScanner(bytes.NewReader(r.body))
		sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data:"); ok && strings.Contains(data, `"id"`) {
				payload = []byte(strings.TrimSpace(data))
			}
		}
	}
	var msg map[string]any
	if err := json.Unmarshal(payload, &msg); err != nil {
		t.Fatalf("MCP response is not JSON-RPC: %v: %s", err, tail(string(r.body), 400))
	}
	if e, ok := msg["error"]; ok {
		t.Fatalf("MCP error: %s", compact(e))
	}
	result, _ := msg["result"].(map[string]any)
	if result == nil {
		t.Fatalf("MCP response has no result: %s", fmt.Sprint(tail(string(payload), 400)))
	}
	return result
}
