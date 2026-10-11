package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

const routeHealthReleaseReviewVersion = 1

type routeHealthReleaseReviewReport struct {
	Version     int                               `json:"version"`
	GeneratedAt time.Time                         `json:"generated_at"`
	ReleaseGate routeHealthReviewGate             `json:"release_gate"`
	Previews    []routeHealthReleaseReviewPreview `json:"previews"`
}

type routeHealthReleaseReviewPreview struct {
	App                 string                   `json:"app,omitempty"`
	Preview             string                   `json:"preview"`
	CandidateDeployment string                   `json:"candidate_deployment,omitempty"`
	Status              string                   `json:"status"`
	Reason              string                   `json:"reason,omitempty"`
	Report              *routeHealthReviewReport `json:"report,omitempty"`
}

func cmdRoutesHealthReviewRelease(args []string) int {
	flags, positional := splitArgsForFlags(args, "fail-on-incomplete")
	fs := newFlagSet("routes health review-release", flag.ContinueOnError)
	releaseReportPath := fs.String("release-report", "", "aggregate JSON report from gregale preview review")
	failOnIncomplete := fs.Bool("fail-on-incomplete", false, "exit nonzero unless every app has complete, healthy affected-route coverage")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if *releaseReportPath == "" || len(positional) != 0 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale routes health review-release --release-report release-review.json [--fail-on-incomplete] [--json]", "routes")
		return 1
	}
	release, err := readPreviewReleaseRouteReview(*releaseReportPath)
	if err != nil {
		return printErr("Invalid --release-report", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report := collectRouteHealthReleaseReview(ctx, client, release)
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		renderRouteHealthReleaseReview(osStdout, report)
	}
	for _, preview := range report.Previews {
		if preview.Status == "unavailable" {
			return 1
		}
	}
	if *failOnIncomplete && report.ReleaseGate.Status != "ready" {
		return 1
	}
	return 0
}

