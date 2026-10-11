package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func migrationProgressFixture(now time.Time, startAgo, endAgo time.Duration, totalRequests int64, oldEvidence, successorEvidence string) previewCustomerMigrationProgressSnapshotData {
	start := now.Add(-startAgo).UTC()
	end := now.Add(-endAgo).UTC()
	generated := end.Add(2 * time.Minute)
	customerRequests := int64(0)
	if totalRequests > 0 {
		oldEvidence = "observed"
	}
	if oldEvidence == "observed" {
		customerRequests = 2
	}
	totalEvidence := "not_observed"
	status := "no_current_evidence"
	var observed []previewCustomerMigrationSuccessor
	if totalRequests > 0 {
		totalEvidence = "observed"
		status = "old_route_active"
	}
	if successorEvidence == "observed" {
		status = "successor_observed"
		observed = []previewCustomerMigrationSuccessor{{Route: migrationEndpoint("api", "GET", "/new"), Requests: 3, LastObservedAt: end.Add(-time.Minute).Format(time.RFC3339Nano)}}
	}
	if oldEvidence == "observed" {
		status = "old_route_active"
	}
	if oldEvidence == "incomplete" || successorEvidence == "incomplete" {
		status = "incomplete"
	}
	usage := previewCustomerMigrationAppReport{
		App: "api", DeploymentID: migrationDeployment, Status: "available",
		From: start.Format(time.RFC3339Nano), Until: end.Format(time.RFC3339Nano), AsOf: end.Add(time.Minute).Format(time.RFC3339Nano),
		Coverage: "observed_only",
	}
	return previewCustomerMigrationProgressSnapshotData{
		report: previewCustomerMigrationReport{
			Version: previewCustomerMigrationVersion, GeneratedAt: generated, GroupBy: "consumer", Status: "complete",
			Deployments: []previewCustomerMigrationAppReport{usage}, Customers: []previewCustomerMigrationCustomer{{
				ID: migrationConsumerC, IdentityScope: "app", App: "api", Routes: []previewCustomerMigrationRoute{{
					From:             migrationEndpoint("api", "GET", "/old"),
					Successors:       []previewCustomerMigrationEndpoint{migrationEndpoint("api", "GET", "/new")},
					OldRouteEvidence: oldEvidence, OldRouteRequests: customerRequests, OldRouteTotalEvidence: totalEvidence,
					OldRouteTotalRequests: totalRequests, SuccessorEvidence: successorEvidence,
					ObservedSuccessors: observed, Status: status, IncompleteReasons: []string{},
				}},
			}},
		},
		links: map[previewCustomerMigrationProgressLinkKey]previewCustomerMigrationRoute{
			{identity: previewCustomerMigrationIdentity{id: migrationConsumerC, app: "api"}, from: previewCustomerMigrationEndpointKey(migrationEndpoint("api", "GET", "/old"))}: {
				From: migrationEndpoint("api", "GET", "/old"), Successors: []previewCustomerMigrationEndpoint{migrationEndpoint("api", "GET", "/new")},
				OldRouteEvidence: oldEvidence, OldRouteTotalEvidence: totalEvidence, OldRouteTotalRequests: totalRequests,
				SuccessorEvidence: successorEvidence, ObservedSuccessors: observed, Status: status,
			},
		},
		deployments: map[string]previewCustomerMigrationAppReport{"api": usage}, generated: generated,
	}
}

func indexMigrationProgressFixtures(t *testing.T, values ...previewCustomerMigrationProgressSnapshotData) []previewCustomerMigrationProgressSnapshotData {
	t.Helper()
	result := make([]previewCustomerMigrationProgressSnapshotData, 0, len(values))
	for _, value := range values {
		indexed, err := indexPreviewCustomerMigrationProgressSnapshot(value.report)
		if err != nil {
			t.Fatalf("index migration progress fixture: %v", err)
		}
		result = append(result, indexed)
	}
	return result
}

