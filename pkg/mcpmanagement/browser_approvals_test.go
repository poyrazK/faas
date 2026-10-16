package mcpmanagement

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEnvironmentBrowserApprovalSurvivesRestartAndKeepsCredentialsPrivate(t *testing.T) {
	f, backend := newEnvironmentFixture(t)
	f.protected = true
	store, path, key := testStateStore(t)
	opts := environmentTestOptions(backend, true)
	opts.planStore, opts.planNamespace = store, "browser-grant"
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	plan := callTool(t, ctx, cs, "gregale_plan_environment_promotion", environmentPlanArgs(), false)
	id := environmentPlanID(t, plan)
	checkPrivate := func(value map[string]json.RawMessage) {
		t.Helper()
		encoded, _ := json.Marshal(value)
		if strings.Contains(string(encoded), "private-poll-code") || strings.Contains(string(encoded), "private-human-approval") {
			t.Fatal("private exchange material escaped to the agent")
		}
	}
	pending := callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": id}, false)
	checkPrivate(pending)
	if string(pending["status"]) != `"approval_pending"` || pending["review_url"] == nil {
		t.Fatal("missing human review link")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "private-poll-code") {
		t.Fatal("polling secret persisted in plaintext")
	}
	restored, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close() }()
	opts.planStore = restored
	cs, ctx = connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	checkPrivate(callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": id}, false))
	f.mu.Lock()
	if f.writes != 0 || f.reviewRequests != 1 || f.approvalPolls != 1 {
		t.Fatal("request duplicated after restart or pending review changed traffic")
	}
	f.approved, f.ambiguous = true, true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": id}, true)
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	opts.planStore = third
	cs, ctx = connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	result := callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": id}, false)
	checkPrivate(result)
	if result["promotion_id"] == nil {
		t.Fatal("approved promotion did not resume")
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
	fourth, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer fourth.Close()
	opts.planStore = fourth
	cs, ctx = connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	cached := callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": id}, false)
	checkPrivate(cached)
	if string(cached["promotion_id"]) != string(result["promotion_id"]) {
		t.Fatal("committed promotion receipt changed after restart")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.commits) != 1 || f.writes != 2 || f.keys[0] != f.keys[1] || f.bodies[0] != f.bodies[1] || !strings.Contains(f.bodies[1], "private-human-approval") || f.approvalPolls != 2 {
		t.Fatal("retry changed the approved payload or exchanged another credential")
	}
}

func TestEnvironmentBrowserApprovalStopsWhenPlanChanges(t *testing.T) {
	f, backend := newEnvironmentFixture(t)
	f.protected = true
	cs, ctx := connectConfiguredTestServer(t, backend, environmentTestOptions(backend, true), "2026-07-28")
	id := environmentPlanID(t, callTool(t, ctx, cs, "gregale_plan_environment_promotion", environmentPlanArgs(), false))
	callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": id}, false)
	f.mu.Lock()
	f.approved, f.hash = true, strings.Repeat("b", 64)
	f.mu.Unlock()
	result := callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": id}, true)
	if string(result["code"]) != `"plan_stale"` {
		t.Fatal("stale human review resumed promotion")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.approvalPolls != 0 || f.writes != 0 {
		t.Fatal("stale plan exchanged approval or wrote deployment state")
	}
}

func TestEnvironmentBrowserApprovalRefusalAndExpirySurviveRestart(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "refused", true: "expired"}[expired], func(t *testing.T) {
			f, backend := newEnvironmentFixture(t)
			f.protected = true
			store, path, key := testStateStore(t)
			opts := environmentTestOptions(backend, true)
			opts.planStore, opts.planNamespace = store, "refused-grant"
			_, manager, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			output, err := manager.planEnvironmentPromotion(t.Context(), planEnvironmentPromotionArgs{Project: "shop", FromEnvironment: "staging", ToEnvironment: "production", Reason: "human approval qualification"})
			if err != nil {
				t.Fatal(err)
			}
			id := output.(environmentPlan).ID
			if _, err := manager.applyEnvironmentPromotion(t.Context(), applyEnvironmentPromotionArgs{PlanID: id}); err != nil {
				t.Fatal(err)
			}
			if expired {
				manager.mu.Lock()
				manager.environmentPlans[id].approvalRequest.ExpiresAt = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
				err = manager.persistEnvironmentPlans()
				manager.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			} else {
				f.mu.Lock()
				f.approvalFailure = true
				f.mu.Unlock()
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			restored, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			opts.planStore = restored
			cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			for range 2 {
				result := callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": id}, true)
				want := `"approval_unavailable"`
				if expired {
					want = `"approval_expired"`
				}
				if string(result["code"]) != want {
					t.Fatalf("unexpected safe failure: %s", result["code"])
				}
				data, _ := json.Marshal(result)
				if strings.Contains(string(data), "private-") {
					t.Fatal("failed approval leaked private material")
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 0 || f.reviewRequests != 1 || expired && f.approvalPolls != 0 {
				t.Fatal("failed approval was renewed or changed target releases")
			}
		})
	}
}
