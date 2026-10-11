package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const previewCustomerCutoverDiffVersion = 1

type previewCustomerCutoverDiffReport struct {
	Version           int                                `json:"version"`
	GeneratedAt       time.Time                          `json:"generated_at"`
	BeforeGeneratedAt time.Time                          `json:"before_generated_at"`
	AfterGeneratedAt  time.Time                          `json:"after_generated_at"`
	Outcome           string                             `json:"outcome"`
	GroupBy           string                             `json:"group_by"`
	Summary           previewCustomerCutoverDiffSummary  `json:"summary"`
	Changes           []previewCustomerCutoverDiffChange `json:"changes"`
	Caveats           []string                           `json:"caveats"`
}

type previewCustomerCutoverDiffSummary struct {
	CustomerRouteLinks int `json:"customer_route_links"`
	Regressions        int `json:"regressions"`
	EvidenceDegraded   int `json:"evidence_degraded"`
	Improvements       int `json:"improvements"`
	EvidenceUpdated    int `json:"evidence_updated"`
	Unchanged          int `json:"unchanged"`
}

type previewCustomerCutoverDiffChange struct {
	Classification                string                                  `json:"classification"`
	Reason                        string                                  `json:"reason"`
	Route                         previewCustomerMigrationEndpoint        `json:"route"`
	CustomerID                    string                                  `json:"customer_id"`
	IdentityScope                 string                                  `json:"identity_scope"`
	CustomerApp                   string                                  `json:"customer_app,omitempty"`
	BeforeMigrationEvidence       string                                  `json:"before_migration_evidence"`
	AfterMigrationEvidence        string                                  `json:"after_migration_evidence"`
	BeforeLatestSuccessorEvidence string                                  `json:"before_latest_successor_evidence"`
	AfterLatestSuccessorEvidence  string                                  `json:"after_latest_successor_evidence"`
	BeforeLatestSuccessors        []previewCustomerCutoverLatestSuccessor `json:"before_latest_successors"`
	AfterLatestSuccessors         []previewCustomerCutoverLatestSuccessor `json:"after_latest_successors"`
	BeforeOldRouteReuseSignal     string                                  `json:"before_old_route_reuse_signal,omitempty"`
	AfterOldRouteReuseSignal      string                                  `json:"after_old_route_reuse_signal,omitempty"`
	BeforeOldRouteReuseObservedAt string                                  `json:"before_old_route_reuse_observed_at,omitempty"`
	AfterOldRouteReuseObservedAt  string                                  `json:"after_old_route_reuse_observed_at,omitempty"`
	BeforeNextStep                string                                  `json:"before_next_step"`
	AfterNextStep                 string                                  `json:"after_next_step"`
	BeforeLatestEventAt           string                                  `json:"before_latest_event_at,omitempty"`
	AfterLatestEventAt            string                                  `json:"after_latest_event_at,omitempty"`
}

type previewCustomerCutoverDiffLinkKey struct {
	identity previewCustomerMigrationIdentity
	from     previewCustomerMigrationRouteKey
}

type previewCustomerCutoverDiffLink struct {
	route    previewCustomerMigrationEndpoint
	customer previewCustomerCutoverReviewCustomer
}

