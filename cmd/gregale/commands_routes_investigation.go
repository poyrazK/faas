package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/routeimpact"
)

func cmdRoutesHealthInvestigate(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes health investigate", flag.ContinueOnError)
	deployment := fs.String("deployment", "", "candidate deployment UUID")
	label := fs.String("route", "", "exact configured telemetry label, e.g. POST /checkout")
	sourceImpactPath := fs.String("source-impact", "", "correlate a local route impact report with both deployment revisions")
	output := fs.String("out", "", "save investigation JSON to a new file")
	var opts api.RouteHealthInvestigationOptions
	fs.StringVar(&opts.Signal, "signal", "", "errors (default) or latency; latency requires a configured latency check")
	fs.IntVar(&opts.StatusCode, "status", 0, "watched 4xx code; 0 (default) selects all 5xx")
	fs.StringVar(&opts.CustomerGroupBy, "customer-group-by", "", "tenant (default) or consumer; requires --customer-id")
	fs.StringVar(&opts.CustomerID, "customer-id", "", "investigate one recorded customer UUID; explicitly includes this ID")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	opts.Method, opts.Path, _ = strings.Cut(*label, " ")
	zero := int64(0)
	selectorErr := routehealth.Validate(api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: opts.Method, Path: opts.Path}}})
	if err := routeHealthTargetError(positional, *deployment); err != nil {
		return printErr("Invalid route investigation", err)
	}
	if selectorErr != nil {
		return printErr("Invalid route investigation", fmt.Errorf("--route must be an exact METHOD /path label such as \"POST /checkout\": %w", selectorErr))
	}
	if err := opts.Validate(); err != nil {
		return printErr("Invalid route investigation", err)
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return printErr("Invalid --out", errors.New("choose a new path; existing files and symlinks are not replaced"))
		}
	}
	var source routeimpact.Report
	var sourceDigest string
	if *sourceImpactPath != "" {
		var err error
		source, sourceDigest, err = readPreviewSourceImpact(*sourceImpactPath)
		if err != nil {
			return printErr("Invalid source impact report", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
	defer cancel()
	report, err := client.GetRouteHealthInvestigation(ctx, positional[0], *deployment, opts)
	if err != nil {
		return printErr("Could not investigate route", err)
	}
	if err := validateCLIInvestigation(report, opts, positional[0], *deployment); err != nil {
		return printErr("Invalid route investigation response", err)
	}
	var sourceCorrelation *routeInvestigationSourceCorrelation
	if *sourceImpactPath != "" {
		correlation := correlateRouteInvestigationSource(ctx, client, report, source, sourceDigest)
		sourceCorrelation = &correlation
	}
	if *output != "" {
		body, err := marshalRouteInvestigation(report, sourceCorrelation)
		if err != nil {
			return printErr("Could not encode investigation", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return printErr("Could not save investigation", err)
		}
	}
	if jsonOutput {
		if sourceCorrelation != nil {
			return jsonOut(writeJSON(routeInvestigationWithSource{RouteHealthInvestigation: report, SourceCorrelation: sourceCorrelation}))
		}
		return jsonOut(writeJSON(report))
	}
	renderRouteInvestigation(report, positional[0])
	if sourceCorrelation != nil {
		renderRouteInvestigationSource(*sourceCorrelation)
	}
	return 0
}

func marshalRouteInvestigation(report api.RouteHealthInvestigation, source *routeInvestigationSourceCorrelation) ([]byte, error) {
	if source == nil {
		return json.MarshalIndent(report, "", "  ")
	}
	return json.MarshalIndent(routeInvestigationWithSource{RouteHealthInvestigation: report, SourceCorrelation: source}, "", "  ")
}

func validateCLIInvestigation(r api.RouteHealthInvestigation, opts api.RouteHealthInvestigationOptions, slug, deployment string) error {
	if err := validateRouteHealthReport(r.Report, deployment); err != nil {
		return err
	}
	if err := routehealth.ValidateClientErrors(r.Report); err != nil {
		return err
	}
	comparison := r.Report
	comparison.Routes = []api.RouteHealthFinding{r.Finding}
	comparison.Status, comparison.Reason = r.Finding.Status, r.Finding.Reason
	comparison.MinimumLatencyRequests = 0
	if routehealth.LatencyEnabled(r.Finding.CheckLatency, r.Finding.MaxP95MS) {
		comparison.MinimumLatencyRequests = api.RouteHealthMinLatencyRequests
	}
	comparison.ClientErrorStatus, comparison.ClientErrorReason = "", ""
	if r.Finding.ClientErrors != nil {
		comparison.ClientErrorStatus, comparison.ClientErrorReason = r.Finding.ClientErrors.Status, r.Finding.ClientErrors.Reason
	}
	if err := validateRouteHealthReport(comparison, deployment); err != nil {
		return err
	}
	if err := routehealth.ValidateClientErrors(comparison); err != nil {
		return err
	}
	return routehealth.ValidateInvestigation(r, opts, slug)
}

func renderRouteInvestigation(r api.RouteHealthInvestigation, slug string) {
	_, _ = fmt.Fprintf(osStdout, "Investigation: %s %s — %s (%s)\nCandidate: %s (%s)\nStable: %s (%s)\nEvidence: %s; observed telemetry only\n", r.Selection.Method, previewReportText(r.Selection.Path), r.Status, previewReportText(r.Reason), r.Report.DeploymentID, previewReportText(r.Report.CandidateCommitSHA), r.Report.StableDeploymentID, previewReportText(r.Report.StableCommitSHA), r.EvidenceStatus)
	if r.Selection.CustomerID != "" {
		_, _ = fmt.Fprintf(osStdout, "Recorded %s: %s\n", r.Selection.CustomerGroupBy, r.Selection.CustomerID)
	}
	for wi, w := range r.Windows {
		_, _ = fmt.Fprintf(osStdout, "\n%s–%s\n", w.Start.UTC().Format("15:04:05Z"), w.End.UTC().Format("15:04:05Z"))
		if r.Selection.Signal == "latency" {
			renderRouteLatencyDiagnostics(r.Finding.Windows[wi], w.Diagnostics, slug)
		}
		for i, side := range []api.RouteHealthInvestigationSide{w.Candidate, w.Stable} {
			name := "candidate"
			if i == 1 {
				name = "stable"
			}
			_, _ = fmt.Fprintf(osStdout, "  %s: %d matching represented requests, %d retained rows; examples truncated: %t\n", name, side.MatchingRequests, side.ObservedRows, side.ExamplesTruncated)
			for _, e := range side.Examples {
				_, _ = fmt.Fprintf(osStdout, "    HTTP %d; latency bucket %dms; represents %d requests; trace link: %t\n      gregale debug requests inspect %s %s\n", e.Status, e.LatencyMS, e.RepresentedRequests, e.TraceID != "", slug, e.TelemetryID)
				if e.TraceID != "" {
					_, _ = fmt.Fprintf(osStdout, "      gregale debug requests trace %s %s\n", slug, e.TelemetryID)
				}
			}
		}
	}
	_, _ = fmt.Fprintln(osStdout, "\nExamples may represent collapsed rows. Traces can be absent or expire; inspect uses current debugger retention.")
}
