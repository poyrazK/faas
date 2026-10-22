// adr: 20261022161757
// adr: 20261022183756
package mcpmanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func actionDetail(t *testing.T, m *management, args getActionArgs) actionHistoryDetail {
	t.Helper()
	result, err := m.getAction(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	guard := result.(*guardedRead)
	if err := guard.check(t.Context()); err != nil {
		t.Fatal(err)
	}
	return guard.value.(actionHistoryDetail)
}

func actionPage(t *testing.T, m *management, args listActionsArgs) actionHistoryPage {
	t.Helper()
	result, err := m.listActions(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	guard := result.(*guardedRead)
	if err := guard.check(t.Context()); err != nil {
		t.Fatal(err)
	}
	return guard.value.(actionHistoryPage)
}

func TestActionHistoryMCPReceiptsAreReadOnlyAndRedacted(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f, _, _ := newReleaseFixture(t, true)
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			t.Cleanup(backend.Close)
			cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true}, protocol)
			plan := callTool(t, ctx, cs, "gregale_plan_deploy", map[string]any{"app": "alpha", "image": testImage, "reason": "PRIVATE_REASON"}, false)
			id := planID(t, plan)
			f.mu.Lock()
			f.ambiguous = true
			f.mu.Unlock()
			callTool(t, ctx, cs, "gregale_apply_deploy", applyArgs{PlanID: id}, true)
			page := callTool(t, ctx, cs, "gregale_list_actions", listActionsArgs{App: "alpha"}, false)
			var items []actionView
			if json.Unmarshal(page["items"], &items) != nil || len(items) != 1 || items[0].Apply.State != "outcome_unknown" || items[0].RetryTool != "gregale_apply_deploy" {
				t.Fatalf("unknown attempt was lost: %s", page)
			}
			callTool(t, ctx, cs, "gregale_apply_deploy", applyArgs{PlanID: id}, false)
			callTool(t, ctx, cs, "gregale_promote_deployment", applyArgs{PlanID: id}, false)
			out := callTool(t, ctx, cs, "gregale_get_action", getActionArgs{App: "alpha", PlanID: id}, false)
			var entry actionView
			if json.Unmarshal(out["action"], &entry) != nil || entry.Apply.State != "accepted" || entry.Promotion == nil || entry.Promotion.State != "accepted" || entry.Apply.Receipt.DeploymentID != "release-new" || string(out["native_completion"]) != `"not_observed"` || string(out["durable"]) != "false" {
				t.Fatalf("receipt overstated completion: %s", out)
			}
			encoded, _ := json.Marshal(out)
			for _, secret := range []string{"PRIVATE_REASON", testImage, "configuration_hash", "expected_review", "mcp-deploy-"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatalf("history included private plan data: %s", secret)
				}
			}
			rollback := callTool(t, ctx, cs, "gregale_plan_rollback", planRollbackArgs{App: "alpha", TargetDeploymentID: "release-old", Reason: "PRIVATE_ROLLBACK"}, false)
			rollbackID := planID(t, rollback)
			callTool(t, ctx, cs, "gregale_apply_rollback", applyArgs{PlanID: rollbackID}, false)
			out = callTool(t, ctx, cs, "gregale_get_action", getActionArgs{App: "alpha", PlanID: rollbackID}, false)
			if json.Unmarshal(out["action"], &entry) != nil || entry.Apply.Receipt.RollbackOperationID != "rollback-one" || entry.Apply.State != "accepted" {
				t.Fatalf("asynchronous rollback receipt missing: %s", out)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 3 {
				t.Fatalf("history or replay caused an extra mutation: %d", f.writes)
			}
		})
	}
}