func cmdPreviewCustomersMigrationDiff(args []string) int {
	flags, positional := splitArgsForFlags(args, "fail-on-regression")
	fs := newFlagSet("preview customers migration diff", flag.ContinueOnError)
	beforePath := fs.String("before", "", "previous version 1 customer migration cutover review JSON")
	afterPath := fs.String("after", "", "current version 1 customer migration cutover review JSON")
	format := fs.String("format", "text", "report format: text, markdown, or csv (or use --json)")
	output := fs.String("out", "", "write the machine-readable migration diff to a new JSON file")
	failOnRegression := fs.Bool("fail-on-regression", false, "exit nonzero when confirmed customer migration regressions are found")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 || *beforePath == "" || *afterPath == "" ||
		!slices.Contains([]string{"text", "markdown", "csv"}, *format) || (jsonOutput && *format != "text") || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale preview customers migration diff --before <REVIEW.json> --after <REVIEW.json> [--format text|markdown|csv] [--out <PATH>] [--fail-on-regression] [--json]", "preview")
		return 1
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return printErr("Invalid --out", errors.New("choose a new path; existing files and symlinks are not replaced"))
		}
	}
	before, err := readPreviewCustomerCutoverReview(*beforePath)
	if err != nil {
		return printErr("Could not read previous cutover review", err)
	}
	after, err := readPreviewCustomerCutoverReview(*afterPath)
	if err != nil {
		return printErr("Could not read current cutover review", err)
	}
	report, err := buildPreviewCustomerCutoverDiff(before, after, time.Now().UTC())
	if err != nil {
		return printErr("Could not compare cutover reviews", err)
	}
	if *output != "" {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return printErr("Could not encode migration diff", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return printErr("Could not save migration diff", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		var rendered bytes.Buffer
		var renderErr error
		switch *format {
		case "markdown":
			renderPreviewCustomerCutoverDiffMarkdown(&rendered, report)
		case "csv":
			renderErr = renderPreviewCustomerCutoverDiffCSV(&rendered, report)
		default:
			renderPreviewCustomerCutoverDiffText(&rendered, report)
		}
		if renderErr != nil {
			return printErr("Could not encode migration diff", renderErr)
		}
		if _, err := osStdout.Write(rendered.Bytes()); err != nil {
			return printErr("Could not write migration diff", err)
		}
	}
	if *failOnRegression && report.Summary.Regressions > 0 {
		return 1
	}
	return 0
}

func readPreviewCustomerCutoverReview(path string) (previewCustomerCutoverReviewReport, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return previewCustomerCutoverReviewReport{}, errors.New("use a readable regular cutover review file without symlinks")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteImpactReportMaxBytes+1))
	if err != nil || int64(len(body)) > api.RouteImpactReportMaxBytes {
		return previewCustomerCutoverReviewReport{}, errors.New("could not read cutover review or it exceeds the 64 MiB limit")
	}
	var report previewCustomerCutoverReviewReport
	if err := json.Unmarshal(body, &report); err != nil || report.Version != previewCustomerCutoverReviewVersion {
		return previewCustomerCutoverReviewReport{}, errors.New("cutover review must be version 1 JSON from gregale preview customers migration review")
	}
	return report, nil
}

