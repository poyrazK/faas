package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeimpact"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

const (
	previewReviewMaxPreviews      = 20
	previewReviewWorkers          = 4
	previewReviewEvidenceMaxBytes = api.RouteImpactReportMaxBytes
)

type previewReviewAssignments []string

func (values *previewReviewAssignments) String() string {
	if values == nil {
		return ""
	}
	return strings.Join(*values, ",")
}

func (values *previewReviewAssignments) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type previewReviewEvidenceInput struct {
	baselineDeployment string
	sourceImpact       *routeimpact.Report
	sourceImpactDigest string
	testReceipts       []testRunReceipt
	testReportDigest   string
	requirements       *routerequirements.PreviewConfig
	requirementsDigest string
}

type previewReleaseRouteReview struct {
	Version          int                                `json:"version"`
	GeneratedAt      time.Time                          `json:"generated_at"`
	Outcome          string                             `json:"outcome"`
	Summary          previewReleaseRouteReviewSummary   `json:"summary"`
	ReviewPriorities []previewReleaseReviewPriority     `json:"review_priorities"`
	Previews         []previewReleaseRouteReviewPreview `json:"previews"`
}

type previewReleaseRouteReviewSummary struct {
	PreviewCount          int `json:"preview_count"`
	AvailablePreviews     int `json:"available_previews"`
	UnavailablePreviews   int `json:"unavailable_previews"`
	CapturedRoutes        int `json:"captured_routes"`
	SourceUnmatchedRoutes int `json:"source_unmatched_routes"`
	ReviewItems           int `json:"review_items"`
	PreviewsNeedingReview int `json:"previews_needing_review"`
}

type previewReleaseRouteReviewPreview struct {
	Slug   string              `json:"slug"`
	Status string              `json:"status"`
	Reason string              `json:"reason,omitempty"`
	Report *previewRouteReport `json:"report,omitempty"`
}

type previewReleaseReviewPriority struct {
	Preview         string                      `json:"preview"`
	Parent          string                      `json:"parent,omitempty"`
	Method          string                      `json:"method,omitempty"`
	Path            string                      `json:"path,omitempty"`
	Scope           string                      `json:"scope"`
	Priority        string                      `json:"priority"`
	Reasons         []string                    `json:"reasons"`
	CandidateChecks string                      `json:"candidate_checks"`
	BaselineTraffic *previewReportTraffic       `json:"baseline_traffic,omitempty"`
	CustomerImpact  *previewRouteCustomerImpact `json:"customer_impact,omitempty"`
	NextActions     []string                    `json:"next_actions"`
}

type previewReleaseReviewGates struct {
	breaking        bool
	requestBreaking bool
	security        bool
	policyDrift     bool
	incomplete      bool
	requirements    bool
}

