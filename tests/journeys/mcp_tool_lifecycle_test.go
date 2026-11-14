package journeys

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPToolLifecycle is opt-in preview acceptance against a deployed target.
// It uses the real CLI, builder, native journals and hosted bridge. No fixture
// server or direct database access stands in for a deployed component.
func TestMCPToolLifecycle(t *testing.T) {
	tg := needs(t, tierFull, true)
	if !tg.mcpTools {
		t.Skip("preview journey: enable --mcp-tool-lifecycle against an explicit staging target")
	}
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
	t.Cleanup(cancel)
	j := &mcpToolJourney{tg: tg, root: t, ctx: ctx, grants: map[string]string{}}
	j.app = tg.app(t, "tools")
	s := newStory(t)
	s.step("preflight_hosted_invocation", j.preflight)
	s.step("scaffold_reviewed_tool", j.scaffold)
	s.step("publish_verified_tool", j.publish)
	s.step("recover_publication_after_reconnect", j.recoverPublication)
	s.step("discover_only_granted_tools", j.grantAndDiscover)
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		s.step("invoke_and_recover_"+protocol, func(t *testing.T) { j.invokeAndRecover(t, protocol) })
	}
	s.step("cancel_without_rolling_back_recorded_call", j.cancelInvocation)
	s.step("revoke_tool_access", j.revokeAccess)
}

type mcpToolJourney struct {
	tg                                                         *target
	root                                                       *testing.T
	ctx                                                        context.Context
	app, appID, path, key, keyID, version, deployment, preview string
	publisherKey                                               string
	owner                                                      *mcp.ClientSession
	grants                                                     map[string]string
	invocationIDs                                              []string
	grantHistory                                               map[string]string
	private                                                    *privateToolCredentials
}

func (j *mcpToolJourney) preflight(t *testing.T) {
	owner := j.tg.apiCall(http.MethodGet, "/v1/tool-grants?limit=1", nil, j.tg.token)
	if owner.err != nil || owner.status != http.StatusOK {
		t.Fatalf("preview requires the migrated native API and an unscoped personal owner token: HTTP %d", owner.status)
	}
	minted := object(t, j.tg.ok(t, time.Minute, "keys", "add", j.tg.slug("tool-key"), "--scopes", "agent").stdout)
	j.key, j.keyID = str(minted, "plaintext"), str(minted, "id")
	if j.key == "" || j.keyID == "" {
		t.Fatal("agent key mint omitted its credential or identity")
	}
	j.root.Cleanup(func() { j.tg.run(j.root, time.Minute, "key", "delete", j.keyID) })
	identity := j.native(t, http.MethodGet, "/v1/account", nil, j.key, http.StatusOK)
	var scopes []string
	values, ok := identity["key_scopes"].([]any)
	if !ok {
		t.Fatal("native identity omitted agent key scopes")
	}
	for _, value := range values {
		scopes = append(scopes, fmt.Sprint(value))
	}
	if len(scopes) != 2 || !slices.Contains(scopes, "agent") || !slices.Contains(scopes, "apps:read") {
		t.Fatal("journey agent must carry apps-read and agent scopes without deployment authority")
	}
	remote := j.connectHosted(t, "2026-07-28", nil)
	j.requireTools(t, remote, "gregale_search_tools", "gregale_describe_tool", "gregale_invoke_tool", "gregale_get_tool_invocation", "gregale_cancel_tool_invocation")
	page := j.call(t, remote, "gregale_search_tools", map[string]any{}, false)
	if versions, ok := page["versions"].([]any); !ok || len(versions) != 0 {
		t.Fatal("a new agent key inherited private registry entries")
	}
}

