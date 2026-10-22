// adr: 20261022192200
package mcpmanagement

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func environmentHistoryDetail(t *testing.T, m *management, id string) environmentActionDetail {
	t.Helper()
	out, err := m.getEnvironmentAction(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	guard := out.(*guardedRead)
	if err := guard.check(t.Context()); err != nil {
		t.Fatal(err)
	}
	return guard.value.(environmentActionDetail)
}

func environmentHistoryPage(t *testing.T, m *management, args listEnvironmentActionsArgs) environmentActionPage {
	t.Helper()
	out, err := m.listEnvironmentActions(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	guard := out.(*guardedRead)
	if err := guard.check(t.Context()); err != nil {
		t.Fatal(err)
	}
	return guard.value.(environmentActionPage)
}

func planHistoryEnvironment(t *testing.T, m *management, rollback bool) string {
	t.Helper()
	var out any
	var err error
	if rollback {
		out, err = m.planEnvironmentRollback(t.Context(), planEnvironmentRollbackArgs{Project: "shop", Environment: "production", PromotionID: "promotion-one", Reason: "PRIVATE_ENV_REASON"})
	} else {
		out, err = m.planEnvironmentPromotion(t.Context(), planEnvironmentPromotionArgs{Project: "shop", FromEnvironment: "staging", ToEnvironment: "production", Reason: "PRIVATE_ENV_REASON"})
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.(environmentPlan).ID
}

func applyHistoryEnvironment(t *testing.T, m *management, id string, rollback bool) error {
	t.Helper()
	if rollback {
		_, err := m.applyEnvironmentRollback(t.Context(), applyArgs{PlanID: id})
		return err
	}
	_, err := m.applyEnvironmentPromotion(t.Context(), applyEnvironmentPromotionArgs{PlanID: id})
	return err
}

func newHistoryEnvironment(t *testing.T, b *httptest.Server, opts Options) *management {
	t.Helper()
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEnvironmentActionHistoryMCPApprovalAndReceipts(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f, b := newEnvironmentFixture(t)
			f.protected = true
			cs, ctx := connectConfiguredTestServer(t, b, environmentTestOptions(b, true), protocol)
			id := environmentPlanID(t, callTool(t, ctx, cs, "gregale_plan_environment_promotion", environmentPlanArgs(), false))
			args := getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id}
			callTool(t, ctx, cs, "gregale_apply_environment_promotion", applyEnvironmentPromotionArgs{PlanID: id}, false)
			out := callTool(t, ctx, cs, "gregale_get_environment_action", args, false)
			var detail environmentActionView
			if json.Unmarshal(out["action"], &detail) != nil || detail.Apply.State != "approval_pending" || !detail.ApprovalRequired || detail.ApprovalExpiresAt == nil || detail.RetryTool != "gregale_apply_environment_promotion" || detail.Observation != nil {
				t.Fatalf("pending human review was lost or promoted to an outcome: %s", out)
			}
			f.mu.Lock()
			if f.writes != 0 || f.reviewRequests != 1 || f.approvalPolls != 0 {
				t.Fatal("history polled or bypassed human approval")
			}
			f.approved, f.ambiguous = true, true
			f.mu.Unlock()
			callTool(t, ctx, cs, "gregale_apply_environment_promotion", applyEnvironmentPromotionArgs{PlanID: id}, true)
			out = callTool(t, ctx, cs, "gregale_get_environment_action", args, false)
			if json.Unmarshal(out["action"], &detail) != nil || detail.Apply.State != "outcome_unknown" || detail.Observation != nil {
				t.Fatal("lost response fabricated a promotion receipt")
			}
			callTool(t, ctx, cs, "gregale_apply_environment_promotion", applyEnvironmentPromotionArgs{PlanID: id}, false)
			callTool(t, ctx, cs, "gregale_apply_environment_promotion", applyEnvironmentPromotionArgs{PlanID: id}, false)
			out = callTool(t, ctx, cs, "gregale_get_environment_action", args, false)
			if json.Unmarshal(out["action"], &detail) != nil || detail.Apply.State != "accepted" || detail.Apply.Receipt.PromotionID != "promotion-one" || detail.Observation == nil || detail.Observation.Arguments.Environment != "production" || detail.Observation.Arguments.PromotionID != "promotion-one" || string(out["native_completion"]) != `"not_observed"` {
				t.Fatal("acceptance was confused with native completion")
			}
			rollback := callTool(t, ctx, cs, "gregale_plan_environment_rollback", planEnvironmentRollbackArgs{Project: "shop", Environment: "production", PromotionID: "promotion-one", Reason: "PRIVATE_ENV_REASON"}, false)
			rollbackID := environmentPlanID(t, rollback)
			callTool(t, ctx, cs, "gregale_apply_environment_rollback", applyArgs{PlanID: rollbackID}, false)
			out = callTool(t, ctx, cs, "gregale_get_environment_action", getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: rollbackID}, false)
			if json.Unmarshal(out["action"], &detail) != nil || detail.Apply.State != "accepted" || detail.PromotionID != "promotion-one" || string(out["native_completion"]) != `"not_observed"` {
				t.Fatal("rollback receipt implied observed service recovery")
			}
			page := callTool(t, ctx, cs, "gregale_list_environment_actions", listEnvironmentActionsArgs{Project: "shop", Environment: "production"}, false)
			var items []environmentActionView
			if json.Unmarshal(page["items"], &items) != nil || len(items) != 2 {
				t.Fatal("environment actions were not discoverable")
			}
			data, _ := json.Marshal(page)
			for _, private := range []string{"PRIVATE_ENV_REASON", "exact-preview-token", "private-poll-code", "private-human-approval", "public-ticket", "review_url", "promotion_hash", "promotion_token", "approval_token", "changes", "workloads", "rolled_back", "completed"} {
				if strings.Contains(string(data), private) {
					t.Fatalf("environment history leaked private/native payload: %s", private)
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 3 || len(f.commits) != 2 || f.approvalPolls != 1 || f.keys[0] != f.keys[1] || f.bodies[0] != f.bodies[1] {
				t.Fatal("history or replay changed original request/approval identity")
			}
		})
	}
}

func TestEnvironmentActionHistoryDurableUnknownAndExpiry(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		for _, expire := range []bool{false, true} {
			t.Run(fmt.Sprintf("rollback=%t/expired=%t", rollback, expire), func(t *testing.T) {
				f, b := newEnvironmentFixture(t)
				store, path, key := testStateStore(t)
				opts := environmentTestOptions(b, true)
				opts.planStore, opts.planNamespace = store, "environment-history"
				m := newHistoryEnvironment(t, b, opts)
				id := planHistoryEnvironment(t, m, rollback)
				f.ambiguous = true
				if applyHistoryEnvironment(t, m, id, rollback) == nil {
					t.Fatal("lost response fixture succeeded")
				}
				if expire {
					m.mu.Lock()
					m.environmentPlans[id].ExpiresAt = time.Now().Add(-time.Second)
					err := m.persistEnvironmentPlans()
					m.mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				next, err := OpenStateStore(path, key)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = next.Close() })
				opts.planStore = next
				restored := newHistoryEnvironment(t, b, opts)
				detail := environmentHistoryDetail(t, restored, id)
				if detail.Action.Apply.State != "outcome_unknown" || detail.Action.PlanAvailable == expire || !detail.Durable || (detail.Action.Observation != nil) != rollback {
					t.Fatal("restart lost uncertainty, expiry or exact rollback target")
				}
				if expire {
					if detail.Action.RetryTool != "" || applyHistoryEnvironment(t, restored, id, rollback) == nil {
						t.Fatal("retained metadata extended original write authority")
					}
				} else {
					if detail.Action.RetryTool == "" || applyHistoryEnvironment(t, restored, id, rollback) != nil {
						t.Fatal("original retry handle was lost")
					}
					if applyHistoryEnvironment(t, restored, id, rollback) != nil || environmentHistoryDetail(t, restored, id).Action.Apply.State != "accepted" {
						t.Fatal("native receipt was not recovered")
					}
				}
				opts.AllowWrites = false
				reader := newHistoryEnvironment(t, b, opts)
				if environmentHistoryDetail(t, reader, id).Action.RetryTool != "" {
					t.Fatal("read-only history offered a mutation tool")
				}
				opts.planNamespace = "other-grant"
				other := newHistoryEnvironment(t, b, opts)
				if len(environmentHistoryPage(t, other, listEnvironmentActionsArgs{Project: "shop", Environment: "production"}).Items) != 0 {
					t.Fatal("another grant inherited environment history")
				}
				if _, err := other.getEnvironmentAction(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id}); err == nil {
					t.Fatal("known ID crossed grant boundaries")
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				want := 2
				if expire {
					want = 1
				}
				if f.writes != want || len(f.commits) != 1 {
					t.Fatal("history caused extra native intent")
				}
			})
		}
	}
}

