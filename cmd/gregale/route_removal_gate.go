package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
)

// This is an opt-in CLI preflight, not an API authorization or a replacement
// for the existing server contract gate. Local evidence and owner attestations
// are trusted CI inputs; hashes bind their exact bytes to one proposed change.
type routeRemovalGateReport struct {
	Version              int                                `json:"version"`
	GeneratedAt          time.Time                          `json:"generated_at"`
	Mode                 string                             `json:"mode"`
	Status               string                             `json:"status"`
	App                  string                             `json:"app"`
	BaselineDeployment   string                             `json:"baseline_deployment"`
	CandidateDeployment  string                             `json:"candidate_deployment"`
	BaselineContractSHA  string                             `json:"baseline_contract_sha256,omitempty"`
	CandidateContractSHA string                             `json:"candidate_contract_sha256,omitempty"`
	ReadinessSHA         string                             `json:"readiness_sha256,omitempty"`
	MappingSHA           string                             `json:"mapping_sha256,omitempty"`
	ApprovalSHA          string                             `json:"approval_sha256,omitempty"`
	ApprovedBy           string                             `json:"approved_by,omitempty"`
	Blockers             []string                           `json:"blockers"`
	Routes               []routeRemovalGateRoute            `json:"removed_routes"`
	CurrentObservation   *previewCustomerMigrationAppReport `json:"current_production_observation,omitempty"`
}

type routeRemovalGateRoute struct {
	From           previewCustomerMigrationEndpoint `json:"from"`
	Status         string                           `json:"status"`
	Blockers       []string                         `json:"blockers"`
	Successors     []routeMigrationPairReview       `json:"successors"`
	CurrentTraffic *routeMigrationSuggestionTraffic `json:"current_production_traffic,omitempty"`
}

type routeRemovalOwnerApproval struct {
	Version              int       `json:"version"`
	ApprovedBy           string    `json:"approved_by"`
	ApprovedAt           time.Time `json:"approved_at"`
	App                  string    `json:"app"`
	BaselineDeployment   string    `json:"baseline_deployment"`
	CandidateDeployment  string    `json:"candidate_deployment"`
	BaselineContractSHA  string    `json:"baseline_contract_sha256"`
	CandidateContractSHA string    `json:"candidate_contract_sha256"`
	ReadinessSHA         string    `json:"readiness_sha256"`
	MappingSHA           string    `json:"mapping_sha256"`
}

type routeRemovalGateEvidence struct {
	readiness                             *previewCustomerCutoverReviewReport
	mappings                              map[previewCustomerMigrationRouteKey]previewCustomerMigrationMapping
	approval                              *routeRemovalOwnerApproval
	readinessSHA, mappingSHA, approvalSHA string
}

func readRouteRemovalFile(path string) ([]byte, string, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, "", errors.New("use a readable regular evidence file without symlinks")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteImpactReportMaxBytes+1))
	if err != nil || int64(len(body)) > api.RouteImpactReportMaxBytes {
		return nil, "", errors.New("evidence file is unreadable or exceeds 64 MiB")
	}
	return body, fmt.Sprintf("%x", openapidiff.SumSHA256(body)), nil
}

func readRouteRemovalGateEvidence(readinessPath, mappingPath, approvalPath string) (routeRemovalGateEvidence, error) {
	var evidence routeRemovalGateEvidence
	for _, input := range []struct{ path, kind string }{{readinessPath, "readiness"}, {mappingPath, "mapping"}, {approvalPath, "approval"}} {
		if input.path == "" {
			continue
		}
		body, digest, err := readRouteRemovalFile(input.path)
		if err != nil {
			return evidence, fmt.Errorf("%s: %w", input.kind, err)
		}
		switch input.kind {
		case "readiness":
			var report previewCustomerCutoverReviewReport
			if err := json.Unmarshal(body, &report); err != nil || report.Version != previewCustomerCutoverReviewVersion {
				return evidence, errors.New("readiness must be a version 1 migration readiness report")
			}
			evidence.readiness, evidence.readinessSHA = &report, digest
		case "mapping":
			evidence.mappings, err = parsePreviewCustomerMigrationMappings(body)
			if err != nil {
				return evidence, err
			}
			evidence.mappingSHA = digest
		case "approval":
			var approval routeRemovalOwnerApproval
			if err := json.Unmarshal(body, &approval); err != nil || approval.Version != 1 {
				return evidence, errors.New("owner approval must be version 1 JSON")
			}
			evidence.approval, evidence.approvalSHA = &approval, digest
		}
	}
	return evidence, nil
}

