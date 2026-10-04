package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// adr: 584
func TestKeyedInvocationReplayIndependentEventRecovery(t *testing.T) {
	e := setup(t, api.PlanPro)
	work := seedAPIReceipt(t, e)
	ctx := context.Background()
	sub := work.RecipientSnapshot[1]
	rootID := state.PublishedEventInvocationID(e.acct.ID, "orders", "receipt-1", sub.ID)
	first := work.RecipientSnapshot[0]
	firstID := state.PublishedEventInvocationID(e.acct.ID, "orders", "receipt-1", first.ID)
	if _, err := e.store.EnqueueInvocation(ctx, state.Invocation{ID: firstID, AccountID: e.acct.ID, AppID: first.AppID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := forceInvocationState(t, e, firstID, "completed"); err != nil {
		t.Fatal(err)
	}
	policy := workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest, ExpiresAfter: time.Hour}
	root, err := e.store.EnqueueKeyedInvocation(ctx, state.Invocation{ID: rootID, AccountID: e.acct.ID, AppID: sub.AppID,
		Source: state.InvocationAsyncInvoke, WorkPolicyRevision: 7, Payload: json.RawMessage(`{"order":123}`), DueAt: time.Now()}, policy, "s:order-123")
	if err != nil {
		t.Fatal(err)
	}
	if err := finishKeyedReplayExecution(t, e, root.ID, false); err != nil {
		t.Fatal(err)
	}
	read := func() api.EventReceiptResponse {
		t.Helper()
		rec := e.do(t, "GET", eventReceiptURL("orders", "receipt-1"), nil, nil)
		var receipt api.EventReceiptResponse
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &receipt) != nil {
			t.Fatalf("receipt: %d %s", rec.Code, rec.Body)
		}
		return receipt
	}
	initial := read()
	if len(initial.Recipients[1].RecoveryActions) != 1 || initial.Recipients[1].RecoveryActions[0].Kind != "keyed_handler_replay" {
		t.Fatalf("safe action missing: %+v", initial.Recipients[1])
	}
	if rec := e.do(t, "POST", "/v1/invocations/"+root.ID+"/replay", nil, nil); rec.Code != http.StatusConflict {
		t.Fatalf("generic replay stripped lane: %d %s", rec.Code, rec.Body)
	}
	newer, err := e.store.EnqueueKeyedInvocation(ctx, state.Invocation{AccountID: e.acct.ID, AppID: sub.AppID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now()}, policy, "s:order-123")
	if err != nil {
		t.Fatal(err)
	}
	action := initial.Recipients[1].RecoveryActions[0]
	var child api.AsyncInvokeResponse
	for range 2 {
		rec := e.do(t, "POST", action.URL, nil, nil) // durable dedup also works without Idempotency-Key
		var result api.AsyncInvokeResponse
		if rec.Code != 202 || json.Unmarshal(rec.Body.Bytes(), &result) != nil {
			t.Fatalf("replay: %d %s", rec.Code, rec.Body)
		}
		if child.ID != "" && child.ID != result.ID {
			t.Fatal("duplicate request created a second child")
		}
		child = result
	}
	pending := read()
	if pending.Recipients[0].Execution.State != "completed" || pending.Recipients[0].Recovery != nil ||
		pending.Recipients[1].Execution.State != "failed" || pending.Recipients[1].Recovery.LatestReplay.State != "pending" || len(pending.Recipients[1].RecoveryActions) != 0 {
		t.Fatalf("independent pending recovery: %+v", pending)
	}
	if err := finishKeyedReplayExecution(t, e, newer.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := finishKeyedReplayExecution(t, e, child.ID, false); err != nil {
		t.Fatal(err)
	}
	failed := read().Recipients[1]
	if len(failed.RecoveryActions) != 1 || failed.RecoveryActions[0].URL != "/v1/invocations/"+child.ID+"/replay-keyed" {
		t.Fatalf("action did not target latest keyed child: %+v", failed)
	}
	rec := e.do(t, "POST", failed.RecoveryActions[0].URL, nil, map[string]string{"Idempotency-Key": "keyed-replay-next"})
	var next api.AsyncInvokeResponse
	if rec.Code != 202 || json.Unmarshal(rec.Body.Bytes(), &next) != nil {
		t.Fatalf("next replay: %d %s", rec.Code, rec.Body)
	}
	if err := finishKeyedReplayExecution(t, e, next.ID, true); err != nil {
		t.Fatal(err)
	}
	recovered := read().Recipients[1]
	if recovered.Execution.State != "failed" || recovered.Recovery.RetainedReplayCount != 2 ||
		recovered.Recovery.LatestReplay.State != "completed" || recovered.Recovery.LatestReplay.ReplayedFromInvocationID != child.ID || len(recovered.RecoveryActions) != 0 {
		t.Fatalf("recovery lineage: %+v", recovered)
	}
	// Pruning the child does not turn the same failed parent into fresh work.
	if _, err := e.store.DeleteInvocationsByIDs(ctx, []string{next.ID}); err != nil {
		t.Fatal(err)
	}
	if pruned := read().Recipients[1]; len(pruned.RecoveryActions) != 0 {
		t.Fatalf("pruned child still offers replay: %+v", pruned)
	}
	if rec := e.do(t, "POST", failed.RecoveryActions[0].URL, nil, nil); rec.Code != 409 {
		t.Fatalf("pruned child re-executed: %d %s", rec.Code, rec.Body)
	}
	foreign := setup(t, api.PlanPro)
	if rec := foreign.do(t, "POST", action.URL, nil, nil); rec.Code != 404 {
		t.Fatalf("foreign replay: %d %s", rec.Code, rec.Body)
	}
}

func TestKeyedInvocationReplayExpiryAndEligibility(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	appID := mustSeedApp(t, e, "keyed-expiry")
	policy := workpolicy.Policy{Name: "expiry", MaxRunningPerKey: 1, ExpiresAfter: 2 * time.Second}
	root, err := e.store.EnqueueKeyedInvocation(ctx, state.Invocation{AccountID: e.acct.ID, AppID: appID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now()}, policy, "s:expiring")
	if err != nil {
		t.Fatal(err)
	}
	if err := finishKeyedReplayExecution(t, e, root.ID, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(*root.WorkExpiresAt) + time.Millisecond)
	rec := e.do(t, "POST", "/v1/invocations/"+root.ID+"/replay-keyed", nil, nil)
	var problem api.Problem
	if rec.Code != 409 || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != "keyed_replay_expired" {
		t.Fatalf("expiry: %d %s", rec.Code, rec.Body)
	}
	id, _ := seedInvocation(t, e, "failed", state.InvocationAsyncInvoke)
	if rec := e.do(t, "POST", "/v1/invocations/"+id+"/replay-keyed", nil, nil); rec.Code != 409 {
		t.Fatalf("unkeyed recovery: %d %s", rec.Code, rec.Body)
	}
}

func finishKeyedReplayExecution(t *testing.T, e testEnv, id string, success bool) error {
	t.Helper()
	ctx := context.Background()
	claimed, err := e.store.ClaimInvocationWithCap(ctx, id, "test-inst", 30, 10)
	if err != nil {
		return err
	}
	if success {
		return e.store.CompleteKeyedInvocation(ctx, id, claimed.Attempts, nil)
	}
	return e.store.FailInvocation(ctx, id, "keyed handler failure", 0, 0, state.WithClaimAttempt(claimed.Attempts))
}

func TestKeyedInvocationReplayScope(t *testing.T) {
	for _, test := range []struct {
		scope  string
		status int
	}{
		{api.ScopeAppsRead, http.StatusForbidden},
		{api.ScopeDeployWrite, http.StatusNotFound},
	} {
		t.Run(test.scope, func(t *testing.T) {
			e := setupWithScopes(t, []string{test.scope})
			if rec := e.do(t, "POST", "/v1/invocations/missing/replay-keyed", nil, nil); rec.Code != test.status {
				t.Fatalf("scope: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestKeyedInvocationReplayPreservesCustomerAndBlocksSelfBypass(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	appID := mustSeedApp(t, e, "keyed-customer")
	tenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "customer", "Customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	issued := e.do(t, "POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{
		Name: "work", Scopes: []string{api.ScopePlatformTenantInvocationsManage}}, nil)
	var access api.CreatePlatformTenantAccessTokenResponse
	if issued.Code != http.StatusCreated || json.Unmarshal(issued.Body.Bytes(), &access) != nil {
		t.Fatalf("customer token: %d %s", issued.Code, issued.Body)
	}
	root, err := e.store.EnqueueKeyedInvocation(ctx, state.Invocation{AppID: appID, AccountID: e.acct.ID,
		PlatformTenantID: tenant.ID, DeploymentScope: "staging", Source: state.InvocationAsyncInvoke, DueAt: time.Now()},
		workpolicy.Policy{Name: "customer-work", MaxRunningPerKey: 1}, "s:order-123")
	if err != nil {
		t.Fatal(err)
	}
	if err := finishKeyedReplayExecution(t, e, root.ID, false); err != nil {
		t.Fatal(err)
	}
	self := e.do(t, "POST", "/v1/platform-tenant-self/invocations/"+root.ID+"/replay", nil, map[string]string{"Authorization": "Bearer " + access.Token})
	var problem api.Problem
	if self.Code != http.StatusConflict || json.Unmarshal(self.Body.Bytes(), &problem) != nil || problem.Code != "keyed_replay_requires_policy" {
		t.Fatalf("customer bypass: %d %s", self.Code, self.Body)
	}
	if _, err := e.store.SetPlatformTenantStatus(ctx, e.acct.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	path := "/v1/invocations/" + root.ID + "/replay-keyed"
	if rec := e.do(t, "POST", path, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("suspended admission: %d %s", rec.Code, rec.Body)
	}
	if _, err := e.store.SetPlatformTenantStatus(ctx, e.acct.ID, tenant.ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "POST", path, nil, nil)
	var result api.AsyncInvokeResponse
	if rec.Code != http.StatusAccepted || json.Unmarshal(rec.Body.Bytes(), &result) != nil {
		t.Fatalf("owner recovery: %d %s", rec.Code, rec.Body)
	}
	child, err := e.store.InvocationByID(ctx, result.ID)
	if err != nil || child.PlatformTenantID != tenant.ID || child.DeploymentScope != "staging" {
		t.Fatalf("captured customer/scope lost: %+v %v", child, err)
	}
}

func TestKeyedInvocationReplayDuplicateSurvivesPinExpiry(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	appID := mustSeedApp(t, e, "keyed-pins")
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := e.store.UpdateApp(ctx, appID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	old, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: appID, ImageDigest: "sha256:old"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	newer, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: appID, ImageDigest: "sha256:new"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, newer.ID); err != nil {
		t.Fatal(err)
	}
	root, err := e.store.EnqueueKeyedInvocation(ctx, state.Invocation{AppID: appID, AccountID: e.acct.ID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now(), Headers: json.RawMessage(`{"X-Gregale-Revision":"` + old.ID + `"}`)},
		workpolicy.Policy{Name: "pinned-orders", MaxRunningPerKey: 1}, "s:order-123")
	if err != nil {
		t.Fatal(err)
	}
	if err := finishKeyedReplayExecution(t, e, root.ID, false); err != nil {
		t.Fatal(err)
	}
	path := "/v1/invocations/" + root.ID + "/replay-keyed"
	first := e.do(t, "POST", path, nil, nil)
	var result api.AsyncInvokeResponse
	if first.Code != 202 || json.Unmarshal(first.Body.Bytes(), &result) != nil {
		t.Fatalf("initial replay: %d %s", first.Code, first.Body)
	}
	manifest.RevisionPinTTLSeconds = 0
	if _, err := e.store.UpdateApp(ctx, appID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.ResolveInvocationVersion(ctx, e.store, root); err == nil {
		t.Fatal("fixture pin did not expire")
	}
	duplicate := e.do(t, "POST", path, nil, nil)
	var repeated api.AsyncInvokeResponse
	if duplicate.Code != 202 || json.Unmarshal(duplicate.Body.Bytes(), &repeated) != nil || repeated.ID != result.ID {
		t.Fatalf("expired pin lost duplicate response: %d %s", duplicate.Code, duplicate.Body)
	}
	if err := finishKeyedReplayExecution(t, e, result.ID, false); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, "POST", "/v1/invocations/"+result.ID+"/replay-keyed", nil, nil); rec.Code != http.StatusGone {
		t.Fatalf("expired pin admitted new recovery: %d %s", rec.Code, rec.Body)
	}
}

func TestKeyedInvocationReplayRequiresMFA(t *testing.T) {
	h, acct, manager, sid := setupMW(t, api.PlanPro, true)
	cookie := reissueWithMFAFlag(t, manager, sid, acct.ID, true)
	rec := cookieDo(t, h, cookie, "POST", "/v1/invocations/missing/replay-keyed", nil)
	var problem api.Problem
	if rec.Code != http.StatusForbidden || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodeMFARequired {
		t.Fatalf("MFA gate: %d %s", rec.Code, rec.Body)
	}
}
