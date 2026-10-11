package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func sunsetDiffFixture(t *testing.T) (routeSunsetReport, routeSunsetReport, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	makeReport := func(end time.Time) routeSunsetReport {
		last := end.Add(-time.Hour).Format(time.RFC3339)
		return routeSunsetReport{Version: 1, App: "api", DeploymentID: routeMigrationFromDeployment, ImportedSHA256: strings.Repeat("a", 64), GeneratedAt: end, Horizon: "720h", Observation: api.RouteCustomerUsageResponse{Slug: "api", DeploymentID: routeMigrationFromDeployment, From: end.Add(-24 * time.Hour).Format(time.RFC3339), Until: end.Format(time.RFC3339), AsOf: end.Format(time.RFC3339), Coverage: "observed_only"}, Routes: []routeSunsetRow{{Method: "GET", Path: "/old", Status: "upcoming", DeprecatedAt: "2026-10-01T00:00:00Z", SunsetAt: "2026-10-09T00:00:00Z", SuccessorURL: "https://example.com/new", Requests: 10, LastObservedAt: last, Evidence: "observed_only", ContractStatus: "no_supported_breaks", Customers: []api.RouteCustomerObservation{{ConsumerID: "c", Requests: 10, LastObservedAt: last}}, Successors: []routeSunsetSuccessor{{To: previewCustomerMigrationEndpoint{App: "api", Method: "GET", Path: "/new"}, IdentityComparable: true, Evidence: "observed_only", ContractStatus: "no_supported_breaks", Requests: 5, CallersAlsoObserved: 1}}}}}
	}
	return makeReport(now.Add(-24 * time.Hour)), makeReport(now), now
}
func hasSunsetDiffSignal(change routeSunsetDiffChange, signal string) bool {
	for _, s := range change.Signals {
		if s == signal {
			return true
		}
	}
	return false
}
func TestRouteSunsetDiffProgressAndRegressions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mutate        func(*routeSunsetReport)
		class, signal string
	}{
		{"continued activity", func(r *routeSunsetReport) {}, "evidence_updated", "old_route_callers_reobserved"},
		{"less old activity", func(r *routeSunsetReport) { r.Routes[0].Requests = 5; r.Routes[0].Customers[0].Requests = 5 }, "improvement", "old_route_activity_reduced"},
		{"more successor activity", func(r *routeSunsetReport) { r.Routes[0].Successors[0].Requests = 20 }, "improvement", "successor_usage_increased"},
		{"new old caller", func(r *routeSunsetReport) { r.Routes[0].Customers[0].ConsumerID = "new" }, "regression", "newly_observed_old_route_callers"},
		{"sunset elapsed", func(r *routeSunsetReport) { r.Routes[0].SunsetAt = "2026-10-07T00:00:00Z" }, "regression", "old_route_observed_after_sunset"},
		{"date changed", func(r *routeSunsetReport) { r.Routes[0].SunsetAt = "2026-11-01T00:00:00Z" }, "changed", "lifecycle_metadata_changed"},
		{"mapping changed", func(r *routeSunsetReport) { r.Routes[0].Successors[0].To.Path = "/other" }, "changed", "successor_mapping_changed"},
		{"unknown telemetry", func(r *routeSunsetReport) { r.Routes[0].Evidence = "incomplete" }, "evidence_degraded", "evidence_incomplete_or_not_comparable"},
		{"anonymous activity", func(r *routeSunsetReport) { r.Routes[0].AnonymousRequests = 1 }, "evidence_degraded", "evidence_incomplete_or_not_comparable"},
		{"successor evidence degraded", func(r *routeSunsetReport) { r.Routes[0].Successors[0].Evidence = "incomplete" }, "evidence_degraded", "evidence_incomplete_or_not_comparable"},
		{"route removed", func(r *routeSunsetReport) { r.Routes = nil }, "changed", "operation_missing_from_current_report"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b, now := sunsetDiffFixture(t)
			tc.mutate(&b)
			diff, err := buildRouteSunsetDiff(a, b, now, 24*time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			row := diff.Changes[0]
			if row.Classification != tc.class || !hasSunsetDiffSignal(row, tc.signal) {
				t.Fatalf("change %+v", row)
			}
		})
	}
}
func TestRouteSunsetDiffWindowAndEvidence(t *testing.T) {
	a, b, now := sunsetDiffFixture(t)
	b.Observation.From = now.Add(-48 * time.Hour).Format(time.RFC3339)
	b.Routes[0].Successors[0].Requests = 20
	diff, err := buildRouteSunsetDiff(a, b, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Changes[0].Classification != "evidence_degraded" || hasSunsetDiffSignal(diff.Changes[0], "successor_usage_increased") {
		t.Fatalf("overlap progress %+v", diff)
	}
	a, b, now = sunsetDiffFixture(t)
	diff, err = buildRouteSunsetDiff(a, b, now.Add(48*time.Hour), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Outcome != "review_required" || len(diff.Findings) == 0 {
		t.Fatalf("staleness %+v", diff)
	}
	a, b, now = sunsetDiffFixture(t)
	b.ImportedSHA256 = strings.Repeat("b", 64)
	b.Routes[0].Successors[0].Requests = 20
	diff, err = buildRouteSunsetDiff(a, b, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if hasSunsetDiffSignal(diff.Changes[0], "successor_usage_increased") {
		t.Fatal("import change claimed progress")
	}
	a, b, now = sunsetDiffFixture(t)
	a.Routes[0].Requests = 0
	a.Routes[0].LastObservedAt = ""
	a.Routes[0].Customers = nil
	a.Routes[0].Successors[0].CallersAlsoObserved = 0
	diff, err = buildRouteSunsetDiff(a, b, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSunsetDiffSignal(diff.Changes[0], "old_route_activity_resumed") {
		t.Fatalf("resumed %+v", diff)
	}
}
func TestRouteSunsetDiffRejectsInvalidSnapshots(t *testing.T) {
	for _, mutate := range []func(*routeSunsetReport){
		func(r *routeSunsetReport) { r.Version = 2 }, func(r *routeSunsetReport) { r.App = "other" }, func(r *routeSunsetReport) { r.DeploymentID = routeMigrationToDeployment },
		func(r *routeSunsetReport) { r.Routes = append(r.Routes, r.Routes[0]) }, func(r *routeSunsetReport) { r.Routes[0].Requests = -1 },
		func(r *routeSunsetReport) { r.Routes[0].LastObservedAt = "2026-10-01T00:00:00Z" }, func(r *routeSunsetReport) { r.GeneratedAt = r.GeneratedAt.Add(-48 * time.Hour) },
		func(r *routeSunsetReport) {
			r.Routes[0].Customers = append(r.Routes[0].Customers, r.Routes[0].Customers[0])
		},
		func(r *routeSunsetReport) { r.Observation.DeploymentID = routeMigrationToDeployment },
	} {
		a, b, now := sunsetDiffFixture(t)
		mutate(&b)
		if _, err := buildRouteSunsetDiff(a, b, now, 24*time.Hour); err == nil {
			t.Fatal("accepted invalid snapshot")
		}
	}
}
func TestRouteSunsetDiffCommand(t *testing.T) {
	resetJSONOut(t)
	a, b, now := sunsetDiffFixture(t)
	delta := time.Now().UTC().Sub(now)
	for _, r := range []*routeSunsetReport{&a, &b} {
		r.GeneratedAt = r.GeneratedAt.Add(delta)
		from, _ := time.Parse(time.RFC3339Nano, r.Observation.From)
		until, _ := time.Parse(time.RFC3339Nano, r.Observation.Until)
		r.Observation.From = from.Add(delta).Format(time.RFC3339Nano)
		r.Observation.Until = until.Add(delta).Format(time.RFC3339Nano)
		r.Observation.AsOf = r.Observation.Until
		r.Routes[0].LastObservedAt = r.GeneratedAt.Add(-time.Hour).Format(time.RFC3339Nano)
		r.Routes[0].Customers[0].LastObservedAt = r.Routes[0].LastObservedAt
	}
	b.Routes[0].Customers[0].ConsumerID = "new"
	dir := t.TempDir()
	before, after := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
	for path, r := range map[string]routeSunsetReport{before: a, after: b} {
		body, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	old, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = old, oldJSON })
	t.Setenv("FAAS_TOKEN", "") // local comparisons must not require account access.
	saved := filepath.Join(dir, "diff.json")
	if code := cmdRoutes([]string{"sunsets", "diff", "--before", before, "--after", after, "--out", saved, "--fail-on-regression"}); code != 1 {
		t.Fatalf("expected regression exit %d: %s", code, output.String())
	}
	var diff routeSunsetDiff
	if err := json.Unmarshal(output.Bytes(), &diff); err != nil {
		t.Fatal(err)
	}
	if diff.Outcome != "regressions" || len(diff.Changes[0].NewlyObservedCallers) != 1 {
		t.Fatalf("diff %+v", diff)
	}
	if _, err := os.Stat(saved); err != nil {
		t.Fatal(err)
	}
	// Incompleteness must remain actionable even when the row is a regression.
	b.Routes[0].Evidence = "incomplete"
	body, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, body, 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if code := cmdRoutesSunsetsDiff([]string{"--before", before, "--after", after, "--fail-on-incomplete"}); code != 1 {
		t.Fatal("regression hid incomplete evidence")
	}
	// Reusing an output path must preserve the saved result.
	original, _ := os.ReadFile(saved)
	if code := cmdRoutesSunsetsDiff([]string{"--before", before, "--after", after, "--out", saved}); code != 1 {
		t.Fatal("overwrote output")
	}
	current, _ := os.ReadFile(saved)
	if !bytes.Equal(original, current) {
		t.Fatal("output replaced")
	}
}

func TestRouteSunsetDiffChangedCaptureSuppressesProgress(t *testing.T) {
	a, b, now := sunsetDiffFixture(t)
	a.ContractReview = &routeMigrationReviewReport{FromDeployments: []routeMigrationDeploymentEvidence{{App: "api", DeploymentID: a.DeploymentID, ContractSHA: strings.Repeat("a", 64)}}}
	b.ContractReview = &routeMigrationReviewReport{FromDeployments: []routeMigrationDeploymentEvidence{{App: "api", DeploymentID: b.DeploymentID, ContractSHA: strings.Repeat("b", 64)}}}
	b.Routes[0].Requests = 5
	b.Routes[0].Successors[0].Requests = 20
	diff, err := buildRouteSunsetDiff(a, b, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Outcome != "review_required" || diff.Changes[0].Classification != "evidence_degraded" || hasSunsetDiffSignal(diff.Changes[0], "successor_usage_increased") || hasSunsetDiffSignal(diff.Changes[0], "old_route_activity_reduced") {
		t.Fatalf("changed capture %+v", diff)
	}
	// Generation time alone must not mark otherwise identical contract evidence changed.
	b.ContractReview.FromDeployments[0].ContractSHA = a.ContractReview.FromDeployments[0].ContractSHA
	b.ContractReview.GeneratedAt = now
	if !sameSunsetContracts(a.ContractReview, b.ContractReview) {
		t.Fatal("generation time changed contract identity")
	}
}