func TestEnvironmentActionHistoryPendingRestartIsPassive(t *testing.T) {
	f, b := newEnvironmentFixture(t)
	f.protected = true
	store, path, key := testStateStore(t)
	opts := environmentTestOptions(b, true)
	opts.planStore, opts.planNamespace = store, "approval-history"
	m := newHistoryEnvironment(t, b, opts)
	id := planHistoryEnvironment(t, m, false)
	if applyHistoryEnvironment(t, m, id, false) != nil {
		t.Fatal("review request failed")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	opts.planStore = next
	m = newHistoryEnvironment(t, b, opts)
	f.approved = true
	if environmentHistoryDetail(t, m, id).Action.Apply.State != "approval_pending" {
		t.Fatal("history pretended to poll human approval")
	}
	if f.approvalPolls != 0 || f.writes != 0 {
		t.Fatal("history resumed approval or native execution")
	}
	if applyHistoryEnvironment(t, m, id, false) != nil || environmentHistoryDetail(t, m, id).Action.Apply.State != "accepted" {
		t.Fatal("original reviewed plan did not resume after restart")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-human-approval") || strings.Contains(string(data), "private-poll-code") {
		t.Fatal("approval credentials were not encrypted")
	}
}

func TestEnvironmentActionHistoryIdentityAndConsentRechecked(t *testing.T) {
	for _, change := range []string{"project", "source", "target", "app", "membership", "account", "source-consent", "target-consent"} {
		t.Run(change, func(t *testing.T) {
			f, b := newEnvironmentFixture(t)
			m := newHistoryEnvironment(t, b, environmentTestOptions(b, true))
			id := planHistoryEnvironment(t, m, false)
			args := getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id}
			result, err := m.getEnvironmentAction(t.Context(), args)
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			switch change {
			case "project":
				f.projectID = "replacement-project"
			case "source":
				f.stagingID = "replacement-source"
			case "target":
				f.productionID = "replacement-target"
			case "app":
				f.appID = "replacement-app"
			case "membership":
				f.extra = true
			case "account":
				f.account = "replacement-account"
			case "source-consent":
				delete(m.opts.Environments.Named, "shop/staging")
			case "target-consent":
				delete(m.opts.Environments.Named, "shop/production")
			}
			f.mu.Unlock()
			if result.(*guardedRead).check(t.Context()) == nil {
				t.Fatal("in-flight identity/consent change disclosed history")
			}
			if _, err := m.getEnvironmentAction(t.Context(), args); err == nil {
				t.Fatal("recreated identity or narrowed consent inherited history")
			}
		})
	}
}