func routeRemovalFresh(at, now time.Time, maxAge time.Duration) bool {
	return !at.IsZero() && !at.After(now) && now.Sub(at) <= maxAge
}

func buildRouteRemovalGate(app, baselineID, candidateID, baselineSHA, candidateSHA, mode string, baseline, candidate *openapidiff.Spec, evidence routeRemovalGateEvidence, maxAge time.Duration, now time.Time) routeRemovalGateReport {
	report := routeRemovalGateReport{Version: 1, GeneratedAt: now.UTC(), Mode: mode, Status: "passed", App: app, BaselineDeployment: baselineID, CandidateDeployment: candidateID, BaselineContractSHA: baselineSHA, CandidateContractSHA: candidateSHA, ReadinessSHA: evidence.readinessSHA, MappingSHA: evidence.mappingSHA, ApprovalSHA: evidence.approvalSHA, Blockers: []string{}, Routes: []routeRemovalGateRoute{}}
	block := func(code string) { report.Blockers = appendUniqueCutoverCaveats(report.Blockers, code) }
	if baseline == nil || candidate == nil || baselineSHA == "" || candidateSHA == "" || baselineID == "" || candidateID == "" {
		block("captured_contracts_unavailable")
		report.Status = "blocked"
		return report
	}
	if mode != "report" && mode != "enforce" || maxAge <= 0 || maxAge > 72*time.Hour {
		block("invalid_gate_policy")
	}
	for path, item := range baseline.Paths {
		if item == nil {
			continue
		}
		for method, operation := range item.Methods {
			if operation == nil {
				continue
			}
			source := previewCustomerMigrationEndpoint{App: app, Method: strings.ToUpper(method), Path: path}
			if routeMigrationSuggestionOperation(candidate, source) != nil {
				continue
			}
			report.Routes = append(report.Routes, routeRemovalGateRoute{From: source, Status: "blocked", Blockers: []string{}, Successors: []routeMigrationPairReview{}})
		}
	}
	sort.Slice(report.Routes, func(i, j int) bool { return migrationEndpointLess(report.Routes[i].From, report.Routes[j].From) })
	if len(report.Routes) == 0 {
		if len(report.Blockers) > 0 {
			report.Status = "blocked"
		} else {
			report.Status = "not_required"
		}
		return report
	}
	if len(report.Routes) > routeMigrationSuggestionMaxOperations {
		block("removed_route_limit_exceeded")
		report.Routes = report.Routes[:routeMigrationSuggestionMaxOperations]
	}
	if evidence.mappings == nil {
		block("reviewed_mapping_missing")
	}
	ready := evidence.readiness
	var readyRoutes map[previewCustomerMigrationRouteKey]previewCustomerCutoverReviewRoute
	if ready == nil {
		block("readiness_missing")
	} else {
		var err error
		readyRoutes, err = previewCustomerCutoverRouteMap(*ready)
		if err != nil || len(ready.Routes) == 0 || !ready.OwnerReviewRequired || (ready.GroupBy != "consumer" && ready.GroupBy != "tenant") {
			block("readiness_invalid")
		}
		if !routeRemovalFresh(ready.GeneratedAt, now, maxAge) || !routeRemovalFresh(ready.ContractReviewGeneratedAt, now, maxAge) {
			block("readiness_stale_or_future")
		}
		// The readiness series must observe the serving revision with both old
		// and successor routes. A candidate-only zero-traffic series cannot
		// establish that customers stopped using the production old route.
		from, to := indexRouteMigrationReviewDeployments(ready.FromDeployments), indexRouteMigrationReviewDeployments(ready.ToDeployments)
		if from == nil || to == nil || from[app].Status != "available" || to[app].Status != "available" || from[app].DeploymentID != baselineID || to[app].DeploymentID != baselineID || from[app].ContractSHA != baselineSHA || to[app].ContractSHA != baselineSHA {
			block("readiness_serving_contract_mismatch")
		}
		if !validRouteRemovalWindows(*ready, now, maxAge) {
			block("readiness_windows_incomplete_or_stale")
		}
	}
	approval := evidence.approval
	if approval == nil {
		block("owner_approval_missing")
	} else {
		if approval.App != app || approval.BaselineDeployment != baselineID || approval.CandidateDeployment != candidateID || approval.BaselineContractSHA != baselineSHA || approval.CandidateContractSHA != candidateSHA || approval.ReadinessSHA != evidence.readinessSHA || approval.MappingSHA != evidence.mappingSHA || approval.ReadinessSHA == "" || approval.MappingSHA == "" {
			block("owner_approval_binding_mismatch")
		}
		if approval.Version != 1 || strings.TrimSpace(approval.ApprovedBy) == "" || len(approval.ApprovedBy) > 128 || strings.IndexFunc(approval.ApprovedBy, func(r rune) bool { return r < 32 || r == 127 }) >= 0 || !routeRemovalFresh(approval.ApprovedAt, now, maxAge) || ready != nil && approval.ApprovedAt.Before(ready.GeneratedAt) {
			block("owner_approval_invalid_or_stale")
		}
		report.ApprovedBy = approval.ApprovedBy
	}
	pairs := 0
	for i := range report.Routes {
		row := &report.Routes[i]
		mapping, ok := evidence.mappings[previewCustomerMigrationEndpointKey(row.From)]
		if !ok || len(mapping.Successors) == 0 {
			row.Blockers = append(row.Blockers, "source_successor_mapping_missing")
		} else {
			pairs += len(mapping.Successors)
			if pairs > routeMigrationSuggestionMaxPairs {
				row.Blockers = append(row.Blockers, "successor_comparison_limit_exceeded")
			} else {
				for _, successor := range mapping.Successors {
					pair := routeMigrationPairReview{To: successor, Status: "unknown", Findings: []openapidiff.RoutePairFinding{}}
					if successor.App != app {
						pair.Findings = append(pair.Findings, openapidiff.RoutePairFinding{Severity: "unknown", Code: "cross_app_successor_not_verified"})
					} else {
						comparison, err := openapidiff.CompareRoutePair(baseline, row.From.Method, row.From.Path, candidate, successor.Method, successor.Path)
						if err == nil {
							pair.Status, pair.Findings = comparison.Status, comparison.Findings
						}
					}
					if pair.Status != "no_supported_breaks" {
						row.Blockers = appendUniqueCutoverCaveats(row.Blockers, "candidate_successor_contract_"+pair.Status)
					}
					row.Successors = append(row.Successors, pair)
				}
			}
		}
		observed, exists := readyRoutes[previewCustomerMigrationEndpointKey(row.From)]
		if !exists {
			row.Blockers = append(row.Blockers, "source_readiness_missing")
		} else {
			if !validRouteRemovalReadinessRoute(observed, ready, now, maxAge) {
				row.Blockers = append(row.Blockers, "source_cutover_not_ready")
			}
			contract := routeMigrationRouteReview{From: row.From, Successors: []routeMigrationPairReview{}}
			for _, successor := range observed.Successors {
				contract.Successors = append(contract.Successors, routeMigrationPairReview{To: successor.To})
			}
			if !ok || comparePreviewCustomerCutoverSuccessors(row.From, mapping.Successors, contract) != nil {
				row.Blockers = append(row.Blockers, "readiness_mapping_mismatch")
			}
		}
		if len(row.Blockers) == 0 && len(report.Blockers) == 0 {
			row.Status = "approved"
		} else {
			report.Status = "blocked"
		}
	}
	if len(report.Blockers) > 0 {
		report.Status = "blocked"
	}
	return report
}