func TestPreviewCustomerMigrationProgressRequiresSustainedAggregateZeroTraffic(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	snapshots := indexMigrationProgressFixtures(t,
		migrationProgressFixture(now, 40*24*time.Hour, 20*24*time.Hour, 0, "not_observed", "not_observed"),
		migrationProgressFixture(now, 20*24*time.Hour, time.Hour, 0, "not_observed", "not_observed"),
	)
	report, err := buildPreviewCustomerMigrationProgressReport(snapshots, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "owner_review_ready" || report.Summary.OwnerReviewReadyRoutes != 1 {
		t.Fatalf("zero traffic across a continuous grace period should be ready for owner review: %+v", report)
	}
	if got := report.Routes[0].Status; got != "owner_review_ready" || report.Routes[0].OldRouteTrafficWindows != 2 {
		t.Fatalf("unexpected route evidence: status=%s windows=%d", got, report.Routes[0].OldRouteTrafficWindows)
	}
	if report.Routes[0].Customers[0].Status != "no_current_evidence" || report.Summary.NoCurrentEvidenceLinks != 1 {
		t.Fatalf("silence should remain distinct from a verified customer migration: %+v", report.Routes[0].Customers[0])
	}
}

func TestPreviewCustomerMigrationProgressNeedsLatestQuietGraceAfterOldTraffic(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	snapshots := indexMigrationProgressFixtures(t,
		migrationProgressFixture(now, 90*24*time.Hour, 70*24*time.Hour, 5, "observed", "observed"),
		migrationProgressFixture(now, 70*24*time.Hour, 40*24*time.Hour, 0, "not_observed", "observed"),
		migrationProgressFixture(now, 40*24*time.Hour, time.Hour, 0, "not_observed", "observed"),
	)
	report, err := buildPreviewCustomerMigrationProgressReport(snapshots, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "owner_review_ready" || report.Routes[0].OldRouteTrafficWindows != 2 {
		t.Fatalf("old activity should reset the grace period, then allow a clean continuous suffix: %+v", report.Routes[0])
	}
}

func TestPreviewCustomerMigrationProgressBlocksAnonymousOrOutOfCohortOldTraffic(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	snapshots := indexMigrationProgressFixtures(t,
		migrationProgressFixture(now, 40*24*time.Hour, 20*24*time.Hour, 0, "not_observed", "observed"),
		migrationProgressFixture(now, 20*24*time.Hour, time.Hour, 7, "not_observed", "observed"),
	)
	report, err := buildPreviewCustomerMigrationProgressReport(snapshots, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "review_required" || report.Routes[0].Status != "old_route_active" || report.Routes[0].Blockers[0] != "old_route_traffic_observed" {
		t.Fatalf("aggregate traffic must block readiness even if the cohort customer is silent: %+v", report)
	}
}

func TestPreviewCustomerMigrationProgressFailsClosedOnIncompleteOrGappedEvidence(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		firstFrom    time.Duration
		firstTo      time.Duration
		secondFrom   time.Duration
		secondTo     time.Duration
		missingTotal bool
		wantStatus   string
	}{
		{name: "telemetry gap", firstFrom: 40 * 24 * time.Hour, firstTo: 25 * 24 * time.Hour, secondFrom: 20 * 24 * time.Hour, secondTo: time.Hour, wantStatus: "insufficient_observation"},
		{name: "old snapshot schema lacks aggregate evidence", firstFrom: 40 * 24 * time.Hour, firstTo: 20 * 24 * time.Hour, secondFrom: 20 * 24 * time.Hour, secondTo: time.Hour, missingTotal: true, wantStatus: "incomplete"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := migrationProgressFixture(now, test.firstFrom, test.firstTo, 0, "not_observed", "observed")
			second := migrationProgressFixture(now, test.secondFrom, test.secondTo, 0, "not_observed", "observed")
			if test.missingTotal {
				routeKey := previewCustomerMigrationProgressLinkKey{identity: previewCustomerMigrationIdentity{id: migrationConsumerC, app: "api"}, from: previewCustomerMigrationEndpointKey(migrationEndpoint("api", "GET", "/old"))}
				route := second.report.Customers[0].Routes[0]
				route.OldRouteTotalEvidence = ""
				second.report.Customers[0].Routes[0] = route
				second.links[routeKey] = second.report.Customers[0].Routes[0]
			}
			snapshots := indexMigrationProgressFixtures(t, first, second)
			report, err := buildPreviewCustomerMigrationProgressReport(snapshots, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", now)
			if err != nil {
				t.Fatal(err)
			}
			if report.Routes[0].Status != test.wantStatus || report.Outcome != "incomplete" {
				t.Fatalf("unexpected fail-closed result: outcome=%s route=%+v", report.Outcome, report.Routes[0])
			}
		})
	}
}

func TestPreviewCustomerMigrationProgressRejectsDuplicateWindowsAndStaleEvidence(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	first := migrationProgressFixture(now, 40*24*time.Hour, 20*24*time.Hour, 0, "not_observed", "observed")
	duplicate := migrationProgressFixture(now, 40*24*time.Hour, 20*24*time.Hour, 0, "not_observed", "observed")
	duplicate.report.GeneratedAt = duplicate.report.GeneratedAt.Add(time.Hour)
	indexed := indexMigrationProgressFixtures(t, first, duplicate)
	if _, err := buildPreviewCustomerMigrationProgressReport(indexed, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", now); err == nil || !strings.Contains(err.Error(), "duplicate observation window") {
		t.Fatalf("the same interval must not count as two independent windows: %v", err)
	}

	later := migrationProgressFixture(now, 20*24*time.Hour, time.Hour, 0, "not_observed", "observed")
	indexed = indexMigrationProgressFixtures(t, first, later)
	staleCheckAt := now.Add(5 * 24 * time.Hour)
	report, err := buildPreviewCustomerMigrationProgressReport(indexed, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", staleCheckAt)
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "incomplete" || report.Routes[0].Status != "incomplete" || report.Routes[0].Reason != "latest_observation_is_stale" {
		t.Fatalf("stale telemetry must not qualify a route: outcome=%s route=%+v", report.Outcome, report.Routes[0])
	}
}

func TestPreviewCustomerMigrationProgressRequiresStableCohortMappingAndDeployment(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	first := migrationProgressFixture(now, 40*24*time.Hour, 20*24*time.Hour, 0, "not_observed", "observed")
	second := migrationProgressFixture(now, 20*24*time.Hour, time.Hour, 0, "not_observed", "observed")
	second.report.Customers[0].Routes[0].Successors[0].Path = "/v2"
	second.report.Customers[0].Routes[0].SuccessorEvidence = "not_observed"
	second.report.Customers[0].Routes[0].ObservedSuccessors = []previewCustomerMigrationSuccessor{}
	second.report.Customers[0].Routes[0].Status = "no_current_evidence"
	if _, err := indexPreviewCustomerMigrationProgressSnapshot(second.report); err != nil {
		t.Fatal(err)
	}
	snapshots := indexMigrationProgressFixtures(t, first, second)
	if _, err := buildPreviewCustomerMigrationProgressReport(snapshots, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", now); err == nil || !strings.Contains(err.Error(), "successor mappings") {
		t.Fatalf("changed mappings must start a new evidence series: %v", err)
	}
	second = migrationProgressFixture(now, 20*24*time.Hour, time.Hour, 0, "not_observed", "observed")
	second.report.Deployments[0].DeploymentID = migrationCatalogDeployment
	if _, err := indexPreviewCustomerMigrationProgressSnapshot(second.report); err != nil {
		t.Fatal(err)
	}
	snapshots = indexMigrationProgressFixtures(t, first, second)
	if _, err := buildPreviewCustomerMigrationProgressReport(snapshots, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", now); err == nil || !strings.Contains(err.Error(), "same immutable deployment") {
		t.Fatalf("changed deployments must start a new evidence series: %v", err)
	}
}

func TestPreviewCustomerMigrationProgressCLIReadsSavedSnapshots(t *testing.T) {
	resetJSONOut(t)
	now := time.Now().UTC()
	first := migrationProgressFixture(now, 40*24*time.Hour, 20*24*time.Hour, 0, "not_observed", "observed").report
	second := migrationProgressFixture(now, 20*24*time.Hour, time.Hour, 0, "not_observed", "observed").report
	directory := t.TempDir()
	firstPath := filepath.Join(directory, "first.json")
	secondPath := filepath.Join(directory, "second.json")
	for path, value := range map[string]previewCustomerMigrationReport{firstPath: first, secondPath: second} {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	outputPath := filepath.Join(directory, "progress.json")
	var output bytes.Buffer
	previous := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = previous })
	jsonOutput = true
	code := cmdPreviewCustomers([]string{"progress", "--snapshot", firstPath, "--snapshot", secondPath, "--grace-period", "30d", "--out", outputPath})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, output.String())
	}
	var stdoutReport, savedReport previewCustomerMigrationProgressReport
	if err := json.Unmarshal(output.Bytes(), &stdoutReport); err != nil {
		t.Fatalf("decode output: %v; %s", err, output.String())
	}
	savedBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(savedBytes, &savedReport); err != nil {
		t.Fatal(err)
	}
	if stdoutReport.Outcome != "owner_review_ready" || savedReport.Summary.OwnerReviewReadyRoutes != 1 {
		t.Fatalf("unexpected CLI progress output: stdout=%+v saved=%+v", stdoutReport, savedReport)
	}
}

