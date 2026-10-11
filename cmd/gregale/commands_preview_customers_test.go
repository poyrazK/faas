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

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	rosterConsumerA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	rosterConsumerB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	rosterTenantX   = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	rosterTenantY   = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
)

func previewCustomerRosterFixture() previewCustomerRosterInput {
	generatedAt := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	makeRoute := func(method, path, change string, breaks []previewReportBreak, customers []api.RouteCustomerObservation, consumerCount, tenantCount int64) previewReportRoute {
		var requests int64
		for _, customer := range customers {
			requests += customer.Requests
		}
		return previewReportRoute{
			Method: method, Path: path, Change: change, RouteSource: "captured_deployment_contract",
			Breaks: breaks, PolicyKinds: []string{}, TestProfiles: []previewReportTest{}, SourceTestProfiles: []previewReportTest{},
			NextActions: []string{}, PolicyScope: "deployment_pair",
			CustomerImpact: &previewRouteCustomerImpact{Status: "observed", Usage: &api.RouteCustomerUsage{
				Method: method, Route: method + " " + path, Requests: requests, IdentifiedRequests: requests,
				ConsumerCount: consumerCount, PlatformTenantCount: tenantCount,
				LastObservedAt: "2026-10-07T11:00:00Z", Customers: customers,
			}},
		}
	}
	changed := previewRouteReport{
		Version: 7, Preview: "pr-42-api", Parent: "api", BaselineDeployment: customerBaseline,
		GeneratedAt: generatedAt,
		Customers: previewReportCustomerEvidence{
			previewReportEvidence: previewReportEvidence{Status: "advisory", Reason: "baseline_observed_exposure"},
			DeploymentID:          customerBaseline, From: "2026-10-06T12:00:00Z", Until: generatedAt.Format(time.RFC3339Nano),
			Coverage: "observed_only", DetailsIncluded: true,
		},
		Routes: []previewReportRoute{
			makeRoute("GET", "/users/{id}", "changed", nil, []api.RouteCustomerObservation{
				{ConsumerID: rosterConsumerA, PlatformTenantID: rosterTenantX, Requests: 8, LastObservedAt: "2026-10-07T10:30:00Z"},
				{ConsumerID: rosterConsumerA, PlatformTenantID: rosterTenantY, Requests: 2, LastObservedAt: "2026-10-07T11:00:00Z"},
				{ConsumerID: rosterConsumerB, PlatformTenantID: rosterTenantX, Requests: 15, LastObservedAt: "2026-10-07T10:00:00Z"},
			}, 2, 2),
			makeRoute("GET", "/orders", "changed", nil, []api.RouteCustomerObservation{
				{ConsumerID: rosterConsumerA, PlatformTenantID: rosterTenantY, Requests: 12, LastObservedAt: "2026-10-07T10:00:00Z"},
			}, 1, 1),
			makeRoute("GET", "/legacy", "removed", nil, []api.RouteCustomerObservation{
				{ConsumerID: rosterConsumerB, PlatformTenantID: rosterTenantX, Requests: 3, LastObservedAt: "2026-10-07T09:00:00Z"},
			}, 1, 1),
		},
	}
	return previewCustomerRosterInput{reports: []previewRouteReport{changed}, previewReports: 1, generatedAt: generatedAt}
}

func TestBuildPreviewCustomerRosterRanksBreakingExposureAndGroupsPairedRows(t *testing.T) {
	input := previewCustomerRosterFixture()
	input.reports[0].Routes = append(input.reports[0].Routes, previewReportRoute{Method: "GET", Path: "/new", Change: "added"})
	report, err := buildPreviewCustomerRoster(input, "consumer")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "advisory" || report.Summary.ChangedRoutes != 3 || report.Summary.AddedRoutesOmitted != 1 || report.Summary.ObservedChangedRoutes != 3 || report.Summary.Customers != 2 {
		t.Fatalf("unexpected summary: %+v; status %s", report.Summary, report.Status)
	}
	if got := report.Customers[0]; got.ID != rosterConsumerB || got.BreakingRoutes != 1 || got.AffectedRoutes != 2 || got.ObservedRequests != 18 {
		t.Fatalf("breaking customer should rank first with deduplicated route counts: %+v", got)
	}
	if got := report.Customers[1]; got.ID != rosterConsumerA || got.BreakingRoutes != 0 || got.AffectedRoutes != 2 || got.ObservedRequests != 22 || got.LastObservedAt != "2026-10-07T11:00:00Z" {
		t.Fatalf("paired consumer rows did not aggregate correctly: %+v", got)
	}
	if len(report.Customers[1].Routes) != 2 || report.Customers[1].Routes[0].Path != "/orders" {
		t.Fatalf("routes not sorted by observed request count: %+v", report.Customers[1].Routes)
	}
}