func (j *mcpToolJourney) scaffold(t *testing.T) {
	if j.tg.mcpHostedTools {
		j.scaffoldHosted(t)
		return
	}
	j.owner = j.connectOwner(t)
	j.requireTools(t, j.owner, "gregale_get_tool_template", "gregale_plan_tool_deploy", "gregale_apply_tool_deploy", "gregale_get_tool_deploy")
	manifest := j.call(t, j.owner, "gregale_get_tool_template", map[string]any{"runtime": "node"}, false)
	if len(str(manifest, "template_sha256")) != 64 {
		t.Fatal("template omitted its source identity")
	}
	j.path = filepath.Join(j.root.TempDir(), "tool")
	res := j.tg.exec(t, j.tg.token, j.tg.home, time.Minute, "mcp", "server", "init", "--path", j.path, "--runtime", "node", "--auth", "open")
	if res.exit != 0 {
		t.Fatalf("materialize trusted starter: %s", j.tg.redact(res.problem()))
	}
	chunk := j.call(t, j.owner, "gregale_get_tool_template", map[string]any{"runtime": "node", "file": "gregale-mcp.json"}, false)
	raw, err := base64.StdEncoding.DecodeString(str(chunk, "content"))
	local, readErr := os.ReadFile(filepath.Join(j.path, "gregale-mcp.json"))
	if err != nil || readErr != nil || !bytes.Equal(raw, local) || num(chunk, "next_offset") != -1 || str(chunk, "template_sha256") != str(manifest, "template_sha256") {
		t.Fatal("CLI scaffold differs from the agent-discovered versioned starter")
	}
	if err = prepareMCPToolSource(j.path, j.nonce(t, "fixture")); err != nil {
		t.Fatalf("prepare synthetic tool: %v", err)
	}
}

func (j *mcpToolJourney) publish(t *testing.T) {
	select {
	case j.tg.deploySlots <- struct{}{}:
		defer func() { <-j.tg.deploySlots }()
	case <-j.ctx.Done():
		t.Fatal("timed out waiting for a deploy slot")
	}
	var plan map[string]any
	if j.tg.mcpHostedTools {
		plan = j.planHosted(t)
	} else {
		args := map[string]any{"path": j.path, "app": j.app, "reason": "Opt-in staging tool lifecycle acceptance", "access_mode": j.accessMode(), "allow_tool_execution": true}
		if j.private != nil {
			args["token_env"] = privateToolTokenEnv
		}
		plan = j.call(t, j.owner, "gregale_plan_tool_deploy", args, false)
	}
	j.version = str(plan, "plan_id")
	if len(j.version) != 64 || len(str(plan, "source_sha256")) != 64 || str(plan, "status") != "planned" {
		t.Fatal("publication review omitted its retained identity or source digest")
	}
	j.call(t, j.owner, "gregale_apply_tool_deploy", map[string]any{"plan_id": j.version}, false)
	// Poll only. A timeout or uncertain result never repeats apply or fixtures.
	eventually(t, "verified publication", 32*time.Minute, 3*time.Second, func() (bool, string) {
		out := j.call(t, j.owner, "gregale_get_tool_deploy", map[string]any{"plan_id": j.version}, false)
		switch str(out, "status") {
		case "completed":
			return true, "completed"
		case "failed", "outcome_unknown":
			t.Fatalf("publication ended %s; inspect the retained plan before a new attempt", str(out, "status"))
		}
		return false, str(out, "status")
	})
	p := j.native(t, http.MethodGet, "/v1/tool-publications/"+j.version, nil, j.tg.token, http.StatusOK)
	j.deployment = str(p, "outcome.deployment_id")
	if str(p, "status") != "completed" || str(p, "intent.app") != j.app || p["evidence_origin"] != "publisher_attested" || field(p, "outcome.protocol_verified") != true || str(p, "intent.source_sha256") != str(plan, "source_sha256") {
		t.Fatal("native publication lacks bound source, protocol or publisher evidence")
	}
	checks, ok := field(p, "outcome.checks").([]any)
	if !ok || len(checks) != 1 || str(checks[0], "status") != "passed" {
		t.Fatal("reviewed behavioral fixture did not pass")
	}
	app := j.native(t, http.MethodGet, "/v1/apps/"+j.app, nil, j.tg.token, http.StatusOK)
	j.appID = str(app, "id")
	if j.appID == "" || num(app, "scaling_policy.max_instances") != 1 || num(app, "effective_limits.max_instances") != 1 {
		t.Fatal("counter proof requires the configured and effective single-instance ceiling")
	}
	dep := j.native(t, http.MethodGet, "/v1/deployments/"+j.deployment, nil, j.tg.token, http.StatusOK)
	if str(dep, "app_id") != j.appID || str(dep, "status") != "live" || num(dep, "traffic_percent") != 100 || str(dep, "rollout_state") != "complete" {
		t.Fatal("verified publication did not finish promoting the exact deployment")
	}
	preview := j.native(t, http.MethodGet, "/v1/deployments/"+j.deployment+"/url", nil, j.tg.token, http.StatusOK)
	u, err := url.Parse(str(preview, "url"))
	if err != nil || u.Scheme != "https" || u.Host != str(preview, "host") || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		str(preview, "app_id") != j.appID || str(preview, "deployment_id") != j.deployment || preview["alive"] != true {
		t.Fatal("exact deployment preview is unavailable or has changed identity")
	}
	j.preview = strings.TrimRight(u.String(), "/")
}

