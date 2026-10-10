package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/openapidiff"
)

func TestPreviewCustomerCutoverReviewCSVProvidesPrioritizedActionQueue(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	report := previewCustomerCutoverReviewReport{Routes: []previewCustomerCutoverReviewRoute{{
		From: migrationEndpoint("api", "GET", `/old,"legacy"`),
		Customers: []previewCustomerCutoverReviewCustomer{
			{ID: "verify", IdentityScope: "app", App: "client", MigrationEvidence: "successor_observed", NextStep: "verify_successor_usage_with_customer_or_owner", LatestSuccessorEvidence: "determined", LatestSuccessors: []previewCustomerCutoverLatestSuccessor{{
				Route: migrationEndpoint("api", "GET", "/v2,blue"), ContractStatus: "no_supported_breaks", LastObservedAt: base.Add(-time.Hour), Requests: 12,
			}}},
			{ID: "=2+2", IdentityScope: "app", App: "client", MigrationEvidence: "successor_observed", NextStep: "review_customer_old_route_reuse_with_owner", LatestSuccessorEvidence: "determined", LatestSuccessors: []previewCustomerCutoverLatestSuccessor{{
				Route: migrationEndpoint("api", "GET", "/v2"), ContractStatus: "breaking", LastObservedAt: base.Add(-2 * time.Hour), Requests: 3,
			}}},
			{ID: "ambiguous", IdentityScope: "app", App: "client", MigrationEvidence: "no_current_evidence", NextStep: "verify_usage_with_customer_or_owner", LatestSuccessorEvidence: "ambiguous"},
			{ID: "old-active", IdentityScope: "app", App: "client", MigrationEvidence: "old_route_active", NextStep: "migrate_customer_from_old_route"},
			{ID: "reuse", IdentityScope: "app", App: "client", MigrationEvidence: "successor_observed", NextStep: "review_customer_old_route_reuse_with_owner", LatestSuccessorEvidence: "determined", LatestSuccessors: []previewCustomerCutoverLatestSuccessor{{
				Route: migrationEndpoint("api", "GET", "/v2"), ContractStatus: "no_supported_breaks", LastObservedAt: base.Add(-3 * time.Hour), Requests: 5,
			}}, ObservedOldRouteReuse: &previewCustomerCutoverOldRouteReuse{Signal: "old_route_reobserved_after_successor", OldRouteLastObservedAt: base.Add(-time.Hour)}},
		},
	}}}

	var output bytes.Buffer
	if err := renderPreviewCustomerCutoverReviewCSV(&output, report); err != nil {
		t.Fatal(err)
	}
	var secondOutput bytes.Buffer
	if err := renderPreviewCustomerCutoverReviewCSV(&secondOutput, report); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), secondOutput.Bytes()) {
		t.Fatal("CSV action queue should render deterministically")
	}
	records, err := csv.NewReader(bytes.NewReader(output.Bytes())).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV action queue: %v\n%s", err, output.String())
	}
	if len(records) != 6 {
		t.Fatalf("got %d CSV rows, want header and five actions: %s", len(records), output.String())
	}
	columns := make(map[string]int, len(records[0]))
	for i, name := range records[0] {
		columns[name] = i
	}
	want := []struct{ priority, customer string }{
		{"P0", "'=2+2"}, {"P1", "reuse"}, {"P2", "old-active"}, {"P3", "ambiguous"}, {"P4", "verify"},
	}
	for i, expected := range want {
		row := records[i+1]
		if row[columns["priority"]] != expected.priority || row[columns["customer_id"]] != expected.customer {
			t.Errorf("action row %d = priority %q customer %q, want %q / %q", i+1,
				row[columns["priority"]], row[columns["customer_id"]], expected.priority, expected.customer)
		}
	}
	if got := records[1][columns["old_route_path"]]; got != `/old,"legacy"` {
		t.Errorf("CSV should preserve quoted route paths, got %q", got)
	}
	if got := records[5][columns["latest_successors"]]; !strings.Contains(got, "/v2,blue") {
		t.Errorf("CSV should preserve commas in latest successor details, got %q", got)
	}
}

const cutoverBaselineDeployment = "11111111-1111-4111-8111-111111111111"
const cutoverSuccessorDeployment = "22222222-2222-4222-8222-222222222222"

