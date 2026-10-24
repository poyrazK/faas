// adr: 20261024120000
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func checkedView(t *testing.T, value any, err error) *planCheck {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	guard := value.(*guardedRead)
	if err := guard.check(t.Context()); err != nil {
		t.Fatal(err)
	}
	return guard.value.(*planCheck)
}

func appCheck(t *testing.T, m *management, args getActionArgs) *planCheck {
	t.Helper()
	value, err := m.checkPlan(t.Context(), args)
	return checkedView(t, value, err)
}

func environmentCheck(t *testing.T, m *management, id string) *planCheck {
	t.Helper()
	value, err := m.checkEnvironmentPlan(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id})
	return checkedView(t, value, err)
}

func TestPlanCheckMCPReleaseBaselines(t *testing.T) {
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
				tool, args := "gregale_plan_deploy", map[string]any{"app": "alpha", "image": testImage, "reason": "check intent"}
				switch kind {
				case "source":
					tool, args = "gregale_plan_source_deploy", sourcePlanArgs()
				case "rollback":
					tool, args = "gregale_plan_rollback", map[string]any{"app": "alpha", "target_deployment_id": "release-old", "reason": "check rollback"}
				case "service", "worker":
					tool = "gregale_plan_workload_rollout"
				}
				original := callTool(t, ctx, cs, tool, args, false)
				id := planID(t, original)
				f.mu.Lock()
				f.sourceHead = strings.Repeat("b", 40)
				f.mu.Unlock()
				out := callTool(t, ctx, cs, "gregale_check_plan", getActionArgs{App: "alpha", PlanID: id}, false)
				if string(out["apply_eligibility"]) != `"no_observed_blockers"` || string(out["baseline_check"]) != `"unchanged"` || !bytes.Equal(out["expires_at"], original["expires_at"]) {
					t.Fatalf("fresh check changed proposal or eligibility: %s", out)
				}
				f.mu.Lock()
				f.app.RAMMB++
				f.mu.Unlock()
				out = callTool(t, ctx, cs, "gregale_check_plan", getActionArgs{App: "alpha", PlanID: id}, false)
				if string(out["apply_eligibility"]) != `"blocked"` || !bytes.Contains(out["blockers"], []byte("plan_stale")) {
					t.Fatalf("changed configuration was not blocked: %s", out)
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				if f.writes != 0 {
					t.Fatal("checker executed a release")
				}
			})
		}
	}
}