func validRouteRemovalWindows(report previewCustomerCutoverReviewReport, now time.Time, maxAge time.Duration) bool {
	grace, err := parsePreviewCustomerMigrationProgressDuration(report.GracePeriod)
	staleness, ageErr := parsePreviewCustomerMigrationProgressDuration(report.MaxStaleness)
	if err != nil || ageErr != nil || grace <= 0 || staleness <= 0 || report.MinWindows < 2 || report.MinWindows > len(report.Snapshots) || len(report.Snapshots) > previewCustomerMigrationProgressMaxSnapshots {
		return false
	}
	if staleness < maxAge {
		maxAge = staleness
	}
	windows := append([]previewCustomerMigrationProgressSnapshot{}, report.Snapshots...)
	sort.Slice(windows, func(i, j int) bool { return windows[i].GeneratedAt.Before(windows[j].GeneratedAt) })
	var start, end time.Time
	seen := map[string]bool{}
	for i, window := range windows {
		from, e1 := time.Parse(time.RFC3339Nano, window.From)
		until, e2 := time.Parse(time.RFC3339Nano, window.Until)
		key := from.Format(time.RFC3339Nano) + "/" + until.Format(time.RFC3339Nano)
		if window.Status != "complete" || e1 != nil || e2 != nil || !until.After(from) || until.After(window.GeneratedAt) || window.GeneratedAt.After(report.GeneratedAt) || seen[key] || i > 0 && !window.GeneratedAt.After(windows[i-1].GeneratedAt) {
			return false
		}
		seen[key] = true
		if i == 0 {
			start = from
		} else if from.After(end) || until.Before(end) {
			return false
		}
		end = until
	}
	return len(windows) >= report.MinWindows && end.Sub(start) >= grace && routeRemovalFresh(end, now, maxAge)
}

