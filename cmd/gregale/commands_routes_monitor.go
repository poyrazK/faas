package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

type routeMonitorCLIOptions struct {
	action, slug, mode, routes, incident, before, out string
	revision                                          int64
	limit                                             int
	fail                                              bool
}

func parseRouteMonitorCLI(args []string) (routeMonitorCLIOptions, error) {
	o := routeMonitorCLIOptions{}
	if len(args) == 0 {
		return o, errors.New("usage: gregale routes monitor <get|set|report|incidents|explain> APP [flags]")
	}
	o.action = args[0]
	flags, pos := splitArgsForFlags(args[1:], "fail-on-unhealthy")
	fs := newFlagSet("routes monitor "+o.action, flag.ContinueOnError)
	switch o.action {
	case "get":
	case "set":
		fs.StringVar(&o.mode, "mode", "", "enabled or disabled")
		fs.StringVar(&o.routes, "routes", "", "JSON array of production route budgets")
		fs.Int64Var(&o.revision, "expected-revision", -1, "current revision; 0 initially")
	case "report":
		fs.BoolVar(&o.fail, "fail-on-unhealthy", false, "exit nonzero unless observed route budgets are healthy")
	case "incidents":
		fs.IntVar(&o.limit, "limit", api.RouteMonitorPageSize, "page size (1–10)")
		fs.StringVar(&o.before, "before", "", "page before a retained incident UUID")
	case "explain":
		fs.StringVar(&o.incident, "incident", "", "saved incident UUID")
		fs.StringVar(&o.out, "out", "", "save incident JSON to a new file")
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
	if o.action == "set" {
		routes, err := readRouteMonitorRoutes(o.routes)
		if err != nil {
			return printErr("Invalid route budgets", err)
		}
		request = api.SetRouteMonitorRequest{Enabled: o.mode == "enabled", ExpectedRevision: &o.revision, Routes: routes}
		if err := routemonitor.Validate(request); err != nil {
			return printErr("Invalid route monitor configuration", err)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
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
		if o.action == "set" && (config.Enabled != request.Enabled || !routemonitor.RoutesEqual(config.Routes, request.Routes) || config.Revision != o.revision && config.Revision != o.revision+1) {
			return printErr("Invalid route monitor response", errors.New("configuration does not match submitted budgets or revision"))
		}
		if jsonOutput {
			return jsonOut(writeJSON(config))
		}
		_, _ = fmt.Fprintf(osStdout, "Production route monitoring for %s: enabled=%t (revision %d)\n", o.slug, config.Enabled, config.Revision)
		for _, r := range config.Routes {
			renderRouteMonitorBudget(r)
		}
		return 0
	case "report":
		r, err := c.GetRouteMonitorReport(ctx, o.slug)
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
	case "explain":
		i, err := c.GetRouteMonitorIncident(ctx, o.slug, o.incident)
		if err != nil {
			return printErr("Could not read route incident", err)
		}
		if i.ID != o.incident {
			return printErr("Invalid route incident", errors.New("incident identity does not match"))
		}
		if err := routemonitor.ValidateIncident(i, o.slug); err != nil {
			return printErr("Invalid route incident", err)
		}
		if o.out != "" {
			if err := writeRouteMonitorIncidentFile(o.out, i); err != nil {
				return printErr("Could not save route incident", err)
			}
		}
		if jsonOutput {
			return jsonOut(writeJSON(i))
		}
		renderRouteMonitorIncident(i, o.slug)
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

// Reuse the existing owner-only, create-new evidence export path.
func writeRouteMonitorIncidentFile(path string, i api.RouteMonitorIncident) error {
	body, err := json.MarshalIndent(i, "", "  ")
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
}
func renderRouteMonitorIncident(i api.RouteMonitorIncident, slug string) {
	_, _ = fmt.Fprintf(osStdout, "Incident %s: %s; opened %s\n", i.ID, i.Status, i.OpenedAt.Format("2006-01-02 15:04:05Z"))
	renderRouteMonitorReport(i.OpeningReport)
	for _, e := range i.Evidence {
		_, _ = fmt.Fprintf(osStdout, "\n%s %s — %s evidence\n", e.Method, previewReportText(e.Path), e.Signal)
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
}
