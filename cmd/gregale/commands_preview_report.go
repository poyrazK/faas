package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/routeimpact"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

// This report composes existing account-scoped reads. Captured deployment
// contracts are kept separate from current app policy and historical traffic.
type previewRouteReport struct {
	Version               int                              `json:"version"`
	Preview               string                           `json:"preview"`
	Parent                string                           `json:"parent"`
	CandidateDeployment   string                           `json:"candidate_deployment,omitempty"`
	CandidateSourceSHA256 string                           `json:"candidate_source_sha256,omitempty"`
	BaselineDeployment    string                           `json:"baseline_deployment,omitempty"`
	BaselineSelection     string                           `json:"baseline_selection"`
	GeneratedAt           time.Time                        `json:"generated_at"`
	Readiness             previewReportEvidence            `json:"readiness"`
	Outcome               string                           `json:"outcome"`
	Contract              previewReportEvidence            `json:"contract"`
	Policy                previewReportEvidence            `json:"policy"`
	PolicyDrift           previewRoutePolicyDriftEvidence  `json:"policy_drift"`
	Performance           previewReportEvidence            `json:"performance"`
	Requests              previewReportEvidence            `json:"requests"`
	Security              previewReportEvidence            `json:"security"`
	Customers             previewReportCustomerEvidence    `json:"customers"`
	Tests                 previewReportEvidence            `json:"tests"`
	Requirements          *routerequirements.PreviewReport `json:"requirements,omitempty"`
	TestReportSHA256      string                           `json:"test_report_sha256,omitempty"`
	UnboundTestRuns       int                              `json:"unbound_test_runs"`
	BaselineDocumentHash  string                           `json:"baseline_document_sha256,omitempty"`
	CandidateDocumentHash string                           `json:"candidate_document_sha256,omitempty"`
	Routes                []previewReportRoute             `json:"routes"`
	Notes                 []string                         `json:"notes"`
	SourceImpact          *previewSourceImpact             `json:"source_impact,omitempty"`
	ReviewPriorities      []previewRouteReview             `json:"review_priorities,omitempty"`
	baselineSource        previewDeploymentSource
	candidateSource       previewDeploymentSource
	candidateContract     *openapidiff.Spec
	candidateEdgeRules    []api.EdgeRuleResponse
	candidateRulesErr     error
	candidateRulesLoaded  bool
}

type previewReportEvidence struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type previewReportRoute struct {
	Method                 string                      `json:"method"`
	Path                   string                      `json:"path"`
	Change                 string                      `json:"change"`
	RouteSource            string                      `json:"route_source"`
	Breaks                 []previewReportBreak        `json:"breaks,omitempty"`
	RequestContractChanged bool                        `json:"request_contract_changed,omitempty"`
	PolicyKinds            []string                    `json:"policy_kinds"`
	PolicyScope            string                      `json:"policy_scope"`
	TestProfiles           []previewReportTest         `json:"test_profiles"`
	SourceTestProfiles     []previewReportTest         `json:"source_test_profiles"`
	BaselineTraffic        *previewReportTraffic       `json:"baseline_traffic,omitempty"`
	CandidateTraffic       *previewReportTraffic       `json:"candidate_traffic,omitempty"`
	P95ChangeMS            *int                        `json:"p95_change_ms,omitempty"`
	NextActions            []string                    `json:"next_actions"`
	RequestCompatibility   *openapidiff.RequestRoute   `json:"request_compatibility,omitempty"`
	SecurityCompatibility  *openapidiff.SecurityRoute  `json:"security_compatibility,omitempty"`
	PolicyDrift            *previewRoutePolicyDrift    `json:"policy_drift,omitempty"`
	SourceImpact           *previewRouteSource         `json:"source_impact,omitempty"`
	CustomerImpact         *previewRouteCustomerImpact `json:"customer_impact,omitempty"`
}

// Never copy schema Before/After values, rule actions, or test errors into a
// shareable report: these can contain customer examples or credentials.
type previewReportBreak struct {
	Kind         string `json:"kind"`
	Status       string `json:"status,omitempty"`
	PathInSchema string `json:"path_in_schema,omitempty"`
}

type previewReportTest struct {
	Profile string `json:"profile"`
	Passed  int    `json:"passed"`
	Failed  int    `json:"failed"`
}

