package main

import (
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
	resp := api.PreAuthObservationsResponse{
		AppID: app.ID, Range: rng, Source: appmetrics.SourcePrometheus,
		AsOf:     time.Now().UTC().Format(time.RFC3339Nano),
		Policies: make([]api.PreAuthPolicyObservation, 0),
	}
	if config := app.Manifest.PreAuthRateLimit; config != nil && config.Mode != api.PreAuthRateLimitOff {
		if (config.Mode != api.PreAuthRateLimitObserve && config.Mode != api.PreAuthRateLimitEnforce) || config.ValidateRoutes() != nil {
			resp.Source = appmetrics.SourceDegradedPrefix + "invalid pre-auth policy"
			writeJSON(w, http.StatusOK, resp)
			return
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
		}
	}
	if len(resp.Policies) == 0 {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if s.promqlClient == nil {
		resp.Source = appmetrics.SourceDegradedPrefix + "prometheus not configured"
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if strings.ContainsAny(app.ID, "\"\\\r\n") {
		resp.Source = appmetrics.SourceDegradedPrefix + "invalid app id"
		writeJSON(w, http.StatusOK, resp)
		return
	}
	query := fmt.Sprintf(`sum by (policy, outcome) (increase(gateway_pre_auth_policy_shadow_total{app=%q}[%s]))`, app.ID, rng)
	samples, err := s.promqlClient.QueryVector(r.Context(), query)
	if err != nil {
		resp.Source = appmetrics.SourceDegradedPrefix + "prometheus unavailable"
		writeJSON(w, http.StatusOK, resp)
		return
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
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