func validRouteRemovalReadinessRoute(route previewCustomerCutoverReviewRoute, report *previewCustomerCutoverReviewReport, now time.Time, maxAge time.Duration) bool {
	if report == nil || route.Status != "owner_review_ready" || route.ContractStatus != "no_supported_breaks" || route.TelemetryStatus != "owner_review_ready" || len(route.Blockers) > 0 || route.OldRouteObserved != 0 || route.IncompleteCustomers != 0 || route.OldRouteTrafficWindows < report.MinWindows || route.OldRouteTrafficWindows > len(report.Snapshots) {
		return false
	}
	if route.CohortCustomers <= 0 || route.CohortCustomers != len(route.Customers) || route.SuccessorObserved < 0 || route.NoCurrentEvidence < 0 || route.SuccessorObserved+route.NoCurrentEvidence != route.CohortCustomers {
		return false
	}
	start, e1 := time.Parse(time.RFC3339Nano, route.GracePeriodStart)
	end, e2 := time.Parse(time.RFC3339Nano, route.GracePeriodEnd)
	grace, e3 := parsePreviewCustomerMigrationProgressDuration(report.GracePeriod)
	if e1 != nil || e2 != nil || e3 != nil || end.Sub(start) < grace || !routeRemovalFresh(end, now, maxAge) {
		return false
	}
	// Claimed route coverage must be a suffix of the actual saved windows,
	// rather than an independently editable grace-period timestamp pair.
	windows := append([]previewCustomerMigrationProgressSnapshot{}, report.Snapshots...)
	sort.Slice(windows, func(i, j int) bool { return windows[i].GeneratedAt.Before(windows[j].GeneratedAt) })
	startIndex := -1
	for i, window := range windows {
		from, err := time.Parse(time.RFC3339Nano, window.From)
		if err == nil && from.Equal(start) {
			startIndex = i
			break
		}
	}
	if startIndex < 0 || len(windows)-startIndex != route.OldRouteTrafficWindows {
		return false
	}
	lastEnd, err := time.Parse(time.RFC3339Nano, windows[len(windows)-1].Until)
	if err != nil || !lastEnd.Equal(end) {
		return false
	}
	for _, successor := range route.Successors {
		if successor.Status != "no_supported_breaks" || len(successor.Findings) > 0 {
			return false
		}
	}
	seenCustomers := map[string]bool{}
	observedCustomers, silentCustomers := 0, 0
	for _, customer := range route.Customers {
		if !canonicalRouteHealthID(customer.ID) || seenCustomers[customer.ID] {
			return false
		}
		seenCustomers[customer.ID] = true
		if report.GroupBy == "consumer" && (customer.IdentityScope != "app" || customer.App != route.From.App) || report.GroupBy == "tenant" && (customer.IdentityScope != "account" || customer.App != "") {
			return false
		}
		if customer.MigrationEvidence != "successor_observed" && customer.MigrationEvidence != "no_current_evidence" || customer.ObservedOldRouteReuse != nil {
			return false
		}
		if customer.MigrationEvidence == "successor_observed" {
			observedCustomers++
		} else {
			silentCustomers++
		}
		for _, observation := range customer.OldRouteObservations {
			until, err := time.Parse(time.RFC3339Nano, observation.WindowUntil)
			if err != nil || !until.Before(start) && !until.Equal(start) {
				return false
			}
		}
	}
	return observedCustomers == route.SuccessorObserved && silentCustomers == route.NoCurrentEvidence
}

