package main_test

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func previewCutoverDiffFixtures() (main.PreviewCustomerCutoverReviewReport, main.PreviewCustomerCutoverReviewReport) {
	generated := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	route := main.PreviewCustomerCutoverReviewRoute{
		From:           main.MigrationEndpoint("api", "GET", "/old"),
		ContractStatus: "no_supported_breaks",
		Successors: []main.PreviewCustomerCutoverReviewSuccessor{{
			To: main.MigrationEndpoint("api", "GET", "/new"), Status: "no_supported_breaks",
		}, {
			To: main.MigrationEndpoint("api", "GET", "/new-alternative"), Status: "no_supported_breaks",
		}},
	}
	compatibleLatest := []main.PreviewCustomerCutoverLatestSuccessor{{
		Route: main.MigrationEndpoint("api", "GET", "/new"), ContractStatus: "no_supported_breaks",
		Requests: 10, LastObservedAt: generated.Add(-time.Hour),
	}}
	baseCustomer := func(id string) main.PreviewCustomerCutoverReviewCustomer {
		return main.PreviewCustomerCutoverReviewCustomer{
			ID: id, IdentityScope: "app", App: "client", MigrationEvidence: "successor_observed",
			NextStep: "verify_successor_usage_with_customer_or_owner", LatestSuccessorEvidence: "determined",
			LatestSuccessors: append([]main.PreviewCustomerCutoverLatestSuccessor{}, compatibleLatest...),
		}
	}
	oldRouteObserved := baseCustomer("old-seen")
	oldRouteObserved.MigrationEvidence = "old_route_observed"
	oldRouteObserved.NextStep = "confirm_old_route_shutdown"
	oldRouteObserved.LatestSuccessorEvidence = "none"
	oldRouteObserved.LatestSuccessors = nil
	switchedSuccessor := baseCustomer("switched")
	route.Customers = []main.PreviewCustomerCutoverReviewCustomer{
		baseCustomer("reuse"), baseCustomer("breaking"), baseCustomer("degraded"), baseCustomer("improved"), baseCustomer("steady"), oldRouteObserved, switchedSuccessor,
	}
	before := main.PreviewCustomerCutoverReviewReport{
		Version: main.PreviewCustomerCutoverReviewVersion, GeneratedAt: generated.Add(-24 * time.Hour), GroupBy: "consumer",
		FromDeployments: []main.RouteMigrationDeploymentEvidence{{App: "api", DeploymentID: main.CutoverBaselineDeployment}},
		ToDeployments:   []main.RouteMigrationDeploymentEvidence{{App: "api", DeploymentID: main.CutoverSuccessorDeployment}},
		Routes:          []main.PreviewCustomerCutoverReviewRoute{route},
	}
	before.Routes[0].Customers[3].MigrationEvidence = "old_route_active"
	before.Routes[0].Customers[3].NextStep = "migrate_customer_from_old_route"
	before.Routes[0].Customers[3].LatestSuccessorEvidence = "none"
	before.Routes[0].Customers[3].LatestSuccessors = nil
	after := before
	after.GeneratedAt = generated
	after.Routes = append([]main.PreviewCustomerCutoverReviewRoute{}, before.Routes...)
	after.Routes[0].Successors = append([]main.PreviewCustomerCutoverReviewSuccessor{}, before.Routes[0].Successors...)
	after.Routes[0].Customers = append([]main.PreviewCustomerCutoverReviewCustomer{}, before.Routes[0].Customers...)
	after.FromDeployments = append([]main.RouteMigrationDeploymentEvidence{}, before.FromDeployments...)
	after.ToDeployments = append([]main.RouteMigrationDeploymentEvidence{}, before.ToDeployments...)
	after.Routes[0].Customers[0].ObservedOldRouteReuse = &main.PreviewCustomerCutoverOldRouteReuse{
		Signal: "old_route_reobserved_after_successor", OldRouteLastObservedAt: generated.Add(-10 * time.Minute),
	}
	after.Routes[0].Customers[0].NextStep = "review_customer_old_route_reuse_with_owner"
	after.Routes[0].Customers[1].LatestSuccessors = append([]main.PreviewCustomerCutoverLatestSuccessor{}, compatibleLatest...)
	after.Routes[0].Customers[1].LatestSuccessors[0].ContractStatus = "breaking"
	after.Routes[0].Customers[1].NextStep = "move_customer_from_breaking_successor"
	after.Routes[0].Customers[2].MigrationEvidence = "no_current_evidence"
	after.Routes[0].Customers[2].NextStep = "verify_usage_with_customer_or_owner"
	after.Routes[0].Customers[2].LatestSuccessorEvidence = "ambiguous"
	after.Routes[0].Customers[2].LatestSuccessors = nil
	after.Routes[0].Customers[3].MigrationEvidence = "successor_observed"
	after.Routes[0].Customers[3].NextStep = "verify_successor_usage_with_customer_or_owner"
	after.Routes[0].Customers[3].LatestSuccessorEvidence = "determined"
	after.Routes[0].Customers[3].LatestSuccessors = append([]main.PreviewCustomerCutoverLatestSuccessor{}, compatibleLatest...)
	after.Routes[0].Customers[5].MigrationEvidence = "old_route_active"
	after.Routes[0].Customers[5].NextStep = "migrate_customer_from_old_route"
	after.Routes[0].Customers[6].LatestSuccessors = []main.PreviewCustomerCutoverLatestSuccessor{{
		Route: main.MigrationEndpoint("api", "GET", "/new-alternative"), ContractStatus: "no_supported_breaks",
		Requests: 4, LastObservedAt: generated.Add(-30 * time.Minute),
	}}
	return before, after
}