func cmdPreviewReview(args []string) int {
	flags, positional := splitArgsForFlags(args, "customer-details", "fail-on-breaking", "fail-on-request-breaking", "fail-on-security-regression", "fail-on-policy-drift", "fail-on-incomplete", "fail-on-requirements")
	fs := newFlagSet("preview review", flag.ContinueOnError)
	format := fs.String("format", "text", "report format: text or markdown (or use --json)")
	since := fs.String("since", "24h", "traffic lookback duration")
	customerDetails := fs.Bool("customer-details", false, "include observed consumer and tenant IDs in each app report")
	var baselineInputs, sourceInputs, testInputs, requirementInputs previewReviewAssignments
	fs.Var(&baselineInputs, "baseline-deployment", "parent deployment as PREVIEW=ID; repeat per preview")
	fs.Var(&testInputs, "test-report", "JSON test receipts as PREVIEW=PATH; repeat per preview")
	fs.Var(&sourceInputs, "source-impact", "route impact report as PREVIEW=PATH; repeat per preview")
	fs.Var(&requirementInputs, "requirements", "route requirements as PREVIEW=PATH; repeat per preview")
	failBreaking := fs.Bool("fail-on-breaking", false, "exit 1 for known response-contract breaks in any app")
	failRequestBreaking := fs.Bool("fail-on-request-breaking", false, "exit 1 for known request restrictions in any app")
	failSecurity := fs.Bool("fail-on-security-regression", false, "exit 1 for declared authentication regressions in any app")
	failPolicyDrift := fs.Bool("fail-on-policy-drift", false, "exit 1 for changed or incomplete route policy comparison in any app")
	failIncomplete := fs.Bool("fail-on-incomplete", false, "exit 1 if any app report is unavailable or needs review")
	failRequirements := fs.Bool("fail-on-requirements", false, "exit 1 unless every app has satisfied route requirements")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) < 2 || len(positional) > previewReviewMaxPreviews || (*format != "text" && *format != "markdown") || (jsonOutput && *format != "text") || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale preview review <preview-slug> <preview-slug>... [--source-impact PREVIEW=PATH] [--test-report PREVIEW=PATH] [--requirements PREVIEW=PATH] [--format text|markdown] [--fail-on-breaking] [--fail-on-incomplete]", "preview")
		return 1
	}
	if duration, err := time.ParseDuration(*since); err != nil || duration <= 0 {
		return printErr("Invalid --since", errors.New("use a positive duration such as 24h or 168h"))
	}
	slugSet := make(map[string]struct{}, len(positional))
	for _, slug := range positional {
		if !validCLISlug(slug) {
			return printErr("Invalid preview slug", errors.New("supply two or more valid preview slugs"))
		}
		if _, exists := slugSet[slug]; exists {
			return printErr("Invalid preview list", errors.New("each preview slug must be unique"))
		}
		slugSet[slug] = struct{}{}
	}
	baselineByPreview, err := parsePreviewReviewAssignments(baselineInputs, slugSet, "baseline deployment")
	if err != nil {
		return printErr("Invalid --baseline-deployment", err)
	}
	for _, deployment := range baselineByPreview {
		if !deploymentIDPattern.MatchString(deployment) {
			return printErr("Invalid --baseline-deployment", errors.New("use a deployment UUID for each selected preview"))
		}
	}
	sourceByPreview, err := parsePreviewReviewAssignments(sourceInputs, slugSet, "source impact")
	if err != nil {
		return printErr("Invalid --source-impact", err)
	}
	testByPreview, err := parsePreviewReviewAssignments(testInputs, slugSet, "test report")
	if err != nil {
		return printErr("Invalid --test-report", err)
	}
	requirementsByPreview, err := parsePreviewReviewAssignments(requirementInputs, slugSet, "route requirements")
	if err != nil {
		return printErr("Invalid --requirements", err)
	}
	if *failRequirements {
		for _, slug := range positional {
			if requirementsByPreview[slug] == "" {
				return printErr("Missing --requirements", errors.New("--fail-on-requirements requires a route requirements file for every preview"))
			}
		}
	}
	if err := validatePreviewReviewEvidenceSize(sourceByPreview, testByPreview, requirementsByPreview); err != nil {
		return printErr("Invalid preview review evidence", err)
	}
	evidenceByPreview := make(map[string]previewReviewEvidenceInput, len(positional))
	for _, slug := range positional {
		input, err := readPreviewReviewEvidence(slug, baselineByPreview[slug], sourceByPreview[slug], testByPreview[slug], requirementsByPreview[slug])
		if err != nil {
			return printErr("Could not read preview review evidence", err)
		}
		evidenceByPreview[slug] = input
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report := collectPreviewReleaseRouteReview(ctx, client, positional, evidenceByPreview, *since, *customerDetails)
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		renderPreviewReleaseRouteReview(osStdout, report, *format == "markdown")
	}
	gates := previewReleaseReviewGates{
		breaking: *failBreaking, requestBreaking: *failRequestBreaking, security: *failSecurity,
		policyDrift: *failPolicyDrift, incomplete: *failIncomplete, requirements: *failRequirements,
	}
	if previewReleaseReviewFails(report, gates) {
		return 1
	}
	return 0
}

func validatePreviewReviewEvidenceSize(paths ...map[string]string) error {
	var total int64
	for _, byPreview := range paths {
		for _, path := range byPreview {
			file, err := openCustomerFile(path)
			if err != nil {
				return errors.New("use readable regular evidence files without symlinks")
			}
			info, statErr := file.Stat()
			closeErr := file.Close()
			if statErr != nil || closeErr != nil || !info.Mode().IsRegular() || info.Size() < 0 {
				return errors.New("use readable regular evidence files without symlinks")
			}
			if info.Size() > previewReviewEvidenceMaxBytes-total {
				return errors.New("combined evidence files exceed the 64 MiB release-review limit")
			}
			total += info.Size()
		}
	}
	return nil
}