func buildPreviewCustomerCutoverDiff(before, after previewCustomerCutoverReviewReport, now time.Time) (previewCustomerCutoverDiffReport, error) {
	if before.Version != previewCustomerCutoverReviewVersion || after.Version != previewCustomerCutoverReviewVersion {
		return previewCustomerCutoverDiffReport{}, errors.New("both reports must be version 1 customer migration cutover reviews")
	}
	if before.GeneratedAt.IsZero() || after.GeneratedAt.IsZero() || after.GeneratedAt.Before(before.GeneratedAt) {
		return previewCustomerCutoverDiffReport{}, errors.New("current report must have a generation time at or after the previous report")
	}
	if before.GroupBy != after.GroupBy || (before.GroupBy != "consumer" && before.GroupBy != "tenant") {
		return previewCustomerCutoverDiffReport{}, errors.New("reports must use the same customer grouping")
	}
	if !sameRouteMigrationDeploymentSet(before.FromDeployments, after.FromDeployments) || !sameRouteMigrationDeploymentSet(before.ToDeployments, after.ToDeployments) {
		return previewCustomerCutoverDiffReport{}, errors.New("reports must use the same baseline and successor deployment IDs")
	}
	beforeRoutes, err := previewCustomerCutoverRouteMap(before)
	if err != nil {
		return previewCustomerCutoverDiffReport{}, fmt.Errorf("previous report: %w", err)
	}
	afterRoutes, err := previewCustomerCutoverRouteMap(after)
	if err != nil {
		return previewCustomerCutoverDiffReport{}, fmt.Errorf("current report: %w", err)
	}
	if !sameCustomerCutoverRouteMappings(beforeRoutes, afterRoutes) {
		return previewCustomerCutoverDiffReport{}, errors.New("reports do not use the same old-route and successor mapping")
	}
	beforeLinks, err := previewCustomerCutoverDiffLinks(before)
	if err != nil {
		return previewCustomerCutoverDiffReport{}, fmt.Errorf("previous report: %w", err)
	}
	afterLinks, err := previewCustomerCutoverDiffLinks(after)
	if err != nil {
		return previewCustomerCutoverDiffReport{}, fmt.Errorf("current report: %w", err)
	}
	if len(beforeLinks) != len(afterLinks) {
		return previewCustomerCutoverDiffReport{}, errors.New("reports do not contain the same customer-route cohort")
	}
	for key := range beforeLinks {
		if _, ok := afterLinks[key]; !ok {
			return previewCustomerCutoverDiffReport{}, errors.New("reports do not contain the same customer-route cohort")
		}
	}

	report := previewCustomerCutoverDiffReport{
		Version: previewCustomerCutoverDiffVersion, GeneratedAt: now.UTC(),
		BeforeGeneratedAt: before.GeneratedAt, AfterGeneratedAt: after.GeneratedAt,
		GroupBy: before.GroupBy, Outcome: "unchanged", Changes: []previewCustomerCutoverDiffChange{},
		Caveats: []string{
			"The comparison requires identical customer-route cohorts, route mappings, grouping, and immutable deployment IDs. Cohort or mapping drift is rejected; absent customers are never counted as resolved.",
			"Regressions describe observed route or contract evidence changes. Successor traffic does not prove that a customer completed migration, and this report never authorizes route removal.",
			"Evidence degradation and latest successor endpoint changes are reported separately from confirmed regressions. --fail-on-regression fails only for the regression category.",
		},
		Summary: previewCustomerCutoverDiffSummary{CustomerRouteLinks: len(beforeLinks)},
	}
	for key, oldLink := range beforeLinks {
		newLink := afterLinks[key]
		classification, reason := classifyPreviewCustomerCutoverChange(oldLink.customer, newLink.customer)
		if classification == "unchanged" {
			report.Summary.Unchanged++
			continue
		}
		change := previewCustomerCutoverDiffChange{
			Classification: classification, Reason: reason, Route: oldLink.route,
			CustomerID: oldLink.customer.ID, IdentityScope: oldLink.customer.IdentityScope, CustomerApp: oldLink.customer.App,
			BeforeMigrationEvidence: oldLink.customer.MigrationEvidence, AfterMigrationEvidence: newLink.customer.MigrationEvidence,
			BeforeLatestSuccessorEvidence: oldLink.customer.LatestSuccessorEvidence, AfterLatestSuccessorEvidence: newLink.customer.LatestSuccessorEvidence,
			BeforeLatestSuccessors: append([]previewCustomerCutoverLatestSuccessor{}, oldLink.customer.LatestSuccessors...),
			AfterLatestSuccessors:  append([]previewCustomerCutoverLatestSuccessor{}, newLink.customer.LatestSuccessors...),
			BeforeNextStep:         oldLink.customer.NextStep, AfterNextStep: newLink.customer.NextStep,
			BeforeLatestEventAt: previewCustomerCutoverDiffLatestEvent(oldLink.customer),
			AfterLatestEventAt:  previewCustomerCutoverDiffLatestEvent(newLink.customer),
		}
		if reuse := oldLink.customer.ObservedOldRouteReuse; reuse != nil {
			change.BeforeOldRouteReuseSignal = reuse.Signal
			change.BeforeOldRouteReuseObservedAt = reuse.OldRouteLastObservedAt.Format(time.RFC3339Nano)
		}
		if reuse := newLink.customer.ObservedOldRouteReuse; reuse != nil {
			change.AfterOldRouteReuseSignal = reuse.Signal
			change.AfterOldRouteReuseObservedAt = reuse.OldRouteLastObservedAt.Format(time.RFC3339Nano)
		}
		report.Changes = append(report.Changes, change)
		switch classification {
		case "regression":
			report.Summary.Regressions++
		case "evidence_degraded":
			report.Summary.EvidenceDegraded++
		case "improved":
			report.Summary.Improvements++
		case "evidence_updated":
			report.Summary.EvidenceUpdated++
		}
	}
	sort.Slice(report.Changes, func(i, j int) bool {
		left, right := report.Changes[i], report.Changes[j]
		leftRank, rightRank := previewCustomerCutoverDiffClassificationRank(left.Classification), previewCustomerCutoverDiffClassificationRank(right.Classification)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if left.Route != right.Route {
			return migrationEndpointLess(left.Route, right.Route)
		}
		if left.CustomerID != right.CustomerID {
			return left.CustomerID < right.CustomerID
		}
		return left.IdentityScope < right.IdentityScope
	})
	switch {
	case report.Summary.Regressions > 0:
		report.Outcome = "regressions"
	case report.Summary.EvidenceDegraded > 0:
		report.Outcome = "evidence_degraded"
	case report.Summary.Improvements > 0:
		report.Outcome = "improved"
	case report.Summary.EvidenceUpdated > 0:
		report.Outcome = "evidence_updated"
	}
	return report, nil
}