func cutoverContractFixture(status string) routeMigrationReviewReport {
	from := migrationEndpoint("api", "GET", "/old")
	to := migrationEndpoint("api", "GET", "/new")
	return routeMigrationReviewReport{
		Version: routeMigrationReviewVersion, GeneratedAt: time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC),
		Outcome: status, FromDeployments: []routeMigrationDeploymentEvidence{{App: "api", DeploymentID: cutoverBaselineDeployment, Status: "available", ContractSHA: strings.Repeat("a", 64)}},
		ToDeployments: []routeMigrationDeploymentEvidence{{App: "api", DeploymentID: cutoverSuccessorDeployment, Status: "available", ContractSHA: strings.Repeat("b", 64)}},
		Mappings:      []routeMigrationRouteReview{{From: from, Status: status, Successors: []routeMigrationPairReview{{To: to, Status: status, Findings: []openapidiff.RoutePairFinding{}}}}},
	}
}

func cutoverProgressFixtures(t *testing.T, now time.Time) []previewCustomerMigrationProgressSnapshotData {
	t.Helper()
	first := migrationProgressFixture(now, 40*24*time.Hour, 20*24*time.Hour, 0, "not_observed", "observed")
	second := migrationProgressFixture(now, 20*24*time.Hour, time.Hour, 0, "not_observed", "observed")
	for _, snapshot := range []*previewCustomerMigrationProgressSnapshotData{&first, &second} {
		snapshot.report.Deployments[0].DeploymentID = cutoverSuccessorDeployment
	}
	return indexMigrationProgressFixtures(t, first, second)
}