func TestActionHistoryLostResponseRestartAndExpiredAuthority(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%t", expire), func(t *testing.T) {
			store, path, key := testStateStore(t)
			f, _, _ := newReleaseFixture(t, true)
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			t.Cleanup(backend.Close)
			opts := Options{APIBase: backend.URL, AllowWrites: true, planStore: store, planNamespace: "first"}
			_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := m.planDeploy(t.Context(), planDeployArgs{App: "alpha", Image: testImage, Reason: "PRIVATE_REASON"})
			if err != nil {
				t.Fatal(err)
			}
			id := plan.(releasePlan).ID
			f.mu.Lock()
			f.ambiguous = true
			f.mu.Unlock()
			if _, err := m.applyDeploy(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("lost response fixture succeeded")
			}
			if expire {
				m.mu.Lock()
				m.plans[id].ExpiresAt = time.Now().Add(-time.Second)
				err = m.persistPlans()
				m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			opts.planStore = reopened
			_, restored, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			detail := actionDetail(t, restored, getActionArgs{App: "alpha", PlanID: id})
			if !detail.Durable || detail.Action.Apply.State != "outcome_unknown" || detail.Action.PlanAvailable == expire {
				t.Fatalf("restart changed recorded uncertainty/authority: %+v", detail)
			}
			if expire {
				if detail.Action.RetryTool != "" || detail.Action.NextStep != "inspect_native_state_before_replanning" || len(restored.plans) != 0 {
					t.Fatal("expired history reconstructed retry authority")
				}
				if _, err := restored.applyDeploy(t.Context(), applyArgs{PlanID: id}); err == nil {
					t.Fatal("expired recorded plan applied")
				}
			} else {
				if _, err := restored.applyDeploy(t.Context(), applyArgs{PlanID: id}); err != nil {
					t.Fatal(err)
				}
				if actionDetail(t, restored, getActionArgs{App: "alpha", PlanID: id}).Action.Apply.State != "accepted" {
					t.Fatal("recovered receipt not recorded")
				}
			}
			opts.planNamespace = "other"
			_, other, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(actionPage(t, other, listActionsArgs{App: "alpha"}).Items) != 0 {
				t.Fatal("another grant saw action history")
			}
			if _, err := other.getAction(t.Context(), getActionArgs{App: "alpha", PlanID: id}); err == nil {
				t.Fatal("another grant inspected a known plan ID")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 1 {
				t.Fatalf("restart/recovery duplicated native write: %d", f.writes)
			}
		})
	}
}

