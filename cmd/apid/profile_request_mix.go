package main

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) canaryRequestMix(r *http.Request, acct state.Account, app state.App, signal api.CanaryProfileSignal) dashboard.ProfileRequestMixView {
	if signal.RequestMix != nil {
		view := dashboard.ProfileRequestMixSnapshotView(signal.RequestMix, api.NormalizeProfileRegressionOptions(signal.Options).MinimumRequests)
		linkProfileMixSnapshot(&view, signal.RequestMix, app.Slug, acct.Plan)
		return view
	}
	unavailable := func(reason string) dashboard.ProfileRequestMixView {
		return dashboard.ProfileRequestMixView{Message: "Request mix unavailable", Warnings: []string{reason}}
	}
	limits := api.MustLimitsFor(acct.Plan)
	reader, ok := s.store.(state.ProfileRequestMixReader)
	if !ok || !limits.DebugTelemetryEnabled || signal.Baseline == nil || signal.Candidate == nil {
		return unavailable("Request-mix telemetry is unavailable on this plan or installation.")
	}
	cutoff := time.Now().Add(-time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour)
	if signal.Baseline.Start.Before(cutoff) || signal.Candidate.Start.Before(cutoff) {
		return unavailable("The recorded comparison windows are outside request telemetry retention.")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	a, err := reader.ProfileRequestMix(ctx, acct.ID, app.ID, *signal.Baseline)
	if err != nil {
		return unavailable("Stable request-mix telemetry could not be loaded.")
	}
	b, err := reader.ProfileRequestMix(ctx, acct.ID, app.ID, *signal.Candidate)
	if err != nil {
		return unavailable("Canary request-mix telemetry could not be loaded.")
	}
	v := dashboard.BuildProfileRequestMix(a, b, api.NormalizeProfileRegressionOptions(signal.Options).MinimumRequests)
	if signal.BaselineRequests != nil && *signal.BaselineRequests != a.Total || signal.CandidateRequests != nil && *signal.CandidateRequests != b.Total {
		v.Warnings = append(v.Warnings, "Retained request totals differ from the recorded assessment, for example because telemetry arrived later or was removed. Mix shares use the currently retained totals.")
	}
	for i := range v.Routes {
		v.Routes[i].BaselineURL = profileMixTelemetryURL(app.Slug, *signal.Baseline, v.Routes[i].Label)
		v.Routes[i].CandidateURL = profileMixTelemetryURL(app.Slug, *signal.Candidate, v.Routes[i].Label)
	}
	return v
}

func linkProfileMixSnapshot(view *dashboard.ProfileRequestMixView, snapshot *api.ProfileRequestMixSnapshot, slug string, plan api.Plan) {
	limits := api.MustLimitsFor(plan)
	if !limits.DebugTelemetryEnabled || snapshot == nil {
		return
	}
	cutoff := time.Now().Add(-time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour)
	for i := range view.Routes {
		if a := snapshot.Baseline; a != nil && !a.Query.Start.Before(cutoff) {
			view.Routes[i].BaselineURL = profileMixTelemetryURL(slug, a.Query, view.Routes[i].Label)
		}
		if b := snapshot.Candidate; b != nil && !b.Query.Start.Before(cutoff) {
			view.Routes[i].CandidateURL = profileMixTelemetryURL(slug, b.Query, view.Routes[i].Label)
		}
	}
}

func profileMixTelemetryURL(slug string, q api.ProfileQuery, route string) string {
	v := url.Values{"deployment_id": {q.DeploymentID}, "route": {route}, "window_start": {q.Start.Format(time.RFC3339Nano)}, "window_end": {q.End.Format(time.RFC3339Nano)}}
	return "/dashboard/apps/" + url.PathEscape(slug) + "/debug?" + v.Encode() + "#requests"
}

func profileMixDebugWindow(values url.Values, now time.Time, retention time.Duration) (time.Time, time.Time, bool) {
	a, e1 := time.Parse(time.RFC3339Nano, values.Get("window_start"))
	b, e2 := time.Parse(time.RFC3339Nano, values.Get("window_end"))
	return a, b, e1 == nil && e2 == nil && b.After(a) && retention > 0 && !a.Before(now.Add(-retention)) && !b.After(now.Add(time.Minute)) && b.Sub(a) <= retention
}