func sameRouteMigrationDeploymentSet(left, right []routeMigrationDeploymentEvidence) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[string]string, len(left))
	for _, deployment := range left {
		if deployment.App == "" || deployment.DeploymentID == "" {
			return false
		}
		if _, exists := values[deployment.App]; exists {
			return false
		}
		values[deployment.App] = deployment.DeploymentID
	}
	for _, deployment := range right {
		if values[deployment.App] != deployment.DeploymentID {
			return false
		}
		delete(values, deployment.App)
	}
	return len(values) == 0
}

func previewCustomerCutoverRouteMap(report previewCustomerCutoverReviewReport) (map[previewCustomerMigrationRouteKey]previewCustomerCutoverReviewRoute, error) {
	routes := make(map[previewCustomerMigrationRouteKey]previewCustomerCutoverReviewRoute, len(report.Routes))
	for _, route := range report.Routes {
		key := previewCustomerMigrationEndpointKey(route.From)
		if route.From.App == "" || route.From.Method == "" || route.From.Path == "" {
			return nil, errors.New("contains an invalid old route")
		}
		if _, exists := routes[key]; exists {
			return nil, errors.New("contains a duplicate old route")
		}
		seenSuccessors := make(map[previewCustomerMigrationRouteKey]bool, len(route.Successors))
		for _, successor := range route.Successors {
			to := previewCustomerMigrationEndpointKey(successor.To)
			if successor.To.App == "" || successor.To.Method == "" || successor.To.Path == "" || seenSuccessors[to] {
				return nil, errors.New("contains an invalid or duplicate successor route")
			}
			seenSuccessors[to] = true
		}
		routes[key] = route
	}
	return routes, nil
}

func sameCustomerCutoverRouteMappings(left, right map[previewCustomerMigrationRouteKey]previewCustomerCutoverReviewRoute) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftRoute := range left {
		rightRoute, ok := right[key]
		if !ok || len(leftRoute.Successors) != len(rightRoute.Successors) {
			return false
		}
		successors := make(map[previewCustomerMigrationRouteKey]bool, len(leftRoute.Successors))
		for _, successor := range leftRoute.Successors {
			successors[previewCustomerMigrationEndpointKey(successor.To)] = true
		}
		for _, successor := range rightRoute.Successors {
			if !successors[previewCustomerMigrationEndpointKey(successor.To)] {
				return false
			}
			delete(successors, previewCustomerMigrationEndpointKey(successor.To))
		}
		if len(successors) != 0 {
			return false
		}
	}
	return true
}