func (j *mcpToolJourney) recoverPublication(t *testing.T) {
	if err := j.owner.Close(); err != nil {
		t.Fatalf("close original owner process: %v", err)
	}
	if j.tg.mcpHostedTools {
		j.owner = j.connectHostedKey(t, "2026-07-28", nil, j.publisherKey)
	} else {
		j.owner = j.connectOwner(t)
	}
	out := j.call(t, j.owner, "gregale_get_tool_deploy", map[string]any{"plan_id": j.version}, false)
	if out["durable"] != true || str(out, "status") != "completed" || str(out, "result.deployment_id") != j.deployment {
		t.Fatal("fresh authoring connection did not recover the exact completed publication")
	}
}

func (j *mcpToolJourney) grantAndDiscover(t *testing.T) {
	remote := j.connectHosted(t, "2026-07-28", nil)
	j.call(t, remote, "gregale_describe_tool", map[string]any{"version": j.version, "name": "journey_echo"}, true)
	for _, name := range []string{"journey_echo", "journey_wait"} {
		id := uuid.NewString()
		j.grants[name] = id
		j.root.Cleanup(func() { j.tg.run(j.root, time.Minute, "mcp", "server", "tool-grant", "revoke", "--id", id) })
		args := []string{"mcp", "server", "tool-grant", "create", "--id", id, "--connection-key", j.keyID, "--version", j.version, "--tool", name,
			"--expires-at", time.Now().UTC().Add(45 * time.Minute).Format(time.RFC3339), "--allow-invocation"}
		if j.private != nil {
			args = append(args, "--credential-secret-ref", "secret:"+privateToolTokenEnv)
		}
		out := object(t, j.tg.ok(t, time.Minute, args...).stdout)
		if str(out, "id") != id || str(out, "intent.key_id") != j.keyID || str(out, "app_id") != j.appID {
			t.Fatal("owner grant changed the exact key/tool/app binding")
		}
		if j.private != nil {
			j.private.bindings[name] = str(out, "credential.sha256")
			if !validPrivateJourneyBinding(j.private.bindings[name]) {
				t.Fatal("private grant omitted its opaque credential binding")
			}
		}
		j.describe(t, remote, name)
	}
	page := j.call(t, remote, "gregale_search_tools", map[string]any{"query": j.app}, false)
	versions, ok := page["versions"].([]any)
	if !ok || len(versions) != 1 || str(versions[0], "version") != j.version {
		t.Fatal("scoped search did not return the exact granted publication")
	}
	encoded, _ := json.Marshal(field(versions[0], "tools"))
	var names []string
	if json.Unmarshal(encoded, &names) != nil || len(names) != 2 || !slices.Contains(names, "journey_echo") || !slices.Contains(names, "journey_wait") {
		t.Fatal("scoped search disclosed ungranted sibling tools")
	}
	j.call(t, remote, "gregale_describe_tool", map[string]any{"version": j.version, "name": "add"}, true)
}