func TestPlanCheckBudgetCASAndRetryRestart(t *testing.T) {
	f, b, opts := newBudgetFixture(t)
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "plan-check"
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := budgetHistoryPlan(t, m)
	original, _ := json.Marshal(m.plans[id].Budgets.Proposed)
	expires := m.plans[id].ExpiresAt
	if out := appCheck(t, m, budgetHistorySelector(id)); out.ApplyEligibility != "no_observed_blockers" || out.BaselineCheck != "unchanged" {
		t.Fatalf("fresh budget: %+v", out)
	}
	other := budgetHistoryPlan(t, m)
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("expected lost native response")
	}
	if out := appCheck(t, m, budgetHistorySelector(other)); out.ApplyEligibility != "blocked" || !slices.Contains(out.Blockers, "plan_stale") {
		t.Fatalf("missed policy CAS: %+v", out)
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
	_, restored, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := appCheck(t, restored, budgetHistorySelector(id))
	if out.ApplyEligibility != "retry_original_plan" || out.BaselineCheck != "retry_preserved" || len(out.Blockers) != 0 {
		t.Fatalf("committed unknown result incorrectly stale: %+v", out)
	}
	after, _ := os.ReadFile(path)
	req, _ := json.Marshal(restored.plans[id].Budgets.Proposed)
	if !bytes.Equal(before, after) || !bytes.Equal(original, req) || !expires.Equal(restored.plans[id].ExpiresAt) {
		t.Fatal("checker modified durable state, retry payload or expiry")
	}
	if _, err := restored.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	if appCheck(t, restored, budgetHistorySelector(id)).ApplyEligibility != "receipt_recorded" {
		t.Fatal("receipt lost")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	current, err := f.store.GetEnvironmentRouteBudgets(t.Context(), f.account.ID, f.app.ID, "staging")
	if err != nil || current.Revision != 1 || f.writes != 2 {
		t.Fatal("check or retry changed policy again")
	}
}

func TestPlanCheckRegressionVersionAndUnknown(t *testing.T) {
	for _, change := range []string{"version", "unavailable", "triage-consent", "dismissal"} {
		t.Run(change, func(t *testing.T) {
			f, b, opts := newTriageFixture(t)
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			value, err := m.planRegressionAction(t.Context(), regressionActionArgs{App: "alpha", DeploymentID: discoveryDeploymentID, Route: "GET /orders", Action: "dismiss", Reason: "check regression"})
			if err != nil {
				t.Fatal(err)
			}
			id := value.(releasePlan).ID
			if appCheck(t, m, getActionArgs{App: "alpha", PlanID: id}).BaselineCheck != "unchanged" {
				t.Fatal("fresh regression not checked")
			}
			f.mu.Lock()
			switch change {
			case "version":
				f.row.Version = uuid.NewString()
			case "unavailable":
				f.oldAPI = true
			case "triage-consent":
				delete(m.opts.Environments.RegressionWriteApps, "alpha")
			case "dismissal":
				m.plans[id].Regression.Proposed.DismissedUntil = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
			}
			f.mu.Unlock()
			out := appCheck(t, m, getActionArgs{App: "alpha", PlanID: id})
			want := "blocked"
			if change == "unavailable" {
				want = "unknown"
			}
			if out.ApplyEligibility != want {
				t.Fatalf("%s: %+v", change, out)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 0 {
				t.Fatal("checker mutated triage")
			}
		})
	}
}

func TestEnvironmentPlanCheckMCPApprovalAndStale(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, rollback := range []bool{false, true} {
			t.Run(protocol+"/"+map[bool]string{false: "promotion", true: "rollback"}[rollback], func(t *testing.T) {
				f, b := newEnvironmentFixture(t)
				cs, ctx := connectConfiguredTestServer(t, b, environmentTestOptions(b, true), protocol)
				tool, args := "gregale_plan_environment_promotion", environmentPlanArgs()
				if rollback {
					tool, args = "gregale_plan_environment_rollback", map[string]any{"project": "shop", "environment": "production", "promotion_id": "promotion-one", "reason": "check rollback"}
				}
				original := callTool(t, ctx, cs, tool, args, false)
				id := planID(t, original)
				selector := getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id}
				out := callTool(t, ctx, cs, "gregale_check_environment_plan", selector, false)
				if string(out["apply_eligibility"]) != `"no_observed_blockers"` || !bytes.Equal(out["expires_at"], original["expires_at"]) {
					t.Fatalf("fresh environment: %s", out)
				}
				if rollback && string(out["baseline_check"]) != `"partial_native_rollback_guard_required"` {
					t.Fatal("rollback overstated coverage")
				}
				if !rollback {
					f.mu.Lock()
					f.protected = true
					f.mu.Unlock()
					out = callTool(t, ctx, cs, "gregale_check_environment_plan", selector, false)
					if string(out["apply_eligibility"]) != `"unknown"` || !bytes.Contains(out["blockers"], []byte("approval_not_checked")) {
						t.Fatalf("approval certainty overstated: %s", out)
					}
				}
				f.mu.Lock()
				f.hash = strings.Repeat("b", 64)
				f.mu.Unlock()
				out = callTool(t, ctx, cs, "gregale_check_environment_plan", selector, false)
				if string(out["apply_eligibility"]) != `"blocked"` {
					t.Fatal("changed promotion was not blocked")
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				if f.writes != 0 || f.reviewRequests != 0 || f.approvalPolls != 0 {
					t.Fatal("checker started/polled approval or mutated environment")
				}
			})
		}
	}
}

func TestEnvironmentPlanCheckAmbiguousRetry(t *testing.T) {
	f, b := newEnvironmentFixture(t)
	m := newHistoryEnvironment(t, b, environmentTestOptions(b, true))
	id := planHistoryEnvironment(t, m, false)
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	if applyHistoryEnvironment(t, m, id, false) == nil {
		t.Fatal("expected lost response")
	}
	f.mu.Lock()
	f.hash = strings.Repeat("b", 64)
	f.mu.Unlock()
	if out := environmentCheck(t, m, id); out.ApplyEligibility != "retry_original_plan" || out.BaselineCheck != "retry_preserved" {
		t.Fatalf("retry treated as stale: %+v", out)
	}
	if err := applyHistoryEnvironment(t, m, id, false); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 2 || len(f.commits) != 1 {
		t.Fatal("retry did not preserve native key/body")
	}
}

func TestPlanCheckFinalGuardsAndExpiry(t *testing.T) {
	for _, change := range []string{"read-only", "write-consent", "read-consent", "account", "environment", "expiry", "removed", "storage"} {
		t.Run(change, func(t *testing.T) {
			_, b, opts := newBudgetFixture(t)
			store, _, _ := testStateStore(t)
			opts.planStore, opts.planNamespace = store, "check-guard"
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			id := budgetHistoryPlan(t, m)
			if change == "read-only" {
				m.opts.AllowWrites = false
			}
			if change == "write-consent" {
				g := m.opts.Environments.Named["shop/staging"]
				g.Write = false
				m.opts.Environments.Named["shop/staging"] = g
			}
			value, err := m.checkPlan(t.Context(), budgetHistorySelector(id))
			if err != nil {
				t.Fatal(err)
			}
			if change == "read-only" || change == "write-consent" {
				out := checkedView(t, value, nil)
				if out.ApplyEligibility != "blocked" || len(out.Blockers) == 0 {
					t.Fatal("missing write authority was hidden")
				}
				return
			}
			switch change {
			case "read-consent":
				delete(m.opts.Environments.Named, "shop/staging")
			case "account":
				m.opts.accountID = "replacement"
			case "environment":
				g := m.opts.Environments.Named["shop/staging"]
				g.EnvironmentID = "replacement"
				m.opts.Environments.Named["shop/staging"] = g
			case "expiry":
				m.plans[id].ExpiresAt = time.Now().Add(-time.Second)
			case "removed":
				delete(m.plans, id)
			case "storage":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if value.(*guardedRead).check(t.Context()) == nil {
				t.Fatal("final check released observation after authority changed")
			}
			if change == "expiry" {
				if _, err := m.checkPlan(t.Context(), budgetHistorySelector(id)); err == nil {
					t.Fatal("expired plan was checkable")
				}
				if m.plans[id] == nil {
					t.Fatal("read pruned original expired plan")
				}
			}
		})
	}
}

func TestEnvironmentPlanCheckFinalConsent(t *testing.T) {
	for _, change := range []string{"source", "target", "membership", "expiry", "removed"} {
		t.Run(change, func(t *testing.T) {
			f, b := newEnvironmentFixture(t)
			m := newHistoryEnvironment(t, b, environmentTestOptions(b, true))
			id := planHistoryEnvironment(t, m, false)
			value, err := m.checkEnvironmentPlan(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "source":
				delete(m.opts.Environments.Named, "shop/staging")
			case "target":
				delete(m.opts.Environments.Named, "shop/production")
			case "membership":
				f.mu.Lock()
				f.extra = true
				f.mu.Unlock()
			case "expiry":
				m.environmentPlans[id].ExpiresAt = time.Now().Add(-time.Second)
			case "removed":
				delete(m.environmentPlans, id)
			}
			if value.(*guardedRead).check(t.Context()) == nil {
				t.Fatal("changed environment authority released applicability")
			}
		})
	}
}