func previewCustomerCutoverDiffLinks(report previewCustomerCutoverReviewReport) (map[previewCustomerCutoverDiffLinkKey]previewCustomerCutoverDiffLink, error) {
	links := make(map[previewCustomerCutoverDiffLinkKey]previewCustomerCutoverDiffLink)
	expectedScope := "account"
	if report.GroupBy == "consumer" {
		expectedScope = "app"
	}
	for _, route := range report.Routes {
		for _, customer := range route.Customers {
			if customer.ID == "" || customer.IdentityScope != expectedScope {
				return nil, errors.New("contains an invalid customer identity")
			}
			identity := previewCustomerMigrationIdentity{id: customer.ID}
			if customer.IdentityScope == "app" {
				if customer.App == "" {
					return nil, errors.New("contains an app-scoped customer without an app")
				}
				identity.app = customer.App
			}
			key := previewCustomerCutoverDiffLinkKey{identity: identity, from: previewCustomerMigrationEndpointKey(route.From)}
			if _, exists := links[key]; exists {
				return nil, errors.New("contains a duplicate customer-route link")
			}
			links[key] = previewCustomerCutoverDiffLink{route: route.From, customer: customer}
		}
	}
	return links, nil
}

func classifyPreviewCustomerCutoverChange(before, after previewCustomerCutoverReviewCustomer) (string, string) {
	beforeReuse, afterReuse := before.ObservedOldRouteReuse, after.ObservedOldRouteReuse
	if afterReuse != nil && (beforeReuse == nil || afterReuse.OldRouteLastObservedAt.After(beforeReuse.OldRouteLastObservedAt)) {
		return "regression", "old-route traffic was observed after successor traffic"
	}
	beforeOldRoute, afterOldRoute := previewCustomerCutoverHasOldRouteEvidence(before.MigrationEvidence), previewCustomerCutoverHasOldRouteEvidence(after.MigrationEvidence)
	if (afterOldRoute && !beforeOldRoute) || (after.MigrationEvidence == "old_route_active" && before.MigrationEvidence == "old_route_observed") {
		return "regression", "customer evidence now includes old-route traffic"
	}
	beforeContractRisk, afterContractRisk := previewCustomerCutoverLatestContractRisk(before), previewCustomerCutoverLatestContractRisk(after)
	if afterContractRisk > beforeContractRisk {
		if afterContractRisk >= 2 {
			return "regression", "latest observed successor now has a breaking contract"
		}
		return "evidence_degraded", "latest observed successor contract is no longer confirmed compatible"
	}
	if previewCustomerCutoverEvidenceBecameIncomplete(before.MigrationEvidence, after.MigrationEvidence) ||
		(before.LatestSuccessorEvidence == "determined" && after.LatestSuccessorEvidence != "determined") {
		return "evidence_degraded", "latest successor evidence became ambiguous or unavailable"
	}
	if afterReuse == nil && beforeReuse != nil {
		if !afterOldRoute && after.MigrationEvidence == "successor_observed" && after.LatestSuccessorEvidence == "determined" {
			return "improved", "the prior old-route reuse signal cleared and successor evidence remains determined"
		}
		return "evidence_degraded", "the prior old-route reuse signal is no longer confirmed, but current evidence does not verify a clean cutover"
	}
	if afterContractRisk < beforeContractRisk {
		return "improved", "latest observed successor contract evidence improved"
	}
	if after.MigrationEvidence == "successor_observed" && beforeOldRoute {
		if after.LatestSuccessorEvidence == "determined" {
			return "improved", "customer evidence moved from old-route activity to a determined successor"
		}
		return "evidence_degraded", "successor activity is observed, but the latest event cannot be determined"
	}
	if before.MigrationEvidence == "old_route_active" && after.MigrationEvidence == "old_route_observed" {
		return "improved", "old-route traffic was not observed in the latest customer snapshot"
	}
	if after.LatestSuccessorEvidence == "determined" && before.LatestSuccessorEvidence != "determined" {
		return "improved", "latest successor evidence is now determined"
	}
	if !samePreviewCustomerCutoverLatestSuccessorRoutes(before.LatestSuccessors, after.LatestSuccessors) {
		return "evidence_updated", "the latest observed successor endpoint changed"
	}
	return "unchanged", ""
}