func buildCutoverFixture(t *testing.T, status string, now time.Time) previewCustomerCutoverReviewReport {
	t.Helper()
	snapshots := cutoverProgressFixtures(t, now)
	progress, err := buildPreviewCustomerMigrationProgressReport(snapshots, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", now)
	if err != nil {
		t.Fatalf("build progress fixture: %v", err)
	}
	report, err := buildPreviewCustomerCutoverReview(cutoverContractFixture(status), progress, snapshots, now)
	if err != nil {
		t.Fatalf("build cutover fixture: %v", err)
	}
	return report
}

func TestPreviewCustomerCutoverReviewFlagsObservedOldRouteReuseAfterSuccessor(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	snapshots := cutoverProgressFixtures(t, now)
	latest := &snapshots[1].report.Customers[0].Routes[0]
	oldDeployment := snapshots[1].deployments["api"]
	windowEnd, err := time.Parse(time.RFC3339Nano, oldDeployment.Until)
	if err != nil {
		t.Fatal(err)
	}
	oldRouteAt := windowEnd.Add(-30 * time.Second)
	latest.OldRouteEvidence = "observed"
	latest.OldRouteRequests = 4
	latest.OldRouteLastObservedAt = oldRouteAt.Format(time.RFC3339Nano)
	latest.OldRouteTotalEvidence = "observed"
	latest.OldRouteTotalRequests = 4
	latest.Status = "both"
	snapshots[1], err = indexPreviewCustomerMigrationProgressSnapshot(snapshots[1].report)
	if err != nil {
		t.Fatal(err)
	}

	progress, err := buildPreviewCustomerMigrationProgressReport(snapshots, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", now)
	if err != nil {
		t.Fatal(err)
	}
	report, err := buildPreviewCustomerCutoverReview(cutoverContractFixture("no_supported_breaks"), progress, snapshots, now)
	if err != nil {
		t.Fatal(err)
	}
	customer := report.Routes[0].Customers[0]
	signal := customer.ObservedOldRouteReuse
	if report.Summary.ObservedOldRouteReuseLinks != 1 || signal == nil ||
		signal.Signal != "old_route_reobserved_after_successor" || signal.Successor.Path != "/new" ||
		signal.SuccessorContractStatus != "no_supported_breaks" || !signal.OldRouteLastObservedAt.Equal(oldRouteAt) ||
		!signal.SuccessorLastObservedAt.Before(signal.OldRouteLastObservedAt) || customer.NextStep != "review_customer_old_route_reuse_with_owner" {
		t.Fatalf("expected an evidence-backed customer old-route reuse signal: report=%+v customer=%+v", report.Summary, customer)
	}
	var markdown bytes.Buffer
	renderPreviewCustomerCutoverReviewMarkdown(&markdown, report)
	if !strings.Contains(markdown.String(), "observed old-route reuse signals: **1**") ||
		!strings.Contains(markdown.String(), "old_route_reobserved_after_successor") ||
		!strings.Contains(markdown.String(), oldRouteAt.Format(time.RFC3339Nano)) {
		t.Fatalf("Markdown should show the regression signal and its event time: %s", markdown.String())
	}
}

func TestPreviewCustomerCutoverReviewDoesNotInferReuseFromOverlappingOrUntimedEvidence(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name            string
		successorOffset time.Duration
		omitSuccessorAt bool
	}{
		{name: "overlapping window successor was observed later", successorOffset: -time.Minute},
		{name: "successor timestamp is outside its window", successorOffset: time.Minute},
		{name: "missing successor timestamp", omitSuccessorAt: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshots := cutoverProgressFixtures(t, now)
			var err error
			first := &snapshots[0].report.Customers[0].Routes[0]
			first.SuccessorEvidence = "not_observed"
			first.ObservedSuccessors = []previewCustomerMigrationSuccessor{}
			first.Status = "no_current_evidence"
			snapshots[0], err = indexPreviewCustomerMigrationProgressSnapshot(snapshots[0].report)
			if err != nil {
				t.Fatal(err)
			}

			latest := &snapshots[1].report.Customers[0].Routes[0]
			oldDeployment := snapshots[1].deployments["api"]
			windowEnd, err := time.Parse(time.RFC3339Nano, oldDeployment.Until)
			if err != nil {
				t.Fatal(err)
			}
			oldRouteAt := windowEnd.Add(-2 * time.Minute)
			latest.OldRouteEvidence = "observed"
			latest.OldRouteRequests = 2
			latest.OldRouteLastObservedAt = oldRouteAt.Format(time.RFC3339Nano)
			latest.OldRouteTotalEvidence = "observed"
			latest.OldRouteTotalRequests = 2
			latest.Status = "both"
			if test.omitSuccessorAt {
				latest.ObservedSuccessors[0].LastObservedAt = ""
			} else {
				latest.ObservedSuccessors[0].LastObservedAt = windowEnd.Add(test.successorOffset).Format(time.RFC3339Nano)
			}
			snapshots[1], err = indexPreviewCustomerMigrationProgressSnapshot(snapshots[1].report)
			if err != nil {
				t.Fatal(err)
			}

			progress, err := buildPreviewCustomerMigrationProgressReport(snapshots, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", now)
			if err != nil {
				t.Fatal(err)
			}
			report, err := buildPreviewCustomerCutoverReview(cutoverContractFixture("no_supported_breaks"), progress, snapshots, now)
			if err != nil {
				t.Fatal(err)
			}
			if report.Summary.ObservedOldRouteReuseLinks != 0 || report.Routes[0].Customers[0].ObservedOldRouteReuse != nil {
				t.Fatalf("ambiguous event ordering must not produce a regression signal: %+v", report.Routes[0].Customers[0])
			}
		})
	}
}

func TestPreviewCustomerCutoverCustomerNextStepUsesLatestSuccessorEvidence(t *testing.T) {
	base := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	windowFrom := base.Add(-time.Hour).Format(time.RFC3339Nano)
	windowUntil := base.Add(6 * time.Hour).Format(time.RFC3339Nano)
	earlierWindowUntil := base.Add(3 * time.Hour).Format(time.RFC3339Nano)
	observation := func(at time.Time, generatedAt time.Time, requests int64, from, until string) previewCustomerCutoverSuccessorObservation {
		return previewCustomerCutoverSuccessorObservation{
			SnapshotGeneratedAt: generatedAt, WindowFrom: from, WindowUntil: until,
			Requests: requests, LastObservedAt: at.Format(time.RFC3339Nano),
		}
	}
	oldEndpoint := migrationEndpoint("api", "GET", "/new")
	newEndpoint := migrationEndpoint("api", "GET", "/new-v2")
	oldUseAt := base.Add(time.Hour)
	newUseAt := base.Add(4 * time.Hour)

	for _, test := range []struct {
		name                 string
		latestStatus         string
		latestTimestamp      time.Time
		latestWindowUntil    string
		omitLatestTimestamp  bool
		includeBreakingAtTie bool
		wantEvidence         string
		wantCurrentEndpoints int
		wantNextStep         string
	}{
		{
			name:         "later compatible endpoint clears historical breaking endpoint",
			latestStatus: "no_supported_breaks", latestTimestamp: newUseAt, latestWindowUntil: windowUntil,
			wantEvidence: "determined", wantCurrentEndpoints: 1, wantNextStep: "verify_successor_usage_with_customer_or_owner",
		},
		{
			name:         "latest breaking endpoint needs immediate migration",
			latestStatus: "breaking", latestTimestamp: newUseAt, latestWindowUntil: windowUntil,
			wantEvidence: "determined", wantCurrentEndpoints: 1, wantNextStep: "move_customer_from_breaking_successor",
		},
		{
			name:         "breaking endpoint tied at latest timestamp remains a blocker",
			latestStatus: "no_supported_breaks", latestTimestamp: oldUseAt, latestWindowUntil: windowUntil,
			includeBreakingAtTie: true, wantEvidence: "determined", wantCurrentEndpoints: 2,
			wantNextStep: "move_customer_from_breaking_successor",
		},
		{
			name:         "untimed overlapping observation requires verification",
			latestStatus: "no_supported_breaks", latestWindowUntil: windowUntil, omitLatestTimestamp: true,
			wantEvidence: "ambiguous", wantCurrentEndpoints: 0, wantNextStep: "verify_successor_usage_with_customer_or_owner",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldRows := []previewCustomerCutoverObservedSuccessor{{
				Route: oldEndpoint, ContractStatus: "breaking",
				Observations: []previewCustomerCutoverSuccessorObservation{
					observation(oldUseAt, base.Add(time.Minute), 7, windowFrom, earlierWindowUntil),
				},
			}}
			latestAt := test.latestTimestamp
			latestObservation := observation(latestAt, base.Add(5*time.Minute), 11, windowFrom, test.latestWindowUntil)
			if test.omitLatestTimestamp {
				latestObservation.LastObservedAt = ""
			}
			latestRows := []previewCustomerCutoverObservedSuccessor{{
				Route: newEndpoint, ContractStatus: test.latestStatus,
				Observations: []previewCustomerCutoverSuccessorObservation{latestObservation},
			}}
			observed := append(oldRows, latestRows...)
			if test.includeBreakingAtTie {
				observed[0].Observations = append(observed[0].Observations,
					observation(test.latestTimestamp, base.Add(6*time.Minute), 19, windowFrom, windowUntil))
			}
			latest, evidence := previewCustomerCutoverLatestObservedSuccessors(observed)
			if evidence != test.wantEvidence || len(latest) != test.wantCurrentEndpoints {
				t.Fatalf("latest endpoints = %+v, evidence %q; want %d endpoints with %q", latest, evidence, test.wantCurrentEndpoints, test.wantEvidence)
			}
			if len(latest) == 1 && test.name == "later compatible endpoint clears historical breaking endpoint" &&
				(latest[0].Route != newEndpoint || latest[0].Requests != 11 || latest[0].ContractStatus != "no_supported_breaks") {
				t.Fatalf("current successor must retain its own latest observation without summing windows: %+v", latest[0])
			}
			got := previewCustomerCutoverCustomerNextStep("successor_options", "successor_observed", observed, latest, evidence)
			if got != test.wantNextStep {
				t.Fatalf("next step = %q, want %q", got, test.wantNextStep)
			}
		})
	}
}

