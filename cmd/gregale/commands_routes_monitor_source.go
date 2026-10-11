package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeimpact"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
)

const routeMonitorSourceCaveat = "Source references show possible impact, not runtime execution or regression cause. When available, the report base is checked against a saved deployment with a healthy route-monitor report; repository, commit, and source-root checks are declared metadata alignment and do not verify deployment archive bytes."

type routeMonitorIncidentWithSource struct {
	api.RouteMonitorIncident
	SourceCorrelation *routeMonitorSourceCorrelation `json:"source_correlation,omitempty"`
}

type routeMonitorSourceCorrelation struct {
	Status            string                            `json:"status"`
	Reason            string                            `json:"reason,omitempty"`
	ArtifactSHA256    string                            `json:"artifact_sha256"`
	Repository        string                            `json:"repository,omitempty"`
	SourceRoot        string                            `json:"source_root"`
	BaseRevision      string                            `json:"base_revision"`
	CandidateRevision string                            `json:"candidate_revision"`
	AnalysisStatus    string                            `json:"analysis_status"`
	BaseBinding       previewSourceBinding              `json:"base_binding"`
	CandidateBinding  previewSourceBinding              `json:"candidate_binding"`
	Routes            []routeMonitorIncidentSourceRoute `json:"routes"`
	OwnershipCaveat   string                            `json:"ownership_caveat,omitempty"`
	Caveat            string                            `json:"caveat"`
}

type routeMonitorIncidentSourceRoute struct {
	Method               string                          `json:"method"`
	Path                 string                          `json:"path"`
	Signals              []string                        `json:"signals"`
	Status               string                          `json:"status"`
	Reason               string                          `json:"reason,omitempty"`
	CustomerImpact       *api.RouteMonitorCustomerImpact `json:"customer_impact,omitempty"`
	CustomerImpactStatus string                          `json:"customer_impact_status"`
	Ownership            *routeMonitorSourceOwnership    `json:"ownership,omitempty"`
	Source               *routeInvestigationSourceRoute  `json:"source,omitempty"`
}

type routeMonitorAffectedRoute struct {
	method  string
	path    string
	signals map[string]struct{}
}

func correlateRouteMonitorIncidentSource(ctx context.Context, client *api.Client, incident api.RouteMonitorIncident, source routeimpact.Report, digest string) routeMonitorSourceCorrelation {
	correlation := newRouteMonitorSourceCorrelation(source, digest)
	if incident.DeploymentID == "" || incident.OpeningReport.DeploymentID != incident.DeploymentID {
		correlation.Reason = "incident_deployment_unavailable"
		correlation.CandidateBinding = unavailableSourceBinding(correlation.Reason)
		return correlation
	}
	if incident.Baseline == nil || incident.Baseline.DeploymentID == "" || incident.Baseline.DeploymentID == incident.DeploymentID {
		correlation.Reason = "healthy_baseline_unavailable"
		correlation.BaseBinding = unavailableSourceBinding(correlation.Reason)
		return correlation
	}
	correlation.BaseBinding = bindPreviewSource(source, source.Base, previewSourceHealthyBaseline(*incident.Baseline))
	if correlation.BaseBinding.Status != "declared_match" {
		correlation.Reason = firstSourceBindingReason(correlation.BaseBinding, correlation.CandidateBinding)
		return correlation
	}
	deployment, err := client.GetDeployment(ctx, incident.DeploymentID)
	if err != nil {
		correlation.Reason = "deployment_detail_unavailable"
		correlation.CandidateBinding = unavailableSourceBinding(correlation.Reason)
		return correlation
	}
	if deployment.ID != incident.DeploymentID {
		correlation.Reason = "deployment_identity_mismatch"
		correlation.CandidateBinding = unavailableSourceBinding(correlation.Reason)
		return correlation
	}
	if deployment.AppID != incident.AppID || incident.OpeningReport.AppID != incident.AppID {
		correlation.Reason = "app_identity_mismatch"
		correlation.CandidateBinding = unavailableSourceBinding(correlation.Reason)
		return correlation
	}
	if !routeimpact.ValidCommit(incident.OpeningReport.CommitSHA) || !strings.EqualFold(deployment.CommitSHA, incident.OpeningReport.CommitSHA) {
		correlation.Reason = "incident_deployment_revision_mismatch"
		correlation.CandidateBinding = unavailableSourceBinding(correlation.Reason)
		return correlation
	}

	correlation.CandidateBinding = bindPreviewSource(source, source.Candidate, previewSourceDeployment(&deployment, incident.AppID))
	if correlation.CandidateBinding.Status != "declared_match" {
		correlation.Reason = firstSourceBindingReason(correlation.BaseBinding, correlation.CandidateBinding)
		return correlation
	}
	correlation.Status = "release_pair_bound"
	correlation.Routes = correlateRouteMonitorAffectedRoutes(incident, source)
	return correlation
}

