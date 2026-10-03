package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeimpact"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
)

// Source metadata is declared provenance, never an assertion that the analyzed
// bytes equal the deployment archive. The two fingerprints cover different bytes.
type previewDeploymentSource struct {
	repository, commit, root, referenceCommit string
	reason                                    string
}

type previewSourceBinding struct {
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Repository string `json:"repository,omitempty"`
	Commit     string `json:"commit,omitempty"`
	SourceRoot string `json:"source_root,omitempty"`
}

type previewSourceImpact struct {
	SHA256            string                   `json:"sha256"`
	Repository        string                   `json:"repository,omitempty"`
	SourceRoot        string                   `json:"source_root"`
	BaseRevision      string                   `json:"base_revision"`
	CandidateRevision string                   `json:"candidate_revision"`
	Status            string                   `json:"status"`
	AnalysisStatus    string                   `json:"analysis_status"`
	MappingStatus     string                   `json:"mapping_status"`
	Base              previewSourceBinding     `json:"base"`
	Candidate         previewSourceBinding     `json:"candidate"`
	IssueCount        int                      `json:"issue_count"`
	Unmatched         []previewUnmatchedSource `json:"unmatched"`
	Scope             string                   `json:"scope"`
}

type previewRouteSource struct {
	Change           string                 `json:"change"`
	Precision        string                 `json:"precision"`
	SourcePath       string                 `json:"source_path"`
	Match            string                 `json:"match"`
	Before           *routeimpact.Location  `json:"before,omitempty"`
	After            *routeimpact.Location  `json:"after,omitempty"`
	Evidence         []routeimpact.Evidence `json:"evidence"`
	UncertaintyCount int                    `json:"uncertainty_count"`
}

type previewUnmatchedSource struct {
	Method string             `json:"method"`
	Path   string             `json:"path"`
	Reason string             `json:"reason"`
	Source previewRouteSource `json:"source"`
}

func readPreviewSourceImpact(path string) (routeimpact.Report, string, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return routeimpact.Report{}, "", errors.New("open source impact: use a readable regular file without symlinks")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteImpactReportMaxBytes+1))
	if err != nil {
		return routeimpact.Report{}, "", errors.New("read source impact file failed")
	}
	report, err := routeimpact.ParseReport(body)
	if err != nil {
		return report, "", err
	}
	return report, fmt.Sprintf("%x", sha256.Sum256(body)), nil
}

func previewSourceDeployment(deployment *api.DeploymentResponse, appID string) previewDeploymentSource {
	if deployment == nil {
		return previewDeploymentSource{reason: "deployment_missing"}
	}
	if deployment.ID == "" || appID == "" || deployment.AppID != appID {
		return previewDeploymentSource{reason: "deployment_identity_unavailable"}
	}
	repository, reference := routeimpact.RepositoryReference(deployment.SourceURL)
	if repository == "" {
		return previewDeploymentSource{reason: "repository_unavailable"}
	}
	if !routeimpact.ValidCommit(deployment.CommitSHA) {
		return previewDeploymentSource{repository: repository, reason: "revision_unavailable"}
	}
	root, err := sourcecontext.Normalize(deployment.SourceRoot)
	if err != nil || (deployment.SourceRoot != "" && root != deployment.SourceRoot) {
		return previewDeploymentSource{repository: repository, reason: "source_root_invalid"}
	}
	return previewDeploymentSource{repository: repository, commit: strings.ToLower(deployment.CommitSHA), root: root, referenceCommit: reference}
}

func bindPreviewSource(source routeimpact.Report, snapshot routeimpact.Snapshot, deployment previewDeploymentSource) previewSourceBinding {
	binding := previewSourceBinding{Status: "unbound", Repository: deployment.repository, Commit: deployment.commit, SourceRoot: deployment.root}
	switch {
	case deployment.reason != "":
		binding.Reason = deployment.reason
	case snapshot.Revision == "working-tree":
		binding.Reason = "working_tree_candidate"
	case source.Repository == "":
		binding.Reason = "source_repository_unavailable"
	case source.Repository != deployment.repository:
		binding.Reason = "repository_mismatch"
	case deployment.referenceCommit != "" && deployment.referenceCommit != deployment.commit:
		binding.Reason = "source_reference_conflict"
	case !strings.EqualFold(snapshot.Revision, deployment.commit):
		binding.Reason = "revision_mismatch"
	case source.SourceRoot != deployment.root:
		binding.Reason = "source_root_mismatch"
	default:
		binding.Status = "declared_match"
	}
	return binding
}