func (j *mcpToolJourney) invokeAndRecover(t *testing.T, protocol string) {
	loss := &toolJourneyLostReply{requestID: uuid.NewString()}
	session := j.connectHosted(t, protocol, loss)
	nonce := j.nonce(t, "invoke")
	if j.private != nil {
		nonce += "-reflect"
	}
	args := j.invokeArgs("journey_echo", loss.requestID, nonce)
	ctx, cancel := context.WithTimeout(j.ctx, 45*time.Second)
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_invoke_tool", Arguments: args})
	cancel()
	if err == nil || !loss.dropped.Load() {
		t.Fatal("journey did not lose the successful invocation reply as intended")
	}
	// Recover through a fresh protocol session, then explicitly repeat the same
	// retained intent. Neither path may create another application call.
	fresh := j.connectHosted(t, protocol, nil)
	status := j.waitInvocation(t, fresh, loss.requestID, "completed")
	result := j.result(t, fresh, loss.requestID, status)
	if err = decodeToolJourneyResult(status, result, nonce); err != nil {
		t.Fatal(err)
	}
	before := j.invocation(t, loss.requestID, "journey_echo")
	if j.private != nil && field(before, "outcome.redacted") != true {
		t.Fatal("private credential reflection was not redacted")
	}
	observed := j.observe(t, nonce)
	if num(observed, "count") != 1 || !validJourneyRun(str(observed, "run")) {
		t.Fatal("first invocation was not observed once on the exact runtime")
	}
	j.call(t, fresh, "gregale_invoke_tool", args, false)
	if after := j.invocation(t, loss.requestID, "journey_echo"); compact(before) != compact(after) {
		t.Fatal("repeated invocation changed durable dispatch or result evidence")
	}
	second := j.observe(t, nonce)
	if compact(observed) != compact(second) {
		t.Fatal("same request caused another call or changed the observed runtime")
	}
	if j.private != nil {
		j.native(t, http.MethodPost, "/v1/delegated-tool-invocations/"+loss.requestID+"/dispatch", map[string]any{}, j.key, http.StatusNotFound)
		j.native(t, http.MethodPost, "/v1/delegated-tool-invocations/"+loss.requestID+"/outcome", map[string]any{"status": "outcome_unknown", "code": "worker_interrupted"}, j.key, http.StatusNotFound)
	} else {
		duplicate := j.native(t, http.MethodPost, "/v1/delegated-tool-invocations/"+loss.requestID+"/dispatch", map[string]any{}, j.key, http.StatusOK)
		if duplicate["dispatch_granted"] != false {
			t.Fatal("native API replayed dispatch permission")
		}
	}
	changed := j.invokeArgs("journey_echo", loss.requestID, j.nonce(t, "changed"))
	j.call(t, fresh, "gregale_invoke_tool", changed, true)
	j.invocationIDs = append(j.invocationIDs, loss.requestID)
}

