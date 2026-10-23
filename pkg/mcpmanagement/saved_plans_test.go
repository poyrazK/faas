// adr: 20261023184309
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func checkedSavedReview(t *testing.T, result any, err error) savedPlanReview {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	guard := result.(*guardedRead)
	if err := guard.check(t.Context()); err != nil {
		t.Fatal(err)
	}
	return guard.value.(savedPlanReview)
}

func savedAppReview(t *testing.T, m *management, args getActionArgs) savedPlanReview {
	t.Helper()
	result, err := m.getPlan(t.Context(), args)
	return checkedSavedReview(t, result, err)
}

func savedEnvironmentReview(t *testing.T, m *management, id string) savedPlanReview {
	t.Helper()
	result, err := m.getEnvironmentPlan(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id})
	return checkedSavedReview(t, result, err)
}

func assertSavedReview(t *testing.T, out map[string]json.RawMessage, id string) map[string]json.RawMessage {
	t.Helper()
	if string(out["review_kind"]) != `"saved_original_proposal"` || string(out["apply_eligibility"]) != `"not_checked"` || string(out["native_completion"]) != `"not_observed"` {
		t.Fatal("saved review implied current eligibility or native completion")
	}
	var p map[string]json.RawMessage
	if json.Unmarshal(out["plan"], &p) != nil || string(p["plan_id"]) != fmt.Sprintf("%q", id) {
		t.Fatal("original review identity missing")
	}
	for _, key := range []string{`"request_id"`, `"approval_token"`, `"promotion_token"`, `"promotion_hash"`, `"exchange_code"`, `"review_url"`, `"ticket"`, `"secret_changes"`, `"environment_changes"`} {
		if bytes.Contains(out["plan"], []byte(key)) {
			t.Fatalf("saved review exposed private field %s", key)
		}
	}
	return p
}

func TestSavedPlanMCPOriginalDeployAndRollout(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, kind := range []string{"image", "source", "rollback", "service", "worker"} {
			t.Run(protocol+"/"+kind, func(t *testing.T) {
				f, _, _ := newReleaseFixture(t, false)
				if kind == "service" || kind == "worker" {
					f.app.Manifest.ExecutionMode = kind
				}
				backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
				t.Cleanup(backend.Close)
				cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true, Token: testToken}, protocol)
				tool, args, apply := "gregale_plan_deploy", map[string]any{"app": "alpha", "image": testImage, "reason": "review original intent"}, "gregale_apply_deploy"
				switch kind {
				case "source":
					tool, args = "gregale_plan_source_deploy", sourcePlanArgs()
				case "rollback":
					tool, args, apply = "gregale_plan_rollback", map[string]any{"app": "alpha", "target_deployment_id": "release-old", "reason": "review rollback"}, "gregale_apply_rollback"
				case "service", "worker":
					tool, apply = "gregale_plan_workload_rollout", "gregale_apply_workload_rollout"
				}
				original := callTool(t, ctx, cs, tool, args, false)
				id := planID(t, original)
				f.mu.Lock()
				f.app.RAMMB++
				f.sourceHead = strings.Repeat("b", 40)
				f.mu.Unlock()
				out := callTool(t, ctx, cs, "gregale_get_plan", getActionArgs{App: "alpha", PlanID: id}, false)
				p := assertSavedReview(t, out, id)
				if !bytes.Equal(original["expires_at"], p["expires_at"]) {
					t.Fatal("review renewed the original expiry")
				}
				if kind == "source" {
					if !bytes.Contains(p["source"], []byte(strings.Repeat("a", 40))) || bytes.Contains(p["source"], []byte(strings.Repeat("b", 40))) {
						t.Fatal("review re-resolved the source branch")
					}
				} else if !bytes.Equal(p["image"], original["image"]) {
					t.Fatal("review replaced the original artifact")
				}
				history := callTool(t, ctx, cs, "gregale_get_action", getActionArgs{App: "alpha", PlanID: id}, false)
				var view actionView
				if json.Unmarshal(history["action"], &view) != nil || view.Review == nil || view.Review.Tool != "gregale_get_plan" {
					t.Fatal("history did not provide a review call")
				}
				callTool(t, ctx, cs, view.Review.Tool, view.Review.Arguments, false)
				callTool(t, ctx, cs, apply, applyArgs{PlanID: id}, true)
				f.mu.Lock()
				defer f.mu.Unlock()
				if f.writes != 0 {
					t.Fatal("review or stale apply changed native intent")
				}
			})
		}
	}
}