func TestEnvironmentActionHistoryTargetChangesDuringSourceCheck(t *testing.T) {
	f, _ := newEnvironmentFixture(t)
	var replace atomic.Bool
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		f.serve(recorder, r)
		if r.URL.Path == "/v1/projects/shop/environments/staging" && replace.CompareAndSwap(true, false) {
			f.mu.Lock()
			f.productionID = "replacement-target"
			f.mu.Unlock()
		}
		for key, values := range recorder.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	}))
	t.Cleanup(b.Close)
	m := newHistoryEnvironment(t, b, environmentTestOptions(b, true))
	id := planHistoryEnvironment(t, m, false)
	result, err := m.getEnvironmentAction(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	replace.Store(true)
	if result.(*guardedRead).check(t.Context()) == nil {
		t.Fatal("target replacement during source verification disclosed history")
	}
}

func TestEnvironmentActionHistoryPaginationAndCursorScope(t *testing.T) {
	_, b := newEnvironmentFixture(t)
	opts := environmentTestOptions(b, true)
	opts.planNamespace = "first"
	m := newHistoryEnvironment(t, b, opts)
	for range 3 {
		planHistoryEnvironment(t, m, false)
	}
	args := listEnvironmentActionsArgs{Project: "shop", Environment: "production", Limit: 1}
	page := environmentHistoryPage(t, m, args)
	if len(page.Items) != 1 || page.NextBefore == "" {
		t.Fatal("missing environment history page")
	}
	planHistoryEnvironment(t, m, false)
	args.Limit, args.Before = 2, page.NextBefore
	next := environmentHistoryPage(t, m, args)
	if len(next.Items) != 2 || next.NextBefore != "" || next.Items[0].PlanID == page.Items[0].PlanID {
		t.Fatal("insertion shifted keyset continuation")
	}
	for _, invalidArgs := range []listEnvironmentActionsArgs{
		{Project: "shop", Environment: "production", Limit: -1},
		{Project: "shop", Environment: "production", Limit: api.ManagementMCPActionHistoryPageMax + 1},
		{Project: "shop", Environment: "production", Before: strings.Repeat("a", api.ManagementMCPActionCursorMaxBytes+1)},
		{Project: "shop", Environment: "production", Before: "invalid"},
		{Project: "shop", Environment: "staging", Before: page.NextBefore},
	} {
		if _, err := m.listEnvironmentActions(t.Context(), invalidArgs); err == nil {
			t.Fatal("invalid bounds or cross-target cursor accepted")
		}
	}
	opts.planNamespace = "second"
	other := newHistoryEnvironment(t, b, opts)
	if _, err := other.listEnvironmentActions(t.Context(), args); err == nil {
		t.Fatal("cursor crossed grant namespaces")
	}
}