func TestBuildPreviewCustomerCutoverDiffClassifiesCustomerTransitions(t *testing.T) {
	before, after := previewCutoverDiffFixtures()
	report, err := main.BuildPreviewCustomerCutoverDiff(before, after, time.Date(2026, 10, 7, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "regressions" || report.Summary.CustomerRouteLinks != 7 || report.Summary.Regressions != 3 ||
		report.Summary.EvidenceDegraded != 1 || report.Summary.Improvements != 1 || report.Summary.EvidenceUpdated != 1 ||
		report.Summary.Unchanged != 1 || len(report.Changes) != 6 {
		t.Fatalf("unexpected diff summary: %+v", report)
	}
	want := map[string]string{"reuse": "regression", "breaking": "regression", "old-seen": "regression", "degraded": "evidence_degraded", "improved": "improved", "switched": "evidence_updated"}
	for _, change := range report.Changes {
		if want[change.CustomerID] != change.Classification {
			t.Errorf("customer %q classification = %q, want %q", change.CustomerID, change.Classification, want[change.CustomerID])
		}
		delete(want, change.CustomerID)
	}
	if len(want) != 0 {
		t.Errorf("diff omitted changed customer links: %v", want)
	}
}

func TestBuildPreviewCustomerCutoverDiffRejectsCohortMappingAndDeploymentDrift(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*main.PreviewCustomerCutoverReviewReport)
		wantErr string
	}{
		{name: "cohort", mutate: func(report *main.PreviewCustomerCutoverReviewReport) {
			report.Routes[0].Customers = report.Routes[0].Customers[:len(report.Routes[0].Customers)-1]
		}, wantErr: "same customer-route cohort"},
		{name: "mapping", mutate: func(report *main.PreviewCustomerCutoverReviewReport) {
			report.Routes[0].Successors[0].To.Path = "/other"
		}, wantErr: "same old-route and successor mapping"},
		{name: "deployment", mutate: func(report *main.PreviewCustomerCutoverReviewReport) {
			report.ToDeployments[0].DeploymentID = main.CutoverBaselineDeployment
		}, wantErr: "same baseline and successor deployment IDs"},
		{name: "time order", mutate: func(report *main.PreviewCustomerCutoverReviewReport) {
			report.GeneratedAt = time.Date(2026, 10, 6, 11, 59, 59, 0, time.UTC)
		}, wantErr: "generation time at or after"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before, after := previewCutoverDiffFixtures()
			test.mutate(&after)
			if _, err := main.BuildPreviewCustomerCutoverDiff(before, after, time.Now()); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestPreviewCustomersMigrationDiffCLIEmitsReportAndFailsOnRegression(t *testing.T) {
	before, after := previewCutoverDiffFixtures()
	base := t.TempDir()
	beforePath := filepath.Join(base, "before.json")
	afterPath := filepath.Join(base, "after.json")
	outPath := filepath.Join(base, "diff.json")
	for path, report := range map[string]main.PreviewCustomerCutoverReviewReport{beforePath: before, afterPath: after} {
		body, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldStdout, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	var output bytes.Buffer
	(*main.OsStdout), (*main.JsonOutput) = &output, false
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = oldStdout, oldJSON })
	args := []string{"migration", "diff", "--before", beforePath, "--after", afterPath, "--format", "csv", "--out", outPath, "--fail-on-regression"}
	if code := main.CmdPreviewCustomers(args); code != 1 {
		t.Fatalf("--fail-on-regression exit=%d, want 1: %s", code, output.String())
	}
	if !strings.HasPrefix(output.String(), "classification,reason,customer_id,") {
		t.Fatalf("CSV diff output missing: %s", output.String())
	}
	records, err := csv.NewReader(strings.NewReader(output.String())).ReadAll()
	if err != nil || len(records) != 7 {
		t.Fatalf("CSV rows = %d, error=%v; want header and six changes", len(records), err)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved main.PreviewCustomerCutoverDiffReport
	if err := json.Unmarshal(body, &saved); err != nil || saved.Summary.Regressions != 3 {
		t.Fatalf("saved diff = %+v, decode error=%v", saved, err)
	}

	_, after = previewCutoverDiffFixtures()
	after.Routes[0].Customers[0].ObservedOldRouteReuse = nil
	after.Routes[0].Customers[0].NextStep = "verify_successor_usage_with_customer_or_owner"
	after.Routes[0].Customers[1].LatestSuccessors[0].ContractStatus = "no_supported_breaks"
	after.Routes[0].Customers[1].NextStep = "verify_successor_usage_with_customer_or_owner"
	after.Routes[0].Customers[5].MigrationEvidence = "old_route_observed"
	after.Routes[0].Customers[5].NextStep = "confirm_old_route_shutdown"
	body, err = json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(afterPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	args = []string{"migration", "diff", "--before", beforePath, "--after", afterPath, "--format", "text", "--fail-on-regression"}
	if code := main.CmdPreviewCustomers(args); code != 0 || !strings.Contains(output.String(), "evidence degraded: 1") {
		t.Fatalf("evidence degradation alone should not trip --fail-on-regression: exit=%d output=%s", code, output.String())
	}
}
