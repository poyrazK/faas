package main

// Per-app scale-to-zero savings estimate.
//
// GET /v1/apps/{slug}/savings?since=&until=
//
// Compares the app's billed RAM in the window against an always-on
// counterfactual (pkg/meter.ScaleToZeroSavings). Same auth chain, plan
// gate (Hobby+, Free gets 402 plan_app_usage_summary_not_allowed
// before loadApp) and window vocabulary as getAppUsage, with one
// difference: the window is clamped to savingsMaxWindowDays. The
// actual figure comes from usage_minutes, which keeps 30d (ADR-048);
// a longer window would undercount actual usage and so overstate the
// saving.

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// savingsMaxWindowDays caps the savings window at the usage_minutes
// retention (ADR-048). Unlike usageMaxWindowDays this is a
// correctness bound, not a scan bound.
const savingsMaxWindowDays = 30

// savingsMethodology is printed under every savings figure so the
// estimate is never read as an invoice line.
const savingsMethodology = "Estimate: billed RAM-time compared with keeping the app's minimum instance count (at least one) running from its first billed hour in the window, valued at the plan overage rate. Companion sidecars are excluded from the always-on baseline."

// getAppSavings serves GET /v1/apps/{slug}/savings. Returns
// api.AppSavingsResponse. 200 on success, 400 on a bad window, 402 on
// Free, 404 on a cross-account slug (via loadApp).
func (s *server) getAppSavings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !acct.Plan.AppUsageSummaryAllowed() {
		api.WriteProblem(w, api.ErrPlanAppUsageSummaryNotAllowed(acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context() for its own DB calls; helper is shared across every per-app handler.
	if !ok {
		return
	}

	now := time.Now().UTC()
	since, until, err := parseUsageWindow(r, now)
	if err != nil {
		api.WriteProblem(w, err)
		return
	}
	// Unlike the usage summary, measure up to now: the default
	// midnight snap would hide today's usage, and an until in the
	// future would credit always-on time that has not happened.
	if r.URL.Query().Get("until") == "" || until.After(now) {
		until = now
	}
	if minSince := until.AddDate(0, 0, -savingsMaxWindowDays); since.Before(minSince) {
		since = minSince
	}

	summary, source, sumErr := meter.BuildAppWindowSummary(r.Context(), s.store, acct.ID, app.ID, since, until)
	if sumErr != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"savings estimate fetch failed",
			"could not load usage for the savings estimate; see server logs"))
		return
	}

	writeJSON(w, http.StatusOK, buildAppSavingsResponse(app, since, until, summary, source))
}

// buildAppSavingsResponse turns the window rollup into the wire
// response: it places the always-on baseline, runs the estimate, and
// copies the figures out. Kept separate so getAppSavings stays under
// the 50-line handler bound.
func buildAppSavingsResponse(app state.App, since, until time.Time, summary meter.AppWindowSummary, source string) api.AppSavingsResponse {
	// Start the counterfactual at the first billed hour so an app is
	// never credited for time before it ran. An app with no billed
	// usage in the window gets an empty baseline.
	baselineStart := until
	if !summary.FirstUsageHour.IsZero() {
		baselineStart = summary.FirstUsageHour
		if baselineStart.Before(since) {
			baselineStart = since
		}
	}
	billableRAMMB := api.BillableRAMMB(app.RAMMB)
	baselineInstances := app.MinInstances
	if baselineInstances < 1 {
		baselineInstances = 1
	}
	est := meter.ScaleToZeroSavings(meter.SavingsInput{
		Since:                    baselineStart,
		Until:                    until,
		BillableRAMMB:            billableRAMMB,
		FloorInstances:           baselineInstances,
		ActualMBSeconds:          summary.MBSeconds,
		PriceMillicentsPerGBHour: api.OverageMillicentsPerGBHour,
	})

	return api.AppSavingsResponse{
		Slug:                     app.Slug,
		PeriodStart:              since.UTC(),
		PeriodEnd:                until.UTC(),
		BaselineStart:            baselineStart.UTC(),
		BaselineInstances:        baselineInstances,
		BillableRAMMB:            billableRAMMB,
		AlwaysOnMBSeconds:        est.AlwaysOnMBSeconds,
		ActualMBSeconds:          est.ActualMBSeconds,
		SavedMBSeconds:           est.SavedMBSeconds,
		AlwaysOnGBHours:          est.AlwaysOnGBHours,
		ActualGBHours:            est.ActualGBHours,
		SavedGBHours:             est.SavedGBHours,
		PriceMillicentsPerGBHour: api.OverageMillicentsPerGBHour,
		AlwaysOnMillicents:       est.AlwaysOnMillicents,
		ActualMillicents:         est.ActualMillicents,
		SavedMillicents:          est.SavedMillicents,
		ParkedRatio:              est.ParkedRatio,
		Methodology:              savingsMethodology,
		Source:                   source,
		AsOf:                     time.Now().UTC().Format(time.RFC3339Nano),
	}
}
