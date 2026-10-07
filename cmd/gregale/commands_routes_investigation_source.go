package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeimpact"
)

const routeInvestigationSourceEvidenceLimit = 8

const routeInvestigationSourceCaveat = "Static references indicate possible impact; they do not prove code execution or regression cause. Repository, commit, and source-root agreement is declared metadata alignment; it does not verify deployment archive bytes."

type routeInvestigationWithSource struct {
	api.RouteHealthInvestigation
	SourceCorrelation *routeInvestigationSourceCorrelation `json:"source_correlation,omitempty"`
}

type routeInvestigationSourceCorrelation struct {
	Status            string                         `json:"status"`
	Reason            string                         `json:"reason,omitempty"`
	ArtifactSHA256    string                         `json:"artifact_sha256"`
	Repository        string                         `json:"repository,omitempty"`
	SourceRoot        string                         `json:"source_root"`
	BaseRevision      string                         `json:"base_revision"`
	CandidateRevision string                         `json:"candidate_revision"`
	AnalysisStatus    string                         `json:"analysis_status"`
	Base              previewSourceBinding           `json:"base"`
	Candidate         previewSourceBinding           `json:"candidate"`
	Route             *routeInvestigationSourceRoute `json:"route,omitempty"`
	Caveat            string                         `json:"caveat"`
}

type routeInvestigationSourceRoute struct {
	Method            string                 `json:"method"`
	Path              string                 `json:"path"`
	Mapping           string                 `json:"mapping"`
	Change            string                 `json:"change"`
	Precision         string                 `json:"precision"`
	BeforeHandler     string                 `json:"before_handler,omitempty"`
	AfterHandler      string                 `json:"after_handler,omitempty"`
	BeforeLocation    *routeimpact.Location  `json:"before_location,omitempty"`
	AfterLocation     *routeimpact.Location  `json:"after_location,omitempty"`
	Evidence          []routeimpact.Evidence `json:"evidence"`
	EvidenceTruncated bool                   `json:"evidence_truncated"`
	UncertaintyCount  int                    `json:"uncertainty_count"`
}

func correlateRouteInvestigationSource(ctx context.Context, client *api.Client, investigation api.RouteHealthInvestigation, source routeimpact.Report, digest string) routeInvestigationSourceCorrelation {
	correlation := routeInvestigationSourceCorrelation{
		Status: "unavailable", ArtifactSHA256: digest, Repository: source.Repository, SourceRoot: source.SourceRoot,
		BaseRevision: source.Base.Revision, CandidateRevision: source.Candidate.Revision, AnalysisStatus: source.Status,
		Caveat: routeInvestigationSourceCaveat,
	}
	if investigation.Report.StableDeploymentID == "" {
		correlation.Reason = "stable_deployment_unavailable"
		correlation.Base = unavailableSourceBinding("stable_deployment_unavailable")
		correlation.Candidate = unavailableSourceBinding("not_checked")
		return correlation
	}
	correlation.Base = bindInvestigationSourceDeployment(ctx, client, source, source.Base, investigation.Report.StableDeploymentID, investigation.Report.AppID, investigation.Report.StableCommitSHA)
	correlation.Candidate = bindInvestigationSourceDeployment(ctx, client, source, source.Candidate, investigation.Report.DeploymentID, investigation.Report.AppID, investigation.Report.CandidateCommitSHA)
	if correlation.Base.Status != "declared_match" || correlation.Candidate.Status != "declared_match" {
		correlation.Reason = firstSourceBindingReason(correlation.Base, correlation.Candidate)
		return correlation
	}

	selectedKey := previewSourceRouteKey(investigation.Selection.Method, investigation.Selection.Path)
	if selectedKey == "" {
		correlation.Reason = "unsupported_route_template"
		return correlation
	}
	var matched *routeimpact.Result
	matches := 0
	for index := range source.Routes {
		result := &source.Routes[index]
		if previewSourceRouteKey(result.Method, result.Path) == selectedKey {
			matched = result
			matches++
		}
	}
	switch {
	case matches == 0:
		correlation.Status, correlation.Reason = "route_not_reported", "route_not_found_in_report"
		return correlation
	case matches > 1:
		correlation.Reason = "ambiguous_route_mapping"
		return correlation
	}

	correlation.Status = "matched"
	correlation.Route = investigationSourceRoute(*matched, investigation.Selection.Method, investigation.Selection.Path)
	return correlation
}

func bindInvestigationSourceDeployment(ctx context.Context, client *api.Client, source routeimpact.Report, snapshot routeimpact.Snapshot, deploymentID, appID, expectedCommit string) previewSourceBinding {
	if deploymentID == "" {
		return unavailableSourceBinding("deployment_unavailable")
	}
	deployment, err := client.GetDeployment(ctx, deploymentID)
	if err != nil {
		return unavailableSourceBinding("deployment_detail_unavailable")
	}
	if deployment.ID != deploymentID {
		return unavailableSourceBinding("deployment_identity_mismatch")
	}
	if appID == "" || deployment.AppID != appID {
		return unavailableSourceBinding("app_identity_mismatch")
	}
	binding := bindPreviewSource(source, snapshot, previewSourceDeployment(&deployment, appID))
	if binding.Status == "declared_match" && (!routeimpact.ValidCommit(expectedCommit) || !strings.EqualFold(deployment.CommitSHA, expectedCommit)) {
		binding.Status, binding.Reason = "unbound", "route_health_revision_mismatch"
	}
	return binding
}

