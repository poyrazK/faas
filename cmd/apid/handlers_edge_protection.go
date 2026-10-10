package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// edgeProtectionDefaultRange is long enough to show a burst after the fact
// while staying inside short Prometheus retention.
const edgeProtectionDefaultRange = "1h"

// getAppEdgeProtection serves GET /v1/apps/{slug}/edge-protection.
func (s *server) getAppEdgeProtection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = edgeProtectionDefaultRange
	}
	if !appmetrics.IsValidRange(rng) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid range", "range must be one of: "+strings.Join(appmetrics.Ranges(), ", ")))
		return
	}
	writeJSON(w, http.StatusOK, s.appEdgeProtection(r.Context(), app, rng))
}

// appEdgeProtection is shared by the JSON endpoint and the dashboard panel.
// It reads the same per-app counters the edge security alert presets use.
func (s *server) appEdgeProtection(ctx context.Context, app state.App, rng string) api.EdgeProtectionResponse {
	resp := api.EdgeProtectionResponse{
		AppID: app.ID, Range: rng, Source: appmetrics.SourcePrometheus,
		AsOf:               time.Now().UTC().Format(time.RFC3339Nano),
		ValidationFailures: []api.EdgeProtectionCount{},
		Rejections:         []api.EdgeProtectionRejection{},
		WAF:                api.NewEdgeProtectionWAF(),
	}
	if s.promqlClient == nil {
		resp.Source = appmetrics.SourceDegradedPrefix + "prometheus not configured"
		return resp
	}
	if strings.ContainsAny(app.ID, "\"\\\r\n") {
		resp.Source = appmetrics.SourceDegradedPrefix + "invalid app id"
		return resp
	}
	for _, q := range edgeProtectionQueries(&resp, app.ID, rng) {
		samples, err := s.promqlClient.QueryVector(ctx, q.query)
		if err != nil {
			degraded := api.EdgeProtectionResponse{AppID: resp.AppID, Range: rng, AsOf: resp.AsOf,
				Source:             appmetrics.SourceDegradedPrefix + "prometheus unavailable",
				ValidationFailures: []api.EdgeProtectionCount{}, Rejections: []api.EdgeProtectionRejection{},
				WAF: api.NewEdgeProtectionWAF()}
			return degraded
		}
		for _, sample := range samples {
			if sample.Value < 0 || math.IsNaN(sample.Value) || math.IsInf(sample.Value, 0) || sample.Value >= float64(1<<63-1) {
				continue
			}
			if count := int64(math.Round(sample.Value)); count > 0 {
				q.apply(sample.Labels, count)
			}
		}
	}
	sortEdgeProtection(&resp)
	return resp
}

type edgeProtectionQuery struct {
	query string
	apply func(labels map[string]string, count int64)
}

// edgeProtectionQueries pairs each per-app counter query with the field its
// samples fill in resp.
func edgeProtectionQueries(resp *api.EdgeProtectionResponse, appID, rng string) []edgeProtectionQuery {
	return []edgeProtectionQuery{
		{
			fmt.Sprintf(`sum by (outcome) (increase(gateway_pre_auth_rate_limit_total{app=%q,outcome=~"blocked|route_blocked|would_block|route_would_block"}[%s]))`, appID, rng),
			func(labels map[string]string, count int64) {
				if strings.HasSuffix(labels["outcome"], "would_block") {
					resp.PreAuth.WouldBlock += count
				} else {
					resp.PreAuth.Blocked += count
				}
			},
		},
		{
			fmt.Sprintf(`sum by (mode) (increase(gateway_validate_failures_total{app_id=%q}[%s]))`, appID, rng),
			func(labels map[string]string, count int64) {
				resp.ValidationFailures = append(resp.ValidationFailures, api.EdgeProtectionCount{Name: labels["mode"], Count: count})
			},
		},
		{
			fmt.Sprintf(`sum by (kind, status) (increase(gateway_edge_rejections_total{app=%q}[%s]))`, appID, rng),
			func(labels map[string]string, count int64) {
				resp.Rejections = append(resp.Rejections, api.EdgeProtectionRejection{Gate: labels["kind"], Status: labels["status"], Count: count})
			},
		},
		{
			fmt.Sprintf(`sum by (outcome) (increase(gateway_waf_inspections_total{app=%q}[%s]))`, appID, rng),
			func(labels map[string]string, count int64) {
				switch labels["outcome"] {
				case "detected":
					resp.WAF.Detected += count
					resp.WAF.Inspected += count
				case "clean":
					resp.WAF.Inspected += count
				default:
					resp.WAF.NotInspected += count
				}
			},
		},
		{
			fmt.Sprintf(`sum by (outcome) (increase(gateway_waf_inline_checks_total{app=%q,outcome=~"warned|blocked|skipped"}[%s]))`, appID, rng),
			func(labels map[string]string, count int64) {
				switch labels["outcome"] {
				case "warned":
					resp.WAF.Warned += count
				case "blocked":
					resp.WAF.Blocked += count
				case "skipped":
					resp.WAF.InlineSkipped += count
				}
			},
		},
		{
			fmt.Sprintf(`sum by (category) (increase(gateway_waf_detections_total{app=%q}[%s]))`, appID, rng),
			func(labels map[string]string, count int64) {
				resp.WAF.Categories = append(resp.WAF.Categories, api.EdgeProtectionCount{Name: labels["category"], Count: count})
			},
		},
		{
			fmt.Sprintf(`topk(%d, sum by (rule_id) (increase(gateway_waf_rule_matches_total{app=%q}[%s])))`, api.EdgeProtectionWAFTopRules, appID, rng),
			func(labels map[string]string, count int64) {
				resp.WAF.TopRules = append(resp.WAF.TopRules, api.EdgeProtectionCount{Name: labels["rule_id"], Count: count})
			},
		},
	}
}

// sortEdgeProtection orders modes by name and rejections and WAF counts by
// count (largest first) so output is deterministic.
func sortEdgeProtection(resp *api.EdgeProtectionResponse) {
	byCount := func(c []api.EdgeProtectionCount) {
		sort.Slice(c, func(i, j int) bool {
			if c[i].Count != c[j].Count {
				return c[i].Count > c[j].Count
			}
			return c[i].Name < c[j].Name
		})
	}
	byCount(resp.WAF.Categories)
	byCount(resp.WAF.TopRules)
	sort.Slice(resp.ValidationFailures, func(i, j int) bool { return resp.ValidationFailures[i].Name < resp.ValidationFailures[j].Name })
	sort.Slice(resp.Rejections, func(i, j int) bool {
		a, b := resp.Rejections[i], resp.Rejections[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Gate+a.Status < b.Gate+b.Status
	})
}