func TestPreviewCustomerRosterIncludesPolicyAndSourceOnlyChanges(t *testing.T) {
	input := previewCustomerRosterFixture()
	base := input.reports[0]
	base.Routes = []previewReportRoute{
		{
			Method: "POST", Path: "/checkout", Change: "unchanged",
			PolicyDrift: &previewRoutePolicyDrift{Status: "changed"},
			CustomerImpact: &previewRouteCustomerImpact{Status: "observed", Usage: &api.RouteCustomerUsage{
				Method: "POST", Route: "POST /checkout", Requests: 20, ConsumerCount: 1, PlatformTenantCount: 1,
				Customers: []api.RouteCustomerObservation{{ConsumerID: rosterConsumerA, PlatformTenantID: rosterTenantX, Requests: 20, LastObservedAt: "2026-10-07T11:00:00Z"}},
			}},
		},
		{
			Method: "GET", Path: "/invoice", Change: "unchanged",
			SourceImpact: &previewRouteSource{Change: "source_changed"},
			CustomerImpact: &previewRouteCustomerImpact{Status: "observed", Usage: &api.RouteCustomerUsage{
				Method: "GET", Route: "GET /invoice", Requests: 5, ConsumerCount: 1, PlatformTenantCount: 1,
				Customers: []api.RouteCustomerObservation{{ConsumerID: rosterConsumerB, PlatformTenantID: rosterTenantX, Requests: 5, LastObservedAt: "2026-10-07T11:00:00Z"}},
			}},
		},
	}
	input.reports[0] = base
	report, err := buildPreviewCustomerRoster(input, "consumer")
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.ChangedRoutes != 2 || report.Summary.Customers != 2 {
		t.Fatalf("policy or source changes were omitted: %+v", report.Summary)
	}
	for _, customer := range report.Customers {
		if len(customer.Routes) != 1 || len(customer.Routes[0].Reasons) != 1 {
			t.Fatalf("expected one explicit change reason: %+v", customer)
		}
		want := "policy_drift_changed"
		if customer.ID == rosterConsumerB {
			want = "source_source_changed"
		}
		if customer.Routes[0].Reasons[0] != want {
			t.Fatalf("route reason = %v, want %s", customer.Routes[0].Reasons, want)
		}
	}
}

func TestBuildPreviewCustomerRosterGroupsByAccountTenantAndMarksIncompleteExposure(t *testing.T) {
	input := previewCustomerRosterFixture()
	route := input.reports[0].Routes[0].CustomerImpact.Usage
	route.CustomersTruncated = true
	route.OtherCustomerRequests = 7
	route.AnonymousRequests = 4
	report, err := buildPreviewCustomerRoster(input, "tenant")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "incomplete" || report.Summary.IncompleteCustomers != 2 {
		t.Fatalf("truncated or unattributed exposure was not marked incomplete: %+v", report)
	}
	var tenantX *previewCustomerRosterEntry
	for i := range report.Customers {
		if report.Customers[i].ID == rosterTenantX {
			tenantX = &report.Customers[i]
		}
	}
	if tenantX == nil || tenantX.IdentityScope != "account" || tenantX.App != "" || tenantX.ObservedRequests != 26 || !tenantX.Incomplete {
		t.Fatalf("tenant exposure did not combine consumer pairs: %+v", tenantX)
	}
	var usersRoute *previewCustomerRosterRoute
	for i := range tenantX.Routes {
		if tenantX.Routes[i].Path == "/users/{id}" {
			usersRoute = &tenantX.Routes[i]
		}
	}
	if usersRoute == nil || usersRoute.OmittedCustomerRequests != 7 || !usersRoute.CustomerDetailsTruncated {
		t.Fatalf("truncation evidence was lost: %+v", usersRoute)
	}
}

func TestBuildPreviewCustomerRosterDeduplicatesSharedRoutesAcrossReleasePreviews(t *testing.T) {
	input := previewCustomerRosterFixture()
	first := input.reports[0]
	second := first
	second.Preview = "pr-43-api"
	second.Routes = append([]previewReportRoute(nil), first.Routes...)
	second.Routes[0].CustomerImpact = &previewRouteCustomerImpact{Status: "observed", Usage: cloneRouteCustomerUsage(first.Routes[0].CustomerImpact.Usage)}
	second.Routes[0].CustomerImpact.Usage.Customers[0].Requests = 80
	input.reports = []previewRouteReport{first, second}
	input.previewReports = 2
	report, err := buildPreviewCustomerRoster(input, "consumer")
	if err != nil {
		t.Fatal(err)
	}
	for _, customer := range report.Customers {
		if customer.ID == rosterConsumerA {
			if customer.ObservedRequests != 94 || customer.AffectedRoutes != 2 || len(customer.Previews) != 2 || len(customer.Routes) != 4 {
				t.Fatalf("shared baseline route was double-counted or preview evidence lost: %+v", customer)
			}
			return
		}
	}
	t.Fatalf("consumer %s missing from release roster", rosterConsumerA)
}