func TestActionHistoryTriageConsentAndNamedIdentity(t *testing.T) {
	f, backend, opts := newTriageFixture(t)
	f.scope = "staging"
	opts.Environments.Named = map[string]EnvironmentGrant{"shop/staging": {Project: "shop", ProjectID: "project-one", Environment: "staging", EnvironmentID: "env-one", Apps: map[string]string{"alpha": f.app}, Write: true}}
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	output, err := m.planRegressionAction(t.Context(), regressionActionArgs{App: "alpha", Project: "shop", Environment: "staging", DeploymentID: discoveryDeploymentID, Route: "GET /orders", Action: "resolve", Reason: "PRIVATE_TRIAGE_REASON"})
	if err != nil {
		t.Fatal(err)
	}
	id := output.(releasePlan).ID
	if _, err := m.applyRegressionAction(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	args := getActionArgs{App: "alpha", Project: "shop", Environment: "staging", PlanID: id}
	detail := actionDetail(t, m, args)
	if detail.Action.Apply.State != "committed" || detail.Action.Apply.Receipt.TriageState != "resolved" || detail.NativeCompletion != "not_observed" {
		t.Fatalf("triage commit was confused with service recovery: %+v", detail)
	}
	encoded, _ := json.Marshal(detail)
	for _, private := range []string{"PRIVATE_TRIAGE_REASON", "PRIVATE_SQL", "GET /orders", "regression_triage", "proposed"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("history leaked diagnostic payload: %s", private)
		}
	}
	if _, err := m.getAction(t.Context(), getActionArgs{App: "alpha", PlanID: id}); err == nil {
		t.Fatal("default read found named-environment action")
	}
	result, err := m.getAction(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	delete(m.opts.Environments.RequestReadApps, "alpha")
	if err := result.(*guardedRead).check(t.Context()); err == nil {
		t.Fatal("in-flight diagnostic consent removal released history")
	}
	if _, err := m.getAction(t.Context(), args); err == nil {
		t.Fatal("triage history ignored revoked diagnostic consent")
	}
	if len(actionPage(t, m, listActionsArgs{App: "alpha", Project: "shop", Environment: "staging"}).Items) != 0 {
		t.Fatal("list disclosed triage after diagnostic consent removal")
	}
	m.opts.Environments.RequestReadApps["alpha"] = f.app
	result, err = m.getAction(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.environmentID = "replacement-environment"
	f.mu.Unlock()
	if err := result.(*guardedRead).check(t.Context()); err == nil {
		t.Fatal("recreated environment inherited history")
	}
}

func TestActionHistoryPaginationIdentityAndLegacyImport(t *testing.T) {
	store, _, _ := testStateStore(t)
	f, _, _ := newReleaseFixture(t, true)
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(backend.Close)
	opts := Options{APIBase: backend.URL, AllowWrites: true, planStore: store, planNamespace: "legacy", AllowedApps: map[string]string{"alpha": "app-one"}}
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := m.planDeploy(t.Context(), planDeployArgs{App: "alpha", Image: testImage, Reason: "review"}); err != nil {
			t.Fatal(err)
		}
	}
	page := actionPage(t, m, listActionsArgs{App: "alpha", Limit: 1})
	if len(page.Items) != 1 || page.NextBefore == "" {
		t.Fatalf("missing continuation: %+v", page)
	}
	if _, err := m.planDeploy(t.Context(), planDeployArgs{App: "alpha", Image: testImage, Reason: "newer"}); err != nil {
		t.Fatal(err)
	}
	next := actionPage(t, m, listActionsArgs{App: "alpha", Limit: 2, Before: page.NextBefore})
	if len(next.Items) != 2 || next.NextBefore != "" || next.Items[0].PlanID == page.Items[0].PlanID {
		t.Fatal("a new plan shifted keyset pagination")
	}
	for _, args := range []listActionsArgs{{App: "alpha", Limit: -1}, {App: "alpha", Limit: api.ManagementMCPActionHistoryPageMax + 1}, {App: "alpha", Before: "invalid"}, {App: "alpha", Before: strings.Repeat("x", api.ManagementMCPActionCursorMaxBytes+1)}} {
		if _, err := m.listActions(t.Context(), args); err == nil {
			t.Fatal("invalid bounds/cursor accepted")
		}
	}
	if err := store.update("actions/legacy", nil); err != nil {
		t.Fatal(err)
	}
	opts.AllowWrites = false
	_, restored, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(actionPage(t, restored, listActionsArgs{App: "alpha"}).Items) != 4 {
		t.Fatal("unexpired legacy plans were not imported for read-only inspection")
	}
	result, err := restored.listActions(t.Context(), listActionsArgs{App: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.account = "other-account"
	f.mu.Unlock()
	if result.(*guardedRead).check(t.Context()) == nil || len(actionPage(t, restored, listActionsArgs{App: "alpha"}).Items) != 0 {
		t.Fatal("account change released another account's history")
	}
	if _, err := restored.listActions(t.Context(), listActionsArgs{App: "alpha", Before: page.NextBefore}); err == nil {
		t.Fatal("cursor crossed account binding")
	}
	f.mu.Lock()
	f.account, f.app.ID = "account-one", "replacement-app"
	f.mu.Unlock()
	if _, err := restored.listActions(t.Context(), listActionsArgs{App: "alpha"}); err == nil {
		t.Fatal("recreated app inherited consent")
	}
	if err := store.update("oauth", oauthSnapshot{}); err != nil {
		t.Fatal(err)
	}
	var removed actionHistorySnapshot
	if err := store.read("actions/legacy", &removed); err != nil || len(removed.Items) != 0 {
		t.Fatal("OAuth revocation did not delete action history")
	}
}

func TestActionHistoryRetentionCapacityAndInvalidSnapshots(t *testing.T) {
	now := time.Now().UTC()
	m := &management{plans: map[string]*releasePlan{}, actions: map[string]actionRecord{}}
	for i := range api.ManagementMCPActionHistoryMax + 2 {
		p := &releasePlan{ID: fmt.Sprintf("%064x", i), Action: "rollback", AccountID: "account-one", APIBase: "https://api.example", App: "alpha", AppID: "app-one", Scope: "default", ExpiresAt: now.Add(-time.Minute)}
		entry := recordAction(p)
		entry.RecordedAt, entry.UpdatedAt = now.Add(time.Duration(i-1000)*time.Second), now.Add(-time.Second)
		m.actions[p.ID] = entry
		if i == 0 {
			p.ExpiresAt = now.Add(time.Minute)
			m.plans[p.ID] = p
		}
	}
	expired := recordAction(&releasePlan{ID: strings.Repeat("f", 64), Action: "rollback", ExpiresAt: now.Add(-api.ManagementMCPActionHistoryRetention)})
	m.actions[expired.PlanID] = expired
	if err := m.syncActionHistory(now); err != nil {
		t.Fatal(err)
	}
	if len(m.actions) != api.ManagementMCPActionHistoryMax || m.actions[fmt.Sprintf("%064x", 0)].PlanID == "" || m.actions[expired.PlanID].PlanID != "" || m.actions[fmt.Sprintf("%064x", 1)].PlanID != "" {
		t.Fatal("retention/capacity pruning lost active plans or retained oldest history")
	}
	if m.actionView(m.actions[fmt.Sprintf("%064x", 3)], now).NextStep != "inspect_native_state_before_replanning" {
		t.Fatal("expired historical phase encouraged replanning without native inspection")
	}
	store, _, _ := testStateStore(t)
	if err := store.update("actions/test", actionHistorySnapshot{Version: 2}); err != nil {
		t.Fatal(err)
	}
	client := api.NewClient("https://api.example", testToken)
	if _, err := NewServer(client, Options{APIBase: "https://api.example", planStore: store, planNamespace: "test"}); err == nil {
		t.Fatal("future action history version accepted")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := (&management{opts: Options{planStore: store}}).actionTarget(context.Background(), appArgs{App: "alpha"}); err == nil {
		t.Fatal("unavailable store released uncommitted history")
	}
}

func TestActionHistoryRemoteRefreshGrantIsolationAndRevocation(t *testing.T) {
	store, _, _ := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, true, HTTPOptions{Onboarding: "api-key", StateStore: store})
	var tokens tokenResponse
	response := f.exchange(f.authorizeAccount("fp_test_alice_remote_key", true), testVerifier, f.resource, f.clientID)
	decodeResponse(t, response, &tokens)
	_ = response.Body.Close()
	cs, ctx := f.connect(tokens, "2026-07-28")
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_deploy", planDeployArgs{App: "alpha", Image: testImage, Reason: "PRIVATE_REFRESH_REASON"}, false))
	f.mu.Lock()
	f.apiHook = func(w http.ResponseWriter, r *http.Request, account string) bool {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/apps/alpha" {
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: account + "-app", Slug: "alpha", Type: "app", Manifest: api.AppManifest{ExecutionMode: api.ExecutionModeWorker}})
			return true
		}
		return false
	}
	f.mu.Unlock()
	workloadID := planID(t, callTool(t, ctx, cs, "gregale_plan_workload_rollout", workloadPlanArgs(), false))
	f.mu.Lock()
	f.apiHook = nil
	f.mu.Unlock()
	_ = cs.Close()
	refresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {f.clientID}, "refresh_token": {tokens.RefreshToken}}.Encode()
	var rotated tokenResponse
	response = f.request("POST", "/oauth/token", strings.NewReader(refresh), "application/x-www-form-urlencoded", "")
	decodeResponse(t, response, &rotated)
	_ = response.Body.Close()
	cs, ctx = f.connect(rotated, "2026-07-28")
	result := callTool(t, ctx, cs, "gregale_get_action", getActionArgs{App: "alpha", PlanID: id}, false)
	var entry actionView
	if json.Unmarshal(result["action"], &entry) != nil || entry.PlanID != id || !entry.PlanAvailable {
		t.Fatal("OAuth refresh lost the original action history")
	}
	result = callTool(t, ctx, cs, "gregale_get_action", getActionArgs{App: "alpha", PlanID: workloadID}, false)
	if json.Unmarshal(result["action"], &entry) != nil || entry.PlanID != workloadID || entry.Workload == nil || entry.Workload.Mode != api.ExecutionModeWorker {
		t.Fatal("OAuth refresh lost companion workload history")
	}
	_ = cs.Close()
	var independent tokenResponse
	response = f.exchange(f.authorizeAccount("fp_test_alice_remote_key", true), testVerifier, f.resource, f.clientID)
	decodeResponse(t, response, &independent)
	_ = response.Body.Close()
	other, otherCtx := f.connect(independent, "2026-07-28")
	page := callTool(t, otherCtx, other, "gregale_list_actions", listActionsArgs{App: "alpha"}, false)
	if string(page["items"]) != "[]" {
		t.Fatal("a new grant for the same client/account inherited action history")
	}
	callTool(t, otherCtx, other, "gregale_get_action", getActionArgs{App: "alpha", PlanID: id}, true)
	callTool(t, otherCtx, other, "gregale_get_action", getActionArgs{App: "alpha", PlanID: workloadID}, true)
	response = f.request("POST", "/oauth/token", strings.NewReader(refresh), "application/x-www-form-urlencoded", "")
	_ = response.Body.Close()
	denied := f.request("POST", "/mcp", strings.NewReader(`{}`), "application/json", rotated.AccessToken)
	_ = denied.Body.Close()
	if denied.StatusCode != http.StatusUnauthorized {
		t.Fatal("revoked grant retained remote access")
	}
	store.mu.Lock()
	for namespace := range store.records {
		if strings.HasPrefix(namespace, "actions/") || strings.HasPrefix(namespace, workloadActionsNamespace) {
			store.mu.Unlock()
			t.Fatal("revocation retained the original grant's stored action history")
		}
	}
	store.mu.Unlock()
	callTool(t, otherCtx, other, "gregale_context", noArgs{}, false)
}