func samePreviewCustomerCutoverLatestSuccessorRoutes(before, after []previewCustomerCutoverLatestSuccessor) bool {
	if len(before) != len(after) {
		return false
	}
	routes := make(map[previewCustomerMigrationRouteKey]bool, len(before))
	for _, successor := range before {
		routes[previewCustomerMigrationEndpointKey(successor.Route)] = true
	}
	for _, successor := range after {
		key := previewCustomerMigrationEndpointKey(successor.Route)
		if !routes[key] {
			return false
		}
		delete(routes, key)
	}
	return len(routes) == 0
}

func previewCustomerCutoverHasOldRouteEvidence(evidence string) bool {
	return evidence == "old_route_active" || evidence == "old_route_observed"
}

func previewCustomerCutoverEvidenceBecameIncomplete(before, after string) bool {
	switch after {
	case "incomplete", "in_place_unmeasurable":
		return before != after
	case "no_current_evidence":
		return before == "successor_observed" || before == "old_route_active" || before == "old_route_observed"
	default:
		return false
	}
}

func previewCustomerCutoverLatestContractRisk(customer previewCustomerCutoverReviewCustomer) int {
	risk := 0
	for _, successor := range customer.LatestSuccessors {
		switch successor.ContractStatus {
		case "breaking":
			if risk < 2 {
				risk = 2
			}
		case "review_required", "unknown":
			if risk < 1 {
				risk = 1
			}
		}
	}
	return risk
}

func previewCustomerCutoverDiffLatestEvent(customer previewCustomerCutoverReviewCustomer) string {
	at := previewCustomerCutoverActionLastObservedAt(customer)
	if at.IsZero() {
		return ""
	}
	return at.Format(time.RFC3339Nano)
}

func previewCustomerCutoverDiffClassificationRank(classification string) int {
	switch classification {
	case "regression":
		return 0
	case "evidence_degraded":
		return 1
	case "improved":
		return 2
	default:
		return 3
	}
}

func renderPreviewCustomerCutoverDiffText(w io.Writer, report previewCustomerCutoverDiffReport) {
	_, _ = fmt.Fprintf(w, "Customer migration diff (%s)\nOutcome: %s; regressions: %d; evidence degraded: %d; improvements: %d; successor endpoint changes: %d; unchanged: %d of %d customer-route links\nCompared: %s → %s\n",
		report.GroupBy, report.Outcome, report.Summary.Regressions, report.Summary.EvidenceDegraded, report.Summary.Improvements,
		report.Summary.EvidenceUpdated, report.Summary.Unchanged, report.Summary.CustomerRouteLinks,
		report.BeforeGeneratedAt.Format(time.RFC3339), report.AfterGeneratedAt.Format(time.RFC3339))
	for _, change := range report.Changes {
		_, _ = fmt.Fprintf(w, "\n%s: %s %s in %s; customer %s (%s)", change.Classification, change.Route.Method, change.Route.Path, change.Route.App, change.CustomerID, change.IdentityScope)
		if change.CustomerApp != "" {
			_, _ = fmt.Fprintf(w, "; app %s", change.CustomerApp)
		}
		_, _ = fmt.Fprintf(w, "\n  %s\n  migration: %s → %s; latest successor evidence: %s → %s\n  next step: %s → %s\n",
			change.Reason, change.BeforeMigrationEvidence, change.AfterMigrationEvidence,
			change.BeforeLatestSuccessorEvidence, change.AfterLatestSuccessorEvidence, change.BeforeNextStep, change.AfterNextStep)
		if change.BeforeOldRouteReuseSignal != change.AfterOldRouteReuseSignal {
			_, _ = fmt.Fprintf(w, "  old-route reuse: %s → %s\n", change.BeforeOldRouteReuseSignal, change.AfterOldRouteReuseSignal)
		}
	}
}

