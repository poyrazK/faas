// adr: 20261103155008
package journeys

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPAppAgentCredentials exercises the released CLI, native authorization
// boundary, hosted MCP configuration workflow and connection revocation. It
// creates one empty app; no deployment or billed dependency is needed.
func TestMCPAppAgentCredentials(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug := tg.app(t, "mcp-agent")
	var token, tokenID string
	t.Cleanup(func() {
		if tokenID != "" {
			tg.run(t, time.Minute, "app", "deploy-token", "delete", tokenID, "--app", slug)
		}
	})
	s := newStory(t)
	if !s.step("mint_app_agent", func(t *testing.T) {
		created := tg.exec(t, tg.token, t.TempDir(), time.Minute, "deploy", "--create-only", "--app", "--name", slug)
		if created.exit != 0 {
			t.Fatalf("create empty app: %s", created.problem())
		}
		mint := object(t, tg.ok(t, time.Minute, "app", "deploy-token", "create", "--app", slug,
			"--agent", "--scopes", "deploy:write,env:write,usage:read", "--label", "journey-agent").stdout)
		token, tokenID = str(mint, "plaintext"), str(mint, "id")
		if token == "" || tokenID == "" {
			t.Fatal("mint response omitted the credential or id")
		}
	}) {
		return
	}
	s.check("native_app_boundary", func(t *testing.T) {
		own := tg.apiCall(http.MethodGet, "/v1/apps/"+slug, nil, token)
		if own.status != http.StatusOK {
			t.Fatalf("own app read: HTTP %d", own.status)
		}
		metadata := tg.apiCall(http.MethodGet, "/v1/account", nil, token)
		if metadata.status != http.StatusOK || str(jsonBody(t, metadata), "key_app.slug") != slug || str(jsonBody(t, metadata), "email") != "" {
			t.Fatal("principal metadata omitted its binding or exposed the account email")
		}
		for _, path := range []string{"/v1/apps", "/v1/keys", "/v1/billing/costs"} {
			if r := tg.apiCall(http.MethodGet, path, nil, token); r.status != http.StatusForbidden {
				t.Errorf("app-bound credential reached %s: HTTP %d", path, r.status)
			}
		}
	})
	s.check("native_inspection", func(t *testing.T) {
		preview := tg.apiCall(http.MethodGet, "/v1/apps/"+slug+"/operation-preview?operation=env_change", nil, token)
		if preview.status != http.StatusOK {
			t.Fatalf("native authorization preview: HTTP %d", preview.status)
		}
		policy := jsonBody(t, preview)
		if str(policy, "operation") != "env_change" || str(policy, "authorization") != "allowed" || str(policy, "admission") != "unknown" {
			t.Fatal("preview confused authorization with admission")
		}
		costs := tg.apiCall(http.MethodGet, "/v1/apps/"+slug+"/costs", nil, token)
		if costs.status != http.StatusOK {
			t.Fatalf("native app costs: HTTP %d", costs.status)
		}
		out := jsonBody(t, costs)
		if str(out, "app_slug") != slug || str(out, "app_id") != str(policy, "app_id") || out["bill_estimate_available"] != false {
			t.Fatal("costs omitted app identity or claimed a bill estimate")
		}
		for _, field := range []string{"account_id", "invoices", "forecast"} {
			if _, present := out[field]; present {
				t.Fatalf("app costs exposed %s", field)
			}
		}
	})
	s.check("app_read_resource_inventory", func(t *testing.T) {
		mint := object(t, tg.ok(t, time.Minute, "app", "deploy-token", "create", "--app", slug,
			"--agent", "--scopes", "apps:read", "--label", "journey-inventory-reader").stdout)
		reader, readerID := str(mint, "plaintext"), str(mint, "id")
		if reader == "" || readerID == "" {
			t.Fatal("missing app-read inventory credential")
		}
		defer tg.run(t, time.Minute, "app", "deploy-token", "delete", readerID, "--app", slug)
		for _, kind := range []string{"domains", "crons"} {
			r := tg.apiCall(http.MethodGet, "/v1/apps/"+slug+"/resource-inventory?kind="+kind+"&limit=1", nil, reader)
			if r.status != http.StatusOK {
				t.Fatalf("app read inventory %s: HTTP %d", kind, r.status)
			}
			out := jsonBody(t, r)
			if str(out, "kind") != kind || str(out, "scope") != "default" || str(out, "app_id") == "" {
				t.Fatal("inventory omitted its selection identity")
			}
		}
		for _, path := range []string{"/v1/domains", "/v1/postgres/databases"} {
			if r := tg.apiCall(http.MethodGet, path, nil, reader); r.status != http.StatusForbidden {
				t.Fatalf("reader gained account inventory %s: HTTP %d", path, r.status)
			}
		}
	})
	s.check("hosted_configuration_and_receipt", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		client := &http.Client{Transport: journeyAgentBearer{token: token}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		session, err := mcp.NewClient(&mcp.Implementation{Name: "gregale-journeys", Version: "1"}, nil).Connect(ctx,
			&mcp.StreamableClientTransport{Endpoint: tg.api + "/mcp", HTTPClient: client, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatalf("hosted MCP connect: %v", err)
		}
		defer func() { _ = session.Close() }()
		listed, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		catalog := map[string]bool{}
		for _, tool := range listed.Tools {
			catalog[tool.Name] = true
		}
		if !catalog["gregale_plan_env_change"] || !catalog["gregale_get_plan"] || !catalog["gregale_verify_plan"] || !catalog["gregale_get_restart"] ||
			!catalog["gregale_get_saved_plan"] || !catalog["gregale_check_plan"] || !catalog["gregale_list_actions"] || !catalog["gregale_get_action"] || !catalog["gregale_get_action_outcome"] ||
			catalog["gregale_get_trace"] || catalog["gregale_plan_deploy"] {
			t.Fatal("hosted default catalog was not core")
		}
		call := func(name string, args map[string]any) map[string]any {
			t.Helper()
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
			if err != nil || result.IsError {
				t.Fatalf("%s failed: %v", name, err)
			}
			raw, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			return object(t, raw)
		}
		actions := call("gregale_list_actions", map[string]any{"app": slug, "limit": 1})
		if items, ok := actions["items"].([]any); !ok || len(items) != 0 {
			t.Fatal("new app-bound connection did not return its empty action history")
		}
		supported, ok := actions["supported_actions"].([]any)
		scaling := false
		for _, action := range supported {
			if action == "operation_scale" {
				scaling = true
			}
		}
		if !ok || !scaling {
			t.Fatal("default action history did not advertise scaling recovery")
		}
		operationsClient := &http.Client{Transport: journeyAgentBearer{token: token, toolsets: "operate"}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		operations, err := mcp.NewClient(&mcp.Implementation{Name: "gregale-journeys-operate", Version: "1"}, nil).Connect(ctx,
			&mcp.StreamableClientTransport{Endpoint: tg.api + "/mcp", HTTPClient: operationsClient, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatalf("operations MCP connect: %v", err)
		}
		defer func() { _ = operations.Close() }()
		operationTools, err := operations.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		foundScale := false
		for _, tool := range operationTools.Tools {
			if tool.Name == "gregale_plan_scale" {
				foundScale = true
			}
		}
		if !foundScale {
			t.Fatal("agent key could not select the operations catalog")
		}
		scale, err := operations.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_plan_scale", Arguments: map[string]any{"app": slug, "reason": "journey catalog selection", "ram_mb": 512}})
		if err != nil || scale.IsError {
			t.Fatalf("operations catalog scale plan: %v", err)
		}
		rawScale, err := json.Marshal(scale.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		scaleID := str(object(t, rawScale), "plan_id")
		if scaleID == "" || str(call("gregale_get_plan", map[string]any{"plan_id": scaleID}), "plan_id") != scaleID {
			t.Fatal("catalog selections did not share the original plan")
		}
		plan := call("gregale_plan_env_change", map[string]any{"app": slug, "reason": "journey configuration", "values": map[string]string{"LOG_LEVEL": "debug", "FEATURE_MODE": "journey"}})
		id := str(plan, "plan_id")
		if id == "" || strings.Contains(compact(plan), "debug") {
			t.Fatal("configuration plan omitted its id or exposed private inputs")
		}
		first := call("gregale_apply_env_change", map[string]any{"plan_id": id})
		if str(first, "verification.status") != "not_required" || str(first, "verification.goal") != "configuration_commit" {
			t.Fatal("configuration-only receipt confused commit with runtime readiness")
		}
		outcome := call("gregale_get_action_outcome", map[string]any{"app": slug, "plan_id": id})
		if str(outcome, "outcome") != "verified_receipt" || str(outcome, "restart_status") != "not_requested" || strings.Contains(compact(outcome), "debug") || strings.Contains(compact(outcome), "LOG_LEVEL") {
			t.Fatal("configuration outcome did not preserve value-free commit and restart semantics")
		}
		replayed := call("gregale_apply_env_change", map[string]any{"plan_id": id})
		if compact(first) != compact(replayed) {
			t.Fatal("same-plan retry changed the batch receipt")
		}
		history := tg.apiCall(http.MethodGet, "/v1/apps/"+slug+"/config/revisions", nil, token)
		if history.status != http.StatusOK {
			t.Fatalf("batch history: HTTP %d", history.status)
		}
		rows, _ := jsonBody(t, history)["revisions"].([]any)
		if len(rows) != 2 || str(rows[0].(map[string]any), "source") != "env.batch" {
			t.Fatal("multi-key batch did not record exactly one change and one baseline")
		}
		if receipt := call("gregale_get_plan", map[string]any{"plan_id": id}); str(receipt, "status") != "completed" || str(receipt, "recovery.action") != "none" {
			t.Fatal("completed configuration receipt cannot be recovered")
		}
		stale := call("gregale_plan_env_change", map[string]any{"app": slug, "reason": "journey stale review", "values": map[string]string{"FEATURE_MODE": "old"}})
		fresh := call("gregale_plan_env_change", map[string]any{"app": slug, "reason": "journey competing change", "values": map[string]string{"FEATURE_MODE": "new"}})
		call("gregale_apply_env_change", map[string]any{"plan_id": str(fresh, "plan_id")})
		refused, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_env_change", Arguments: map[string]any{"plan_id": str(stale, "plan_id")}})
		if err != nil || !refused.IsError {
			t.Fatal("stale batch was not refused")
		}
		raw, err := json.Marshal(refused.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		failure := object(t, raw)
		if str(failure, "outcome") != "refused" || str(failure, "recovery.action") != "replan" || str(failure, "recovery.tool") != "gregale_plan_env_change" {
			t.Fatal("stale batch omitted deterministic recovery")
		}
		rollback, err := operations.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_plan_config_rollback", Arguments: map[string]any{"app": slug, "revision": int64(num(first, "result.revision")), "reason": "journey reviewed rollback"}})
		if err != nil || rollback.IsError {
			t.Fatalf("reviewed rollback plan: %v", err)
		}
		raw, err = json.Marshal(rollback.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		rollbackID := str(object(t, raw), "plan_id")
		applied, err := operations.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_operation", Arguments: map[string]any{"plan_id": rollbackID}})
		if err != nil || applied.IsError {
			t.Fatalf("reviewed rollback apply: %v", err)
		}
		rollbackOutcome := call("gregale_get_action_outcome", map[string]any{"app": slug, "plan_id": rollbackID})
		if str(rollbackOutcome, "outcome") != "verified_receipt" || strings.Contains(compact(rollbackOutcome), "FEATURE_MODE") || strings.Contains(compact(rollbackOutcome), "debug") {
			t.Fatal("rollback outcome omitted its value-free receipt")
		}
	})
	s.check("connection_revocation", func(t *testing.T) {
		if list := tg.ok(t, time.Minute, "mcp", "connection", "list"); !strings.Contains(string(list.stdout), tokenID) {
			t.Fatal("app-bound agent missing from connection inventory")
		}
		tg.ok(t, time.Minute, "mcp", "connection", "revoke", tokenID)
		if r := tg.apiCall(http.MethodGet, "/v1/apps/"+slug, nil, token); r.status != http.StatusUnauthorized && r.status != http.StatusForbidden {
			t.Fatalf("revoked credential still reached the app: HTTP %d", r.status)
		}
	})
}

// A real native restart must become observable through a reconnected app-bound
// MCP client. Polling verifies processing and fresh passive health; it never
// resubmits the configuration or restart request.
func TestMCPPostWriteVerification(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug := tg.app(t, "mcp-verify")
	tg.deployVersion(t, slug, "v1")
	minted := object(t, tg.ok(t, time.Minute, "app", "deploy-token", "create", "--app", slug,
		"--agent", "--scopes", "deploy:write,env:write", "--label", "journey-verification").stdout)
	token, tokenID := str(minted, "plaintext"), str(minted, "id")
	if token == "" || tokenID == "" {
		t.Fatal("missing app-agent identity")
	}
	t.Cleanup(func() { tg.run(t, time.Minute, "app", "deploy-token", "delete", tokenID, "--app", slug) })
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	client := &http.Client{Transport: journeyAgentBearer{token: token}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	connect := func() *mcp.ClientSession {
		t.Helper()
		session, err := mcp.NewClient(&mcp.Implementation{Name: "verification-journey", Version: "1"}, nil).Connect(ctx,
			&mcp.StreamableClientTransport{Endpoint: tg.api + "/mcp", HTTPClient: client, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatalf("MCP connect: %v", err)
		}
		return session
	}
	session := connect()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("%s: %v", name, err)
		}
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		return object(t, raw)
	}
	plan := call("gregale_plan_env_change", map[string]any{"app": slug, "reason": "journey restart verification", "values": map[string]string{"FEATURE_MODE": "verified"}, "restart": true})
	id := str(plan, "plan_id")
	applied := call("gregale_apply_env_change", map[string]any{"plan_id": id})
	if str(applied, "verification.status") != "required" || str(applied, "result.wake_id") == "" {
		t.Fatal("restart acceptance omitted verification target")
	}
	_ = session.Close()
	session = connect()
	defer func() { _ = session.Close() }()
	for {
		checked := call("gregale_verify_plan", map[string]any{"plan_id": id})
		status := str(checked, "verification.status")
		if status == "verified" {
			if str(checked, "verification.goal") != "restart_processing_and_health" || str(checked, "verification.checked_at") == "" {
				t.Fatal("verification omitted its evidence scope or time")
			}
			break
		}
		if status != "pending" && status != "unknown" {
			t.Fatalf("restart verification ended as %s", status)
		}
		select {
		case <-ctx.Done():
			t.Fatal("restart verification did not finish before deadline")
		case <-time.After(2 * time.Second):
		}
	}
	// The immutable mutation receipt remains distinct from a fresh verification.
	if replayed := call("gregale_apply_env_change", map[string]any{"plan_id": id}); compact(applied) != compact(replayed) {
		t.Fatal("verification changed the replay receipt")
	}
}

type journeyAgentBearer struct{ token, toolsets string }

func (b journeyAgentBearer) RoundTrip(req *http.Request) (*http.Response, error) {
	copyReq := req.Clone(req.Context())
	copyReq.Header.Set("Authorization", "Bearer "+b.token)
	if b.toolsets != "" {
		copyReq.Header.Set("Gregale-MCP-Toolsets", b.toolsets)
	}
	return http.DefaultTransport.RoundTrip(copyReq)
}
