package main

// Per-app usage summary handler tests (commit 4 of the per-app
// observability PR series).
//
// Coverage matrix:
//   - Free plan → 402 + code assertion
//   - Hobby plan → 200 with overage=0 (under the included band)
//   - Window parsing: invalid since → 400
//   - Window parsing: since > until → 400
//   - Cross-account slug → 404 (IDOR safety)

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestAppUsage_FreePlanReturns402 confirms the Free plan is
// rejected with the documented plan_app_usage_summary_not_allowed
// code. Same gate posture as /metrics and /wake-timeline — Free
// gets 402 (not 404) so a Free customer probing a Hobby+ slug
// never gets a 404 (slug-leak guard).
func TestAppUsage_FreePlanReturns402(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "my-api")

	rec := e.do(t, "GET", "/v1/apps/my-api/usage", nil, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("Free plan status %d, want 402", rec.Code)
	}
	assertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanAppUsageSummaryNotAllowed)
}

// TestAppUsage_HobbyPlanReturns200 confirms Hobby+ passes the
// gate. The handler sends back zeros for an empty fleet (no
// usage_minutes rows yet) — same posture as the rest of the
// per-app dashboard's "fresh app" branches. Overage=0 because
// gb_hours=0 < plan_included (Hobby's 50 GB-h).
func TestAppUsage_HobbyPlanReturns200(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "my-api")

	rec := e.do(t, "GET", "/v1/apps/my-api/usage", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Hobby plan status %d: %s", rec.Code, rec.Body.String())
	}
	var out api.AppUsageSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Slug != "my-api" {
		t.Errorf("slug = %q, want my-api", out.Slug)
	}
	if out.GBHours != 0 || out.MBSeconds != 0 {
		t.Errorf("non-zero usage on empty fleet: %+v", out)
	}
	if out.PlanIncludedGBHours != 50 {
		t.Errorf("plan_included_gb_hours = %v, want 50 (Hobby)", out.PlanIncludedGBHours)
	}
	if out.OverageGBHours != 0 {
		t.Errorf("overage_gb_hours = %v, want 0 (under-band empty fleet)", out.OverageGBHours)
	}
	if out.Source != "usage_minutes" {
		t.Errorf("source = %q, want usage_minutes", out.Source)
	}
	if out.AsOf == "" {
		t.Errorf("as_of empty")
	}
	// period_end defaults to UTC midnight snap.
	now := time.Now().UTC()
	wantEnd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if !out.PeriodEnd.Equal(wantEnd) {
		t.Errorf("period_end = %v, want %v (UTC midnight snap)", out.PeriodEnd, wantEnd)
	}
	// period_start defaults to period_end - 30d.
	wantStart := wantEnd.AddDate(0, 0, -30)
	if !out.PeriodStart.Equal(wantStart) {
		t.Errorf("period_start = %v, want %v (30d default)", out.PeriodStart, wantStart)
	}
}

func TestAppUsage_ExposesCanonicalAndDiagnosticEgressSeparately(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "my-api")
	minute := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	if err := e.store.AppendUsage(t.Context(), e.acct.ID, appID, "instance-egress", minute,
		1, 0, 0, 100, 250, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "GET", "/v1/apps/my-api/usage?since=2026-09-01T00:00:00Z&until=2026-09-03T00:00:00Z", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out api.AppUsageSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.TxBytes != 100 || out.NetTxBytes != 250 {
		t.Fatalf("egress fields = tx:%d net_tx:%d, want diagnostic/canonical 100/250", out.TxBytes, out.NetTxBytes)
	}
}

// TestAppUsage_InvalidSinceReturns400 pins the validation branch:
// a malformed `since` is a 400 (the dashboard sends a parse error
// chip rather than a 5xx).
func TestAppUsage_InvalidSinceReturns400(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "my-api")

	rec := e.do(t, "GET", "/v1/apps/my-api/usage?since=banana", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed since status %d, want 400", rec.Code)
	}
}

