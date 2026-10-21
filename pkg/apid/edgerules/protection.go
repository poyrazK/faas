package edgerules

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// EdgeProtectionDefaultRange is long enough to show a burst after the fact
// while staying inside short Prometheus retention.
const EdgeProtectionDefaultRange = "1h"

// getAppEdgeProtection serves GET /v1/apps/{slug}/edge-protection.
func (s *Service) GetAppEdgeProtection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = EdgeProtectionDefaultRange
	}
	if !appmetrics.IsValidRange(rng) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid range", "range must be one of: "+strings.Join(appmetrics.Ranges(), ", ")))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, s.AppEdgeProtection(r.Context(), app, rng))
}

// appEdgeProtection is shared by the JSON endpoint and the dashboard panel.
// It reads the same per-app counters the edge security alert presets use.
func (s *Service) AppEdgeProtection(ctx context.Context, app state.App, rng string) api.EdgeProtectionResponse {
	resp := api.EdgeProtectionResponse{
		AppID: app.ID, Range: rng, Source: appmetrics.SourcePrometheus,
		AsOf:               time.Now().UTC().Format(time.RFC3339Nano),
		ValidationFailures: []api.EdgeProtectionCount{},
		Rejections:         []api.EdgeProtectionRejection{},
		WAF:                api.NewEdgeProtectionWAF(),
	}
	if row, err := s.attackModeRule(ctx, app.ID); err == nil {
		resp.AttackMode = AttackModeStatus(row, time.Now())
	}
	if s.Host.PromQL() == nil {
		resp.Source = appmetrics.SourceDegradedPrefix + "prometheus not configured"
		return resp
	}
	if strings.ContainsAny(app.ID, "\"\\\r\n") {
		resp.Source = appmetrics.SourceDegradedPrefix + "invalid app id"
		return resp
	}
	for _, q := range edgeProtectionQueries(&resp, app.ID, rng) {
		samples, err := s.Host.PromQL().QueryVector(ctx, q.query)
		if err != nil {
			degraded := api.EdgeProtectionResponse{AppID: resp.AppID, Range: rng, AsOf: resp.AsOf,
				Source:             appmetrics.SourceDegradedPrefix + "prometheus unavailable",
				ValidationFailures: []api.EdgeProtectionCount{}, Rejections: []api.EdgeProtectionRejection{},
				WAF: api.NewEdgeProtectionWAF(), AttackMode: resp.AttackMode}
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

// windowedCount is the count of a per-app counter's increments within rng,
// grouped by the caller's "sum by". These counters are created on their
// first increment, which increase() never sees, so three WAF blocks summed
// to 0 and a throttle 429 to "none" (H10-13). A series that existed at the
// window start is counted by increase(); one born inside it by its value.
func windowedCount(selector, rng string) string {
	return fmt.Sprintf("((increase(%s[%s]) and %s offset %s) or %s)", selector, rng, selector, rng, selector)
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
			"sum by (outcome) " + windowedCount(fmt.Sprintf(`gateway_pre_auth_rate_limit_total{app=%q,outcome=~"blocked|route_blocked|would_block|route_would_block"}`, appID), rng),
			func(labels map[string]string, count int64) {
				if strings.HasSuffix(labels["outcome"], "would_block") {
					resp.PreAuth.WouldBlock += count
				} else {
					resp.PreAuth.Blocked += count
				}
			},
		},
		{
			"sum by (mode) " + windowedCount(fmt.Sprintf(`gateway_validate_failures_total{app_id=%q}`, appID), rng),
			func(labels map[string]string, count int64) {
				resp.ValidationFailures = append(resp.ValidationFailures, api.EdgeProtectionCount{Name: labels["mode"], Count: count})
			},
		},
		{
			"sum by (kind, status) " + windowedCount(fmt.Sprintf(`gateway_edge_rejections_total{app=%q}`, appID), rng),
			func(labels map[string]string, count int64) {
				resp.Rejections = append(resp.Rejections, api.EdgeProtectionRejection{Gate: labels["kind"], Status: labels["status"], Count: count})
			},
		},
		{
			"sum by (outcome) " + windowedCount(fmt.Sprintf(`gateway_waf_inspections_total{app=%q}`, appID), rng),
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
			"sum by (outcome) " + windowedCount(fmt.Sprintf(`gateway_waf_inline_checks_total{app=%q,outcome=~"warned|blocked|skipped"}`, appID), rng),
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
			"sum by (outcome) " + windowedCount(fmt.Sprintf(`gateway_edge_challenge_total{app=%q}`, appID), rng),
			func(labels map[string]string, count int64) {
				switch labels["outcome"] {
				case "issued":
					resp.Challenge.Issued += count
				case "solved":
					resp.Challenge.Solved += count
				case "rejected":
					resp.Challenge.Rejected += count
				case "passed":
					resp.Challenge.Passed += count
				case "bypassed":
					resp.Challenge.Bypassed += count
				case "invalid", "expired":
					resp.Challenge.Invalid += count
				}
			},
		},
		{
			"sum by (category) " + windowedCount(fmt.Sprintf(`gateway_waf_detections_total{app=%q}`, appID), rng),
			func(labels map[string]string, count int64) {
				resp.WAF.Categories = append(resp.WAF.Categories, api.EdgeProtectionCount{Name: labels["category"], Count: count})
			},
		},
		{
			fmt.Sprintf("topk(%d, sum by (rule_id) %s)", api.EdgeProtectionWAFTopRules, windowedCount(fmt.Sprintf(`gateway_waf_rule_matches_total{app=%q}`, appID), rng)),
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
