// Package routehealth compares bounded, identical candidate/stable observation windows.
package routehealth

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
)

func Validate(request api.SetRouteHealthGateRequest) error {
	if request.OnRegression != "" && request.OnRegression != "hold" && request.OnRegression != "abort" {
		return errors.New("on_regression must be hold or abort")
	}
	if request.Mode != "report" && request.Mode != "enforce" || request.ExpectedRevision == nil || *request.ExpectedRevision < 0 || *request.ExpectedRevision > api.RouteRequirementsMaxRevision || request.Routes == nil || len(request.Routes) > api.RouteHealthMaxRoutes || request.Mode == "enforce" && len(request.Routes) == 0 {
		return fmt.Errorf("supply report/enforce, expected_revision, and a routes array of at most %d entries; enforcement requires routes", api.RouteHealthMaxRoutes)
	}
	seen := map[[2]string]bool{}
	for _, route := range request.Routes {
		switch route.Method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		default:
			return errors.New("unsupported route method")
		}
		if len(route.Path) == 0 || len(route.Path) > api.RouteHealthMaxPathBytes || !strings.HasPrefix(route.Path, "/") || strings.ContainsAny(route.Path, "?#*\\") || strings.IndexFunc(route.Path, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 || seen[[2]string{route.Method, route.Path}] {
			return errors.New("routes must be distinct exact normalized telemetry paths without queries or wildcards")
		}
		if route.MaxP95MS < 0 || route.MaxP95MS > api.RouteHealthMaxP95BudgetMS {
			return fmt.Errorf("max_p95_ms must be between 0 (disabled) and %d", api.RouteHealthMaxP95BudgetMS)
		}
		if err := ValidateWatchStatuses(route.WatchStatuses); err != nil {
			return err
		}
		seen[[2]string{route.Method, route.Path}] = true
	}
	return nil
}

// Windows selects closed UTC minutes behind an ingestion allowance. Evidence
// before the current stage or configuration update cannot authorize progress.
func Windows(now time.Time) []api.RouteHealthWindowEvidence {
	end := now.UTC().Add(-api.RouteHealthIngestionLag).Truncate(api.RouteHealthWindow)
	out := make([]api.RouteHealthWindowEvidence, api.RouteHealthWindows)
	for i := range out {
		out[i].End = end.Add(-time.Duration(len(out)-1-i) * api.RouteHealthWindow)
		out[i].Start = out[i].End.Add(-api.RouteHealthWindow)
	}
	return out
}

// LatencyEnabled preserves prior selectors' 5xx-only behavior.
func LatencyEnabled(check bool, budget int64) bool { return check || budget > 0 }

func Evaluate(report *api.RouteHealthReport, anchor *time.Time, unavailable string) {
	report.Status, report.Reason = "healthy", "comparisons_healthy"
	report.Coverage, report.MinimumRequests, report.MinimumLatencyRequests = "observed_only", api.RouteHealthMinRequests, 0
	if len(report.Routes) == 0 {
		report.Status, report.Reason = "disabled", "no_routes_selected"
		return
	}
	for i := range report.Routes {
		finding := &report.Routes[i]
		latency := LatencyEnabled(finding.CheckLatency, finding.MaxP95MS)
		if latency {
			report.MinimumLatencyRequests = api.RouteHealthMinLatencyRequests
		}
		evaluateWindows(finding.Windows, *finding, anchor, unavailable)
		SummarizeFinding(finding)
		applyPooledEvidence(finding, anchor, unavailable)
		report.Status, report.Reason = combine(report.Status, report.Reason, finding.Status, finding.Reason)
	}
}