// TestAppUsage_SinceAfterUntilReturns400 pins the inverted-window
// branch: `since > until` is a 400 (the customer is asking for a
// negative-duration scan, which is meaningless).
func TestAppUsage_SinceAfterUntilReturns400(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "my-api")

	rec := e.do(t, "GET", "/v1/apps/my-api/usage?since=2026-08-27T00:00:00Z&until=2026-08-01T00:00:00Z", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("since > until status %d, want 400", rec.Code)
	}
}

// TestAppUsage_CrossAccount404 confirms IDOR safety. A request from
// account A for account B's slug returns 404 (NOT 200 with B's
// billing data — leaking billing data is a Tier-1 confidentiality
// violation per spec §11).
func TestAppUsage_CrossAccount404(t *testing.T) {
	e := setup(t, api.PlanPro)
	other := state.NewMemStore()
	otherAcct, _ := other.CreateAccount(t.Context(), "other@hobby.com", api.PlanPro)
	mustSeedAppFor(t, other, otherAcct.ID, "their-api")

	rec := e.do(t, "GET", "/v1/apps/their-api/usage", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (IDOR)", rec.Code)
	}
}

// TestAppUsage_UnknownSlug404 mirrors the other per-app handlers'
// unknown-slug branch.
func TestAppUsage_UnknownSlug404(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/apps/ghost/usage", nil, nil)
	assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
}

// TestAppUsage_CallerSuppliedUntilSnappedToMidnight pins the
// code-review contract from PR #1097 (commit 4 of the review-fix
// cluster): parseUsageWindow must snap EVERY until value to UTC
// midnight, not just the omitted-default. A customer passing
// until=2026-08-27T15:00:00Z previously produced
// period_end=15:00 + period_start=2026-07-28T15:00:00Z, which
// contradicted the documented OpenAPI example (midnight-to-
// midnight) and the DTO comment ("Snapped to UTC midnight on the
// inclusive end"). The dashboard tile's row-count was rendered
// against a 30d 9h window, not the documented 30d window.
//
// We seed a fixed `now` via the URL query and assert the response
// period_end is at the day boundary of the customer's date
// (NOT the time-of-day they supplied).
func TestAppUsage_CallerSuppliedUntilSnappedToMidnight(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "my-api")

	// Pass until at 15:00 UTC — the response must snap to
	// 00:00 UTC of the same day.
	rec := e.do(t, "GET", "/v1/apps/my-api/usage?until=2026-08-27T15:00:00Z", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out api.AppUsageSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantEnd := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	if !out.PeriodEnd.Equal(wantEnd) {
		t.Errorf("PeriodEnd = %v, want %v (15:00 input must snap to UTC midnight of same day)", out.PeriodEnd, wantEnd)
	}
	// period_start = period_end - 30d at UTC midnight.
	wantStart := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	if !out.PeriodStart.Equal(wantStart) {
		t.Errorf("PeriodStart = %v, want %v (30d back from snapped until)", out.PeriodStart, wantStart)
	}
}

// TestAppUsage_CallerSuppliedSinceSnappedToMidnight pins the
// same contract on the since side: a customer passing
// since=2026-07-28T15:00:00Z must get period_start at the
// day boundary of that date (not 15:00). The dashboard renders
// the window against day-boundary math, so a half-open
// boundary at 15:00 would shift the rolled-up hours by 15h
// (rolling the last 15h of the trailing day in).
func TestAppUsage_CallerSuppliedSinceSnappedToMidnight(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "my-api")

	rec := e.do(t, "GET", "/v1/apps/my-api/usage?since=2026-07-28T15:00:00Z&until=2026-08-27T15:00:00Z", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out api.AppUsageSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantStart := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	if !out.PeriodStart.Equal(wantStart) {
		t.Errorf("PeriodStart = %v, want %v (15:00 input must snap to UTC midnight of same day)", out.PeriodStart, wantStart)
	}
	wantEnd := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	if !out.PeriodEnd.Equal(wantEnd) {
		t.Errorf("PeriodEnd = %v, want %v (15:00 input must snap to UTC midnight of same day)", out.PeriodEnd, wantEnd)
	}
}