func (j *mcpToolJourney) cancelInvocation(t *testing.T) {
	session := j.connectHosted(t, "2026-07-28", nil)
	id, nonce := uuid.NewString(), j.nonce(t, "cancel")
	j.call(t, session, "gregale_invoke_tool", j.invokeArgs("journey_wait", id, nonce), false)
	eventually(t, "application call before cancellation", 20*time.Second, 250*time.Millisecond, func() (bool, string) {
		r := get(j.preview + "/journey/calls/" + url.PathEscape(nonce))
		return r.status == http.StatusOK, "waiting for the exact synthetic nonce"
	})
	before := j.observe(t, nonce)
	if num(before, "count") != 1 {
		t.Fatal("wait tool was not dispatched exactly once")
	}
	j.call(t, session, "gregale_cancel_tool_invocation", map[string]any{"invocation_id": id}, false)
	status := j.waitInvocation(t, session, id, "outcome_unknown")
	if status["cancellation_requested"] != true || status["effects_may_have_occurred"] != true || str(status, "outcome.code") != "cancel_requested" {
		t.Fatal("cancellation claimed rollback or lost the recorded dispatched effect")
	}
	j.call(t, session, "gregale_invoke_tool", j.invokeArgs("journey_wait", id, nonce), false)
	if compact(before) != compact(j.observe(t, nonce)) {
		t.Fatal("cancelled-wait recovery repeated or rolled back the application call")
	}
}

func (j *mcpToolJourney) revokeAccess(t *testing.T) {
	session := j.connectHosted(t, "2026-07-28", nil)
	id := j.grants["journey_echo"]
	j.tg.ok(t, time.Minute, "mcp", "server", "tool-grant", "revoke", "--id", id)
	j.call(t, session, "gregale_describe_tool", map[string]any{"version": j.version, "name": "journey_echo"}, true)
	for _, invocation := range j.invocationIDs {
		j.call(t, session, "gregale_get_tool_invocation", map[string]any{"invocation_id": invocation}, true)
		j.native(t, http.MethodGet, "/v1/delegated-tool-invocations/"+invocation, nil, j.key, http.StatusNotFound)
		if record := j.invocation(t, invocation, "journey_echo"); str(record, "status") != "completed" {
			t.Fatal("owner lost retained historical evidence after revocation")
		}
	}
	newID, nonce := uuid.NewString(), j.nonce(t, "revoked")
	j.call(t, session, "gregale_invoke_tool", j.invokeArgs("journey_echo", newID, nonce), true)
	j.native(t, http.MethodGet, "/v1/tool-invocations/"+newID, nil, j.tg.token, http.StatusNotFound)
	if r := get(j.preview + "/journey/calls/" + url.PathEscape(nonce)); r.err != nil || r.status != http.StatusNotFound {
		t.Fatal("revoked invocation reached the application or could not be checked")
	}
	// A second grant remains active so key revocation is checked independently.
	j.tg.ok(t, time.Minute, "key", "delete", j.keyID)
	r := j.tg.apiCall(http.MethodGet, "/v1/tool-registry/"+j.version+"/tools/journey_wait", nil, j.key)
	if r.err != nil || (r.status != http.StatusUnauthorized && r.status != http.StatusForbidden && r.status != http.StatusNotFound) {
		t.Fatal("revoked native key retained granted tool access")
	}
	if j.private != nil {
		id, nonce := uuid.NewString(), j.nonce(t, "revoked-key")
		args := j.invokeArgs("journey_wait", id, nonce)
		r := j.tg.apiCall(http.MethodPost, "/v1/delegated-tool-invocations/"+id+"/execute", privateJourneyNativeRequest(args), j.key)
		if r.err != nil || (r.status != http.StatusUnauthorized && r.status != http.StatusForbidden && r.status != http.StatusNotFound) {
			t.Fatal("revoked key retained private execution authority")
		}
		j.native(t, http.MethodGet, "/v1/tool-invocations/"+id, nil, j.tg.token, http.StatusNotFound)
	}
}

func (j *mcpToolJourney) connectOwner(t *testing.T) *mcp.ClientSession {
	command := exec.CommandContext(j.ctx, j.tg.cli, "--non-interactive", "--json", "mcp", "serve", "--transport", "stdio", "--tools", "build-tools,registry", "--allow-writes")
	command.Env, command.Dir = j.tg.cliEnv(j.tg.token), j.tg.home
	if j.private != nil {
		command.Env = append(command.Env, privateToolTokenEnv+"="+j.private.token)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "tool-authoring-journey", Version: "1"}, nil).Connect(j.ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("local management CLI connect: %s", j.tg.redact(err.Error()))
	}
	j.root.Cleanup(func() { _ = session.Close() })
	return session
}

