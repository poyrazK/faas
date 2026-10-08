package main

// Per-app scale-to-zero savings handler tests.
//
// Coverage matrix:
//   - Free plan → 402 + code assertion (gate before loadApp)
//   - Hobby plan, one billed hour in a 2-day window → exact figures
//   - App that never ran → zero baseline, zero saving
//   - Window longer than 30d → clamped to the usage_minutes retention
//   - Cross-account slug → 404 (IDOR safety)

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func mustSeedSizedApp(t *testing.T, e testEnv, slug string, ramMB, minInstances int) string {
	t.Helper()
	app, err := e.store.CreateApp(t.Context(), state.App{
		AccountID:    e.acct.ID,
		Slug:         slug,
		Type:         state.AppTypeApp,
		Status:       state.AppActive,
		RAMMB:        ramMB,
		MinInstances: minInstances,
	})
	if err != nil {
		t.Fatalf("seed app %s: %v", slug, err)
	}
	return app.ID
}

func decodeSavings(t *testing.T, e testEnv, path string) api.AppSavingsResponse {
	t.Helper()
	rec := e.do(t, "GET", path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out api.AppSavingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// TestAppSavings_FreePlanReturns402 pins the shared usage-summary gate:
// Free gets 402 before loadApp so probing a slug never yields a 404.
func TestAppSavings_FreePlanReturns402(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "my-api")

	rec := e.do(t, "GET", "/v1/apps/my-api/savings", nil, nil)
	assertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanAppUsageSummaryNotAllowed)
}

// TestAppSavings_OneBilledHourInTwoDays pins the end-to-end arithmetic:
// a 512 MB app (520 MB billable) that first billed at 12:00 on day one of
// a 2-day window has a 36 h always-on baseline (from its first billed
// hour) and saves the other 35 h, valued at €0.01/GB-h.
func TestAppSavings_OneBilledHourInTwoDays(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appID := mustSeedSizedApp(t, e, "my-api", 512, 0)
	minute := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := e.store.AppendUsage(t.Context(), e.acct.ID, appID, "instance-1", minute,
		520*3600, 10, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	out := decodeSavings(t, e, "/v1/apps/my-api/savings?since=2026-09-01T00:00:00Z&until=2026-09-03T00:00:00Z")
	if out.Slug != "my-api" || out.BillableRAMMB != 520 || out.BaselineInstances != 1 {
		t.Fatalf("shape = %+v, want my-api / 520 MB / 1 instance", out)
	}
	if want := int64(520 * 36 * 3600); out.AlwaysOnMBSeconds != want {
		t.Errorf("always_on_mb_seconds = %d, want %d", out.AlwaysOnMBSeconds, want)
	}
	if out.ActualMBSeconds != 520*3600 {
		t.Errorf("actual_mb_seconds = %d, want %d", out.ActualMBSeconds, 520*3600)
	}
	if want := int64(520 * 35 * 3600); out.SavedMBSeconds != want {
		t.Errorf("saved_mb_seconds = %d, want %d", out.SavedMBSeconds, want)
	}
	// 520 MB × 35 h = 17.7734375 GB-h → 17,773 millicents (half up).
	if out.SavedMillicents != 17_773 {
		t.Errorf("saved_millicents = %d, want 17773", out.SavedMillicents)
	}
	if want := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC); !out.BaselineStart.Equal(want) {
		t.Errorf("baseline_start = %v, want first billed hour %v", out.BaselineStart, want)
	}
	if out.PriceMillicentsPerGBHour != api.OverageMillicentsPerGBHour {
		t.Errorf("price = %d, want overage rate %d", out.PriceMillicentsPerGBHour, api.OverageMillicentsPerGBHour)
	}
	if out.Methodology == "" || out.Source != "usage_minutes" || out.AsOf == "" {
		t.Errorf("missing methodology/source/as_of: %+v", out)
	}
}

// TestAppSavings_NeverRanReportsZero pins the new-app guard: an app with
// no billed usage is not credited with savings for the whole window.
func TestAppSavings_NeverRanReportsZero(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedSizedApp(t, e, "my-api", 512, 0)

	out := decodeSavings(t, e, "/v1/apps/my-api/savings?since=2026-09-01T00:00:00Z&until=2026-09-03T00:00:00Z")
	if out.AlwaysOnMBSeconds != 0 || out.SavedMBSeconds != 0 || out.SavedMillicents != 0 {
		t.Errorf("never-ran saving = %+v, want zero", out)
	}
	if !out.BaselineStart.Equal(out.PeriodEnd) {
		t.Errorf("baseline_start = %v, want period_end %v", out.BaselineStart, out.PeriodEnd)
	}
}

// TestAppSavings_WindowClampedToRetention pins the correctness bound: a
// window past the 30d usage_minutes retention would undercount actual
// usage and overstate the saving, so period_start clamps to until-30d.
func TestAppSavings_WindowClampedToRetention(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedSizedApp(t, e, "my-api", 512, 2)

	out := decodeSavings(t, e, "/v1/apps/my-api/savings?since=2026-06-01T00:00:00Z&until=2026-09-01T00:00:00Z")
	if want := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC); !out.PeriodStart.Equal(want) {
		t.Errorf("period_start = %v, want %v (30d clamp)", out.PeriodStart, want)
	}
	if out.BaselineInstances != 2 {
		t.Errorf("baseline_instances = %d, want min_instances 2", out.BaselineInstances)
	}
}

// TestAppSavings_MeasuresUpToNow pins the window end: today's usage is
// counted (no midnight snap by default) and an until in the future never
// credits always-on time that has not happened yet.
func TestAppSavings_MeasuresUpToNow(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appID := mustSeedSizedApp(t, e, "my-api", 512, 0)
	now := time.Now().UTC()
	if err := e.store.AppendUsage(t.Context(), e.acct.ID, appID, "instance-1", now.Truncate(time.Minute),
		520*60, 1, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	future := now.AddDate(0, 0, 3).Format(time.RFC3339)
	for _, path := range []string{"/v1/apps/my-api/savings", "/v1/apps/my-api/savings?until=" + future} {
		out := decodeSavings(t, e, path)
		if out.ActualMBSeconds != 520*60 {
			t.Errorf("%s: actual_mb_seconds = %d, want today's %d", path, out.ActualMBSeconds, 520*60)
		}
		if out.PeriodEnd.After(time.Now().UTC()) || out.PeriodEnd.Before(now) {
			t.Errorf("%s: period_end = %v, want now (%v)", path, out.PeriodEnd, now)
		}
		if limit := int64(520 * 3600); out.AlwaysOnMBSeconds > limit {
			t.Errorf("%s: always_on_mb_seconds = %d, want at most one hour (%d)", path, out.AlwaysOnMBSeconds, limit)
		}
	}
}

// TestAppSavings_CrossAccount404 confirms IDOR safety: another account's
// slug is a 404, never its billing-derived figures.
func TestAppSavings_CrossAccount404(t *testing.T) {
	e := setup(t, api.PlanPro)
	other := state.NewMemStore()
	otherAcct, _ := other.CreateAccount(t.Context(), "other@pro.com", api.PlanPro)
	mustSeedAppFor(t, other, otherAcct.ID, "their-api")

	rec := e.do(t, "GET", "/v1/apps/their-api/savings", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (IDOR)", rec.Code)
	}
}