func renderPreviewCustomerCutoverDiffMarkdown(w io.Writer, report previewCustomerCutoverDiffReport) {
	_, _ = fmt.Fprintf(w, "## Customer migration diff\n\nOutcome: **%s**. Compared **%d** customer-route links from `%s` to `%s`: **%d** regressions, **%d** evidence degradations, **%d** improvements, **%d** successor endpoint changes, **%d** unchanged.\n\n",
		report.Outcome, report.Summary.CustomerRouteLinks, report.BeforeGeneratedAt.Format(time.RFC3339), report.AfterGeneratedAt.Format(time.RFC3339),
		report.Summary.Regressions, report.Summary.EvidenceDegraded, report.Summary.Improvements,
		report.Summary.EvidenceUpdated, report.Summary.Unchanged)
	if len(report.Changes) == 0 {
		_, _ = fmt.Fprintln(w, "No customer-route evidence changed.")
		return
	}
	_, _ = fmt.Fprintln(w, "| Change | Customer | Old route | Evidence | Latest successor evidence | Reason |")
	_, _ = fmt.Fprintln(w, "|---|---|---|---|---|---|")
	for _, change := range report.Changes {
		_, _ = fmt.Fprintf(w, "| **%s** | %s (%s) | `%s %s %s` | %s → %s | %s → %s | %s |\n",
			change.Classification, previewSourceDisplay(change.CustomerID, true), change.IdentityScope,
			change.Route.App, change.Route.Method, previewSourceDisplay(change.Route.Path, true),
			change.BeforeMigrationEvidence, change.AfterMigrationEvidence,
			change.BeforeLatestSuccessorEvidence, change.AfterLatestSuccessorEvidence, previewSourceDisplay(change.Reason, true))
	}
	_, _ = fmt.Fprintln(w)
}

func renderPreviewCustomerCutoverDiffCSV(w io.Writer, report previewCustomerCutoverDiffReport) error {
	writer := csv.NewWriter(w)
	if err := writer.Write([]string{
		"classification", "reason", "customer_id", "identity_scope", "customer_app", "old_route_app", "old_route_method", "old_route_path",
		"before_migration_evidence", "after_migration_evidence", "before_latest_successor_evidence", "after_latest_successor_evidence",
		"before_latest_successors", "after_latest_successors", "before_old_route_reuse_signal", "after_old_route_reuse_signal",
		"before_old_route_reuse_observed_at", "after_old_route_reuse_observed_at", "before_next_step", "after_next_step",
		"before_latest_event_at", "after_latest_event_at",
	}); err != nil {
		return err
	}
	for _, change := range report.Changes {
		row := []string{
			change.Classification, change.Reason, change.CustomerID, change.IdentityScope, change.CustomerApp,
			change.Route.App, change.Route.Method, change.Route.Path,
			change.BeforeMigrationEvidence, change.AfterMigrationEvidence,
			change.BeforeLatestSuccessorEvidence, change.AfterLatestSuccessorEvidence,
			cutoverLatestSuccessorsCSV(change.BeforeLatestSuccessors), cutoverLatestSuccessorsCSV(change.AfterLatestSuccessors),
			change.BeforeOldRouteReuseSignal, change.AfterOldRouteReuseSignal,
			change.BeforeOldRouteReuseObservedAt, change.AfterOldRouteReuseObservedAt,
			change.BeforeNextStep, change.AfterNextStep, change.BeforeLatestEventAt, change.AfterLatestEventAt,
		}
		for i := range row {
			row[i] = safePreviewCustomerCSVCell(row[i])
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func cutoverLatestSuccessorsCSV(successors []previewCustomerCutoverLatestSuccessor) string {
	customer := previewCustomerCutoverReviewCustomer{LatestSuccessors: successors}
	return previewCustomerCutoverActionLatestSuccessors(customer)
}