func cloneRouteCustomerUsage(in *api.RouteCustomerUsage) *api.RouteCustomerUsage {
	copy := *in
	copy.Customers = append([]api.RouteCustomerObservation(nil), in.Customers...)
	return &copy
}

func TestBuildPreviewCustomerRosterRequiresIdentitiesAndSurfacesUnavailableReleaseReports(t *testing.T) {
	input := previewCustomerRosterFixture()
	input.reports[0].Customers.DetailsIncluded = false
	if _, err := buildPreviewCustomerRoster(input, "consumer"); err == nil || !strings.Contains(err.Error(), "--customer-details") {
		t.Fatalf("missing opt-in customer IDs should explain how to regenerate: %v", err)
	}

	input = previewCustomerRosterFixture()
	input.previewReports = 2
	input.unavailable = 1
	report, err := buildPreviewCustomerRoster(input, "consumer")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "incomplete" || report.Summary.PreviewReports != 2 || report.Summary.CustomerEvidenceUnavailable != 1 {
		t.Fatalf("unavailable app evidence was hidden: %+v", report)
	}
}

func TestReadPreviewCustomerRosterInputAcceptsSingleAndReleaseReports(t *testing.T) {
	directory := t.TempDir()
	input := previewCustomerRosterFixture()
	singlePath := filepath.Join(directory, "preview.json")
	writeRosterFixture(t, singlePath, input.reports[0])
	single, err := readPreviewCustomerRosterInput(singlePath)
	if err != nil || single.previewReports != 1 || len(single.reports) != 1 {
		t.Fatalf("read single report: %+v, %v", single, err)
	}

	releasePath := filepath.Join(directory, "release.json")
	release := previewReleaseRouteReview{
		Version: 1, GeneratedAt: input.generatedAt,
		Previews: []previewReleaseRouteReviewPreview{
			{Slug: input.reports[0].Preview, Status: "available", Report: &input.reports[0]},
			{Slug: "pr-43-worker", Status: "unavailable", Reason: "preview_not_found"},
		},
	}
	writeRosterFixture(t, releasePath, release)
	got, err := readPreviewCustomerRosterInput(releasePath)
	if err != nil || got.previewReports != 2 || got.unavailable != 1 || len(got.reports) != 1 {
		t.Fatalf("read release report: %+v, %v", got, err)
	}
	if _, err := buildPreviewCustomerRoster(got, "consumer"); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(directory, "linked.json")
	if err := os.Symlink(singlePath, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := readPreviewCustomerRosterInput(linkPath); err == nil {
		t.Fatal("report reader followed a symlink")
	}
}

func TestPreviewCustomersCLIWritesCSVAndNewJSONArtifact(t *testing.T) {
	resetJSONOut(t)
	inputPath := filepath.Join(t.TempDir(), "preview.json")
	writeRosterFixture(t, inputPath, previewCustomerRosterFixture().reports[0])
	outputPath := filepath.Join(t.TempDir(), "customer-roster.json")
	var out bytes.Buffer
	old := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = old })
	if code := cmdPreviewCustomers([]string{"--report", inputPath, "--by", "tenant", "--format", "csv", "--out", outputPath}); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil || len(rows) < 2 || rows[0][2] != "customer_id" {
		t.Fatalf("bad CSV output: rows=%v err=%v", rows, err)
	}
	body, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var roster previewCustomerRosterReport
	if err := json.Unmarshal(body, &roster); err != nil || roster.GroupBy != "tenant" || len(roster.Customers) == 0 {
		t.Fatalf("bad JSON artifact: %+v, %v", roster, err)
	}
	if code := cmdPreviewCustomers([]string{"--report", inputPath, "--out", outputPath}); code == 0 {
		t.Fatal("--out replaced an existing file")
	}
}

func TestPreviewCustomerCSVProtectsSpreadsheetFormulaCells(t *testing.T) {
	for _, value := range []string{"=1+1", " +cmd", "-1+2", "@value", "safe"} {
		got := safePreviewCustomerCSVCell(value)
		if strings.HasPrefix(strings.TrimSpace(value), "=") || strings.HasPrefix(strings.TrimSpace(value), "+") ||
			strings.HasPrefix(strings.TrimSpace(value), "-") || strings.HasPrefix(strings.TrimSpace(value), "@") {
			if !strings.HasPrefix(got, "'") {
				t.Errorf("unsafe CSV value %q remained active: %q", value, got)
			}
		} else if got != value {
			t.Errorf("safe CSV value %q changed to %q", value, got)
		}
	}
}

func writeRosterFixture(t *testing.T, path string, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}