func readPreviewReleaseRouteReview(path string) (previewReleaseRouteReview, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return previewReleaseRouteReview{}, errors.New("use a readable regular file without symlinks")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteImpactReportMaxBytes+1))
	if err != nil {
		return previewReleaseRouteReview{}, errors.New("could not read release preview report")
	}
	if int64(len(body)) > api.RouteImpactReportMaxBytes {
		return previewReleaseRouteReview{}, errors.New("release preview report exceeds the 64 MiB limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var report previewReleaseRouteReview
	if err := decoder.Decode(&report); err != nil {
		return previewReleaseRouteReview{}, errors.New("release preview report is not valid versioned JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return previewReleaseRouteReview{}, errors.New("release preview report must contain exactly one JSON object")
	}
	if err := validateRouteHealthReleasePreviewInput(report); err != nil {
		return previewReleaseRouteReview{}, err
	}
	return report, nil
}

func validateRouteHealthReleasePreviewInput(report previewReleaseRouteReview) error {
	if report.Version != 1 || report.GeneratedAt.IsZero() ||
		!slices.Contains([]string{"no_findings", "review_required", "incomplete"}, report.Outcome) ||
		len(report.Previews) < 1 || len(report.Previews) > previewReviewMaxPreviews ||
		report.Summary.PreviewCount != len(report.Previews) {
		return errors.New("release preview report version, outcome or preview count is invalid")
	}
	seen := make(map[string]struct{}, len(report.Previews))
	available, unavailable := 0, 0
	for _, entry := range report.Previews {
		if !validCLISlug(entry.Slug) {
			return errors.New("release preview report contains an invalid preview slug")
		}
		if _, ok := seen[entry.Slug]; ok {
			return errors.New("release preview report contains duplicate preview slugs")
		}
		seen[entry.Slug] = struct{}{}
		switch entry.Status {
		case "available":
			available++
			if entry.Reason != "" || entry.Report == nil || !validCLISlug(entry.Report.Parent) ||
				entry.Report.Preview != entry.Slug || entry.Report.Version != 7 || entry.Report.Routes == nil ||
				len(entry.Report.Routes) > api.RouteImpactMaxComparedRoutes {
				return errors.New("release preview report contains invalid or mismatched app evidence")
			}
			if entry.Report.BaselineDeployment != "" && !canonicalRouteHealthID(entry.Report.BaselineDeployment) ||
				entry.Report.CandidateDeployment != "" && !canonicalRouteHealthID(entry.Report.CandidateDeployment) {
				return errors.New("release preview report contains a non-canonical deployment identity")
			}
			if err := validateRouteHealthReleaseSourceImpact(entry.Report.SourceImpact); err != nil {
				return err
			}
		case "unavailable":
			unavailable++
			if entry.Report != nil || strings.TrimSpace(entry.Reason) == "" {
				return errors.New("release preview report contains an invalid unavailable-app entry")
			}
		default:
			return errors.New("release preview report contains an invalid app status")
		}
	}
	if available != report.Summary.AvailablePreviews || unavailable != report.Summary.UnavailablePreviews {
		return errors.New("release preview report summary does not match its app entries")
	}
	return nil
}

func validateRouteHealthReleaseSourceImpact(source *previewSourceImpact) error {
	if source == nil {
		return nil // Preview review can run without source-impact evidence; the gate will report it as incomplete.
	}
	if !slices.Contains([]string{"aligned", "unbound"}, source.Status) ||
		!slices.Contains([]string{"complete", "incomplete"}, source.AnalysisStatus) ||
		!slices.Contains([]string{"complete", "partial", "not_attempted"}, source.MappingStatus) ||
		source.IssueCount < 0 || len(source.Unmatched) > api.RouteImpactMaxComparedRoutes {
		return errors.New("release preview report contains invalid source-impact evidence")
	}
	if source.Status == "aligned" && (source.Base.Status != "declared_match" || source.Candidate.Status != "declared_match") ||
		source.Status == "unbound" && source.Base.Status == "declared_match" && source.Candidate.Status == "declared_match" {
		return errors.New("release preview report has an inconsistent source-binding summary")
	}
	switch source.MappingStatus {
	case "complete":
		if source.Status != "aligned" || len(source.Unmatched) != 0 {
			return errors.New("release preview report has an inconsistent source-mapping summary")
		}
	case "partial":
		if source.Status != "aligned" || len(source.Unmatched) == 0 {
			return errors.New("release preview report has an inconsistent source-mapping summary")
		}
	case "not_attempted":
		if source.Status != "unbound" {
			return errors.New("release preview report has an inconsistent source-mapping summary")
		}
	}
	return nil
}

func collectRouteHealthReleaseReview(ctx context.Context, client *api.Client, release previewReleaseRouteReview) routeHealthReleaseReviewReport {
	result := routeHealthReleaseReviewReport{
		Version: routeHealthReleaseReviewVersion, GeneratedAt: time.Now().UTC(),
		Previews: make([]routeHealthReleaseReviewPreview, len(release.Previews)),
	}
	workers := previewReviewWorkers
	if workers > len(release.Previews) {
		workers = len(release.Previews)
	}
	tasks := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range tasks {
				result.Previews[index] = reviewRouteHealthReleasePreview(ctx, client, release.Previews[index])
			}
		}()
	}
	for index := range release.Previews {
		tasks <- index
	}
	close(tasks)
	wg.Wait()
	result.ReleaseGate = evaluateRouteHealthReleaseGateSet(result.Previews)
	return result
}