type previewReportTraffic struct {
	Requests     int64   `json:"requests"`
	P95MS        int     `json:"p95_ms"`
	ErrorRatePct float64 `json:"error_rate_pct"`
	ColdRequests int64   `json:"cold_requests"`
	From         string  `json:"from"`
	Until        string  `json:"until"`
}

func cmdPreviewReport(args []string) int {
	flags, pos := splitArgsForFlags(args, "fail-on-breaking", "fail-on-request-breaking", "fail-on-security-regression", "fail-on-policy-drift", "fail-on-incomplete", "fail-on-requirements", "customer-details")
	fs := newFlagSet("preview report", flag.ContinueOnError)
	format := fs.String("format", "text", "report format: text or markdown (or use --json)")
	since := fs.String("since", "24h", "traffic lookback duration")
	customerDetails := fs.Bool("customer-details", false, "include observed consumer and tenant IDs in the report")
	baseline := fs.String("baseline-deployment", "", "explicit parent deployment ID")
	tests := fs.String("test-report", "", "JSON receipts from gregale test")
	sourceImpact := fs.String("source-impact", "", "version 2 FastAPI or version 3 Go net/http report from gregale routes impact")
	requirements := fs.String("requirements", "", "versioned route requirements YAML or JSON file")
	failRequestBreaking := fs.Bool("fail-on-request-breaking", false, "exit 1 for known request-contract restrictions")
	failSecurity := fs.Bool("fail-on-security-regression", false, "exit 1 for known reductions in declared authentication requirements")
	failPolicyDrift := fs.Bool("fail-on-policy-drift", false, "exit 1 for changed or incomplete route rule policy comparison")
	failBreaking := fs.Bool("fail-on-breaking", false, "exit 1 for known response-contract breaks")
	failIncomplete := fs.Bool("fail-on-incomplete", false, "exit 1 when evidence is missing or needs review")
	failRequirements := fs.Bool("fail-on-requirements", false, "exit 1 for violated or unknown route requirements")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 || !validCLISlug(pos[0]) || (*format != "text" && *format != "markdown") || (jsonOutput && *format != "text") {
		PrintUsage(osStderr, "usage: gregale preview report <preview-slug> [--format text|markdown] [--since 24h] [--customer-details] [--source-impact PATH] [--test-report PATH] [--requirements PATH] [--fail-on-breaking] [--fail-on-request-breaking] [--fail-on-security-regression] [--fail-on-policy-drift] [--fail-on-incomplete] [--fail-on-requirements]", "preview")
		return 1
	}
	if d, err := time.ParseDuration(*since); err != nil || d <= 0 {
		return printErr("Invalid --since", errors.New("use a positive duration such as 24h or 168h"))
	}
	if *baseline != "" && !deploymentIDPattern.MatchString(*baseline) {
		return printErr("Invalid --baseline-deployment", errors.New("use a deployment UUID, not a path or app-local revision"))
	}
	if *failRequirements && *requirements == "" {
		return printErr("Missing --requirements", errors.New("--fail-on-requirements requires a route requirements file"))
	}
	var requirementConfig routerequirements.PreviewConfig
	var requirementDigest string
	if *requirements != "" {
		var err error
		requirementConfig, requirementDigest, err = readPreviewCoverageRequirements(*requirements)
		if err != nil {
			return printErr("Invalid route requirements", err)
		}
	}
	var receipts []testRunReceipt
	var impact routeimpact.Report
	var impactDigest string
	if *sourceImpact != "" {
		var err error
		impact, impactDigest, err = readPreviewSourceImpact(*sourceImpact)
		if err != nil {
			return routeImpactError("Invalid source impact report", err)
		}
	}
	var digest string
	if *tests != "" {
		var err error
		receipts, digest, err = readTestBaselineReport(*tests, nil)
		if err != nil {
			return printErr("Could not read test report", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := collectPreviewRouteReport(ctx, client, pos[0], *baseline, *since)
	if err != nil {
		return printErr("Could not build route change report", err)
	}
	attachPreviewReportTests(&report, receipts, digest)
	collectPreviewCustomerUsage(ctx, client, &report, *since, *customerDetails)
	if *requirements != "" {
		attachPreviewCoverageRequirements(ctx, client, &report, requirementConfig, requirementDigest)
	}
	finishPreviewRouteReport(&report)
	if *sourceImpact != "" {
		attachPreviewSourceImpact(&report, impact, impactDigest)
	}
	prioritizePreviewRouteReview(&report)
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		renderPreviewRouteReport(osStdout, report, *format == "markdown")
	}
	if (*failSecurity && previewReportHasSecurityRegressions(report)) || (*failPolicyDrift && previewReportHasPolicyDrift(report)) || (*failRequestBreaking && (previewReportHasRequestBreaks(report) || previewReportHasSecurityBreaks(report))) || (*failBreaking && previewReportHasBreaks(report)) || (*failIncomplete && report.Outcome != "no_findings") ||
		(*failRequirements && report.Requirements.Status != "satisfied") {
		return 1
	}
	return 0
}

func collectPreviewRouteReport(ctx context.Context, client *api.Client, slug, baselineID, since string) (previewRouteReport, error) {
	preview, err := client.GetPreview(ctx, slug)
	if err != nil {
		return previewRouteReport{}, err
	}
	if preview.App.Slug != slug || preview.App.PreviewOfSlug == "" || preview.Parent == nil || preview.Parent.Slug != preview.App.PreviewOfSlug {
		return previewRouteReport{}, errors.New("preview has no accessible parent app")
	}
	report := previewRouteReport{
		Version: 7, Preview: preview.App.Slug, Parent: preview.Parent.Slug,
		GeneratedAt: time.Now().UTC(), BaselineSelection: "latest_live_parent",
		Requests:  previewReportEvidence{Status: "unavailable", Reason: "captured_documents_missing"},
		Security:  previewReportEvidence{Status: "unavailable", Reason: "captured_documents_missing"},
		Contract:  previewReportEvidence{Status: "unavailable", Reason: "deployment_missing"},
		Readiness: previewReportEvidence{Status: "unavailable", Reason: "candidate_not_live"},
		Tests:     previewReportEvidence{Status: "not_supplied"},
		Routes:    []previewReportRoute{}, Notes: []string{
			"Contract classification covers route removals, supported response-schema changes, and supported declared request restrictions; runtime behavior is not verified.",
			"Request comparison supports required inputs, types, scalar enums, nullability, numeric/length/size limits, simple nullable unions, object/array structure, and local input references. Unsupported schemas and serialization remain explicit unknowns.",
			"Security comparison evaluates captured OpenAPI authentication declarations and supported credential combinations; it does not establish runtime enforcement, token validity, or relative credential strength.",
			"Policy drift compares immutable edge-rule snapshots captured when each selected deployment first became live; older deployments without snapshots remain unknown.",
			"Traffic differences are advisory: request mix, load, and warm/cold proportions may differ.",
		},
	}
	production := preview.ProductionDeployment
	if baselineID != "" {
		production, err = previewReportBaseline(ctx, client, baselineID, preview.Parent.ID)
		if err != nil {
			return report, err
		}
		report.BaselineSelection = "explicit_parent_deployment"
	} else if production != nil && production.Status != "live" {
		production = nil
		report.Notes = append(report.Notes, "Latest parent deployment is not live; choose --baseline-deployment to compare a known parent revision.")
	}
	if production != nil {
		report.BaselineDeployment = production.ID
	}
	report.baselineSource = previewSourceDeployment(production, preview.Parent.ID)
	report.candidateSource = previewSourceDeployment(preview.LatestDeployment, preview.App.ID)
	if preview.LatestDeployment != nil {
		report.CandidateDeployment = preview.LatestDeployment.ID
		report.CandidateSourceSHA256 = preview.LatestDeployment.SourceSHA256
		if preview.LatestDeployment.Status == "live" {
			report.Readiness = previewReportEvidence{Status: "available", Reason: "candidate_live"}
		}
		if preview.LatestDeployment.Status != "live" {
			report.Notes = append(report.Notes, "Candidate deployment is not live; this report does not establish readiness.")
		}
	}
	before, beforeHash, beforeState := previewReportDocument(ctx, client, report.Parent, report.BaselineDeployment)
	after, afterHash, afterState := previewReportDocument(ctx, client, report.Preview, report.CandidateDeployment)
	report.BaselineDocumentHash, report.CandidateDocumentHash = beforeHash, afterHash
	report.candidateContract = after
	if before != nil && after != nil {
		report.Contract = previewReportEvidence{Status: "available"}
		report.Routes, report.Requests = comparePreviewContractsWithRequests(before, after)
		report.Security = attachPreviewSecurityComparison(report.Routes, before, after)
	} else {
		report.Contract.Reason = "baseline:" + beforeState + ";candidate:" + afterState
	}
	policy, policyErr := client.PreviewAppOpenAPIPolicy(ctx, slug)
	attachPreviewReportPolicy(&report, policy, policyErr)
	baselineRules, baselineRulesErr := previewDeploymentRoutePolicySnapshot(ctx, client, report.Parent, report.BaselineDeployment, preview.Parent.ID)
	candidatePolicyRules, candidatePolicyErr := previewDeploymentRoutePolicySnapshot(ctx, client, report.Preview, report.CandidateDeployment, preview.App.ID)
	candidateRules, candidateRulesErr := client.ListEdgeRulesForApp(ctx, report.Preview)
	report.candidateEdgeRules, report.candidateRulesErr, report.candidateRulesLoaded = candidateRules, candidateRulesErr, true
	attachPreviewRoutePolicyDrift(&report, baselineRules, candidatePolicyRules, baselineRulesErr, candidatePolicyErr)
	beforeTraffic, beforeErr := client.GetAppRequestAnalytics(ctx, report.Parent, since)
	afterTraffic, afterErr := client.GetAppRequestAnalytics(ctx, report.Preview, since)
	attachPreviewReportTraffic(&report, beforeTraffic, afterTraffic, beforeErr, afterErr)
	return report, nil
}

func previewDeploymentRoutePolicySnapshot(ctx context.Context, client *api.Client, slug, deploymentID, appID string) ([]api.EdgeRuleResponse, error) {
	if deploymentID == "" || appID == "" {
		return nil, errors.New("deployment route policy snapshot is unavailable")
	}
	snapshot, err := client.GetAppsDeploymentRoutePolicySnapshot(ctx, slug, deploymentID)
	if err != nil {
		return nil, err
	}
	if snapshot.DeploymentID != deploymentID || snapshot.AppID != appID || snapshot.SchemaVersion < 1 || snapshot.SHA256 == "" {
		return nil, errors.New("deployment route policy snapshot identity or metadata is invalid")
	}
	if snapshot.Rules == nil {
		snapshot.Rules = []api.EdgeRuleResponse{}
	}
	return snapshot.Rules, nil
}

func previewReportBaseline(ctx context.Context, client *api.Client, id, parentID string) (*api.DeploymentResponse, error) {
	deployment, err := client.GetDeployment(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load baseline deployment: %w", err)
	}
	if deployment.ID != id || parentID == "" || deployment.AppID != parentID {
		return nil, errors.New("baseline deployment must match the requested ID and belong to the preview's parent app")
	}
	return &deployment, nil
}

func previewReportDocument(ctx context.Context, client *api.Client, slug, id string) (*openapidiff.Spec, string, string) {
	if id == "" {
		return nil, "", "deployment_missing"
	}
	doc, err := client.GetAppsDeploymentOpenAPIDoc(ctx, slug, id)
	if err != nil {
		return nil, "", previewReportReadReason(err)
	}
	if doc.DeploymentID != id || doc.Truncated || len(doc.Doc) == 0 {
		return nil, "", "document_incomplete"
	}
	body, err := marshalPreviewReportDocument(doc.Doc)
	if err != nil {
		return nil, "", "document_invalid"
	}
	spec, err := openapidiff.LoadBytes(body)
	if err != nil || spec.OpenAPIVersion() == "" {
		return nil, "", "document_invalid"
	}
	return spec, fmt.Sprintf("%x", openapidiff.SumSHA256(body)), "available"
}

func previewReportReadReason(err error) string {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Code != "" {
		return apiErr.Problem.Code
	}
	return "read_failed"
}

func previewReportHasBreaks(report previewRouteReport) bool {
	for _, route := range report.Routes {
		if len(route.Breaks) != 0 {
			return true
		}
	}
	return false
}

func previewReportRouteKey(method, path string) string {
	return strings.ToUpper(method) + " " + path
}