func TestEnvironmentActionHistoryStoreFailureBlocksNativeWrite(t *testing.T) {
	f, b := newEnvironmentFixture(t)
	store, path, key := testStateStore(t)
	opts := environmentTestOptions(b, true)
	opts.planStore, opts.planNamespace = store, "failure-history"
	m := newHistoryEnvironment(t, b, opts)
	id := planHistoryEnvironment(t, m, false)
	dir := filepath.Dir(path)
	if err := os.Rename(dir, dir+"-moved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(dir+"-moved", dir) })
	if applyHistoryEnvironment(t, m, id, false) == nil || store.available() || f.writes != 0 {
		t.Fatal("failed atomic commit allowed a native mutation")
	}
	if _, err := m.getEnvironmentAction(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id}); err == nil {
		t.Fatal("uncommitted history escaped store failure")
	}
	if err := os.Rename(dir+"-moved", dir); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	opts.planStore = next
	if environmentHistoryDetail(t, newHistoryEnvironment(t, b, opts), id).Action.Apply.State != "not_attempted" {
		t.Fatal("failed commit changed the durable history")
	}
}

func TestEnvironmentActionHistorySharedCapacityAndOldJournal(t *testing.T) {
	_, b := newEnvironmentFixture(t)
	store, _, _ := testStateStore(t)
	opts := environmentTestOptions(b, true)
	opts.planStore, opts.planNamespace = store, "capacity-history"
	m := newHistoryEnvironment(t, b, opts)
	id := planHistoryEnvironment(t, m, false)
	m.environmentPlans[id].ExpiresAt = time.Now().Add(-time.Second)
	if err := m.persistEnvironmentPlans(); err != nil {
		t.Fatal(err)
	}
	// N-1 can fill its known history while ignoring archived environment rows.
	now := time.Now().UTC()
	legacy := actionHistorySnapshot{Version: 1, Items: map[string]actionRecord{}}
	for i := range api.ManagementMCPActionHistoryMax {
		p := &releasePlan{ID: fmt.Sprintf("%064x", i+1), Action: "rollback", AccountID: "account-one", APIBase: b.URL, App: "alpha", AppID: "app-one", Scope: "default", ExpiresAt: now.Add(-time.Minute)}
		e := recordAction(p)
		e.RecordedAt, e.UpdatedAt = now.Add(-time.Hour-time.Duration(i)*time.Second), now.Add(-time.Minute)
		legacy.Items[p.ID] = e
	}
	if err := store.update("actions/"+opts.planNamespace, legacy); err != nil {
		t.Fatal(err)
	}
	m = newHistoryEnvironment(t, b, opts)
	if len(m.actions)+len(m.environmentActions) != api.ManagementMCPActionHistoryMax || m.environmentActions[id].PlanID != id || len(m.environmentPlans) != 0 {
		t.Fatal("startup overwrote archived environment history or resurrected replay authority")
	}
	value, err := m.savePlan(&releasePlan{Action: "rollback", AccountID: "account-one", APIBase: b.URL, App: "alpha", AppID: "app-one", Scope: "default", ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	appID := value.(releasePlan).ID
	liveID := planHistoryEnvironment(t, m, false)
	if len(m.actions)+len(m.environmentActions) != api.ManagementMCPActionHistoryMax || m.actions[appID].PlanID == "" || m.environmentActions[liveID].PlanID == "" || m.environmentActions[id].PlanID == "" {
		t.Fatal("shared pruning evicted live plans or newest archive")
	}
	if environmentHistoryDetail(t, m, id).Action.PlanAvailable {
		t.Fatal("app persistence recreated an expired environment replay payload")
	}
	if err := store.update("oauth", oauthSnapshot{}); err != nil {
		t.Fatal(err)
	}
	var removed environmentActionSnapshot
	if err := store.read(environmentActionsNamespace+opts.planNamespace, &removed); err != nil || len(removed.Items) != 0 {
		t.Fatal("revocation retained environment action history")
	}
}

func TestEnvironmentActionHistoryLegacyImportAndExpiredApproval(t *testing.T) {
	f, b := newEnvironmentFixture(t)
	f.protected = true
	store, _, _ := testStateStore(t)
	opts := environmentTestOptions(b, true)
	opts.planStore, opts.planNamespace = store, "legacy-environment-history"
	m := newHistoryEnvironment(t, b, opts)
	id := planHistoryEnvironment(t, m, false)
	if err := store.update(environmentActionsNamespace+opts.planNamespace, nil); err != nil {
		t.Fatal(err)
	}
	beforeImport := time.Now().UTC()
	m = newHistoryEnvironment(t, b, opts)
	if e := environmentHistoryDetail(t, m, id).Action; e.RecordedAt.Before(beforeImport) || e.Apply.State != "not_attempted" {
		t.Fatal("legacy import invented creation time or lost the plan")
	}
	if applyHistoryEnvironment(t, m, id, false) != nil {
		t.Fatal("pending approval request failed")
	}
	m.environmentPlans[id].approvalRequest.ExpiresAt = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	if err := m.persistEnvironmentPlans(); err != nil {
		t.Fatal(err)
	}
	e := environmentHistoryDetail(t, m, id).Action
	if !e.PlanAvailable || e.Apply.State != "approval_pending" || e.RetryTool != "" {
		t.Fatal("expired human review was represented as valid approval")
	}
	if applyHistoryEnvironment(t, m, id, false) == nil || f.writes != 0 {
		t.Fatal("expired review bypassed native human approval")
	}
}

func TestEnvironmentActionHistoryRejectsInvalidSnapshotsAndReceipts(t *testing.T) {
	_, b := newEnvironmentFixture(t)
	m := newHistoryEnvironment(t, b, environmentTestOptions(b, true))
	id := planHistoryEnvironment(t, m, true)
	entry := m.environmentActions[id]
	for _, tc := range []struct {
		name    string
		version int
		mutate  func(*environmentActionRecord)
	}{
		{name: "future", version: 2},
		{name: "same-environment", version: 1, mutate: func(e *environmentActionRecord) { e.FromID = e.EnvironmentID }},
		{name: "invalid-retention", version: 1, mutate: func(e *environmentActionRecord) { e.RetainedUntil = e.RetainedUntil.Add(time.Hour) }},
		{name: "unbound-receipt", version: 1, mutate: func(e *environmentActionRecord) {
			e.Apply = environmentActionPhase{State: "accepted", Receipt: &environmentActionReceipt{PromotionID: "other-promotion"}}
		}},
		{name: "rollback-approval", version: 1, mutate: func(e *environmentActionRecord) { e.ApprovalRequired, e.Apply.State = true, "approval_pending" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, _ := testStateStore(t)
			e := entry
			if tc.mutate != nil {
				tc.mutate(&e)
			}
			if err := store.update(environmentActionsNamespace+"invalid", environmentActionSnapshot{Version: tc.version, Items: map[string]environmentActionRecord{id: e}}); err != nil {
				t.Fatal(err)
			}
			if _, err := NewServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL, planStore: store, planNamespace: "invalid"}); err == nil {
				t.Fatal("invalid environment snapshot exposed")
			}
		})
	}
	p := m.environmentPlans[id]
	p.attempted = true
	p.result = json.RawMessage(`{"promotion_id":"other-promotion","project_slug":"shop","from_environment":"staging","to_environment":"production","error":"PRIVATE_NATIVE_ERROR"}`)
	if recordEnvironmentAction(p).Apply.State != "outcome_unknown" {
		t.Fatal("mismatched native rollback receipt was accepted")
	}
}