func TestSavedPlanMCPBudgetReadOnly(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f, b, opts := newBudgetFixture(t)
			opts.AllowWrites = false
			cs, ctx := connectConfiguredTestServer(t, b, opts, protocol)
			original := callTool(t, ctx, cs, "gregale_plan_route_budgets", budgetArgs(), false)
			id := planID(t, original)
			out := callTool(t, ctx, cs, "gregale_get_plan", budgetHistorySelector(id), false)
			p := assertSavedReview(t, out, id)
			var budget struct {
				Proposed api.SetEnvironmentRouteBudgetsRequest `json:"proposed"`
			}
			if json.Unmarshal(p["route_budgets"], &budget) != nil || budget.Proposed.RequestID != "" || budget.Proposed.ExpectedAppRevision == nil || *budget.Proposed.ExpectedAppRevision != 0 || len(budget.Proposed.Routes) != 1 || budget.Proposed.Routes[0].Path != "/checkout" {
				t.Fatal("review lost the exact budget proposal or exposed its replay key")
			}
			callTool(t, ctx, cs, "gregale_get_plan", getActionArgs{App: "alpha", PlanID: id}, true)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 0 {
				t.Fatal("read-only review mutated route budgets")
			}
		})
	}
}

func TestSavedEnvironmentPlanMCPReview(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, rollback := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/rollback=%t", protocol, rollback), func(t *testing.T) {
				f, b := newEnvironmentFixture(t)
				f.protected = true
				cs, ctx := connectConfiguredTestServer(t, b, environmentTestOptions(b, false), protocol)
				var original map[string]json.RawMessage
				if rollback {
					original = callTool(t, ctx, cs, "gregale_plan_environment_rollback", map[string]any{"project": "shop", "environment": "production", "promotion_id": "promotion-one", "reason": "review rollback"}, false)
				} else {
					original = callTool(t, ctx, cs, "gregale_plan_environment_promotion", environmentPlanArgs(), false)
				}
				id := planID(t, original)
				out := callTool(t, ctx, cs, "gregale_get_environment_plan", getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id}, false)
				p := assertSavedReview(t, out, id)
				if !bytes.Equal(p["expires_at"], original["expires_at"]) || string(p["from_environment"]) != `"staging"` || string(p["environment"]) != `"production"` {
					t.Fatal("environment review changed its original targets or expiry")
				}
				if rollback && (p["changes"] != nil || string(p["change_coverage"]) != `"promotion_reference_only"` || string(p["promotion_id"]) != `"promotion-one"`) {
					t.Fatal("rollback review fabricated a retained workload snapshot")
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				if f.writes != 0 || f.reviewRequests != 0 || f.approvalPolls != 0 {
					t.Fatal("review polled or bypassed human approval")
				}
			})
		}
	}
}

func TestSavedPlanEncryptedRestartAndExpiry(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint(expired), func(t *testing.T) {
			f, b, opts := newBudgetFixture(t)
			store, path, key := testStateStore(t)
			opts.planStore, opts.planNamespace = store, "saved-review"
			client := api.NewClient(b.URL, testToken)
			_, m, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			id := budgetHistoryPlan(t, m)
			before := savedAppReview(t, m, budgetHistorySelector(id))
			f.mu.Lock()
			f.ambiguous = true
			f.mu.Unlock()
			if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("lost response fixture succeeded")
			}
			if expired {
				m.plans[id].ExpiresAt = time.Now().Add(-time.Second)
				if err := m.persistPlans(); err != nil {
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
			opts.planStore, opts.AllowWrites = next, false
			_, restored, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			stateBefore, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if expired {
				if _, err := restored.getPlan(t.Context(), budgetHistorySelector(id)); err == nil {
					t.Fatal("expired private proposal was exposed")
				}
				if actionDetail(t, restored, budgetHistorySelector(id)).Action.Review != nil {
					t.Fatal("expired history advertised plan review authority")
				}
			} else {
				after := savedAppReview(t, restored, budgetHistorySelector(id))
				if !bytes.Equal(before.Plan, after.Plan) {
					t.Fatal("restart rewrote the reviewed proposal")
				}
				if _, err := restored.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil {
					t.Fatal("read-only review enabled native writes")
				}
			}
			stateAfter, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(stateBefore, stateAfter) {
				t.Fatal("reading review rewrote the durable snapshot")
			}
			opts.planNamespace = "another-grant"
			_, other, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := other.getPlan(t.Context(), budgetHistorySelector(id)); err == nil {
				t.Fatal("another grant inherited private review material")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 1 {
				t.Fatal("review recovery repeated native mutation")
			}
		})
	}
}

func TestSavedPlanInFlightGuards(t *testing.T) {
	for _, change := range []string{"app", "account", "environment", "consent", "expiry", "removed", "storage"} {
		t.Run(change, func(t *testing.T) {
			f, b, opts := newBudgetFixture(t)
			store, _, _ := testStateStore(t)
			opts.planStore, opts.planNamespace = store, "guard-review"
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			id := budgetHistoryPlan(t, m)
			result, err := m.getPlan(t.Context(), budgetHistorySelector(id))
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			switch change {
			case "app":
				f.app.ID = "replacement"
			case "account":
				f.account.ID = "replacement"
			case "environment":
				f.env.ID = "replacement"
			case "consent":
				delete(m.opts.Environments.Named, "shop/staging")
			case "expiry":
				m.plans[id].ExpiresAt = time.Now().Add(-time.Second)
			case "removed":
				delete(m.plans, id)
			case "storage":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			f.mu.Unlock()
			if result.(*guardedRead).check(t.Context()) == nil {
				t.Fatal("changed identity, consent or authority released private review")
			}
		})
	}
}