func evaluateWindows(windows []api.RouteHealthWindowEvidence, finding api.RouteHealthFinding, anchor *time.Time, unavailable string) {
	latency := LatencyEnabled(finding.CheckLatency, finding.MaxP95MS)
	for j := range windows {
		w := &windows[j]
		rate(&w.Candidate)
		rate(&w.Stable)
		w.ErrorStatus, w.ErrorReason = errorWindow(*w, anchor, unavailable)
		w.LatencyStatus, w.LatencyReason, w.LatencyDeltaMS, w.LatencyFactor = "", "", nil, nil
		w.Status, w.Reason = w.ErrorStatus, w.ErrorReason
		if latency {
			w.LatencyStatus, w.LatencyReason = latencyWindow(w, finding.MaxP95MS, finding.CheckLatency, anchor, unavailable)
			w.Status, w.Reason = combine(w.ErrorStatus, w.ErrorReason, w.LatencyStatus, w.LatencyReason)
		}
	}
}

// SummarizeFinding confirms each selected signal independently across windows.
// Legacy 5xx-only windows can use their combined status when error_status is absent.
func SummarizeFinding(finding *api.RouteHealthFinding) {
	errors, latencies := []string{}, []string{}
	latency := LatencyEnabled(finding.CheckLatency, finding.MaxP95MS)
	for _, w := range finding.Windows {
		status := w.ErrorStatus
		if status == "" && !latency {
			status = w.Status
		}
		errors = append(errors, status)
		if latency {
			latencies = append(latencies, w.LatencyStatus)
		}
	}
	finding.ErrorStatus, finding.ErrorReason = consecutive(errors, "consecutive_server_error_regression")
	finding.LatencyStatus, finding.LatencyReason = "", ""
	finding.Status, finding.Reason = finding.ErrorStatus, finding.ErrorReason
	if latency {
		finding.LatencyStatus, finding.LatencyReason = consecutive(latencies, "consecutive_latency_violation")
		finding.Status, finding.Reason = combine(finding.ErrorStatus, finding.ErrorReason, finding.LatencyStatus, finding.LatencyReason)
	}
}

func windowUnavailable(w api.RouteHealthWindowEvidence, anchor *time.Time, unavailable string) string {
	if unavailable != "" {
		return unavailable
	}
	if anchor == nil || w.Start.Before(*anchor) {
		return "observation_window_not_elapsed"
	}
	if !valid(w.Candidate) || !valid(w.Stable) {
		return "invalid_counts"
	}
	return ""
}
func errorWindow(w api.RouteHealthWindowEvidence, anchor *time.Time, unavailable string) (string, string) {
	if reason := windowUnavailable(w, anchor, unavailable); reason != "" {
		return "unknown", reason
	}
	if w.Candidate.Requests < api.RouteHealthMinRequests || w.Stable.Requests < api.RouteHealthMinRequests {
		return "unknown", "insufficient_requests"
	}
	if w.Candidate.ServerErrors >= api.RouteHealthMinErrors && w.Candidate.ErrorRate+api.RouteHealthComparisonEpsilon >= api.RouteHealthErrorRateFloor && w.Candidate.ErrorRate+api.RouteHealthComparisonEpsilon >= w.Stable.ErrorRate*api.RouteHealthErrorRateFactor && w.Candidate.ErrorRate-w.Stable.ErrorRate+api.RouteHealthComparisonEpsilon >= api.RouteHealthErrorRateDelta {
		return "regressed", "server_error_rate_increased"
	}
	return "healthy", "comparison_healthy"
}
func latencyWindow(w *api.RouteHealthWindowEvidence, budget int64, checkRelative bool, anchor *time.Time, unavailable string) (string, string) {
	if reason := windowUnavailable(*w, anchor, unavailable); reason != "" {
		return "unknown", reason
	}
	if w.Candidate.Requests < api.RouteHealthMinLatencyRequests || w.Stable.Requests < api.RouteHealthMinLatencyRequests {
		return "unknown", "insufficient_latency_requests"
	}
	if !validP95(w.Candidate.P95LatencyMS) || !validP95(w.Stable.P95LatencyMS) {
		return "unknown", "latency_evidence_unavailable"
	}
	candidate, stable := *w.Candidate.P95LatencyMS, *w.Stable.P95LatencyMS
	delta := candidate - stable
	w.LatencyDeltaMS = &delta
	if stable > 0 {
		factor := candidate / stable
		if !math.IsInf(factor, 0) {
			w.LatencyFactor = &factor
		}
	}
	exceeded := budget > 0 && candidate > float64(budget)
	increased := checkRelative && candidate+api.RouteHealthComparisonEpsilon >= stable*api.RouteHealthLatencyFactor && delta+api.RouteHealthComparisonEpsilon >= api.RouteHealthLatencyDeltaMS
	switch {
	case exceeded && increased:
		return "regressed", "latency_budget_and_regression"
	case exceeded:
		return "regressed", "latency_budget_exceeded"
	case increased:
		return "regressed", "p95_latency_increased"
	default:
		return "healthy", "latency_comparison_healthy"
	}
}
func validP95(value *float64) bool {
	return value != nil && *value >= 0 && !math.IsNaN(*value) && !math.IsInf(*value, 0)
}
func consecutive(statuses []string, regression string) (string, string) {
	if len(statuses) != api.RouteHealthWindows {
		return "unknown", "comparisons_incomplete_or_unsettled"
	}
	healthy, regressed := true, true
	for _, status := range statuses {
		healthy = healthy && status == "healthy"
		regressed = regressed && status == "regressed"
	}
	if regressed {
		return "regressed", regression
	}
	if healthy {
		return "healthy", "comparisons_healthy"
	}
	return "unknown", "comparisons_incomplete_or_unsettled"
}

