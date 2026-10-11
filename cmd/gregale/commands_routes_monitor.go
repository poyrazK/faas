package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeimpact"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

type routeMonitorCLIOptions struct {
	action, slug, mode, routes, incident, before, out, sourceImpact string
	customerGroupBy, onViolation                                    string
	revision                                                        int64
	limit                                                           int
	fail                                                            bool
	customerDetails                                                 bool
}

func parseRouteMonitorCLI(args []string) (routeMonitorCLIOptions, error) {
	o := routeMonitorCLIOptions{}
	if len(args) == 0 {
		return o, errors.New("usage: gregale routes monitor <get|set|preview|report|incidents|explain> APP [flags]")
	}
	o.action = args[0]
	flags, pos := splitArgsForFlags(args[1:], "fail-on-unhealthy", "customer-details")
	fs := newFlagSet("routes monitor "+o.action, flag.ContinueOnError)
	switch o.action {
	case "get":
	case "set":
		fs.StringVar(&o.mode, "mode", "", "enabled or disabled")
		fs.StringVar(&o.routes, "routes", "", "JSON array of production route budgets")
		fs.StringVar(&o.customerGroupBy, "customer-group-by", "", "optionally evaluate budgets by tenant or consumer")
		fs.StringVar(&o.onViolation, "on-violation", "", "report (default) or rollback to the last healthy deployment after an early error-budget incident")
		fs.Int64Var(&o.revision, "expected-revision", -1, "current revision; 0 initially")
	case "preview":
		fs.StringVar(&o.routes, "routes", "", "JSON array of proposed production route budgets")
		fs.StringVar(&o.customerGroupBy, "customer-group-by", "", "optionally evaluate budgets by tenant or consumer")
		fs.BoolVar(&o.customerDetails, "customer-details", false, "include observed tenant or consumer IDs (when enabled)")
		fs.BoolVar(&o.fail, "fail-on-unhealthy", false, "exit nonzero unless the proposed budgets are healthy")
	case "report":
		fs.BoolVar(&o.fail, "fail-on-unhealthy", false, "exit nonzero unless observed route budgets are healthy")
		fs.BoolVar(&o.customerDetails, "customer-details", false, "include observed tenant or consumer IDs (when enabled)")
	case "incidents":
		fs.IntVar(&o.limit, "limit", api.RouteMonitorPageSize, "page size (1–10)")
		fs.StringVar(&o.before, "before", "", "page before a retained incident UUID")
	case "explain":
		fs.StringVar(&o.incident, "incident", "", "saved incident UUID")
		fs.StringVar(&o.out, "out", "", "save incident JSON to a new file")
		fs.StringVar(&o.sourceImpact, "source-impact", "", "correlate with a source impact report path or analyze the local checkout with auto")
		fs.BoolVar(&o.customerDetails, "customer-details", false, "include observed tenant or consumer IDs (when enabled)")
	default:
		return o, errors.New("unknown route monitor command")
	}
	if err := fs.Parse(flags); err != nil {
		return o, err
	}
	if len(pos) != 1 || !validCLISlug(pos[0]) {
		return o, errors.New("supply an app slug")
	}
	o.slug = pos[0]
	if o.action == "set" && o.mode != "enabled" && o.mode != "disabled" {
		return o, errors.New("supply --mode enabled or disabled")
	}
	if o.action == "preview" && o.routes == "" {
		return o, errors.New("supply --routes")
	}
	if o.action == "incidents" && (o.limit < 1 || o.limit > api.RouteMonitorMaxPage) {
		return o, errors.New("invalid incident page limit")
	}
	for _, id := range []string{o.incident, o.before} {
		if id != "" {
			v, err := uuid.Parse(id)
			if err != nil || v.String() != id {
				return o, errors.New("incident IDs must be canonical UUIDs")
			}
		}
	}
	if o.action == "explain" && o.incident == "" {
		return o, errors.New("supply --incident")
	}
	return o, nil
}
func readRouteMonitorRoutes(path string) ([]api.RouteMonitorRoute, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, fmt.Errorf("open route budgets: %w", err)
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteHealthRequestMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > api.RouteHealthRequestMaxBytes {
		return nil, errors.New("route budgets exceed request limit")
	}
	var routes []api.RouteMonitorRoute
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&routes); err != nil {
		return nil, fmt.Errorf("decode route budgets: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("supply exactly one route budget JSON array")
	}
	if routes == nil {
		return nil, errors.New("supply a JSON array")
	}
	return routes, nil
}
func cmdRoutesMonitor(args []string) int {
	o, err := parseRouteMonitorCLI(args)
	if err != nil {
		return printErr("Invalid route monitor command", err)
	}
	var request api.SetRouteMonitorRequest
	var previewRequest api.PreviewRouteMonitorRequest
	if o.action == "set" || o.action == "preview" {
		routes, err := readRouteMonitorRoutes(o.routes)
		if err != nil {
			return printErr("Invalid route budgets", err)
		}
		if o.action == "set" {
			request = api.SetRouteMonitorRequest{CustomerGroupBy: o.customerGroupBy, OnViolation: o.onViolation, Enabled: o.mode == "enabled", ExpectedRevision: &o.revision, Routes: routes}
			if err := routemonitor.Validate(request); err != nil {
				return printErr("Invalid route monitor configuration", err)
			}
		} else {
			previewRequest = api.PreviewRouteMonitorRequest{CustomerGroupBy: o.customerGroupBy, Routes: routes}
			if err := routemonitor.ValidatePreviewRequest(previewRequest, 0); err != nil {
				return printErr("Invalid route monitor preview", err)
			}
		}
	}
	var source routeimpact.Report
	var sourceDigest string
	if o.sourceImpact != "" && o.sourceImpact != "auto" {
		source, sourceDigest, err = readPreviewSourceImpact(o.sourceImpact)
		if err != nil {
			return printErr("Invalid source impact report", err)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	timeout := api.RouteCheckTimeout
	if o.action == "explain" && o.sourceImpact == "auto" {
		timeout = api.RouteImpactTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	switch o.action {
	case "get", "set":
		var config api.RouteMonitorConfig
		if o.action == "get" {
			config, err = c.GetRouteMonitor(ctx, o.slug)
		} else {
			config, err = c.SetRouteMonitor(ctx, o.slug, request)
		}
		if err != nil {
			return printErr("Could not read or update route monitor", err)
		}
		if err := routemonitor.ValidateConfig(config); err != nil {
			return printErr("Invalid route monitor response", err)
		}
		if o.action == "set" && (config.Enabled != request.Enabled || config.CustomerGroupBy != request.CustomerGroupBy || routemonitor.OnViolation(config.OnViolation) != routemonitor.OnViolation(request.OnViolation) || !routemonitor.RoutesEqual(config.Routes, request.Routes) || config.Revision != o.revision && config.Revision != o.revision+1) {
			return printErr("Invalid route monitor response", errors.New("configuration does not match submitted budgets or revision"))
		}
		if jsonOutput {
			return jsonOut(writeJSON(config))
		}
		_, _ = fmt.Fprintf(osStdout, "Production route monitoring for %s: enabled=%t (revision %d)", o.slug, config.Enabled, config.Revision)
		if config.CustomerGroupBy != "" {
			_, _ = fmt.Fprintf(osStdout, ", grouped by %s", config.CustomerGroupBy)
		}
		if routemonitor.OnViolation(config.OnViolation) == "rollback" {
			_, _ = fmt.Fprint(osStdout, ", rolls back early error-budget violations")
		}
		_, _ = fmt.Fprintln(osStdout)
		for _, r := range config.Routes {
			renderRouteMonitorBudget(r)
		}
		return 0
	case "report":
		r, err := c.GetRouteMonitorReportWithOptions(ctx, o.slug, api.RouteMonitorReadOptions{CustomerDetails: o.customerDetails})
		if err != nil {
			return printErr("Could not read production route health", err)
		}
		if err := routemonitor.ValidateReport(r); err != nil {
			return printErr("Invalid route monitor report", err)
		}
		if jsonOutput {
			if code := jsonOut(writeJSON(r)); code != 0 {
				return code
			}
		} else {
			renderRouteMonitorReport(r)
		}
		if o.fail && r.Status != "healthy" {
			return 1
		}
		return 0
	case "preview":
		preview, err := c.PreviewRouteMonitorWithOptions(ctx, o.slug, previewRequest, api.RouteMonitorReadOptions{CustomerDetails: o.customerDetails})
		if err != nil {
			return printErr("Could not preview production route budgets", err)
		}
		if err := routemonitor.ValidatePreviewForRequest(preview, previewRequest); err != nil {
			return printErr("Invalid route monitor preview response", err)
		}
		if jsonOutput {
			if code := jsonOut(writeJSON(preview)); code != 0 {
				return code
			}
		} else {
			_, _ = fmt.Fprintf(osStdout, "Read-only preview against monitor revision %d; no configuration was changed.\n", preview.CurrentRevision)
			if preview.ConfigChangeResetsObservationAnchor {
				_, _ = fmt.Fprintln(osStdout, "Saving these changed budgets starts a fresh observation window; treat this as current production evidence, not the first post-save report.")
			}
			renderRouteMonitorReport(preview.Report)
		}
		if o.fail && preview.Report.Status != "healthy" {
			return 1
		}
		return 0
	case "explain":
		i, err := c.GetRouteMonitorIncidentWithOptions(ctx, o.slug, o.incident, api.RouteMonitorReadOptions{CustomerDetails: o.customerDetails})
		if err != nil {
			return printErr("Could not read route incident", err)
		}
		if i.ID != o.incident {
			return printErr("Invalid route incident", errors.New("incident identity does not match"))
		}
		if err := routemonitor.ValidateIncident(i, o.slug); err != nil {
			return printErr("Invalid route incident", err)
		}
		var sourceCorrelation *routeMonitorSourceCorrelation
		if o.sourceImpact != "" {
			var correlation routeMonitorSourceCorrelation
			if o.sourceImpact == "auto" {
				correlation = correlateRouteMonitorIncidentSourceAuto(ctx, c, i, o.slug, ".")
			} else {
				correlation = correlateRouteMonitorIncidentSource(ctx, c, i, source, sourceDigest)
			}
			addRouteMonitorSourceOwners(ctx, ".", &correlation)
			sourceCorrelation = &correlation
		}
		if o.out != "" {
			if err := writeRouteMonitorIncidentFileWithSource(o.out, i, sourceCorrelation); err != nil {
				return printErr("Could not save route incident", err)
			}
		}
		if jsonOutput {
			if sourceCorrelation != nil {
				return jsonOut(writeJSON(routeMonitorIncidentWithSource{RouteMonitorIncident: i, SourceCorrelation: sourceCorrelation}))
			}
			return jsonOut(writeJSON(i))
		}
		renderRouteMonitorIncident(i, o.slug)
		if sourceCorrelation != nil {
			renderRouteMonitorIncidentSource(*sourceCorrelation)
		}
		return 0
	case "incidents":
		page, err := c.ListRouteMonitorIncidents(ctx, o.slug, o.limit, o.before)
		if err != nil {
			return printErr("Could not list route incidents", err)
		}
		if err := validateRouteMonitorPage(page, o); err != nil {
			return printErr("Invalid route incident page", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(page))
		}
		for _, i := range page.Incidents {
			_, _ = fmt.Fprintf(osStdout, "%s  %s  %s  deployment %s\n", i.ID, i.Status, i.OpenedAt.Format("2006-01-02 15:04:05Z"), i.DeploymentID)
		}
		if page.NextBefore != "" {
			_, _ = fmt.Fprintf(osStdout, "Next: gregale routes monitor incidents %s --before %s\n", o.slug, page.NextBefore)
		}
		return 0
	}
	return 1
}
func validateRouteMonitorPage(p api.RouteMonitorIncidentPage, o routeMonitorCLIOptions) error {
	_, err := uuid.Parse(p.AppID)
	if err != nil || len(p.Incidents) > o.limit {
		return errors.New("invalid incident page scope or bounds")
	}
	seen := map[string]bool{}
	for j, i := range p.Incidents {
		if i.AppID != p.AppID || seen[i.ID] || i.ID == o.before {
			return errors.New("incident page crosses app or repeats an entry")
		}
		seen[i.ID] = true
		if err := routemonitor.ValidateIncident(i, o.slug); err != nil {
			return err
		}
		if j > 0 {
			prev := p.Incidents[j-1]
			if i.OpenedAt.After(prev.OpenedAt) || i.OpenedAt.Equal(prev.OpenedAt) && i.ID >= prev.ID {
				return errors.New("incident page is not ordered")
			}
		}
	}
	if p.NextBefore != "" && (len(p.Incidents) != o.limit || p.NextBefore != p.Incidents[len(p.Incidents)-1].ID) {
		return errors.New("invalid incident pagination cursor")
	}
	return nil
}

func writeRouteMonitorIncidentFileWithSource(path string, i api.RouteMonitorIncident, source *routeMonitorSourceCorrelation) error {
	var value any = i
	if source != nil {
		value = routeMonitorIncidentWithSource{RouteMonitorIncident: i, SourceCorrelation: source}
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeRoutePolicyPlan(path, append(body, '\n'))
}
func renderRouteMonitorBudget(r api.RouteMonitorRoute) {
	_, _ = fmt.Fprintf(osStdout, "  %s %s", r.Method, previewReportText(r.Path))
	if r.Max5xxRateBPS != nil {
		_, _ = fmt.Fprintf(osStdout, "; 5xx budget %.2f%%", float64(*r.Max5xxRateBPS)/100)
	}
	if r.MaxP95MS > 0 {
		_, _ = fmt.Fprintf(osStdout, "; p95 budget %dms", r.MaxP95MS)
	}
	_, _ = fmt.Fprintln(osStdout)
}
func renderRouteMonitorReport(r api.RouteMonitorReport) {
	_, _ = fmt.Fprintf(osStdout, "Production route health: %s (%s, revision %d)\nDeployment: %s (%s)\nCoverage: observed telemetry only; full capture unknown\n", r.Status, previewReportText(r.Reason), r.Revision, r.DeploymentID, previewReportText(r.CommitSHA))
	for _, f := range r.Routes {
		renderRouteMonitorBudget(f.Route)
		_, _ = fmt.Fprintf(osStdout, "    %s; errors %s, latency %s\n", f.Status, f.ErrorStatus, f.LatencyStatus)
		for _, w := range f.Windows {
			_, _ = fmt.Fprintf(osStdout, "    %s–%s: %d/%d 5xx (%.2f%%)", w.Start.Format("15:04:05Z"), w.End.Format("15:04:05Z"), w.Observed.ServerErrors, w.Observed.Requests, w.Observed.ErrorRate*100)
			if w.Observed.P95LatencyMS != nil {
				_, _ = fmt.Fprintf(osStdout, "; p95 %.1fms", *w.Observed.P95LatencyMS)
			}
			_, _ = fmt.Fprintln(osStdout)
		}
	}
	if r.Customers != nil {
		renderRouteMonitorCustomers(r.Customers)
	}
}

func renderRouteMonitorCustomers(customers *api.RouteMonitorCustomerReport) {
	_, _ = fmt.Fprintf(osStdout, "Customer budgets (%s): %s; observed %d, violated %d, unknown %d, recovery remaining %d\n", customers.GroupBy, customers.Status, customers.ObservedCustomers, customers.ViolatedCustomers, customers.UnknownCustomers, customers.RecoveryRemainingCustomers)
	if customers.RecoveryInventoryIncomplete {
		_, _ = fmt.Fprintln(osStdout, "  Recovery identity inventory is incomplete; this incident will not auto-recover.")
	}
	for _, route := range customers.Routes {
		_, _ = fmt.Fprintf(osStdout, "  %s %s: %d observed, %d violated, %d unknown, %d recovery customers remaining\n", route.Method, previewReportText(route.Path), route.ObservedCustomers, route.ViolatedCustomers, route.UnknownCustomers, route.RecoveryRemainingCustomers)
		for _, w := range route.Windows {
			_, _ = fmt.Fprintf(osStdout, "    %s–%s: %d identified requests, %d unattributed, %d unresolved identity, %d outside displayed cohorts\n", w.Start.Format("15:04:05Z"), w.End.Format("15:04:05Z"), w.IdentifiedRequests, w.UnattributedRequests, w.UnresolvedIdentityRequests, w.OtherCustomerRequests)
		}
		if len(route.ViolatingCustomerIDs) > 0 {
			_, _ = fmt.Fprintf(osStdout, "    violating customer IDs: %s\n", strings.Join(route.ViolatingCustomerIDs, ", "))
		}
		if route.ViolatingCustomersTruncated || route.CustomersTruncated {
			_, _ = fmt.Fprintln(osStdout, "    customer details are capped")
		}
		if customers.DetailsIncluded {
			for _, cohort := range route.Customers {
				_, _ = fmt.Fprintf(osStdout, "    %s: %s (%s)\n", cohort.CustomerID, cohort.Status, previewReportText(cohort.Reason))
			}
		}
	}
}
func renderRouteMonitorIncident(i api.RouteMonitorIncident, slug string) {
	_, _ = fmt.Fprintf(osStdout, "Incident %s: %s; opened %s\n", i.ID, i.Status, i.OpenedAt.Format("2006-01-02 15:04:05Z"))
	if i.Baseline == nil {
		_, _ = fmt.Fprintln(osStdout, "Last healthy deployment baseline: unavailable")
	} else {
		_, _ = fmt.Fprintf(osStdout, "Last healthy deployment baseline: %s", i.Baseline.DeploymentID)
		if i.Baseline.CommitSHA != "" {
			_, _ = fmt.Fprintf(osStdout, " (%s", i.Baseline.CommitSHA)
			if i.Baseline.Repository != "" {
				_, _ = fmt.Fprintf(osStdout, "; %s", i.Baseline.Repository)
			}
			_, _ = fmt.Fprint(osStdout, ")")
		}
		_, _ = fmt.Fprintln(osStdout)
	}
	if rb := i.Rollback; rb != nil {
		switch rb.Status {
		case "requested":
			_, _ = fmt.Fprintf(osStdout, "Automatic rollback: requested to %s for %s (operation %s)\n", rb.TargetDeploymentID, rb.Route, rb.OperationID)
		case "skipped":
			_, _ = fmt.Fprintf(osStdout, "Automatic rollback: skipped (%s)\n", rb.Reason)
		default:
			_, _ = fmt.Fprintf(osStdout, "Automatic rollback: %s\n", rb.Status)
		}
	}
	renderRouteMonitorIncidentTimeline(i)
	renderRouteMonitorReport(i.OpeningReport)
	for _, e := range i.Evidence {
		renderRouteMonitorEvidence(e, slug)
	}
	if i.EvidenceTruncated {
		_, _ = fmt.Fprintln(osStdout, "Additional violated route/signal diagnostics omitted by the saved-evidence cap.")
	}
	if i.ClosedAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Closed: %s\n", i.ClosedAt.Format("2006-01-02 15:04:05Z"))
	}
	if i.RecoveryReport != nil {
		_, _ = fmt.Fprintln(osStdout, "Recovery evidence:")
		renderRouteMonitorReport(*i.RecoveryReport)
	}
	if len(i.Escalations) > 0 {
		_, _ = fmt.Fprintf(osStdout, "\nEscalations: %d retained transition(s)", len(i.Escalations))
		if i.EscalationsTruncated {
			_, _ = fmt.Fprint(osStdout, "; older transitions omitted")
		}
		_, _ = fmt.Fprintln(osStdout)
		for _, escalation := range i.Escalations {
			_, _ = fmt.Fprintf(osStdout, "Transition %s at %s: +%d route(s), +%d signal(s) since %s\n", escalation.TransitionID, escalation.CheckedAt.Format("2006-01-02 15:04:05Z"), escalation.NewlyViolatedRoutes, escalation.NewlyViolatedSignals, escalation.PreviousCheckedAt.Format("2006-01-02 15:04:05Z"))
			for _, signal := range escalation.Signals {
				_, _ = fmt.Fprintf(osStdout, "  Newly violated: %s %s — %s\n", signal.Finding.Route.Method, previewReportText(signal.Finding.Route.Path), signal.Signal)
			}
			for _, evidence := range escalation.Evidence {
				renderRouteMonitorEvidence(evidence, slug)
			}
			if escalation.EvidenceTruncated {
				_, _ = fmt.Fprintln(osStdout, "  Additional transition diagnostics omitted by the saved-evidence cap.")
			}
		}
	}
}

func renderRouteMonitorEvidence(e api.RouteMonitorEvidence, slug string) {
	if e.CustomerID != "" {
		_, _ = fmt.Fprintf(osStdout, "\n%s %s — %s evidence for %s %s\n", e.Method, previewReportText(e.Path), e.Signal, e.CustomerGroupBy, e.CustomerID)
	} else if e.CustomerGroupBy != "" {
		_, _ = fmt.Fprintf(osStdout, "\n%s %s — %s evidence for an undisclosed %s\n", e.Method, previewReportText(e.Path), e.Signal, e.CustomerGroupBy)
	} else {
		_, _ = fmt.Fprintf(osStdout, "\n%s %s — %s evidence\n", e.Method, previewReportText(e.Path), e.Signal)
	}
	for _, w := range e.Windows {
		_, _ = fmt.Fprintf(osStdout, "  %d matching requests in %d retained rows; examples capped=%t\n", w.Requests.MatchingRequests, w.Requests.ObservedRows, w.Requests.ExamplesTruncated)
		for _, x := range w.Requests.Examples {
			_, _ = fmt.Fprintf(osStdout, "  gregale debug requests inspect %s %s (status %d, %dms, weight %d)\n", slug, x.TelemetryID, x.Status, x.LatencyMS, x.RepresentedRequests)
		}
		if w.Diagnostics != nil {
			d := w.Diagnostics
			_, _ = fmt.Fprintf(osStdout, "  Diagnostic rows %d; missing spans %d; capped=%t\n", d.Candidate.SampledRows, d.Candidate.MissingSpanRows, d.Candidate.SamplesTruncated)
			if d.Candidate.GuestP95MS != nil {
				_, _ = fmt.Fprintf(osStdout, "  Measured guest p95 %dms (%d represented requests)\n", *d.Candidate.GuestP95MS, d.Candidate.GuestRequests)
			}
			if d.Candidate.WakeBootP95MS != nil {
				_, _ = fmt.Fprintf(osStdout, "  Wake boot p95 %dms (%d distinct wakes)\n", *d.Candidate.WakeBootP95MS, d.Candidate.WakeSamples)
			}
			if d.DependenciesTruncated || d.Candidate.SpansTruncated || d.Candidate.TimingIncomplete {
				_, _ = fmt.Fprintln(osStdout, "  Dependency evidence is capped or timing is incomplete.")
			}
			for _, dep := range d.Dependencies {
				if dep.Candidate.P95MS != nil {
					_, _ = fmt.Fprintf(osStdout, "  %s/%s span p95 %dms (%d represented calls)\n", dep.Type, previewReportText(dep.Kind), *dep.Candidate.P95MS, dep.Candidate.RepresentedCalls)
				}
			}
		}
	}
}

func renderRouteMonitorIncidentTimeline(i api.RouteMonitorIncident) {
	if len(i.Timeline) == 0 {
		_, _ = fmt.Fprintln(osStdout, "Impact timeline: no follow-up evaluations are available for this incident.")
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Impact timeline: %d observations", len(i.Timeline))
	if i.TimelineTruncated {
		_, _ = fmt.Fprint(osStdout, "; older evaluations omitted")
	}
	_, _ = fmt.Fprintln(osStdout)
	var previous *api.RouteMonitorIncidentTimelineEntry
	for index := range i.Timeline {
		entry := &i.Timeline[index]
		affectedRoutes, unknownRoutes := 0, 0
		for _, route := range entry.Routes {
			if route.Status == "violated" || route.CustomerImpact != nil && route.CustomerImpact.ViolatedCustomers > 0 {
				affectedRoutes++
			}
			if route.Status == "unknown" || route.CustomerImpact != nil && route.CustomerImpact.UnknownCustomers > 0 {
				unknownRoutes++
			}
		}
		_, _ = fmt.Fprintf(osStdout, "  %s  %s: %d affected routes, %d unknown routes (%s)", entry.CheckedAt.Format("2006-01-02 15:04:05Z"), entry.Status, affectedRoutes, unknownRoutes, previewReportText(entry.Reason))
		if impact := entry.CustomerImpact; impact != nil {
			_, _ = fmt.Fprintf(osStdout, "; %d/%d %s cohorts violated, %d unknown", impact.ViolatedCustomers, impact.ObservedCustomers, impact.GroupBy, impact.UnknownCustomers)
		}
		_, _ = fmt.Fprintln(osStdout)
		for _, route := range entry.Routes {
			var prior *api.RouteMonitorIncidentTimelineRoute
			if previous != nil && route.RouteIndex < len(previous.Routes) {
				prior = &previous.Routes[route.RouteIndex]
			}
			changed := prior == nil || prior.ErrorStatus != route.ErrorStatus || prior.LatencyStatus != route.LatencyStatus || timelineCustomerImpactChanged(prior, &route)
			if !changed {
				continue
			}
			if route.RouteIndex < 0 || route.RouteIndex >= len(i.OpeningReport.Routes) {
				continue // ValidateIncident rejects this before rendering.
			}
			selector := i.OpeningReport.Routes[route.RouteIndex].Route
			_, _ = fmt.Fprintf(osStdout, "    %s %s: errors %s", selector.Method, previewReportText(selector.Path), route.ErrorStatus)
			if prior != nil {
				_, _ = fmt.Fprintf(osStdout, " (was %s)", prior.ErrorStatus)
			}
			_, _ = fmt.Fprintf(osStdout, "; latency %s", route.LatencyStatus)
			if prior != nil {
				_, _ = fmt.Fprintf(osStdout, " (was %s)", prior.LatencyStatus)
			}
			if impact := route.CustomerImpact; impact != nil {
				_, _ = fmt.Fprintf(osStdout, "; %d/%d %s cohorts violated, %d unknown", impact.ViolatedCustomers, impact.ObservedCustomers, impact.GroupBy, impact.UnknownCustomers)
				if prior != nil && prior.CustomerImpact != nil && (prior.CustomerImpact.ViolatedCustomers != impact.ViolatedCustomers || prior.CustomerImpact.UnknownCustomers != impact.UnknownCustomers) {
					_, _ = fmt.Fprintf(osStdout, " (was %d violated, %d unknown)", prior.CustomerImpact.ViolatedCustomers, prior.CustomerImpact.UnknownCustomers)
				}
			}
			_, _ = fmt.Fprintln(osStdout)
		}
		previous = entry
	}
}

func timelineCustomerImpactChanged(previous *api.RouteMonitorIncidentTimelineRoute, current *api.RouteMonitorIncidentTimelineRoute) bool {
	if previous == nil || current == nil {
		return previous != current
	}
	if previous.CustomerImpact == nil || current.CustomerImpact == nil {
		return previous.CustomerImpact != current.CustomerImpact
	}
	return previous.CustomerImpact.ObservedCustomers != current.CustomerImpact.ObservedCustomers || previous.CustomerImpact.ViolatedCustomers != current.CustomerImpact.ViolatedCustomers || previous.CustomerImpact.UnknownCustomers != current.CustomerImpact.UnknownCustomers
}
