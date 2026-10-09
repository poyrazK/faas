package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routestatus"
)

// cmdRoutesStatus prints one row per route with every protection that applies
// to it. Each source is read independently; a failed read marks its section
// unavailable instead of failing the command.
func cmdRoutesStatus(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes status", flag.ContinueOnError)
	deployment := fs.String("deployment", "", "serving deployment UUID or vN (default: the app's serving deployment)")
	since := fs.String("since", "168h", "observed usage window (default 168h; also accepts 7d or RFC3339)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) {
		return printErr("Invalid route status command", errors.New("usage: gregale routes status APP [--deployment ID] [--since 168h]"))
	}
	if !validRouteHealthSuggestionSince(*since) {
		return printErr("Invalid route status command", fmt.Errorf("--since must be a duration such as 168h or 7d, or an RFC3339 time; got %q", *since))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*api.RouteCheckTimeout)
	defer cancel()
	inputs, err := fetchRouteStatusInputs(ctx, client, positional[0], *deployment, *since)
	if err != nil {
		return printErr("Could not read app deployments", err)
	}
	status := routestatus.Build(inputs)
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	renderRouteStatus(osStdout, status)
	return 0
}

// routeStatusDeployments picks the app's serving deployment (the live
// default-scope deployment with the most traffic that is not mid-canary) and
// any in-flight canary candidate.
func routeStatusDeployments(deployments []api.DeploymentResponse) (serving, candidate string) {
	best := -1
	for _, d := range deployments {
		if d.Status != "live" || d.TrafficPercent <= 0 || d.Scope != "" && d.Scope != "default" {
			continue
		}
		if d.CanaryTotalSteps > 0 && d.CanaryStep < d.CanaryTotalSteps {
			candidate = d.ID
			continue
		}
		if d.TrafficPercent > best {
			serving, best = d.ID, d.TrafficPercent
		}
	}
	return serving, candidate
}

func fetchRouteStatusInputs(ctx context.Context, client *api.Client, slug, deploymentRef, since string) (routestatus.Inputs, error) {
	in := routestatus.Inputs{App: slug, Since: since, Unavailable: map[string]string{}}
	deployments, err := client.ListAppDeploymentsAll(ctx, slug)
	if err != nil {
		return in, err
	}
	in.ServingDeploymentID, in.CandidateDeployment = routeStatusDeployments(deployments)
	if deploymentRef != "" {
		id, err := resolveDeploymentRef(ctx, client, slug, deploymentRef)
		if err != nil {
			return in, err
		}
		in.ServingDeploymentID = id
	}
	unavailable := func(section string, err error) {
		in.Unavailable[section] = routeStatusReason(err)
	}
	if in.ServingDeploymentID == "" {
		in.Unavailable["traffic"], in.Unavailable["contract"] = "no live deployment serves traffic", "no live deployment serves traffic"
	} else {
		if usage, err := client.GetAppRouteCustomerUsage(ctx, slug, api.RouteCustomerUsageOptions{DeploymentID: in.ServingDeploymentID, Since: since}); err != nil {
			unavailable("traffic", err)
		} else {
			in.Usage = &usage
		}
		if doc, err := client.GetAppsDeploymentOpenAPIDoc(ctx, slug, in.ServingDeploymentID); err != nil {
			unavailable("contract", err)
		} else {
			in.Contract, in.ContractCaptured = routestatus.OperationsFromDoc(doc.Doc), true
		}
	}
	if gate, err := client.GetRouteHealthGate(ctx, slug); err != nil {
		unavailable("canary", err)
	} else {
		in.HealthGate = &gate
		if in.CandidateDeployment != "" && len(gate.Routes) > 0 {
			if report, err := client.GetRouteHealthReport(ctx, slug, in.CandidateDeployment); err != nil {
				unavailable("canary_report", err)
			} else {
				in.HealthReport = &report
			}
		}
	}
	fetchRouteStatusProduction(ctx, client, slug, &in, unavailable)
	if saved, err := client.GetSavedRouteRequirements(ctx, slug); err != nil {
		if !isNotFound(err) {
			unavailable("requirements", err)
		}
	} else {
		in.Requirements = &saved
	}
	if len(in.Unavailable) == 0 {
		in.Unavailable = nil
	}
	return in, nil
}