func (j *mcpToolJourney) connectHosted(t *testing.T, protocol string, loss *toolJourneyLostReply) *mcp.ClientSession {
	return j.connectHostedKey(t, protocol, loss, j.key)
}

func (j *mcpToolJourney) connectHostedKey(t *testing.T, protocol string, loss *toolJourneyLostReply, key string) *mcp.ClientSession {
	var transport http.RoundTripper = journeyAgentBearer{token: key}
	if loss != nil {
		loss.base = transport
		transport = loss
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "tool-invocation-journey", Version: "1"}, nil).Connect(j.ctx, &mcp.StreamableClientTransport{Endpoint: j.tg.mcpURL, HTTPClient: client, DisableStandaloneSSE: true}, &mcp.ClientSessionOptions{ProtocolVersion: protocol})
	if err != nil {
		t.Fatalf("hosted MCP connect: %s", j.tg.redact(err.Error()))
	}
	j.root.Cleanup(func() { _ = session.Close() })
	if session.InitializeResult().ProtocolVersion != protocol {
		t.Fatalf("hosted bridge negotiated %s, want %s", session.InitializeResult().ProtocolVersion, protocol)
	}
	return session
}

func (j *mcpToolJourney) call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any, wantError bool) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(j.ctx, 45*time.Second)
	defer cancel()
	result, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s protocol error: %s", name, j.tg.redact(err.Error()))
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal("invalid structured tool response")
	}
	j.assertPrivateIsolation(t, raw)
	if result.IsError != wantError {
		diagnostic, _ := json.Marshal(result.StructuredContent)
		t.Fatalf("%s error=%v, want %v: %s", name, result.IsError, wantError, j.tg.redact(tail(string(diagnostic), 2000)))
	}
	return object(t, raw)
}

func (j *mcpToolJourney) requireTools(t *testing.T, s *mcp.ClientSession, names ...string) {
	found := map[string]bool{}
	params := &mcp.ListToolsParams{}
	for pages := 0; pages < 64; pages++ {
		listed, err := s.ListTools(j.ctx, params)
		if err != nil {
			t.Fatalf("list tools: %s", j.tg.redact(err.Error()))
		}
		for _, tool := range listed.Tools {
			found[tool.Name] = true
		}
		if listed.NextCursor == "" {
			for _, name := range names {
				if !found[name] {
					t.Fatalf("missing %s: configure this bridge's toolsets and --allow-tool-invocation before running acceptance", name)
				}
			}
			return
		}
		if listed.NextCursor == params.Cursor {
			t.Fatal("tool catalog pagination did not advance")
		}
		params.Cursor = listed.NextCursor
	}
	t.Fatal("tool catalog exceeded bounded pagination")
}

func (j *mcpToolJourney) native(t *testing.T, method, path string, body any, key string, want int) map[string]any {
	t.Helper()
	r := j.tg.apiCall(method, path, body, key)
	if r.err != nil || r.status != want {
		t.Fatalf("%s %s: HTTP %d, want %d", method, path, r.status, want)
	}
	j.assertPrivateIsolation(t, r.body)
	return jsonBody(t, r)
}

func (j *mcpToolJourney) nonce(t *testing.T, label string) string {
	return j.tg.slug(label) + "-" + randomHex(t, 8)
}

func (j *mcpToolJourney) invokeArgs(name, id, nonce string) map[string]any {
	args, _ := json.Marshal(map[string]string{"nonce": nonce})
	if j.grantHistory == nil {
		j.grantHistory = map[string]string{}
	}
	if j.grantHistory[id] == "" {
		j.grantHistory[id] = j.grants[name]
	}
	out := map[string]any{"version": j.version, "name": name, "grant_id": j.grants[name], "request_id": id, "arguments_json": string(args), "access_mode": j.accessMode(), "allow_tool_execution": true, "timeout_seconds": 180}
	if j.private != nil {
		out["credential_binding"] = j.private.bindings[name]
	}
	return out
}

