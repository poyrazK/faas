package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// getAppPreAuthObservations exposes only the caller's configured policy keys.
// Prometheus labels for removed or foreign policies never appear on the wire.
func (s *server) getAppPreAuthObservations(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = appmetrics.DefaultRange
	}
	if !appmetrics.IsValidRange(rng) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid range", "range must be one of: "+strings.Join(appmetrics.Ranges(), ", ")))
		return
	}
	writeJSON(w, http.StatusOK, s.appPreAuthObservations(r.Context(), app, rng))
}

// appPreAuthObservations is shared by the public JSON endpoint and the
// read-only dashboard. Both surfaces use the same policy scope and degraded
// source semantics without a loopback HTTP request.
func (s *server) appPreAuthObservations(ctx context.Context, app state.App, rng string) api.PreAuthObservationsResponse {
	resp := api.PreAuthObservationsResponse{
		AppID: app.ID, Range: rng, Source: appmetrics.SourcePrometheus,
		AsOf:     time.Now().UTC().Format(time.RFC3339Nano),
		Policies: make([]api.PreAuthPolicyObservation, 0),
	}
	if config := app.Manifest.PreAuthRateLimit; config != nil && config.Mode != api.PreAuthRateLimitOff {
		if (config.Mode != api.PreAuthRateLimitObserve && config.Mode != api.PreAuthRateLimitEnforce) || config.ValidateRoutes() != nil {
			resp.Source = appmetrics.SourceDegradedPrefix + "invalid pre-auth policy"
			return resp
		}
		resp.Policies = append(resp.Policies, api.PreAuthPolicyObservation{PolicyID: "app", Kind: "app"})
		for i, route := range config.Routes {
			key := fmt.Sprint(i)
			resp.Policies = append(resp.Policies, api.PreAuthPolicyObservation{
				PolicyID: "route_" + key, Kind: "route", Method: route.Method, Path: route.Path,
			})
			if route.FailedResponses != nil {
				resp.Policies = append(resp.Policies, api.PreAuthPolicyObservation{
					PolicyID: "failures_" + key, Kind: "failures", Method: route.Method, Path: route.Path,
				})
			}
			if route.ObserveTargets {
				resp.Policies = append(resp.Policies, api.PreAuthPolicyObservation{
					PolicyID: "targets_" + key, Kind: "targets", Method: route.Method, Path: route.Path,
				})
			}
		}
	}
	if len(resp.Policies) == 0 {
		return resp
	}
	if s.promqlClient == nil {
		resp.Source = appmetrics.SourceDegradedPrefix + "prometheus not configured"
		return resp
	}
	if strings.ContainsAny(app.ID, "\"\\\r\n") {
		resp.Source = appmetrics.SourceDegradedPrefix + "invalid app id"
		return resp
	}
	query := fmt.Sprintf(`sum by (policy, outcome) (increase(gateway_pre_auth_policy_shadow_total{app=%q}[%s]))`, app.ID, rng)
	samples, err := s.promqlClient.QueryVector(ctx, query)
	if err != nil {
		resp.Source = appmetrics.SourceDegradedPrefix + "prometheus unavailable"
		return resp
	}
	byPolicy := make(map[string]*api.PreAuthPolicyObservation, len(resp.Policies))
	for i := range resp.Policies {
		byPolicy[resp.Policies[i].PolicyID] = &resp.Policies[i]
	}
	for _, sample := range samples {
		policy := byPolicy[sample.Labels["policy"]]
		if policy == nil || sample.Value < 0 || math.IsNaN(sample.Value) || math.IsInf(sample.Value, 0) || sample.Value >= float64(1<<63-1) {
			continue
		}
		count := int64(math.Round(sample.Value))
		switch sample.Labels["outcome"] {
		case "would_block":
			policy.WouldBlock = count
		case "result_2xx":
			policy.Result2xx = count
		case "result_3xx":
			policy.Result3xx = count
		case "result_4xx":
			policy.Result4xx = count
		case "result_5xx":
			policy.Result5xx = count
		case "result_unknown":
			policy.ResultUnknown = count
		case "target_failure":
			policy.TargetFailures = count
		case "target_threshold":
			policy.TargetThreshold = count
		case "target_missing":
			policy.TargetMissing = count
		case "target_invalid":
			policy.TargetInvalid = count
		case "target_fallback":
			policy.TargetFallback = count
		}
	}
	if app.Manifest.PreAuthRateLimit.Mode == api.PreAuthRateLimitObserve {
		// Advice only; a failed volume query omits the suggestion rather than
		// guessing at readiness.
		if requests, ok := s.appRequestCount(ctx, app.ID, rng); ok {
			suggestion := api.SuggestPreAuthEnforcement(rng, requests, resp.Policies)
			resp.Suggestion = &suggestion
		}
	}
	return resp
}

// appRequestCount is the app's total gateway request count over rng.
func (s *server) appRequestCount(ctx context.Context, appID, rng string) (int64, bool) {
	samples, err := s.promqlClient.QueryVector(ctx, fmt.Sprintf(`sum(increase(gateway_requests_total{app=%q}[%s]))`, appID, rng))
	if err != nil {
		return 0, false
	}
	if len(samples) == 0 {
		return 0, true
	}
	value := samples[0].Value
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) || value >= float64(1<<63-1) {
		return 0, false
	}
	return int64(math.Round(value)), true
}