func renderRouteRemovalGate(w io.Writer, report routeRemovalGateReport) {
	_, _ = fmt.Fprintf(w, "Route removal gate (%s): %s; removed routes: %d\n", report.Mode, report.Status, len(report.Routes))
	for _, blocker := range report.Blockers {
		_, _ = fmt.Fprintf(w, "  blocker: %s\n", previewReportText(blocker))
	}
	for _, row := range report.Routes {
		_, _ = fmt.Fprintf(w, "  %s %s %s: %s\n", row.From.App, row.From.Method, previewReportText(row.From.Path), row.Status)
		if row.CurrentTraffic != nil {
			_, _ = fmt.Fprintf(w, "    current production traffic: %s (%d observed requests)\n", row.CurrentTraffic.Status, row.CurrentTraffic.Requests)
		}
		for _, blocker := range row.Blockers {
			_, _ = fmt.Fprintf(w, "    blocker: %s\n", previewReportText(blocker))
		}
		for _, pair := range row.Successors {
			for _, finding := range pair.Findings {
				_, _ = fmt.Fprintf(w, "    %s %s: %s at %s\n", pair.To.Method, previewReportText(pair.To.Path), finding.Code, previewReportText(finding.Location))
			}
		}
	}
	_, _ = fmt.Fprintln(w, "CLI preflight only. Owner attestations are local evidence; API, direct deploy and traffic set paths retain their existing gates.")
}

// Recheck the serving deployment after the historical grace-period review.
// Missing/truncated usage is a blocker, rather than an inferred zero count.
func refreshRouteRemovalTraffic(ctx context.Context, client *api.Client, report *routeRemovalGateReport, maxAge time.Duration, now time.Time) {
	if len(report.Routes) == 0 {
		return
	}
	evidence := readPreviewCustomerMigrationApp(ctx, client, report.App, report.BaselineDeployment, maxAge.String(), now)
	report.CurrentObservation = &evidence.report
	asOf, err := time.Parse(time.RFC3339Nano, evidence.report.AsOf)
	from, fromErr := time.Parse(time.RFC3339Nano, evidence.report.From)
	until, untilErr := time.Parse(time.RFC3339Nano, evidence.report.Until)
	// Internal consistency alone is insufficient: an API response containing
	// an older/narrower window must not stand in for the requested fresh read.
	available := evidence.report.Status == "available" && err == nil && fromErr == nil && untilErr == nil &&
		!asOf.After(now.Add(time.Minute)) && !until.Before(now.Add(-time.Minute)) && !until.After(now.Add(time.Minute)) &&
		!from.After(now.Add(-maxAge).Add(time.Minute)) && routeRemovalFresh(asOf, now.Add(time.Minute), maxAge+time.Minute)
	for i := range report.Routes {
		row := &report.Routes[i]
		traffic := routeMigrationTraffic(row.From, map[string]previewCustomerMigrationAppEvidence{report.App: evidence})
		row.CurrentTraffic = &traffic
		if !available || traffic.Status == "unknown" {
			row.Blockers = appendUniqueCutoverCaveats(row.Blockers, "current_production_traffic_incomplete")
		} else if traffic.Requests > 0 {
			row.Blockers = appendUniqueCutoverCaveats(row.Blockers, "current_production_old_route_active")
		}
		if len(row.Blockers) > 0 {
			row.Status = "blocked"
			report.Status = "blocked"
		}
	}
}

