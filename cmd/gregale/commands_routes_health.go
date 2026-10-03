package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func cmdRoutesHealth(args []string) int {
	if len(args) > 0 && args[0] == "explain" {
		return cmdRoutesHealthExplain(args[1:])
	}
	if len(args) == 0 || args[0] != "get" && args[0] != "set" && args[0] != "report" {
		return printErr("Invalid route health command", errors.New("usage: gregale routes health <get|set|report|explain> APP [flags]"))
	}
	action := args[0]
	flags, positional := splitArgsForFlags(args[1:], "fail-on-unhealthy", "customers", "customer-details")
	fs := newFlagSet("routes health "+action, flag.ContinueOnError)
	var mode, path, deployment string
	var onRegression string
	var revision int64
	var fail bool
	var customerOpts api.RouteHealthReportOptions
	if action == "set" {
		fs.StringVar(&mode, "mode", "", "report or enforce observed route health")
		fs.StringVar(&onRegression, "on-regression", "hold", "hold or automatically abort on confirmed route 5xx regression")
		fs.StringVar(&path, "routes", "", "JSON array of exact method/path selectors with optional latency checks and watched response statuses")
		fs.Int64Var(&revision, "expected-revision", -1, "current configuration revision; 0 initially")
	}
	if action == "report" {
		fs.StringVar(&deployment, "deployment", "", "candidate deployment UUID")
		fs.BoolVar(&fail, "fail-on-unhealthy", false, "exit nonzero unless all selected routes are healthy")
		fs.BoolVar(&customerOpts.Customers, "customers", false, "include advisory customer health comparisons")
		fs.StringVar(&customerOpts.CustomerGroupBy, "customer-group-by", "", "group customer health by tenant (default) or consumer")
		fs.BoolVar(&customerOpts.CustomerDetails, "customer-details", false, "include customer IDs in advisory comparisons; requires --customers")
	}
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) {
		return printErr("Invalid route health command", errors.New("supply an app slug"))
	}
	var request api.SetRouteHealthGateRequest
	if action == "set" {
		routes, err := readRouteHealthSelectors(path)
		if err != nil {
			return printErr("Invalid route selectors", err)
		}
		request = api.SetRouteHealthGateRequest{Mode: mode, OnRegression: onRegression, ExpectedRevision: &revision, Routes: routes}
		if err := routehealth.Validate(request); err != nil {
			return printErr("Invalid route health configuration", err)
		}
	}
	if action == "report" {
		if err := customerOpts.Validate(); err != nil {
			return printErr("Invalid customer health options", err)
		}
		id, err := uuid.Parse(deployment)
		if err != nil || id.String() != deployment {
			return printErr("Invalid route health report", errors.New("supply --deployment with a canonical UUID"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
	defer cancel()
	if action == "report" {
		report, err := client.GetRouteHealthReportWithOptions(ctx, positional[0], deployment, customerOpts)
		if err != nil {
			return printErr("Could not read route health", err)
		}
		if err := validateRouteHealthReport(report, deployment); err != nil {
			return printErr("Invalid route health response", err)
		}
		if err := routehealth.ValidateClientErrors(report); err != nil {
			return printErr("Invalid client error evidence", err)
		}
		if err := prepareCustomerHealthReport(&report, customerOpts); err != nil {
			return printErr("Invalid customer health response", err)
		}
		if jsonOutput {
			if code := jsonOut(writeJSON(report)); code != 0 {
				return code
			}
		} else {
			renderRouteHealthReport(report)
		}
		if fail && report.Status != "healthy" {
			return 1
		}
		return 0
	}
	var gate api.RouteHealthGate
	if action == "set" {
		gate, err = client.SetRouteHealthGate(ctx, positional[0], request)
	} else {
		gate, err = client.GetRouteHealthGate(ctx, positional[0])
	}
	if err != nil {
		return printErr("Could not read or update route health", err)
	}
	if err := validateRouteHealthGate(gate); err != nil {
		return printErr("Invalid route health response", err)
	}
	if action == "set" && (gate.Mode != mode || routehealth.RegressionAction(gate.OnRegression) != onRegression || !routehealth.RoutesEqual(gate.Routes, request.Routes) || gate.Revision != revision && gate.Revision != revision+1) {
		return printErr("Invalid route health response", errors.New("configuration does not match submitted selectors or revision"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(gate))
	}
	_, _ = fmt.Fprintf(osStdout, "Route health for %s: %s (revision %d); on regression: %s\n", positional[0], gate.Mode, gate.Revision, routehealth.RegressionAction(gate.OnRegression))
	for _, r := range gate.Routes {
		_, _ = fmt.Fprintf(osStdout, "  %s %s", r.Method, previewReportText(r.Path))
		if r.MaxP95MS > 0 {
			_, _ = fmt.Fprintf(osStdout, "; p95 budget %dms", r.MaxP95MS)
		}
		if len(r.WatchStatuses) > 0 {
			_, _ = fmt.Fprintf(osStdout, "; advisory watched statuses %v", r.WatchStatuses)
		}
		if r.CheckLatency {
			_, _ = fmt.Fprint(osStdout, "; relative slowdown check enabled")
		}
		_, _ = fmt.Fprintln(osStdout)
	}
	return 0
}
func readRouteHealthSelectors(path string) ([]api.RouteHealthRoute, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, fmt.Errorf("open route selectors: %w", err)
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteHealthRequestMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read route selectors: %w", err)
	}
	if len(body) > api.RouteHealthRequestMaxBytes {
		return nil, errors.New("route selectors exceed the request limit")
	}
	var routes []api.RouteHealthRoute
	if err := json.Unmarshal(body, &routes); err != nil {
		return nil, fmt.Errorf("decode route selectors JSON array: %w", err)
	}
	if routes == nil {
		return nil, errors.New("supply a JSON array; [] explicitly removes selectors in report mode")
	}
	// Detect misspelled selector fields before writing customer intent.
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&routes); err != nil {
		return nil, fmt.Errorf("decode route selectors: %w", err)
	}
	return routes, nil
}
func validateRouteHealthGate(g api.RouteHealthGate) error {
	if _, err := uuid.Parse(g.AppID); err != nil {
		return errors.New("invalid app identity")
	}
	if err := routehealth.Validate(api.SetRouteHealthGateRequest{Mode: g.Mode, OnRegression: g.OnRegression, ExpectedRevision: &g.Revision, Routes: g.Routes}); err != nil {
		return err
	}
	if g.Revision == 0 && (g.Mode != "report" || len(g.Routes) > 0 || routehealth.RegressionAction(g.OnRegression) != "hold") || g.Revision > 0 && g.UpdatedAt == nil {
		return errors.New("invalid configuration revision or timestamp")
	}
	return nil
}
func validateRouteHealthReport(r api.RouteHealthReport, deployment string) error {
	if r.DeploymentID != deployment || r.Coverage != "observed_only" || r.CheckedAt.IsZero() || r.MinimumRequests != api.RouteHealthMinRequests || !slices.Contains([]string{"healthy", "regressed", "unknown", "disabled"}, r.Status) {
		return errors.New("report identity or evidence is invalid")
	}
	selectors := []api.RouteHealthRoute{}
	expected := routehealth.Windows(r.CheckedAt)
	for _, f := range r.Routes {
		selectors = append(selectors, api.RouteHealthRoute{Method: f.Method, Path: f.Path, CheckLatency: f.CheckLatency, MaxP95MS: f.MaxP95MS, WatchStatuses: f.WatchStatuses})
		if !slices.Contains([]string{"healthy", "regressed", "unknown"}, f.Status) || len(f.Windows) != api.RouteHealthWindows {
			return errors.New("invalid route verdict")
		}
		for i, w := range f.Windows {
			if !w.Start.Equal(expected[i].Start) || !w.End.Equal(expected[i].End) || !slices.Contains([]string{"healthy", "regressed", "unknown"}, w.Status) {
				return errors.New("invalid observation window")
			}
			for _, c := range []api.RouteHealthCounts{w.Candidate, w.Stable} {
				want := 0.0
				if c.Requests > 0 {
					want = float64(c.ServerErrors) / float64(c.Requests)
				}
				if c.P95LatencyMS != nil && (*c.P95LatencyMS < 0 || math.IsNaN(*c.P95LatencyMS) || math.IsInf(*c.P95LatencyMS, 0)) {
					return errors.New("invalid p95 latency")
				}
				if c.Requests < 0 || c.ServerErrors < 0 || c.ServerErrors > c.Requests || math.IsNaN(c.ErrorRate) || math.Abs(c.ErrorRate-want) > 1e-12 {
					return errors.New("invalid request counts or rate")
				}
			}
		}
	}
	latencySelected := false
	for _, selector := range selectors {
		latencySelected = latencySelected || routehealth.LatencyEnabled(selector.CheckLatency, selector.MaxP95MS)
	}
	if latencySelected && r.MinimumLatencyRequests != api.RouteHealthMinLatencyRequests {
		return errors.New("latency sample minimum is unavailable")
	}
	if !latencySelected && r.MinimumLatencyRequests != 0 {
		return errors.New("unexpected latency sample minimum")
	}
	if err := validateRouteHealthVerdicts(r); err != nil {
		return err
	}
	if r.StableDeploymentID != "" {
		if _, err := uuid.Parse(r.StableDeploymentID); err != nil {
			return errors.New("invalid stable deployment")
		}
	}
	if err := validateRouteHealthGate(api.RouteHealthGate{AppID: r.AppID, Mode: r.Mode, OnRegression: r.OnRegression, Revision: r.Revision, Routes: selectors, UpdatedAt: &r.CheckedAt}); err != nil {
		return err
	}
	if (len(selectors) == 0) != (r.Status == "disabled") || r.Status == "healthy" && r.StableDeploymentID == "" {
		return errors.New("report has no comparison identity")
	}
	return nil
}
func renderRouteHealthReport(r api.RouteHealthReport) {
	_, _ = fmt.Fprintf(osStdout, "Route health: %s (%s, revision %d)\nCandidate: %s (%s)\nStable: %s (%s)\nCoverage: observed telemetry only; full capture unknown\n", r.Status, r.Mode, r.Revision, r.DeploymentID, previewReportText(r.CandidateCommitSHA), r.StableDeploymentID, previewReportText(r.StableCommitSHA))
	for _, f := range r.Routes {
		_, _ = fmt.Fprintf(osStdout, "\n%s %s: %s\n", f.Method, previewReportText(f.Path), f.Status)
		if routehealth.LatencyEnabled(f.CheckLatency, f.MaxP95MS) {
			_, _ = fmt.Fprintf(osStdout, "  Latency: %s; minimum %d requests per deployment/window", f.LatencyStatus, r.MinimumLatencyRequests)
			if f.MaxP95MS > 0 {
				_, _ = fmt.Fprintf(osStdout, "; p95 budget %dms", f.MaxP95MS)
			}
			if f.CheckLatency {
				_, _ = fmt.Fprint(osStdout, "; relative slowdown check enabled")
			}
			_, _ = fmt.Fprintln(osStdout)
		}
		renderRouteClientErrors(f.ClientErrors, "  ")
		for _, w := range f.Windows {
			_, _ = fmt.Fprintf(osStdout, "  %s–%s candidate %d/%d 5xx (%.1f%%), stable %d/%d (%.1f%%): %s (%s)\n", w.Start.Format("15:04:05Z"), w.End.Format("15:04:05Z"), w.Candidate.ServerErrors, w.Candidate.Requests, w.Candidate.ErrorRate*100, w.Stable.ServerErrors, w.Stable.Requests, w.Stable.ErrorRate*100, w.Status, previewReportText(w.Reason))
			if routehealth.LatencyEnabled(f.CheckLatency, f.MaxP95MS) {
				renderRouteLatencyWindow(w)
			}
		}
	}
	renderRouteCustomerHealth(r.Customers)
}

func validateRouteHealthVerdicts(r api.RouteHealthReport) error {
	summary := "healthy"
	if len(r.Routes) == 0 {
		summary = "disabled"
	}
	for _, f := range r.Routes {
		claimed := f
		routehealth.SummarizeFinding(&claimed)
		if f.Status != claimed.Status || f.ErrorStatus != "" && f.ErrorStatus != claimed.ErrorStatus || f.LatencyStatus != claimed.LatencyStatus {
			return errors.New("route verdict does not match consecutive signal windows")
		}
		observedFinding := api.RouteHealthFinding{CheckLatency: f.CheckLatency, MaxP95MS: f.MaxP95MS, Windows: slices.Clone(f.Windows)}
		observed := api.RouteHealthReport{Routes: []api.RouteHealthFinding{observedFinding}}
		anchor := r.ObservationAnchor
		if anchor == nil {
			anchor = &f.Windows[0].Start
		}
		routehealth.Evaluate(&observed, anchor, "")
		for i, w := range f.Windows {
			expected := observed.Routes[0].Windows[i]
			latency := routehealth.LatencyEnabled(f.CheckLatency, f.MaxP95MS)
			if !latency {
				if w.ErrorStatus != "" && w.ErrorStatus != w.Status {
					return errors.New("error verdict does not match window verdict")
				}
				if w.Status != "unknown" && w.Status != expected.Status {
					return errors.New("window verdict does not match observed counts")
				}
			} else {
				if !validHealthStatus(w.ErrorStatus) || !validHealthStatus(w.LatencyStatus) || w.ErrorStatus != "unknown" && w.ErrorStatus != expected.ErrorStatus || w.LatencyStatus != "unknown" && w.LatencyStatus != expected.LatencyStatus {
					return errors.New("signal verdict does not match observed evidence")
				}
				status := "healthy"
				if w.ErrorStatus == "unknown" || w.LatencyStatus == "unknown" {
					status = "unknown"
				}
				if w.ErrorStatus == "regressed" || w.LatencyStatus == "regressed" {
					status = "regressed"
				}
				if w.Status != status {
					return errors.New("window verdict does not match selected signals")
				}
			}
			if err := validateLatencyComparison(w); err != nil {
				return err
			}
		}
		if f.Status == "regressed" || f.Status == "unknown" && summary != "regressed" {
			summary = f.Status
		}
	}
	if r.Status != summary {
		return errors.New("report verdict does not match selected routes")
	}
	return nil
}
func validHealthStatus(value string) bool {
	return value == "healthy" || value == "regressed" || value == "unknown"
}
func validateLatencyComparison(w api.RouteHealthWindowEvidence) error {
	if w.LatencyDeltaMS == nil && w.LatencyFactor == nil {
		return nil
	}
	if w.Candidate.P95LatencyMS == nil || w.Stable.P95LatencyMS == nil {
		return errors.New("latency comparison has no percentile evidence")
	}
	if w.LatencyDeltaMS != nil && (math.IsNaN(*w.LatencyDeltaMS) || math.IsInf(*w.LatencyDeltaMS, 0) || math.Abs(*w.LatencyDeltaMS-(*w.Candidate.P95LatencyMS-*w.Stable.P95LatencyMS)) > api.RouteHealthComparisonEpsilon) {
		return errors.New("latency delta does not match percentiles")
	}
	if w.LatencyFactor != nil && (*w.Stable.P95LatencyMS <= 0 || math.IsNaN(*w.LatencyFactor) || math.IsInf(*w.LatencyFactor, 0) || math.Abs(*w.LatencyFactor - *w.Candidate.P95LatencyMS / *w.Stable.P95LatencyMS) > api.RouteHealthComparisonEpsilon) {
		return errors.New("latency factor does not match percentiles")
	}
	return nil
}

func renderRouteLatencyWindow(w api.RouteHealthWindowEvidence) {
	candidate, stable := "unavailable", "unavailable"
	if w.Candidate.P95LatencyMS != nil {
		candidate = fmt.Sprintf("%.1fms", *w.Candidate.P95LatencyMS)
	}
	if w.Stable.P95LatencyMS != nil {
		stable = fmt.Sprintf("%.1fms", *w.Stable.P95LatencyMS)
	}
	_, _ = fmt.Fprintf(osStdout, "    p95 candidate %s, stable %s", candidate, stable)
	if w.LatencyDeltaMS != nil {
		_, _ = fmt.Fprintf(osStdout, ", delta %+.1fms", *w.LatencyDeltaMS)
	}
	if w.LatencyFactor != nil {
		_, _ = fmt.Fprintf(osStdout, ", %.2fx", *w.LatencyFactor)
	}
	_, _ = fmt.Fprintf(osStdout, ": %s (%s)\n", w.LatencyStatus, previewReportText(w.LatencyReason))
}