func TestPreviewCustomerCutoverReviewRequiresCompatibleContractAndQuietGrace(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	report := buildCutoverFixture(t, "no_supported_breaks", now)
	if report.Outcome != "owner_review_ready" || report.Summary.OwnerReviewReadyRoutes != 1 {
		t.Fatalf("compatible contract and sustained aggregate silence should allow owner review: %+v", report)
	}
	route := report.Routes[0]
	if route.Status != "owner_review_ready" || route.ContractStatus != "no_supported_breaks" || route.TelemetryStatus != "owner_review_ready" {
		t.Fatalf("unexpected route decision: %+v", route)
	}
	if len(route.Customers) != 1 || route.Customers[0].MigrationEvidence != "successor_observed" || route.Customers[0].NextStep != "verify_successor_usage_with_customer_or_owner" {
		t.Fatalf("customer adoption evidence should remain advisory: %+v", route.Customers)
	}
	observed := route.Customers[0].ObservedSuccessors
	if len(observed) != 1 || observed[0].Route.Path != "/new" || observed[0].ContractStatus != "no_supported_breaks" || len(observed[0].Observations) != 2 ||
		observed[0].Observations[0].Requests != 3 || observed[0].Observations[0].LastObservedAt == "" || report.Summary.CustomersOnCompatibleSuccessors != 1 {
		t.Fatalf("customer successor route, contract, and per-snapshot evidence should be joined: %+v", route.Customers[0])
	}
	if route.Customers[0].LatestSuccessorEvidence != "determined" || len(route.Customers[0].LatestSuccessors) != 1 ||
		route.Customers[0].LatestSuccessors[0].Route.Path != "/new" || report.Summary.LatestCompatibleSuccessorLinks != 1 ||
		report.Summary.AmbiguousLatestSuccessorLinks != 0 {
		t.Fatalf("latest successor status should be based on the newest timestamped event: %+v", route.Customers[0])
	}
	var markdown bytes.Buffer
	renderPreviewCustomerCutoverReviewMarkdown(&markdown, report)
	if !strings.Contains(markdown.String(), "SHA-256 `"+strings.Repeat("b", 64)+"`") ||
		!strings.Contains(markdown.String(), "Successor `api GET /new`: **no_supported_breaks**") ||
		!strings.Contains(markdown.String(), "Customer `"+migrationConsumerC+"` observed successor `api GET /new` (**no_supported_breaks**)") ||
		!strings.Contains(markdown.String(), "Latest successor evidence") || !strings.Contains(markdown.String(), "Latest observed successor status") ||
		!strings.Contains(markdown.String(), "**3** requests") {
		t.Fatalf("Markdown should identify reviewed contract bytes and successor results: %s", markdown.String())
	}
}