func newRouteMonitorSourceCorrelation(source routeimpact.Report, digest string) routeMonitorSourceCorrelation {
	return routeMonitorSourceCorrelation{
		Status: "unavailable", ArtifactSHA256: digest, Repository: source.Repository, SourceRoot: source.SourceRoot,
		BaseRevision: source.Base.Revision, CandidateRevision: source.Candidate.Revision, AnalysisStatus: source.Status,
		BaseBinding: unavailableSourceBinding("healthy_baseline_unavailable"), CandidateBinding: unavailableSourceBinding("not_checked"),
		Routes: []routeMonitorIncidentSourceRoute{}, Caveat: routeMonitorSourceCaveat,
	}
}

// correlateRouteMonitorIncidentSourceAuto analyzes the exact release pair saved
// on an incident using only the caller's current local checkout. It never
// fetches or clones source; routeimpact.Analyze reads bounded Git snapshots.
func correlateRouteMonitorIncidentSourceAuto(ctx context.Context, client *api.Client, incident api.RouteMonitorIncident, app, startDirectory string) routeMonitorSourceCorrelation {
	if incident.DeploymentID == "" || incident.OpeningReport.DeploymentID != incident.DeploymentID {
		return unavailableAutomaticRouteMonitorSource(incident, routeimpact.Report{}, "incident_deployment_unavailable")
	}
	if incident.Baseline == nil || incident.Baseline.DeploymentID == "" || incident.Baseline.DeploymentID == incident.DeploymentID {
		return unavailableAutomaticRouteMonitorSource(incident, routeimpact.Report{}, "healthy_baseline_unavailable")
	}
	baseline := previewSourceHealthyBaseline(*incident.Baseline)
	if baseline.reason != "" {
		return unavailableAutomaticRouteMonitorSource(incident, routeimpact.Report{
			Repository: baseline.repository, SourceRoot: baseline.root,
			Base:      routeimpact.Snapshot{Revision: strings.ToLower(incident.Baseline.CommitSHA)},
			Candidate: routeimpact.Snapshot{Revision: strings.ToLower(incident.OpeningReport.CommitSHA)},
		}, baseline.reason)
	}
	if !routeimpact.ValidCommit(incident.OpeningReport.CommitSHA) {
		return unavailableAutomaticRouteMonitorSource(incident, routeimpact.Report{
			Repository: baseline.repository, SourceRoot: baseline.root,
			Base: routeimpact.Snapshot{Revision: baseline.commit},
		}, "incident_deployment_revision_unavailable")
	}

	sourcePath, err := localRouteMonitorSourcePath(ctx, startDirectory, baseline.root)
	if err != nil {
		return unavailableAutomaticRouteMonitorSource(incident, routeimpact.Report{
			Repository: baseline.repository, SourceRoot: baseline.root,
			Base:      routeimpact.Snapshot{Revision: baseline.commit},
			Candidate: routeimpact.Snapshot{Revision: strings.ToLower(incident.OpeningReport.CommitSHA)},
		}, "local_repository_unavailable")
	}
	source, err := routeimpact.Analyze(ctx, routeimpact.Options{
		Path: sourcePath, Base: baseline.commit, Head: strings.ToLower(incident.OpeningReport.CommitSHA), App: app,
	})
	if err != nil {
		return unavailableAutomaticRouteMonitorSource(incident, routeimpact.Report{
			Repository: baseline.repository, SourceRoot: baseline.root,
			Base:      routeimpact.Snapshot{Revision: baseline.commit},
			Candidate: routeimpact.Snapshot{Revision: strings.ToLower(incident.OpeningReport.CommitSHA)},
		}, "local_analysis_unavailable")
	}
	body, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return unavailableAutomaticRouteMonitorSource(incident, source, "local_analysis_unavailable")
	}
	body = append(body, '\n')
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	return correlateRouteMonitorIncidentSource(ctx, client, incident, source, digest)
}