func (j *mcpToolJourney) accessMode() string {
	if j.private != nil {
		return "external-oauth"
	}
	return "open"
}

func (j *mcpToolJourney) invocation(t *testing.T, id, name string) map[string]any {
	record := j.native(t, http.MethodGet, "/v1/tool-invocations/"+id, nil, j.tg.token, http.StatusOK)
	grant := j.grants[name]
	if original := j.grantHistory[id]; original != "" {
		grant = original
	}
	if str(record, "id") != id || str(record, "intent.version") != j.version || str(record, "intent.name") != name || str(record, "target.app_id") != j.appID ||
		str(record, "target.deployment_id") != j.deployment || str(record, "delegation.grant_id") != grant || str(record, "delegation.key_id") != j.keyID || str(record, "dispatched_at") == "" {
		t.Fatal("native invocation lost its exact publication, deployment, key, grant or dispatch binding")
	}
	return record
}

func (j *mcpToolJourney) observe(t *testing.T, nonce string) map[string]any {
	r := get(j.preview + "/journey/calls/" + url.PathEscape(nonce))
	if r.err != nil || r.status != http.StatusOK {
		t.Fatal("exact runtime call observation unavailable")
	}
	return jsonBody(t, r)
}

func (j *mcpToolJourney) waitInvocation(t *testing.T, s *mcp.ClientSession, id, want string) map[string]any {
	var out map[string]any
	eventually(t, "durable invocation "+want, 2*time.Minute, time.Second, func() (bool, string) {
		out = j.call(t, s, "gregale_get_tool_invocation", map[string]any{"invocation_id": id}, false)
		status := str(out, "status")
		if status != "queued" && status != "running" && status != want {
			t.Fatalf("invocation ended %s, want %s; no automatic replay", status, want)
		}
		return status == want, status
	})
	return out
}

func (j *mcpToolJourney) describe(t *testing.T, s *mcp.ClientSession, name string) {
	var catalog []byte
	var first map[string]any
	for offset, pages := 0, 0; pages < 64; pages++ {
		doc := j.call(t, s, "gregale_describe_tool", map[string]any{"version": j.version, "name": name, "catalog_offset": offset}, false)
		if first == nil {
			first = doc
		}
		raw, next := toolJourneyChunk(t, doc, "catalog", offset)
		catalog = append(catalog, raw...)
		if next == -1 {
			binding := ""
			if j.private != nil {
				binding = j.private.bindings[name]
				if bytes.Contains(catalog, []byte(privateToolTokenEnv)) || bytes.Contains(mustJourneyJSON(first), []byte(privateToolTokenEnv)) {
					t.Fatal("private descriptor exposed the owner secret reference")
				}
			}
			if err := checkToolJourneyDescriptorBinding(first, catalog, j.version, j.appID, j.deployment, j.grants[name], name, j.accessMode(), binding); err != nil {
				t.Fatal(err)
			}
			return
		}
		offset = next
	}
	t.Fatal("catalog exceeded bounded pagination")
}

func (j *mcpToolJourney) result(t *testing.T, s *mcp.ClientSession, id string, first map[string]any) []byte {
	var raw []byte
	doc := first
	for offset, pages := 0, 0; pages < 64; pages++ {
		chunk, next := toolJourneyChunk(t, doc, "result", offset)
		raw = append(raw, chunk...)
		if next == -1 {
			j.assertPrivateIsolation(t, raw)
			return raw
		}
		offset = next
		doc = j.call(t, s, "gregale_get_tool_invocation", map[string]any{"invocation_id": id, "result_offset": offset}, false)
	}
	t.Fatal("result exceeded bounded pagination")
	return nil
}

func validJourneyRun(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}