func cmdRoutesMigrationGate(args []string, approve bool) int {
	name := "gate"
	if approve {
		name = "approve"
	}
	fs := newFlagSet("routes migration "+name, flag.ContinueOnError)
	app := fs.String("app", "", "production app slug")
	candidateApp := fs.String("candidate-app", "", "candidate app slug (defaults to --app; may be a preview)")
	baselineID := fs.String("baseline-deployment", "", "serving baseline deployment UUID")
	candidateID := fs.String("candidate-deployment", "", "candidate deployment UUID")
	readiness := fs.String("readiness", "", "migration readiness JSON for the serving deployment")
	mapping := fs.String("mapping", "", "reviewed successor mapping JSON")
	approval := fs.String("owner-approval", "", "owner attestation JSON")
	mode := fs.String("mode", "report", "report or enforce (enforce returns 1 for blockers)")
	maxAge := fs.Duration("max-evidence-age", 72*time.Hour, "maximum evidence age (at most 72h)")
	output := fs.String("out", "", "save JSON to a new file")
	approvedBy := fs.String("approved-by", "", "owner identity recorded in the local attestation")
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 0 || !validCLISlug(*app) || !canonicalRouteHealthID(*baselineID) || !canonicalRouteHealthID(*candidateID) || *baselineID == *candidateID || *mode != "report" && *mode != "enforce" || *maxAge <= 0 || *maxAge > 72*time.Hour || *candidateApp != "" && !validCLISlug(*candidateApp) || approve && (strings.TrimSpace(*approvedBy) == "" || *output == "" || *readiness == "" || *mapping == "") || !approve && *approvedBy != "" || approve && *approval != "" || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale routes migration "+name+" --app APP --baseline-deployment UUID --candidate-deployment UUID [--candidate-app APP] [--readiness PATH --mapping PATH] [--owner-approval PATH] [--mode report|enforce] [--approved-by OWNER --out PATH]", "cli")
		return 1
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return printErr("Invalid --out", errors.New("choose a new file; existing files and symlinks are not replaced"))
		}
	}
	evidence, err := readRouteRemovalGateEvidence(*readiness, *mapping, *approval)
	if err != nil {
		return printErr("Invalid removal evidence", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if *candidateApp == "" {
		*candidateApp = *app
	}
	base, err := readRouteMigrationDeploymentSet(ctx, client, map[string]string{*app: *baselineID})
	if err != nil {
		return printErr("Could not read baseline", err)
	}
	prop, err := readRouteMigrationDeploymentSet(ctx, client, map[string]string{*candidateApp: *candidateID})
	if err != nil {
		return printErr("Could not read candidate", err)
	}
	if serving := base[*app].deployment; serving.Status != statusLive || serving.TrafficPercent != 100 {
		return printErr("Invalid serving baseline", errors.New("the baseline must be live at 100% production traffic"))
	}
	now := time.Now().UTC()
	report := buildRouteRemovalGate(*app, *baselineID, *candidateID, base[*app].evidence.ContractSHA, prop[*candidateApp].evidence.ContractSHA, *mode, base[*app].spec, prop[*candidateApp].spec, evidence, *maxAge, now)
	refreshRouteRemovalTraffic(ctx, client, &report, *maxAge, now)
	var result any = report
	if approve {
		if len(report.Routes) == 0 || len(report.Blockers) != 1 || report.Blockers[0] != "owner_approval_missing" {
			renderRouteRemovalGate(osStderr, report)
			return printErr("Cannot approve removal", errors.New("resolve all contract and cutover blockers first"))
		}
		for _, row := range report.Routes {
			if len(row.Blockers) > 0 {
				renderRouteRemovalGate(osStderr, report)
				return printErr("Cannot approve removal", errors.New("resolve all route blockers first"))
			}
		}
		result = routeRemovalOwnerApproval{Version: 1, ApprovedBy: strings.TrimSpace(*approvedBy), ApprovedAt: now, App: report.App, BaselineDeployment: report.BaselineDeployment, CandidateDeployment: report.CandidateDeployment, BaselineContractSHA: report.BaselineContractSHA, CandidateContractSHA: report.CandidateContractSHA, ReadinessSHA: report.ReadinessSHA, MappingSHA: report.MappingSHA}
		if len(strings.TrimSpace(*approvedBy)) > 128 || strings.IndexFunc(*approvedBy, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return printErr("Invalid owner identity", errors.New("use a nonempty owner identity of at most 128 characters without controls"))
		}
	}
	if *output != "" {
		body, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return printErr("Could not encode removal report", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return printErr("Could not save removal report", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else if approve {
		PrintOK(osStdout, "Recorded owner attestation in %s for %s → %s.", *output, *baselineID, *candidateID)
	} else {
		var rendered bytes.Buffer
		renderRouteRemovalGate(&rendered, report)
		if _, err := osStdout.Write(rendered.Bytes()); err != nil {
			return printErr("Could not write removal report", err)
		}
	}
	if !approve && *mode == "enforce" && report.Status == "blocked" {
		return 1
	}
	return 0
}