func unavailableAutomaticRouteMonitorSource(incident api.RouteMonitorIncident, source routeimpact.Report, reason string) routeMonitorSourceCorrelation {
	correlation := newRouteMonitorSourceCorrelation(source, "")
	correlation.Reason = reason
	correlation.AnalysisStatus = "unavailable"
	correlation.BaseBinding = unavailableSourceBinding(reason)
	correlation.CandidateBinding = unavailableSourceBinding(reason)
	if incident.Baseline == nil || incident.Baseline.DeploymentID == "" || incident.Baseline.DeploymentID == incident.DeploymentID {
		correlation.BaseBinding = unavailableSourceBinding("healthy_baseline_unavailable")
	}
	return correlation
}

// localRouteMonitorSourcePath finds the local Git root from the user's current
// directory, then resolves the stored repository-relative build root beneath it.
func localRouteMonitorSourcePath(ctx context.Context, startDirectory, sourceRoot string) (string, error) {
	if startDirectory == "" {
		startDirectory = "."
	}
	cmd := exec.CommandContext(ctx, "git", "-C", startDirectory, "rev-parse", "--show-toplevel")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1")
	cmd.Stderr = io.Discard
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	root := strings.TrimSuffix(string(output), "\n")
	if root == "" || strings.ContainsAny(root, "\r\n") {
		return "", fmt.Errorf("git returned an invalid repository root")
	}
	if sourceRoot == "" || sourceRoot == "." {
		return root, nil
	}
	return filepath.Join(root, filepath.FromSlash(sourceRoot)), nil
}

func previewSourceHealthyBaseline(b api.RouteMonitorDeploymentBaseline) previewDeploymentSource {
	if b.DeploymentID == "" {
		return previewDeploymentSource{reason: "healthy_baseline_unavailable"}
	}
	repository, reference := routeimpact.RepositoryReference(b.Repository)
	if repository == "" {
		return previewDeploymentSource{reason: "repository_unavailable"}
	}
	if !routeimpact.ValidCommit(b.CommitSHA) {
		return previewDeploymentSource{repository: repository, reason: "revision_unavailable"}
	}
	if b.SourceRoot == "" {
		return previewDeploymentSource{repository: repository, commit: strings.ToLower(b.CommitSHA), reason: "source_root_unavailable"}
	}
	root, err := sourcecontext.Normalize(b.SourceRoot)
	if err != nil || root != b.SourceRoot {
		return previewDeploymentSource{repository: repository, commit: strings.ToLower(b.CommitSHA), reason: "source_root_invalid"}
	}
	return previewDeploymentSource{repository: repository, commit: strings.ToLower(b.CommitSHA), root: root, referenceCommit: reference}
}

