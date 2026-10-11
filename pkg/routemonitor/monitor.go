// Package routemonitor evaluates absolute budgets on observed production traffic.
package routemonitor

import (
	"errors"
	"math"
	"math/big"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func Validate(req api.SetRouteMonitorRequest) error {
	if req.CustomerGroupBy != "" && req.CustomerGroupBy != "tenant" && req.CustomerGroupBy != "consumer" {
		return errors.New("customer_group_by must be tenant or consumer; omit to disable")
	}
	selectors := make([]api.RouteHealthRoute, len(req.Routes))
	for i, r := range req.Routes {
		selectors[i] = api.RouteHealthRoute{Method: r.Method, Path: r.Path, MaxP95MS: r.MaxP95MS}
		if r.Max5xxRateBPS == nil && r.MaxP95MS == 0 {
			return errors.New("each route requires an error or latency budget")
		}
		if r.Max5xxRateBPS != nil && (*r.Max5xxRateBPS < 0 || *r.Max5xxRateBPS > api.RouteMonitorMaxRateBPS) {
			return errors.New("max_5xx_rate_bps must be between 0 and 10000; omit to disable")
		}
	}
	if req.Routes == nil || req.Enabled && len(req.Routes) == 0 {
		return errors.New("supply a routes array; enabled monitoring requires routes")
	}
	switch OnViolation(req.OnViolation) {
	case "report":
	case "rollback":
		// Automatic rollback acts only on error budgets (ADR-845).
		if !slices.ContainsFunc(req.Routes, func(r api.RouteMonitorRoute) bool { return r.Max5xxRateBPS != nil }) {
			return errors.New("on_violation rollback requires at least one route with max_5xx_rate_bps")
		}
	default:
		return errors.New("on_violation must be report or rollback")
	}
	return routehealth.Validate(api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: req.ExpectedRevision, Routes: selectors})
}

func ValidatePreviewRequest(req api.PreviewRouteMonitorRequest, currentRevision int64) error {
	return Validate(api.SetRouteMonitorRequest{
		CustomerGroupBy:  req.CustomerGroupBy,
		Enabled:          true,
		ExpectedRevision: &currentRevision,
		Routes:           req.Routes,
	})
}

func ValidatePreview(preview api.RouteMonitorPreview) error {
	if !preview.PreviewOnly || preview.CurrentRevision < 0 || preview.Report.Revision != preview.CurrentRevision || !preview.Report.Enabled {
		return errors.New("invalid route monitor preview metadata")
	}
	return ValidateReport(preview.Report)
}

func ValidatePreviewForRequest(preview api.RouteMonitorPreview, req api.PreviewRouteMonitorRequest) error {
	if err := ValidatePreview(preview); err != nil {
		return err
	}
	if preview.Report.CustomerGroupBy != req.CustomerGroupBy || len(preview.Report.Routes) != len(req.Routes) {
		return errors.New("route monitor preview does not match the proposed budgets")
	}
	routes := make([]api.RouteMonitorRoute, len(preview.Report.Routes))
	for i := range preview.Report.Routes {
		routes[i] = preview.Report.Routes[i].Route
	}
	if !RoutesEqual(routes, req.Routes) {
		return errors.New("route monitor preview does not match the proposed budgets")
	}
	return nil
}
func CloneRoutes(in []api.RouteMonitorRoute) []api.RouteMonitorRoute {
	out := slices.Clone(in)
	for i := range out {
		if out[i].Max5xxRateBPS != nil {
			v := *out[i].Max5xxRateBPS
			out[i].Max5xxRateBPS = &v
		}
	}
	return out
}
func RoutesEqual(a, b []api.RouteMonitorRoute) bool { return reflect.DeepEqual(a, b) }
func NewReport(config api.RouteMonitorConfig, now time.Time) api.RouteMonitorReport {
	r := api.RouteMonitorReport{CustomerGroupBy: config.CustomerGroupBy, Version: api.RouteMonitorVersion, AppID: config.AppID, Enabled: config.Enabled, Revision: config.Revision, CheckedAt: now.UTC(), Coverage: "observed_only", MinimumRequests: api.RouteHealthMinRequests, MinimumLatencyRequests: api.RouteHealthMinLatencyRequests, Routes: []api.RouteMonitorFinding{}}
	for _, selector := range CloneRoutes(config.Routes) {
		f := api.RouteMonitorFinding{Route: selector, Windows: []api.RouteMonitorWindow{}}
		for _, w := range routehealth.Windows(now) {
			f.Windows = append(f.Windows, api.RouteMonitorWindow{Start: w.Start, End: w.End})
		}
		r.Routes = append(r.Routes, f)
	}
	initializeCustomers(&r)
	return r
}
func Evaluate(r *api.RouteMonitorReport, unavailable string) {
	r.Status, r.Reason = "healthy", "budgets_satisfied"
	if !r.Enabled {
		unavailable = "monitor_disabled"
		r.Status, r.Reason = "disabled", unavailable
	}
	for i := range r.Routes {
		f := &r.Routes[i]
		evaluateFinding(f, r.ObservationAnchor, unavailable)
		if r.Enabled {
			r.Status = combine(r.Status, f.Status)
		}
	}
	if r.Enabled && r.Status == "violated" {
		r.Reason = "sustained_budget_violation"
	}
	if r.Enabled && r.Status == "unknown" {
		r.Reason = "evidence_incomplete_or_unsettled"
		if unavailable != "" {
			r.Reason = unavailable
		}
	}
	evaluateCustomers(r, unavailable)
}
func evaluateFinding(f *api.RouteMonitorFinding, anchor *time.Time, unavailable string) {
	evaluateWindows(f, anchor, unavailable)
	applyPooledEvidence(f, anchor, unavailable)
}