func TestParsePreviewCustomerMigrationProgressDuration(t *testing.T) {
	for input, want := range map[string]time.Duration{"30d": 30 * 24 * time.Hour, "72h": 72 * time.Hour, "45m": 45 * time.Minute} {
		got, err := parsePreviewCustomerMigrationProgressDuration(input)
		if err != nil || got != want {
			t.Errorf("parse %q = %s, %v; want %s", input, got, err, want)
		}
	}
	for _, input := range []string{"0s", "3651d", "later", "-2h"} {
		if _, err := parsePreviewCustomerMigrationProgressDuration(input); err == nil {
			t.Errorf("parse %q unexpectedly succeeded", input)
		}
	}
}

func TestRenderPreviewCustomerMigrationProgressIncludesExplicitSuccessors(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	snapshots := indexMigrationProgressFixtures(t,
		migrationProgressFixture(now, 40*24*time.Hour, 20*24*time.Hour, 0, "not_observed", "observed"),
		migrationProgressFixture(now, 20*24*time.Hour, time.Hour, 0, "not_observed", "observed"),
	)
	report, err := buildPreviewCustomerMigrationProgressReport(snapshots, 30*24*time.Hour, "30d", 2, 72*time.Hour, "72h", now)
	if err != nil {
		t.Fatal(err)
	}
	var textOutput, markdownOutput bytes.Buffer
	renderPreviewCustomerMigrationProgressText(&textOutput, report)
	renderPreviewCustomerMigrationProgressMarkdown(&markdownOutput, report)
	if !strings.Contains(textOutput.String(), "successors: api GET /new") || !strings.Contains(markdownOutput.String(), "api GET /new") {
		t.Fatalf("rendered progress omitted the explicit successor mapping:\ntext: %s\nmarkdown: %s", textOutput.String(), markdownOutput.String())
	}
}