func correlateRouteMonitorAffectedRoutes(incident api.RouteMonitorIncident, source routeimpact.Report) []routeMonitorIncidentSourceRoute {
	affected := make(map[int]*routeMonitorAffectedRoute)
	add := func(index int, signal string) {
		if index < 0 || index >= len(incident.OpeningReport.Routes) {
			return
		}
		finding := incident.OpeningReport.Routes[index]
		entry := affected[index]
		if entry == nil {
			entry = &routeMonitorAffectedRoute{method: finding.Route.Method, path: finding.Route.Path, signals: map[string]struct{}{}}
			affected[index] = entry
		}
		entry.signals[signal] = struct{}{}
	}
	for index, finding := range incident.OpeningReport.Routes {
		if finding.ErrorStatus == "violated" {
			add(index, "errors")
		}
		if finding.LatencyStatus == "violated" {
			add(index, "latency")
		}
		if finding.Status == "violated" && finding.ErrorStatus != "violated" && finding.LatencyStatus != "violated" {
			add(index, "customer_impact")
		}
	}
	for _, escalation := range incident.Escalations {
		for _, signal := range escalation.Signals {
			add(signal.RouteIndex, signal.Signal)
		}
	}

	indexes := make([]int, 0, len(affected))
	for index := range affected {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	byRoute := make(map[string][]routeimpact.Result, len(source.Routes))
	for _, result := range source.Routes {
		key := previewSourceRouteKey(result.Method, result.Path)
		if key != "" {
			byRoute[key] = append(byRoute[key], result)
		}
	}

	correlated := make([]routeMonitorIncidentSourceRoute, 0, len(indexes))
	for _, index := range indexes {
		item := affected[index]
		entry := routeMonitorIncidentSourceRoute{
			Method: item.method, Path: item.path, Signals: orderedRouteMonitorSignals(item.signals), Status: "unavailable",
			CustomerImpactStatus: "not_configured",
		}
		entry.CustomerImpact, entry.CustomerImpactStatus = routeMonitorIncidentCustomerImpact(incident, item.method, item.path)
		key := previewSourceRouteKey(item.method, item.path)
		if key == "" {
			entry.Reason = "unsupported_route_template"
			correlated = append(correlated, entry)
			continue
		}
		matches := byRoute[key]
		switch len(matches) {
		case 0:
			entry.Status, entry.Reason = "route_not_reported", "route_not_found_in_report"
		case 1:
			entry.Status = "matched"
			entry.Source = investigationSourceRoute(matches[0], item.method, item.path)
		default:
			entry.Reason = "ambiguous_route_mapping"
		}
		correlated = append(correlated, entry)
	}
	return correlated
}

func orderedRouteMonitorSignals(signals map[string]struct{}) []string {
	ordered := make([]string, 0, len(signals))
	for _, signal := range []string{"errors", "latency", "customer_impact"} {
		if _, ok := signals[signal]; ok {
			ordered = append(ordered, signal)
		}
	}
	for signal := range signals {
		if signal != "errors" && signal != "latency" && signal != "customer_impact" {
			ordered = append(ordered, signal)
		}
	}
	if len(ordered) > 3 {
		sort.Strings(ordered[3:])
	}
	return ordered
}

func renderRouteMonitorIncidentSource(correlation routeMonitorSourceCorrelation) {
	_, _ = fmt.Fprintf(osStdout, "\nSource change correlation: %s", correlation.Status)
	if correlation.Reason != "" {
		_, _ = fmt.Fprintf(osStdout, " (%s)", previewReportText(correlation.Reason))
	}
	_, _ = fmt.Fprintln(osStdout)
	_, _ = fmt.Fprintf(osStdout, "  Report range: %s → %s; static analysis %s; base binding %s; candidate binding %s\n", previewReportText(correlation.BaseRevision), previewReportText(correlation.CandidateRevision), correlation.AnalysisStatus, correlation.BaseBinding.Status, correlation.CandidateBinding.Status)
	if correlation.BaseBinding.Status == "declared_match" {
		_, _ = fmt.Fprintf(osStdout, "  Healthy baseline deployment: %s (%s)\n", correlation.BaseBinding.Commit, previewReportText(correlation.BaseBinding.Repository))
	} else {
		_, _ = fmt.Fprintln(osStdout, "  A saved healthy deployment baseline could not be matched to the report base revision.")
	}
	for _, route := range correlation.Routes {
		_, _ = fmt.Fprintf(osStdout, "  %s %s — %s: %s", route.Method, previewReportText(route.Path), strings.Join(route.Signals, ", "), route.Status)
		if route.Reason != "" {
			_, _ = fmt.Fprintf(osStdout, " (%s)", previewReportText(route.Reason))
		}
		if route.Source != nil {
			_, _ = fmt.Fprintf(osStdout, "; source %s (%s precision, %s mapping)\n", route.Source.Change, route.Source.Precision, route.Source.Mapping)
			if route.Source.BeforeHandler != "" || route.Source.BeforeLocation != nil {
				_, _ = fmt.Fprintf(osStdout, "    Before: %s at %s\n", previewReportText(route.Source.BeforeHandler), routeInvestigationLocation(route.Source.BeforeLocation))
			}
			if route.Source.AfterHandler != "" || route.Source.AfterLocation != nil {
				_, _ = fmt.Fprintf(osStdout, "    After:  %s at %s\n", previewReportText(route.Source.AfterHandler), routeInvestigationLocation(route.Source.AfterLocation))
			}
			for _, evidence := range route.Source.Evidence {
				_, _ = fmt.Fprintf(osStdout, "    Static reference: %s %s %s at %s\n", evidence.Kind, evidence.Revision, evidence.Change, previewReportText(fmt.Sprintf("%s:%d", evidence.File, evidence.Line)))
				if len(evidence.ViaSymbols) > 0 {
					chain := make([]string, 0, len(evidence.ViaSymbols))
					for _, symbol := range evidence.ViaSymbols {
						chain = append(chain, previewReportText(fmt.Sprintf("%s (%s:%d)", symbol.Name, symbol.File, symbol.Line)))
					}
					_, _ = fmt.Fprintf(osStdout, "      reference chain: %s\n", strings.Join(chain, " → "))
				} else if len(evidence.Via) > 0 {
					chain := make([]string, 0, len(evidence.Via))
					for _, path := range evidence.Via {
						chain = append(chain, previewReportText(path))
					}
					_, _ = fmt.Fprintf(osStdout, "      module path chain: %s\n", strings.Join(chain, " → "))
				}
			}
			if route.Source.EvidenceTruncated {
				_, _ = fmt.Fprintf(osStdout, "    Static reference chains truncated at %d.\n", routeInvestigationSourceEvidenceLimit)
			}
			if route.Source.UncertaintyCount > 0 {
				_, _ = fmt.Fprintf(osStdout, "    Source analysis has %d route uncertainty item(s).\n", route.Source.UncertaintyCount)
			}
		} else {
			_, _ = fmt.Fprintln(osStdout)
		}
		if route.CustomerImpact != nil {
			impact := route.CustomerImpact
			_, _ = fmt.Fprintf(osStdout, "    Customer impact: %d observed, %d violated, %d unknown (%s coverage)\n", impact.ObservedCustomers, impact.ViolatedCustomers, impact.UnknownCustomers, previewReportText(impact.Coverage))
		} else {
			_, _ = fmt.Fprintf(osStdout, "    Customer impact: %s\n", route.CustomerImpactStatus)
		}
		if route.Ownership != nil {
			renderRouteMonitorSourceOwnership(*route.Ownership)
		}
	}
	_, _ = fmt.Fprintf(osStdout, "  %s\n", correlation.Caveat)
	if correlation.OwnershipCaveat != "" {
		_, _ = fmt.Fprintf(osStdout, "  %s\n", correlation.OwnershipCaveat)
	}
}