// An established violation takes priority over unavailable evidence in another signal.
func combine(a, ar, b, br string) (string, string) {
	if a == "regressed" {
		return a, ar
	}
	if b == "regressed" {
		return b, br
	}
	if a == "unknown" {
		return a, ar
	}
	if b == "unknown" {
		return b, br
	}
	return a, ar
}
func valid(c api.RouteHealthCounts) bool {
	return c.Requests >= 0 && c.ServerErrors >= 0 && c.ServerErrors <= c.Requests
}
func rate(c *api.RouteHealthCounts) {
	c.ErrorRate = 0
	if c.Requests > 0 {
		c.ErrorRate = float64(c.ServerErrors) / float64(c.Requests)
	}
}
func Decision(report api.RouteHealthReport) api.RouteHealthDecision {
	status := "report_only"
	if report.Mode == "enforce" {
		status = "blocked"
		if report.Status == "healthy" {
			status = "allowed"
		}
	}
	return api.RouteHealthDecision{OnRegression: report.OnRegression, Mode: report.Mode, Revision: report.Revision, DeploymentID: report.DeploymentID, StableDeploymentID: report.StableDeploymentID, CheckedAt: report.CheckedAt, Status: status, Reason: report.Reason}
}

// ValidateWatchStatuses accepts only explicit, distinct supported client errors.
func ValidateWatchStatuses(statuses []int) error {
	if len(statuses) > api.RouteHealthMaxWatchedStatuses {
		return errors.New("too many watched statuses")
	}
	seen := map[int]bool{}
	for _, code := range statuses {
		if !slices.Contains([]int{401, 403, 404, 422, 429}, code) || seen[code] {
			return errors.New("watch_statuses must contain distinct codes from 401, 403, 404, 422, 429")
		}
		seen[code] = true
	}
	return nil
}

func RoutesEqual(a, b []api.RouteHealthRoute) bool {
	return slices.EqualFunc(a, b, func(x, y api.RouteHealthRoute) bool {
		return x.Method == y.Method && x.Path == y.Path && x.CheckLatency == y.CheckLatency && x.MaxP95MS == y.MaxP95MS && slices.Equal(x.WatchStatuses, y.WatchStatuses)
	})
}

func CloneRoutes(routes []api.RouteHealthRoute) []api.RouteHealthRoute {
	out := slices.Clone(routes)
	for i := range out {
		out[i].WatchStatuses = slices.Clone(out[i].WatchStatuses)
	}
	return out
}