func reviewRouteHealthReleasePreview(ctx context.Context, client *api.Client, input previewReleaseRouteReviewPreview) routeHealthReleaseReviewPreview {
	entry := routeHealthReleaseReviewPreview{Preview: input.Slug}
	if input.Status == "unavailable" {
		entry.Status, entry.Reason = "unavailable", "preview_unavailable"
		return entry
	}
	preview := input.Report
	entry.App = preview.Parent
	entry.CandidateDeployment = preview.CandidateDeployment
	switch {
	case preview.SourceImpact == nil:
		entry.Status, entry.Reason = "not_ready", "source_impact_unavailable"
		return entry
	case preview.BaselineDeployment == "":
		entry.Status, entry.Reason = "not_ready", "baseline_deployment_missing"
		return entry
	case preview.CandidateDeployment == "":
		entry.Status, entry.Reason = "not_ready", "candidate_deployment_missing"
		return entry
	}
	health, err := client.GetRouteHealthReport(ctx, input.Report.Parent, preview.CandidateDeployment)
	if err != nil {
		entry.Status, entry.Reason = "unavailable", "candidate_route_health_unavailable"
		return entry
	}
	if err := validateRouteHealthReport(health, preview.CandidateDeployment); err != nil {
		entry.Status, entry.Reason = "unavailable", "candidate_route_health_invalid"
		return entry
	}
	if err := routehealth.ValidateClientErrors(health); err != nil {
		entry.Status, entry.Reason = "unavailable", "candidate_route_health_invalid"
		return entry
	}
	review, err := buildReleaseRouteHealthReviewReport(health, input.Report.Parent, preview.CandidateDeployment, *preview)
	if err != nil {
		entry.Status, entry.Reason = "not_ready", "route_health_review_incomplete"
		return entry
	}
	entry.Report = &review
	entry.Status = review.ReleaseGate.Status
	return entry
}
func evaluateRouteHealthReleaseGateSet(previews []routeHealthReleaseReviewPreview) routeHealthReviewGate {
	reasons := []string{}
	for _, preview := range previews {
		if preview.Status == "ready" {
			continue
		}
		scope := preview.App
		if scope == "" {
			scope = "preview[" + preview.Preview + "]"
		}
		if preview.Reason != "" {
			reasons = append(reasons, scope+":"+preview.Reason)
			continue
		}
		if preview.Report != nil {
			for _, reason := range preview.Report.ReleaseGate.Reasons {
				reasons = append(reasons, scope+":"+reason)
			}
		}
	}
	status := "ready"
	if len(reasons) > 0 {
		status = "not_ready"
	}
	return routeHealthReviewGate{Status: status, Reasons: reasons}
}

func renderRouteHealthReleaseReview(w io.Writer, report routeHealthReleaseReviewReport) {
	ready, unavailable := 0, 0
	for _, preview := range report.Previews {
		if preview.Status == "ready" {
			ready++
		}
		if preview.Status == "unavailable" {
			unavailable++
		}
	}
	_, _ = fmt.Fprintf(w, "Release-wide route health review: %s\nApps: %d ready, %d not ready, %d unavailable\n",
		report.ReleaseGate.Status, ready, len(report.Previews)-ready-unavailable, unavailable)
	for _, preview := range report.Previews {
		label := "preview " + previewReportText(preview.Preview)
		if preview.App != "" {
			label = previewReportText(preview.App) + " (preview " + previewReportText(preview.Preview) + ")"
		}
		_, _ = fmt.Fprintf(w, "\n%s, candidate %s: %s",
			label, previewReportText(preview.CandidateDeployment), preview.Status)
		if preview.Reason != "" {
			_, _ = fmt.Fprintf(w, " (%s)", preview.Reason)
		}
		if preview.Report != nil {
			_, _ = fmt.Fprintf(w, "; affected %d, healthy %d, regressed %d, insufficient %d",
				preview.Report.ReleaseScope.AffectedExistingRoutes, preview.Report.ReleaseScope.HealthyAffectedRoutes,
				preview.Report.ReleaseScope.RegressedAffectedRoutes, preview.Report.ReleaseScope.InsufficientAffectedRoutes)
		}
		_, _ = fmt.Fprintln(w)
	}
	if len(report.ReleaseGate.Reasons) > 0 {
		_, _ = fmt.Fprintln(w, "\nRelease gate reasons:")
		for _, reason := range report.ReleaseGate.Reasons {
			_, _ = fmt.Fprintf(w, "- %s\n", reason)
		}
	}
}