func previewSourceRoute(result routeimpact.Result) previewRouteSource {
	source := previewRouteSource{Change: result.Change, Precision: result.Precision, SourcePath: result.Path, Evidence: result.Evidence, UncertaintyCount: len(result.Uncertainties)}
	if result.Before != nil {
		location := result.Before.Source
		source.Before = &location
	}
	if result.After != nil {
		location := result.After.Source
		source.After = &location
	}
	return source
}

func attachPreviewSourceImpact(report *previewRouteReport, source routeimpact.Report, digest string) {
	report.Version = 5
	joined := &previewSourceImpact{
		SHA256: digest, Status: "unbound", AnalysisStatus: source.Status, MappingStatus: "not_attempted",
		Repository: source.Repository, SourceRoot: source.SourceRoot, BaseRevision: source.Base.Revision, CandidateRevision: source.Candidate.Revision,
		Base: bindPreviewSource(source, source.Base, report.baselineSource), Candidate: bindPreviewSource(source, source.Candidate, report.candidateSource),
		IssueCount: len(source.Issues), Unmatched: []previewUnmatchedSource{},
		Scope: "Customer-supplied static analysis. Repository, commit, and build-root agreement is declared metadata alignment, not archive-byte verification. References show possible impact, not runtime execution. Passing receipts cover supplied request samples, not every request on a route. Baseline traffic orders attention within each priority and does not measure change risk.",
	}
	report.SourceImpact = joined
	if joined.Base.Status == "declared_match" && joined.Candidate.Status == "declared_match" {
		joined.Status, joined.MappingStatus = "aligned", "complete"
	}
	// Match only captured deployment contracts. Current policy and observation
	// rows cannot establish membership in either selected deployment.
	captured := map[string][]int{}
	static := map[string]int{}
	for index, row := range report.Routes {
		if row.RouteSource == "captured_deployment_contract" {
			if key := previewSourceRouteKey(row.Method, row.Path); key != "" {
				captured[key] = append(captured[key], index)
			}
		}
	}
	for _, result := range source.Routes {
		if key := previewSourceRouteKey(result.Method, result.Path); key != "" {
			static[key]++
		}
	}
	for _, result := range source.Routes {
		entry := previewSourceRoute(result)
		reason := ""
		key := previewSourceRouteKey(result.Method, result.Path)
		matches := captured[key]
		switch {
		case joined.Status != "aligned":
			reason = "source_unbound"
		case key == "":
			reason = "unsupported_route_template"
		case static[key] > 1 || len(matches) > 1:
			reason = "ambiguous_route_template"
		case len(matches) == 0:
			reason = "captured_route_missing"
		case !previewSourcePresenceMatches(result, report.Routes[matches[0]]):
			reason = "route_presence_conflict"
		default:
			row := &report.Routes[matches[0]]
			entry.Match = "exact"
			if result.Path != row.Path {
				entry.Match = "parameter_names"
			}
			row.SourceImpact = &entry
		}
		if reason != "" {
			joined.Unmatched = append(joined.Unmatched, previewUnmatchedSource{Method: result.Method, Path: result.Path, Reason: reason, Source: entry})
			if joined.Status == "aligned" {
				joined.MappingStatus = "partial"
			}
		}
	}
}

func previewSourcePresenceMatches(source routeimpact.Result, captured previewReportRoute) bool {
	switch source.Change {
	case "added", "removed":
		return source.Change == captured.Change
	case "unknown":
		return (source.Before == nil || captured.Change != "added") && (source.After == nil || captured.Change != "removed")
	default:
		return captured.Change == "changed" || captured.Change == "unchanged"
	}
}

// Only whole-segment identifiers are interchangeable. Embedded parameters,
// converters, escaped paths, and ambiguous aliases remain unresolved.
func previewSourceRouteKey(method, path string) string {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?%#\\") {
		return ""
	}
	parts := strings.Split(path, "/")
	for index, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") && len(part) > 2 {
			name := part[1 : len(part)-1]
			for i, char := range name {
				if char != '_' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (i == 0 || char < '0' || char > '9') {
					return ""
				}
			}
			parts[index] = "{}"
		} else if strings.ContainsAny(part, "{}") || part == "." || part == ".." {
			return ""
		}
	}
	return previewReportRouteKey(method, strings.Join(parts, "/"))
}