func unavailableSourceBinding(reason string) previewSourceBinding {
	return previewSourceBinding{Status: "unbound", Reason: reason}
}

func firstSourceBindingReason(base, candidate previewSourceBinding) string {
	if base.Status != "declared_match" {
		if base.Reason != "" {
			return "base_" + base.Reason
		}
		return "base_unbound"
	}
	if candidate.Reason != "" {
		return "candidate_" + candidate.Reason
	}
	return "candidate_unbound"
}

func investigationSourceRoute(result routeimpact.Result, selectedMethod, selectedPath string) *routeInvestigationSourceRoute {
	route := &routeInvestigationSourceRoute{
		Method: result.Method, Path: result.Path, Mapping: "exact", Change: result.Change, Precision: result.Precision,
		Evidence: []routeimpact.Evidence{}, UncertaintyCount: len(result.Uncertainties),
	}
	if result.Method != selectedMethod || result.Path != selectedPath {
		route.Mapping = "parameter_names"
	}
	if result.Before != nil {
		route.BeforeHandler = result.Before.Handler
		location := result.Before.Source
		route.BeforeLocation = &location
	}
	if result.After != nil {
		route.AfterHandler = result.After.Handler
		location := result.After.Source
		route.AfterLocation = &location
	}
	if len(result.Evidence) > routeInvestigationSourceEvidenceLimit {
		route.EvidenceTruncated = true
		result.Evidence = result.Evidence[:routeInvestigationSourceEvidenceLimit]
	}
	route.Evidence = append(route.Evidence, result.Evidence...)
	return route
}

func renderRouteInvestigationSource(correlation routeInvestigationSourceCorrelation) {
	_, _ = fmt.Fprintf(osStdout, "\nSource correlation: %s", correlation.Status)
	if correlation.Reason != "" {
		_, _ = fmt.Fprintf(osStdout, " (%s)", previewReportText(correlation.Reason))
	}
	_, _ = fmt.Fprintln(osStdout)
	if correlation.Status == "matched" && correlation.Route != nil {
		route := correlation.Route
		_, _ = fmt.Fprintf(osStdout, "  Static route: %s %s; %s (%s precision, %s mapping; analysis %s)\n", route.Method, route.Path, route.Change, route.Precision, route.Mapping, correlation.AnalysisStatus)
		if route.BeforeHandler != "" || route.BeforeLocation != nil {
			_, _ = fmt.Fprintf(osStdout, "  Before: %s at %s\n", previewReportText(route.BeforeHandler), routeInvestigationLocation(route.BeforeLocation))
		}
		if route.AfterHandler != "" || route.AfterLocation != nil {
			_, _ = fmt.Fprintf(osStdout, "  After:  %s at %s\n", previewReportText(route.AfterHandler), routeInvestigationLocation(route.AfterLocation))
		}
		for _, evidence := range route.Evidence {
			_, _ = fmt.Fprintf(osStdout, "  Static reference: %s %s %s at %s\n", evidence.Kind, evidence.Revision, evidence.Change, previewReportText(fmt.Sprintf("%s:%d", evidence.File, evidence.Line)))
			if len(evidence.ViaSymbols) > 0 {
				chain := make([]string, 0, len(evidence.ViaSymbols))
				for _, symbol := range evidence.ViaSymbols {
					chain = append(chain, previewReportText(fmt.Sprintf("%s (%s:%d)", symbol.Name, symbol.File, symbol.Line)))
				}
				_, _ = fmt.Fprintf(osStdout, "    reference chain: %s\n", strings.Join(chain, " → "))
			} else if len(evidence.Via) > 0 {
				chain := make([]string, 0, len(evidence.Via))
				for _, path := range evidence.Via {
					chain = append(chain, previewReportText(path))
				}
				_, _ = fmt.Fprintf(osStdout, "    module path chain: %s\n", strings.Join(chain, " → "))
			}
		}
		if route.EvidenceTruncated {
			_, _ = fmt.Fprintf(osStdout, "  Static reference chains truncated at %d.\n", routeInvestigationSourceEvidenceLimit)
		}
		if route.UncertaintyCount > 0 {
			_, _ = fmt.Fprintf(osStdout, "  Source analysis has %d route uncertainty item(s).\n", route.UncertaintyCount)
		}
	}
	_, _ = fmt.Fprintf(osStdout, "  %s\n", correlation.Caveat)
}

func routeInvestigationLocation(location *routeimpact.Location) string {
	if location == nil {
		return "unavailable"
	}
	return previewReportText(fmt.Sprintf("%s:%d", location.File, location.Line))
}
