package main

// Per-app wake-timeline JSON mirror tests (commit 3 of the per-app
// observability PR series).
//
// Coverage matrix:
//   - Free plan → 402 + code assertion
//   - Hobby plan → 200 + documented shape
//   - cross-account slug → 404 (IDOR safety, via loadApp)
//   - empty instance list → 200 with zero counts + empty rows + non-nil histogram
//   - descending-cutoff break: rows pre-24h do NOT appear in Rows

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestAppWakeTimeline_FreePlanReturns402 confirms the Free plan is
// rejected with the documented plan_per_app_metrics_not_allowed
// code. Same gate as /metrics — see PerAppMetricsAllowed on
// pkg/api/limits.go.
func TestAppWakeTimeline_FreePlanReturns402(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "my-api")

	rec := e.do(t, "GET", "/v1/apps/my-api/wake-timeline", nil, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("Free plan status %d, want 402", rec.Code)
	}
	assertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanPerAppMetricsNotAllowed)
}

// TestAppWakeTimeline_HobbyPlanReturns200 confirms Hobby+ passes
// the gate and the documented wire shape lands. The handler
// degrades to empty rows on a fresh app (no instance rows yet)
// — same posture as the dashboard HTML page's
// `renderAppWakeTimeline` failure-is-non-fatal branch at
// handlers_dashboard.go:2562-2566.
func TestAppWakeTimeline_HobbyPlanReturns200(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "my-api")

	rec := e.do(t, "GET", "/v1/apps/my-api/wake-timeline", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Hobby plan status %d: %s", rec.Code, rec.Body.String())
	}
	var out api.AppWakeTimelineResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.App.Slug != "my-api" {
		t.Errorf("app.slug = %q, want my-api", out.App.Slug)
	}
	if out.App.AppID == "" {
		t.Errorf("app.app_id empty")
	}
	if out.WakeCount24h != 0 || out.WakeCountWithMeta != 0 || out.AtCapacityCount != 0 {
		t.Errorf("non-zero counts on empty fleet: %+v", out)
	}
	if out.AtCapacityPct != 0 {
		t.Errorf("at_capacity_pct = %v, want 0 on empty fleet", out.AtCapacityPct)
	}
	if out.TriggerHistogram == nil {
		t.Errorf("trigger_histogram is nil — wire shape contract requires non-nil empty map")
	}
	if len(out.TriggerHistogram) != 0 {
		t.Errorf("trigger_histogram not empty on fresh app: %+v", out.TriggerHistogram)
	}
	if out.TriggerClassHistogram == nil {
		t.Errorf("trigger_class_histogram is nil — wire shape contract requires non-nil empty map")
	}
	if len(out.TriggerClassHistogram) != 0 {
		t.Errorf("trigger_class_histogram not empty on fresh app: %+v", out.TriggerClassHistogram)
	}
	if out.Rows == nil {
		t.Errorf("rows is nil — wire shape contract requires non-nil empty slice")
	}
	if len(out.Rows) != 0 {
		t.Errorf("rows not empty on fresh app: %d", len(out.Rows))
	}
	if out.AsOf == "" {
		t.Errorf("as_of empty")
	}
}

// TestAppWakeTimeline_CrossAccount404 confirms IDOR safety: a
// request from account A for account B's slug returns 404 (NOT
// 200 with B's data). Mirrors TestAppMetrics_CrossAccount404 —
// both gates share loadApp.
func TestAppWakeTimeline_CrossAccount404(t *testing.T) {
	e := setup(t, api.PlanPro)
	other := state.NewMemStore()
	otherAcct, _ := other.CreateAccount(context.Background(), "other@hobby.com", api.PlanPro)
	mustSeedAppFor(t, other, otherAcct.ID, "their-api")

	rec := e.do(t, "GET", "/v1/apps/their-api/wake-timeline", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (IDOR)", rec.Code)
	}
}

// TestAppWakeTimeline_DescendingCutoffBreak pins the load-bearing
// invariant from the PR-A review cluster (PR #1031 finding #4):
// the moment a row's started_at falls before the trailing-24h
// instant, the loop breaks (no further iteration). Rows that
// pre-date the 24h cutoff must NOT appear in the response.
//
// We seed two instance rows via MemStore directly — one inside
// the 24h window, one pre-cutoff (50 hours ago) — and assert the
// pre-cutoff row is absent from the response. CreateInstance
// stamps started_at = now(); we then BackdateForTest the old row
// to 50h-ago so the descending-cutoff break engages.
func TestAppWakeTimeline_DescendingCutoffBreak(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "my-api")

	recent, err := e.store.CreateInstance(context.Background(),
		dep.AppID, dep.ID, string(state.StateRunning), 512, "node-1", "wake-recent")
	if err != nil {
		t.Fatalf("CreateInstance recent: %v", err)
	}

	old, err := e.store.CreateInstance(context.Background(),
		dep.AppID, dep.ID, string(state.StateRunning), 512, "node-1", "wake-old")
	if err != nil {
		t.Fatalf("CreateInstance old: %v", err)
	}

	// Backdate the old instance 50 hours so the descending-cutoff
	// break must engage. MemStore sets started_at=now() on
	// CreateInstance; BackdateForTest is the test-only escape
	// hatch to fabricate aged rows (also used by the §6.1
	// watchdog tests in pkg/sched).
	e.store.BackdateForTest(old.ID, time.Now().UTC().Add(-50*time.Hour))
	_ = recent // unused — the test pins the old row's absence

	rec := e.do(t, "GET", "/v1/apps/my-api/wake-timeline", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out api.AppWakeTimelineResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.WakeCount24h != 1 {
		t.Errorf("wake_count_24h = %d, want 1 (descending-cutoff break must drop the 50h-old row)", out.WakeCount24h)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("rows len = %d, want 1", len(out.Rows))
	}
	if out.Rows[0].At == "" {
		t.Errorf("row.at empty (handler must stamp RFC3339)")
	}
	// At must round-trip close to "now" (UTC seconds precision);
	// the recent instance has StartedAt≈now() so the descending-
	// cutoff break has NOT fired yet for it.
	gotTime, err := time.Parse(time.RFC3339, out.Rows[0].At)
	if err != nil {
		t.Fatalf("parse row.at %q: %v", out.Rows[0].At, err)
	}
	if time.Since(gotTime) > 5*time.Minute {
		t.Errorf("row.at drift: got %v is older than 5m — descending-cutoff break failed to drop the old row", gotTime)
	}
}

// TestAppWakeTimeline_UnknownSlug404 mirrors the other per-app
// handlers' unknown-slug branch.
func TestAppWakeTimeline_UnknownSlug404(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/apps/ghost/wake-timeline", nil, nil)
	assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
}