func parsePreviewReviewAssignments(values []string, slugs map[string]struct{}, label string) (map[string]string, error) {
	result := make(map[string]string, len(values))
	for _, value := range values {
		slug, assigned, ok := strings.Cut(value, "=")
		if !ok || !validCLISlug(slug) || strings.TrimSpace(assigned) == "" {
			return nil, fmt.Errorf("use PREVIEW=VALUE for each %s", label)
		}
		if _, exists := slugs[slug]; !exists {
			return nil, fmt.Errorf("%s references a preview not listed in the command", label)
		}
		if _, duplicate := result[slug]; duplicate {
			return nil, fmt.Errorf("supply %s at most once per preview", label)
		}
		result[slug] = assigned
	}
	return result, nil
}

func readPreviewReviewEvidence(slug, baselinePath, sourcePath, testPath, requirementsPath string) (previewReviewEvidenceInput, error) {
	input := previewReviewEvidenceInput{baselineDeployment: baselinePath}
	if sourcePath != "" {
		report, digest, err := readPreviewSourceImpact(sourcePath)
		if err != nil {
			return input, fmt.Errorf("invalid source impact for %s: %w", slug, err)
		}
		input.sourceImpact, input.sourceImpactDigest = &report, digest
	}
	if testPath != "" {
		receipts, digest, err := readTestBaselineReport(testPath, nil)
		if err != nil {
			return input, errors.New("a test report is invalid or unreadable")
		}
		input.testReceipts, input.testReportDigest = receipts, digest
	}
	if requirementsPath != "" {
		config, digest, err := readPreviewCoverageRequirements(requirementsPath)
		if err != nil {
			return input, errors.New("a route requirements file is invalid or unreadable")
		}
		input.requirements, input.requirementsDigest = &config, digest
	}
	return input, nil
}

func collectPreviewReleaseRouteReview(ctx context.Context, client *api.Client, slugs []string, evidence map[string]previewReviewEvidenceInput, since string, customerDetails bool) previewReleaseRouteReview {
	result := previewReleaseRouteReview{
		Version: 1, GeneratedAt: time.Now().UTC(), Outcome: "no_findings",
		ReviewPriorities: []previewReleaseReviewPriority{}, Previews: make([]previewReleaseRouteReviewPreview, len(slugs)),
	}
	result.Summary.PreviewCount = len(slugs)
	tasks := make(chan int)
	workers := previewReviewWorkers
	if workers > len(slugs) {
		workers = len(slugs)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range tasks {
				slug := slugs[index]
				result.Previews[index] = collectOnePreviewReleaseRouteReview(ctx, client, slug, evidence[slug], since, customerDetails)
			}
		}()
	}
	for index := range slugs {
		tasks <- index
	}
	close(tasks)
	wg.Wait()
	for _, preview := range result.Previews {
		if preview.Report == nil {
			result.Summary.UnavailablePreviews++
			continue
		}
		result.Summary.AvailablePreviews++
		report := preview.Report
		result.Summary.CapturedRoutes += len(report.Routes)
		if report.SourceImpact != nil {
			result.Summary.SourceUnmatchedRoutes += len(report.SourceImpact.Unmatched)
		}
		if report.Outcome != "no_findings" {
			result.Summary.PreviewsNeedingReview++
		}
		appendPreviewReleaseReviewPriorities(&result, report)
	}
	result.Summary.ReviewItems = len(result.ReviewPriorities)
	sort.SliceStable(result.ReviewPriorities, func(i, j int) bool {
		left, right := result.ReviewPriorities[i], result.ReviewPriorities[j]
		if previewReviewPriority(left.Priority) != previewReviewPriority(right.Priority) {
			return previewReviewPriority(left.Priority) < previewReviewPriority(right.Priority)
		}
		leftReach, leftRequests := previewReleaseReviewExposure(left)
		rightReach, rightRequests := previewReleaseReviewExposure(right)
		if leftReach != rightReach {
			return leftReach > rightReach
		}
		if leftRequests != rightRequests {
			return leftRequests > rightRequests
		}
		leftKey := left.Preview + " " + left.Scope + " " + previewReportRouteKey(left.Method, left.Path)
		rightKey := right.Preview + " " + right.Scope + " " + previewReportRouteKey(right.Method, right.Path)
		return leftKey < rightKey
	})
	if result.Summary.UnavailablePreviews > 0 {
		result.Outcome = "incomplete"
	} else {
		for _, preview := range result.Previews {
			if preview.Report != nil && preview.Report.Outcome == "incomplete" {
				result.Outcome = "incomplete"
				break
			}
			if preview.Report != nil && preview.Report.Outcome != "no_findings" {
				result.Outcome = "review_required"
			}
		}
	}
	return result
}