func fetchRouteStatusProduction(ctx context.Context, client *api.Client, slug string, in *routestatus.Inputs, unavailable func(string, error)) {
	monitor, err := client.GetRouteMonitor(ctx, slug)
	if err != nil {
		unavailable("production", err)
		return
	}
	in.Monitor = &monitor
	if !monitor.Enabled {
		return
	}
	if report, err := client.GetRouteMonitorReport(ctx, slug); err != nil {
		unavailable("production_report", err)
	} else {
		in.MonitorReport = &report
	}
	if page, err := client.ListRouteMonitorIncidents(ctx, slug, 1, ""); err != nil {
		unavailable("incidents", err)
	} else if len(page.Incidents) > 0 {
		in.LatestIncident = &page.Incidents[0]
	}
}

func routeStatusReason(err error) string {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Detail != "" {
		return apiErr.Problem.Detail
	}
	return err.Error()
}

func renderRouteStatus(w io.Writer, s routestatus.Status) {
	_, _ = fmt.Fprintf(w, "Routes for %s", s.App)
	if s.ServingDeploymentID != "" {
		_, _ = fmt.Fprintf(w, " — serving %s", s.ServingDeploymentID)
	}
	if s.CandidateDeployment != "" {
		_, _ = fmt.Fprintf(w, ", canary %s", s.CandidateDeployment)
	}
	_, _ = fmt.Fprintf(w, "\nTraffic observed over %s (observed telemetry only)\n", s.Since)
	_, _ = fmt.Fprintf(w, "%d routes · %d protected · %d with automatic rollback · %d with traffic but no protection\n\n", s.Summary.Routes, s.Summary.Protected, s.Summary.Rollback, s.Summary.Unprotected)
	if len(s.Routes) == 0 {
		_, _ = fmt.Fprintln(w, "No routes observed, captured or configured yet.")
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "METHOD\tPATH\tTENANTS\tREQUESTS\tCANARY\tPRODUCTION\tCONTRACT\tGAPS")
		for _, r := range s.Routes {
			contract := "-"
			if r.InContract {
				contract = "yes"
			}
			gaps := strings.Join(r.Gaps, ",")
			if gaps == "" {
				gaps = "-"
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\n", r.Method, previewReportText(r.Path), r.Tenants, r.Requests, routeStatusCanary(r.Canary), routeStatusProduction(r.Production), contract, gaps)
		}
		_ = tw.Flush()
	}
	if i := s.LatestIncident; i != nil {
		_, _ = fmt.Fprintf(w, "\nLatest production incident %s: %s, opened %s", i.ID, i.Status, i.OpenedAt)
		if rb := i.Rollback; rb != nil {
			_, _ = fmt.Fprintf(w, "; automatic rollback %s", rb.Status)
			if rb.OperationID != "" {
				_, _ = fmt.Fprintf(w, " (operation %s)", rb.OperationID)
			}
			if rb.Reason != "" {
				_, _ = fmt.Fprintf(w, " (%s)", rb.Reason)
			}
		}
		_, _ = fmt.Fprintln(w)
	}
	if len(s.Unavailable) > 0 {
		_, _ = fmt.Fprintln(w, "\nUnavailable:")
		for _, section := range []string{"traffic", "contract", "canary", "canary_report", "production", "production_report", "incidents", "requirements"} {
			if reason, ok := s.Unavailable[section]; ok {
				_, _ = fmt.Fprintf(w, "  %s: %s\n", section, previewReportText(reason))
			}
		}
	}
}

func routeStatusCanary(c *routestatus.Canary) string {
	if c == nil {
		return "-"
	}
	out := c.Mode
	if c.Status != "" {
		out += " " + c.Status
	}
	if c.Evidence == "pooled" {
		out += " (pooled)"
	}
	return out
}

func routeStatusProduction(p *routestatus.Production) string {
	if p == nil {
		return "-"
	}
	out := "report"
	if p.OnViolation == "rollback" && p.Max5xxRateBPS != nil {
		out = "rollback"
	}
	if p.Status != "" {
		out += " " + p.Status
	}
	if p.Evidence == "pooled" {
		out += " (pooled)"
	}
	return out
}