func TestPreviewCustomerCutoverReviewContractBreakAndUnknownBlockReadiness(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		contractStatus string
		wantStatus     string
		wantOutcome    string
	}{
		{contractStatus: "breaking", wantStatus: "breaking_contract", wantOutcome: "breaking_changes"},
		{contractStatus: "unknown", wantStatus: "contract_unknown", wantOutcome: "incomplete"},
		{contractStatus: "review_required", wantStatus: "contract_review_required", wantOutcome: "review_required"},
	} {
		t.Run(test.contractStatus, func(t *testing.T) {
			report := buildCutoverFixture(t, test.contractStatus, now)
			if report.Outcome != test.wantOutcome || report.Routes[0].Status != test.wantStatus || report.Summary.OwnerReviewReadyRoutes != 0 {
				t.Fatalf("contract %s must block owner-review readiness: %+v", test.contractStatus, report)
			}
		})
	}
}

func TestPreviewCustomerCutoverReviewCountsBreakingSuccessorAmongOptions(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	snapshots := cutoverProgressFixtures(t, now)
	for i := range snapshots {
		route := snapshots[i].report.Customers[0].Routes[0]
		route.Successors = append(route.Successors, migrationEndpoint("api", "GET", "/new-alternative"))
		snapshots[i].report.Customers[0].Routes[0] = route
		indexed, err := indexPreviewCustomerMigrationProgressSnapshot(snapshots[i].report)
		if err != nil {
			t.Fatal(err)
		}
		snapshots[i] = indexed
	}
	progress, err := buildPreviewCustomerMigrationProgressReport(snapshots, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", now)
	if err != nil {
		t.Fatal(err)
	}
	contracts := cutoverContractFixture("successor_options")
	contracts.Mappings[0].Successors = append(contracts.Mappings[0].Successors, routeMigrationPairReview{
		To: migrationEndpoint("api", "GET", "/new-alternative"), Status: "no_supported_breaks", Findings: []openapidiff.RoutePairFinding{},
	})
	contracts.Mappings[0].Successors[0].Status = "breaking"
	contracts.Mappings[0].Successors[0].Findings = []openapidiff.RoutePairFinding{{Severity: "breaking", Code: "response_field_removed"}}
	joined, err := buildPreviewCustomerCutoverReview(contracts, progress, snapshots, now)
	if err != nil {
		t.Fatal(err)
	}
	if joined.Outcome != "breaking_changes" || joined.Summary.BreakingContractRoutes != 1 || joined.Summary.CustomersOnBreakingSuccessors != 1 ||
		joined.Routes[0].Status != "contract_review_required" || joined.Routes[0].Customers[0].NextStep != "move_customer_from_breaking_successor" {
		t.Fatalf("breaking successor option should be visible and prevent passing the breaking gate: %+v", joined)
	}
}

func TestPreviewCustomerCutoverReviewRejectsDeploymentAndMappingDrift(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	snapshots := cutoverProgressFixtures(t, now)
	progress, err := buildPreviewCustomerMigrationProgressReport(snapshots, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", now)
	if err != nil {
		t.Fatal(err)
	}
	contracts := cutoverContractFixture("no_supported_breaks")
	snapshots[0].deployments["api"] = previewCustomerMigrationAppReport{App: "api", DeploymentID: migrationDeployment}
	if _, err := buildPreviewCustomerCutoverReview(contracts, progress, snapshots, now); err == nil || !strings.Contains(err.Error(), "contract review used successor deployment") {
		t.Fatalf("mismatched telemetry deployment should be rejected: %v", err)
	}

	snapshots = cutoverProgressFixtures(t, now)
	contracts.Mappings[0].Successors[0].To.Path = "/another"
	if _, err := buildPreviewCustomerCutoverReview(contracts, progress, snapshots, now); err == nil || !strings.Contains(err.Error(), "successor mapping") {
		t.Fatalf("mismatched successor mapping should be rejected: %v", err)
	}
}

func TestPreviewCustomersMigrationReviewLoadsAndWritesJoinedReport(t *testing.T) {
	resetJSONOut(t)
	base := t.TempDir()
	contractsPath := filepath.Join(base, "contract-review.json")
	firstPath := filepath.Join(base, "snapshot-1.json")
	secondPath := filepath.Join(base, "snapshot-2.json")
	outputPath := filepath.Join(base, "cutover-review.json")
	// The CLI evaluates freshness against wall time; keep its evidence recent.
	now := time.Now().UTC()
	snapshots := cutoverProgressFixtures(t, now)
	contractBody, err := json.Marshal(cutoverContractFixture("no_supported_breaks"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contractsPath, contractBody, 0o600); err != nil {
		t.Fatal(err)
	}
	for i, snapshot := range snapshots {
		body, err := json.Marshal(snapshot.report)
		if err != nil {
			t.Fatal(err)
		}
		path := firstPath
		if i == 1 {
			path = secondPath
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var output bytes.Buffer
	oldStdout, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldStdout, oldJSON })
	baseArgs := []string{"migration", "review", "--contract-review", contractsPath, "--snapshot", firstPath, "--snapshot", secondPath}
	args := append(append([]string{}, baseArgs...), "--out", outputPath)
	if code := cmdPreviewCustomers(args); code != 0 {
		t.Fatalf("migration cutover review exit=%d: %s", code, output.String())
	}
	var stdoutReport, savedReport previewCustomerCutoverReviewReport
	if err := json.Unmarshal(output.Bytes(), &stdoutReport); err != nil {
		t.Fatalf("decode stdout report: %v (%s)", err, output.String())
	}
	savedBody, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(savedBody, &savedReport); err != nil {
		t.Fatalf("decode saved report: %v", err)
	}
	if stdoutReport.Outcome != "owner_review_ready" || savedReport.Routes[0].Customers[0].NextStep == "" {
		t.Fatalf("joined cutover report is incomplete: stdout=%+v saved=%+v", stdoutReport, savedReport)
	}

	output.Reset()
	args = append(append([]string{}, baseArgs...), "--fail-on-not-ready")
	if code := cmdPreviewCustomers(args); code != 0 {
		t.Fatalf("ready report should pass --fail-on-not-ready: %s", output.String())
	}

	output.Reset()
	jsonOutput = false
	args = append(append([]string{}, baseArgs...), "--format", "csv")
	if code := cmdPreviewCustomers(args); code != 0 {
		t.Fatalf("CSV migration action queue exit=%d: %s", code, output.String())
	}
	if !strings.HasPrefix(output.String(), "priority,priority_reason,customer_id,") || !strings.Contains(output.String(), "verify_successor_usage_with_customer_or_owner") {
		t.Fatalf("--format csv should write the prioritized customer action queue: %s", output.String())
	}
}

func TestMigrationCutoverUsageKeepsCommandSpecificDocumentation(t *testing.T) {
	for _, tc := range []struct {
		name, command, topic string
		refresh              bool
	}{
		{"preview", "preview customers migration review", "preview", false},
		{"routes", "routes migration readiness", "cli", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			oldErr, oldJSON := osStderr, jsonOutput
			osStderr, jsonOutput = &stderr, false
			defer func() { osStderr, jsonOutput = oldErr, oldJSON }()
			if code := cmdRouteMigrationCutoverReview(nil, tc.refresh); code != 1 {
				t.Fatalf("missing arguments returned %d", code)
			}
			if output := stderr.String(); !strings.Contains(output, "usage: gregale "+tc.command) || !strings.Contains(output, docsURLForTopic(tc.topic)) {
				t.Fatalf("command-specific usage and docs missing: %s", output)
			}
		})
	}
}