func appendPreviewReleaseReviewPriorities(release *previewReleaseRouteReview, report *previewRouteReport) {
	seen := map[string]bool{}
	appendItem := func(item previewRouteReview) {
		key := item.Scope + " " + previewReportRouteKey(item.Method, item.Path)
		if item.Method != "" && item.Path != "" {
			if seen[key] {
				return
			}
			seen[key] = true
		}
		release.ReviewPriorities = append(release.ReviewPriorities, previewReleaseReviewPriority{
			Preview: report.Preview, Parent: report.Parent, Method: item.Method, Path: item.Path,
			Scope: item.Scope, Priority: item.Priority, Reasons: append([]string{}, item.Reasons...),
			CandidateChecks: item.CandidateChecks, BaselineTraffic: item.BaselineTraffic,
			CustomerImpact: item.CustomerImpact, NextActions: append([]string{}, item.NextActions...),
		})
	}
	for _, priority := range report.ReviewPriorities {
		appendItem(priority)
	}
	for _, row := range report.Routes {
		priority, relevant := previewSourceReviewRoute(row, previewReviewRequirementStatus(report, row))
		if relevant {
			appendItem(priority)
		}
	}
}

func collectOnePreviewReleaseRouteReview(ctx context.Context, client *api.Client, slug string, input previewReviewEvidenceInput, since string, customerDetails bool) previewReleaseRouteReviewPreview {
	entry := previewReleaseRouteReviewPreview{Slug: slug, Status: "unavailable", Reason: "preview_or_evidence_unavailable"}
	report, err := collectPreviewRouteReport(ctx, client, slug, input.baselineDeployment, since)
	if err != nil {
		return entry
	}
	attachPreviewReportTests(&report, input.testReceipts, input.testReportDigest)
	collectPreviewCustomerUsage(ctx, client, &report, since, customerDetails)
	if input.requirements != nil {
		attachPreviewCoverageRequirements(ctx, client, &report, *input.requirements, input.requirementsDigest)
	}
	finishPreviewRouteReport(&report)
	if input.sourceImpact != nil {
		attachPreviewSourceImpact(&report, *input.sourceImpact, input.sourceImpactDigest)
	}
	prioritizePreviewRouteReview(&report)
	entry.Status, entry.Reason, entry.Report = "available", "", &report
	return entry
}

func previewReleaseReviewExposure(priority previewReleaseReviewPriority) (reach, requests int64) {
	if priority.CustomerImpact != nil && priority.CustomerImpact.Usage != nil {
		usage := priority.CustomerImpact.Usage
		reach = usage.ConsumerCount
		if usage.PlatformTenantCount > reach {
			reach = usage.PlatformTenantCount
		}
		requests = usage.Requests
	}
	if requests == 0 && priority.BaselineTraffic != nil {
		requests = priority.BaselineTraffic.Requests
	}
	return reach, requests
}

func previewReleaseReviewFails(report previewReleaseRouteReview, gates previewReleaseReviewGates) bool {
	for _, preview := range report.Previews {
		child := preview.Report
		if child == nil {
			return true
		}
		if gates.security && previewReportHasSecurityRegressions(*child) {
			return true
		}
		if gates.policyDrift && previewReportHasPolicyDrift(*child) {
			return true
		}
		if gates.requestBreaking && (previewReportHasRequestBreaks(*child) || previewReportHasSecurityBreaks(*child)) {
			return true
		}
		if gates.breaking && previewReportHasBreaks(*child) {
			return true
		}
		if gates.incomplete && child.Outcome != "no_findings" {
			return true
		}
		if gates.requirements && (child.Requirements == nil || child.Requirements.Status != "satisfied") {
			return true
		}
	}
	return false
}