// evaluateWindows derives window and finding verdicts from f.Windows.
func evaluateWindows(f *api.RouteMonitorFinding, anchor *time.Time, unavailable string) {
	errors, latencies := []string{}, []string{}
	for j := range f.Windows {
		w := &f.Windows[j]
		w.Observed.ErrorRate = 0
		if w.Observed.Requests > 0 {
			w.Observed.ErrorRate = float64(w.Observed.ServerErrors) / float64(w.Observed.Requests)
		}
		reason := unavailable
		if reason == "" && (anchor == nil || w.Start.Before(*anchor)) {
			reason = "observation_window_not_elapsed"
		}
		w.ErrorStatus, w.ErrorReason = errorWindow(w.Observed, f.Route.Max5xxRateBPS, reason)
		w.LatencyStatus, w.LatencyReason = latencyWindow(w.Observed, f.Route.MaxP95MS, reason)
		errors = append(errors, w.ErrorStatus)
		latencies = append(latencies, w.LatencyStatus)
	}
	f.ErrorStatus = consecutive(errors)
	f.LatencyStatus = consecutive(latencies)
	f.Status = combine(f.ErrorStatus, f.LatencyStatus)
	f.Reason = "budgets_satisfied"
	if f.Status == "violated" {
		f.Reason = "sustained_budget_violation"
	}
	if f.Status == "unknown" {
		f.Reason = "evidence_incomplete_or_unsettled"
	}
}
func EvaluateFinding(f *api.RouteMonitorFinding, anchor *time.Time) { evaluateFinding(f, anchor, "") }
func errorWindow(c api.RouteHealthCounts, budget *int64, unavailable string) (string, string) {
	if budget == nil {
		return "disabled", "budget_not_selected"
	}
	if unavailable != "" {
		return "unknown", unavailable
	}
	if c.Requests < api.RouteHealthMinRequests {
		return "unknown", "insufficient_requests"
	}
	// Compare integers without overflowing int64 or rounding a threshold equality.
	left := new(big.Int).Mul(big.NewInt(c.ServerErrors), big.NewInt(api.RouteMonitorMaxRateBPS))
	right := new(big.Int).Mul(big.NewInt(c.Requests), big.NewInt(*budget))
	if left.Cmp(right) > 0 {
		if c.ServerErrors < api.RouteHealthMinErrors {
			return "unknown", "insufficient_errors"
		}
		return "violated", "server_error_budget_exceeded"
	}
	return "healthy", "error_budget_satisfied"
}
func latencyWindow(c api.RouteHealthCounts, budget int64, unavailable string) (string, string) {
	if budget == 0 {
		return "disabled", "budget_not_selected"
	}
	if unavailable != "" {
		return "unknown", unavailable
	}
	if c.Requests < api.RouteHealthMinLatencyRequests {
		return "unknown", "insufficient_latency_requests"
	}
	if c.P95LatencyMS == nil {
		return "unknown", "latency_evidence_unavailable"
	}
	if *c.P95LatencyMS > float64(budget) {
		return "violated", "latency_budget_exceeded"
	}
	return "healthy", "latency_budget_satisfied"
}
func consecutive(statuses []string) string {
	if len(statuses) != api.RouteHealthWindows {
		return "unknown"
	}
	for _, s := range statuses {
		if s != statuses[0] {
			return "unknown"
		}
	}
	return statuses[0]
}
func combine(a, b string) string {
	if a == "violated" || b == "violated" {
		return "violated"
	}
	if a == "unknown" || b == "unknown" {
		return "unknown"
	}
	if a == "healthy" || b == "healthy" {
		return "healthy"
	}
	return "disabled"
}
func ValidateConfig(c api.RouteMonitorConfig) error {
	if !validUUID(c.AppID) || c.Revision == 0 && (c.CustomerGroupBy != "" || c.Enabled || len(c.Routes) > 0 || c.UpdatedAt != nil) || c.Revision > 0 && (c.UpdatedAt == nil || c.UpdatedAt.IsZero()) {
		return errors.New("invalid monitor configuration identity or revision")
	}
	return Validate(api.SetRouteMonitorRequest{CustomerGroupBy: c.CustomerGroupBy, Enabled: c.Enabled, ExpectedRevision: &c.Revision, Routes: c.Routes})
}
func validUUID(id string) bool { _, e := uuid.Parse(id); return e == nil }
func ValidateReport(r api.RouteMonitorReport) error {
	if r.Version != api.RouteMonitorVersion || !validUUID(r.AppID) || r.Coverage != "observed_only" || r.CheckedAt.IsZero() || r.MinimumRequests != api.RouteHealthMinRequests || r.MinimumLatencyRequests != api.RouteHealthMinLatencyRequests || r.DeploymentID != "" && !validUUID(r.DeploymentID) {
		return errors.New("invalid monitor report identity or policy")
	}
	selectors := []api.RouteMonitorRoute{}
	expected := routehealth.Windows(r.CheckedAt)
	copy := r
	copy.Routes = slices.Clone(r.Routes)
	copy.Customers = cloneCustomers(r.Customers)
	for i, f := range r.Routes {
		selectors = append(selectors, f.Route)
		if len(f.Windows) != len(expected) {
			return errors.New("invalid monitor windows")
		}
		copy.Routes[i].Windows = slices.Clone(f.Windows)
		for j, w := range f.Windows {
			c := w.Observed
			if !w.Start.Equal(expected[j].Start) || !w.End.Equal(expected[j].End) || !validMonitorCounts(c) {
				return errors.New("invalid monitor observations")
			}
		}
		if len(f.PooledWindows) > 0 {
			pooled, ok := PooledWindows(r.ObservationAnchor, r.CheckedAt)
			if !ok || len(f.PooledWindows) != len(pooled) {
				return errors.New("invalid pooled monitor windows")
			}
			copy.Routes[i].PooledWindows = slices.Clone(f.PooledWindows)
			for j, w := range f.PooledWindows {
				if !w.Start.Equal(pooled[j].Start) || !w.End.Equal(pooled[j].End) || !validMonitorCounts(w.Observed) {
					return errors.New("invalid pooled monitor observations")
				}
			}
		}
	}
	if err := Validate(api.SetRouteMonitorRequest{CustomerGroupBy: r.CustomerGroupBy, Enabled: r.Enabled, ExpectedRevision: &r.Revision, Routes: selectors}); err != nil {
		return err
	}
	unavailable := ""
	switch r.Reason {
	case "telemetry_not_entitled", "account_ineligible", "serving_deployment_ambiguous_or_missing", "serving_deployment_not_ready", "telemetry_unavailable":
		unavailable = r.Reason
	}
	if r.Enabled && unavailable == "" && (r.DeploymentID == "" || r.ObservationAnchor == nil) {
		return errors.New("monitor has no serving context")
	}
	if err := validateCustomers(r); err != nil {
		return err
	}
	Evaluate(&copy, unavailable)
	if !reflect.DeepEqual(copy, r) {
		return errors.New("monitor verdict or rates do not match observed evidence")
	}
	return nil
}

func validMonitorCounts(c api.RouteHealthCounts) bool {
	return c.Requests >= 0 && c.ServerErrors >= 0 && c.ServerErrors <= c.Requests && !math.IsNaN(c.ErrorRate) && !math.IsInf(c.ErrorRate, 0) && (c.P95LatencyMS == nil || *c.P95LatencyMS >= 0 && !math.IsNaN(*c.P95LatencyMS) && !math.IsInf(*c.P95LatencyMS, 0))
}
