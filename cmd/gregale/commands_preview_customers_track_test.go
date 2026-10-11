package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	migrationDeployment        = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	migrationCatalogDeployment = "12121212-1212-4212-8212-121212121212"
	migrationConsumerC         = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	migrationConsumerD         = "99999999-9999-4999-8999-999999999999"
)

func migrationEndpoint(app, method, path string) previewCustomerMigrationEndpoint {
	return previewCustomerMigrationEndpoint{App: app, Method: method, Path: path}
}

func migrationRosterFixture() previewCustomerRosterReport {
	makeRoute := func(path, change string, incomplete bool) previewCustomerRosterRoute {
		return previewCustomerRosterRoute{Preview: "pr-42-api", App: "api", Method: "GET", Path: path, Change: change,
			Reasons: []string{"contract_changed"}, ObservedRequests: 8, LastObservedAt: "2026-10-07T10:00:00Z", Incomplete: incomplete}
	}
	return previewCustomerRosterReport{Version: 1, GeneratedAt: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
		GroupBy: "consumer", Status: "advisory", Customers: []previewCustomerRosterEntry{
			{ID: rosterConsumerA, IdentityScope: "app", App: "api", Routes: []previewCustomerRosterRoute{makeRoute("/old", "changed", false), makeRoute("/same", "changed", false)}},
			{ID: rosterConsumerB, IdentityScope: "app", App: "api", Routes: []previewCustomerRosterRoute{makeRoute("/old", "removed", false), makeRoute("/retired", "removed", false)}},
			{ID: migrationConsumerC, IdentityScope: "app", App: "api", Routes: []previewCustomerRosterRoute{makeRoute("/old", "changed", false)}},
			{ID: migrationConsumerD, IdentityScope: "app", App: "api", Routes: []previewCustomerRosterRoute{makeRoute("/old", "changed", false)}},
		},
	}
}

func migrationMappingFixture() map[previewCustomerMigrationRouteKey]previewCustomerMigrationMapping {
	items := []previewCustomerMigrationMapping{
		{From: migrationEndpoint("api", "GET", "/old"), Successors: []previewCustomerMigrationEndpoint{migrationEndpoint("api", "GET", "/new")}},
		{From: migrationEndpoint("api", "GET", "/same"), Successors: []previewCustomerMigrationEndpoint{migrationEndpoint("api", "GET", "/same")}},
		{From: migrationEndpoint("api", "GET", "/retired"), Successors: []previewCustomerMigrationEndpoint{}},
	}
	result := make(map[previewCustomerMigrationRouteKey]previewCustomerMigrationMapping, len(items))
	for _, item := range items {
		result[previewCustomerMigrationEndpointKey(item.From)] = item
	}
	return result
}

func migrationUsageFixture() api.RouteCustomerUsageResponse {
	stamp := "2026-10-07T11:00:00Z"
	return api.RouteCustomerUsageResponse{
		Slug: "api", DeploymentID: migrationDeployment, From: "2026-10-01T00:00:00Z",
		Until: "2026-10-07T12:00:00Z", AsOf: "2026-10-07T12:00:01Z", Coverage: "observed_only",
		RoutesLimit: api.RouteCustomerUsageMaxRoutes, CustomersLimit: api.RouteCustomerUsageMaxCustomers,
		Routes: []api.RouteCustomerUsage{
			{Route: "GET /old", Method: "GET", Requests: 20, IdentifiedRequests: 20, ConsumerCount: 2, PlatformTenantCount: 1,
				LastObservedAt: stamp, Customers: []api.RouteCustomerObservation{
					{ConsumerID: rosterConsumerA, PlatformTenantID: rosterTenantX, Requests: 10, LastObservedAt: stamp},
					{ConsumerID: rosterConsumerB, PlatformTenantID: rosterTenantX, Requests: 10, LastObservedAt: stamp},
				}},
			{Route: "GET /new", Method: "GET", Requests: 20, IdentifiedRequests: 20, ConsumerCount: 2, PlatformTenantCount: 2,
				LastObservedAt: stamp, Customers: []api.RouteCustomerObservation{
					{ConsumerID: rosterConsumerA, PlatformTenantID: rosterTenantX, Requests: 12, LastObservedAt: stamp},
					{ConsumerID: migrationConsumerC, PlatformTenantID: rosterTenantY, Requests: 8, LastObservedAt: stamp},
				}},
			{Route: "GET /same", Method: "GET", Requests: 10, IdentifiedRequests: 10, ConsumerCount: 1, PlatformTenantCount: 1,
				LastObservedAt: stamp, Customers: []api.RouteCustomerObservation{
					{ConsumerID: rosterConsumerA, PlatformTenantID: rosterTenantX, Requests: 10, LastObservedAt: stamp},
				}},
		},
	}
}

func migrationBuildInput() previewCustomerMigrationBuildInput {
	usage := migrationUsageFixture()
	app := indexPreviewCustomerMigrationUsage(previewCustomerMigrationAppReport{
		App: "api", DeploymentID: migrationDeployment, Status: "available", From: usage.From, Until: usage.Until, AsOf: usage.AsOf, Coverage: usage.Coverage,
	}, usage)
	return previewCustomerMigrationBuildInput{roster: migrationRosterFixture(), mappings: migrationMappingFixture(),
		apps: map[string]previewCustomerMigrationAppEvidence{"api": app}, since: "14d", generated: time.Date(2026, 10, 7, 12, 1, 0, 0, time.UTC)}
}

func TestBuildPreviewCustomerMigrationClassifiesCohortWithoutInferringFromMissingTraffic(t *testing.T) {
	report, err := buildPreviewCustomerMigrationReport(migrationBuildInput())
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "complete" || report.Summary.CustomerRouteLinks != 6 || report.Summary.CohortCustomers != 4 ||
		report.Summary.Both != 1 || report.Summary.OldRouteActive != 1 || report.Summary.SuccessorObserved != 1 ||
		report.Summary.NoCurrentEvidence != 2 || report.Summary.InPlaceUnmeasurable != 1 || report.Summary.Incomplete != 0 {
		t.Fatalf("unexpected migration summary: %+v; status=%s", report.Summary, report.Status)
	}
	want := map[string]map[string]string{
		rosterConsumerA:    {"/old": "both", "/same": "in_place_unmeasurable"},
		rosterConsumerB:    {"/old": "old_route_active", "/retired": "no_current_evidence"},
		migrationConsumerC: {"/old": "successor_observed"},
		migrationConsumerD: {"/old": "no_current_evidence"},
	}
	for _, customer := range report.Customers {
		for _, route := range customer.Routes {
			if got := want[customer.ID][route.From.Path]; got != route.Status {
				t.Errorf("%s %s status=%s, want %s", customer.ID, route.From.Path, route.Status, got)
			}
		}
	}
	if report.Summary.NoCurrentEvidence != 2 {
		t.Fatal("no-current-evidence must be distinct from successful migration")
	}
	for _, customer := range report.Customers {
		for _, route := range customer.Routes {
			if route.From.Path == "/old" && (route.OldRouteTotalEvidence != "observed" || route.OldRouteTotalRequests != 20) {
				t.Fatalf("tracker omitted aggregate old-route traffic: %+v", route)
			}
			if route.From.Path == "/retired" && (route.OldRouteTotalEvidence != "not_observed" || route.OldRouteTotalRequests != 0) {
				t.Fatalf("tracker did not preserve complete zero-traffic evidence for an absent route: %+v", route)
			}
		}
	}
	for _, customer := range report.Customers {
		for _, route := range customer.Routes {
			if customer.ID == rosterConsumerA && route.From.Path == "/old" && (route.OldRouteRequests != 10 || len(route.ObservedSuccessors) != 1 || route.ObservedSuccessors[0].Requests != 12) {
				t.Fatalf("per-customer adoption volume was lost: %+v", route)
			}
		}
	}
}

func TestBuildPreviewCustomerMigrationCombinesTenantAcrossConsumers(t *testing.T) {
	input := migrationBuildInput()
	input.roster.GroupBy = "tenant"
	input.roster.Customers = []previewCustomerRosterEntry{
		{ID: rosterTenantX, IdentityScope: "account", Routes: []previewCustomerRosterRoute{
			{App: "api", Method: "GET", Path: "/old"}, {App: "api", Method: "GET", Path: "/same"},
		}},
		{ID: rosterTenantY, IdentityScope: "account", Routes: []previewCustomerRosterRoute{{App: "api", Method: "GET", Path: "/old"}}},
		{ID: "abababab-abab-4bab-8bab-abababababab", IdentityScope: "account", Routes: []previewCustomerRosterRoute{{App: "api", Method: "GET", Path: "/old"}}},
	}
	report, err := buildPreviewCustomerMigrationReport(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.GroupBy != "tenant" || report.Summary.CohortCustomers != 3 {
		t.Fatalf("tenant cohort was not preserved: %+v", report)
	}
	for _, customer := range report.Customers {
		if customer.ID == rosterTenantX && customer.Routes[0].From.Path == "/old" {
			if customer.Routes[0].Status != "both" || customer.Routes[0].OldRouteRequests != 20 || customer.Routes[0].ObservedSuccessors[0].Requests != 12 {
				t.Fatalf("tenant activity should aggregate consumer rows: %+v", customer.Routes[0])
			}
		}
	}
}

func TestBuildPreviewCustomerMigrationTracksTenantAcrossApps(t *testing.T) {
	oldUsage := migrationUsageFixture()
	oldUsage.Routes = oldUsage.Routes[:1]
	oldEvidence := indexPreviewCustomerMigrationUsage(previewCustomerMigrationAppReport{
		App: "api", DeploymentID: migrationDeployment, Status: "available", From: oldUsage.From, Until: oldUsage.Until,
		AsOf: oldUsage.AsOf, Coverage: oldUsage.Coverage,
	}, oldUsage)
	targetUsage := migrationUsageFixture()
	targetUsage.Slug = "catalog"
	targetUsage.DeploymentID = migrationCatalogDeployment
	targetUsage.Routes = targetUsage.Routes[1:2]
	targetUsage.Routes[0].Route = "GET /new"
	targetEvidence := indexPreviewCustomerMigrationUsage(previewCustomerMigrationAppReport{
		App: "catalog", DeploymentID: migrationCatalogDeployment, Status: "available", From: targetUsage.From,
		Until: targetUsage.Until, AsOf: targetUsage.AsOf, Coverage: targetUsage.Coverage,
	}, targetUsage)
	roster := previewCustomerRosterReport{Version: 1, GroupBy: "tenant", Customers: []previewCustomerRosterEntry{{
		ID: rosterTenantX, IdentityScope: "account", Routes: []previewCustomerRosterRoute{{App: "api", Method: "GET", Path: "/old"}},
	}}}
	mappings := map[previewCustomerMigrationRouteKey]previewCustomerMigrationMapping{
		previewCustomerMigrationEndpointKey(migrationEndpoint("api", "GET", "/old")): {
			From: migrationEndpoint("api", "GET", "/old"), Successors: []previewCustomerMigrationEndpoint{migrationEndpoint("catalog", "GET", "/new")},
		},
	}
	report, err := buildPreviewCustomerMigrationReport(previewCustomerMigrationBuildInput{
		roster: roster, mappings: mappings,
		apps:      map[string]previewCustomerMigrationAppEvidence{"api": oldEvidence, "catalog": targetEvidence},
		generated: time.Now().UTC(), since: "14d",
	})
	if err != nil {
		t.Fatal(err)
	}
	route := report.Customers[0].Routes[0]
	if route.Status != "both" || route.ObservedSuccessors[0].Route.App != "catalog" || report.Summary.Both != 1 {
		t.Fatalf("tenant identity didn't follow a cross-app successor: %+v", report)
	}
}

func TestPreviewCustomerMigrationMarksTruncatedIdentityAndClampedWindowIncomplete(t *testing.T) {
	input := migrationBuildInput()
	old := input.apps["api"].rows[routeLifecycleRouteKey{method: "GET", path: "/old"}]
	old.CustomersTruncated = true
	old.ConsumerCount = 4
	app := input.apps["api"]
	app.usage.WindowClamped = true
	app.report.WindowClamped = true
	input.apps["api"] = app
	report, err := buildPreviewCustomerMigrationReport(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "incomplete" || report.Summary.Incomplete == 0 {
		t.Fatalf("bounded telemetry did not lower confidence: %+v status=%s", report.Summary, report.Status)
	}
	for _, customer := range report.Customers {
		for _, route := range customer.Routes {
			if route.From.Path == "/old" && customer.ID == migrationConsumerD && route.Status != "incomplete" {
				t.Fatalf("missing truncated customer was inferred absent: %+v", route)
			}
		}
	}
}

func TestPreviewCustomerMigrationRequiresExplicitMappingsAndImmutableDeployments(t *testing.T) {
	roster := migrationRosterFixture()
	mappings := migrationMappingFixture()
	deployments := map[string]string{"api": migrationDeployment}
	delete(mappings, previewCustomerMigrationEndpointKey(migrationEndpoint("api", "GET", "/old")))
	if _, err := validatePreviewCustomerMigrationCohort(roster, mappings, deployments); err == nil || !strings.Contains(err.Error(), "no explicit successor mapping") {
		t.Fatalf("missing mapping should fail closed: %v", err)
	}
	mappings = migrationMappingFixture()
	if _, err := validatePreviewCustomerMigrationCohort(roster, mappings, map[string]string{}); err == nil || !strings.Contains(err.Error(), "supply --deployment api=") {
		t.Fatalf("missing deployment should fail closed: %v", err)
	}
	mappings = migrationMappingFixture()
	oldKey := previewCustomerMigrationEndpointKey(migrationEndpoint("api", "GET", "/old"))
	mapping := mappings[oldKey]
	mapping.Successors[0].App = "catalog"
	mappings[oldKey] = mapping
	if _, err := validatePreviewCustomerMigrationCohort(roster, mappings, map[string]string{"api": migrationDeployment, "catalog": migrationDeployment}); err == nil || !strings.Contains(err.Error(), "tenant roster") {
		t.Fatalf("app-scoped consumers shouldn't be correlated across apps: %v", err)
	}
}

func TestReadPreviewCustomerMigrationMappingsValidatesAndNormalizesRoutes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mapping.json")
	body := `{"version":1,"mappings":[{"from":{"app":"api","method":"get","path":"/old"},"successors":[{"app":"catalog","method":"GET","path":"/new"}]}]}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	mappings, err := readPreviewCustomerMigrationMappings(path)
	if err != nil {
		t.Fatal(err)
	}
	got := mappings[previewCustomerMigrationEndpointKey(migrationEndpoint("api", "GET", "/old"))]
	if len(got.Successors) != 1 || got.Successors[0].App != "catalog" || got.Successors[0].Method != "GET" {
		t.Fatalf("mapping wasn't normalized: %+v", got)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"mappings":[{"from":{"app":"api","method":"GET","path":"/old?x=1"},"successors":[]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPreviewCustomerMigrationMappings(path); err == nil {
		t.Fatal("query strings must not be accepted as route path templates")
	}
}

func TestPreviewCustomersTrackReadsSelectedDeploymentAndWritesReport(t *testing.T) {
	resetJSONOut(t)
	rosterPath := filepath.Join(t.TempDir(), "roster.json")
	writeRosterFixture(t, rosterPath, migrationRosterFixture())
	mappingPath := filepath.Join(t.TempDir(), "mapping.json")
	mappingBytes, err := json.Marshal(previewCustomerMigrationMappingFile{Version: 1, Mappings: []previewCustomerMigrationMapping{
		{From: migrationEndpoint("api", "GET", "/old"), Successors: []previewCustomerMigrationEndpoint{migrationEndpoint("api", "GET", "/new")}},
		{From: migrationEndpoint("api", "GET", "/same"), Successors: []previewCustomerMigrationEndpoint{migrationEndpoint("api", "GET", "/same")}},
		{From: migrationEndpoint("api", "GET", "/retired"), Successors: []previewCustomerMigrationEndpoint{}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mappingPath, mappingBytes, 0600); err != nil {
		t.Fatal(err)
	}
	usage := migrationUsageFixture()
	sawRequest := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/api/analytics/route-customers" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization header missing: %q", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("deployment_id") != migrationDeployment || r.URL.Query().Get("since") != "14d" {
			t.Errorf("selected query not forwarded: %s", r.URL.RawQuery)
		}
		until, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("until"))
		if err != nil {
			t.Errorf("fixed until timestamp missing: %s", r.URL.RawQuery)
		} else {
			usage.Until = until.Format(time.RFC3339Nano)
			usage.From = until.Add(-14 * 24 * time.Hour).Format(time.RFC3339Nano)
			usage.AsOf = until.Add(time.Second).Format(time.RFC3339Nano)
			for i := range usage.Routes {
				usage.Routes[i].LastObservedAt = until.Add(-time.Second).Format(time.RFC3339Nano)
				for j := range usage.Routes[i].Customers {
					usage.Routes[i].Customers[j].LastObservedAt = usage.Routes[i].LastObservedAt
				}
			}
		}
		sawRequest = true
		_ = json.NewEncoder(w).Encode(usage)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	outputPath := filepath.Join(t.TempDir(), "tracker.json")
	var out bytes.Buffer
	previous := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previous })
	jsonOutput = true
	args := []string{"track", "--roster", rosterPath, "--mapping", mappingPath, "--deployment", "api=" + migrationDeployment, "--since", "14d", "--out", outputPath}
	if code := cmdPreviewCustomers(args); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if !sawRequest {
		t.Fatal("route-customer telemetry API wasn't queried")
	}
	var stdoutReport previewCustomerMigrationReport
	if err := json.Unmarshal(out.Bytes(), &stdoutReport); err != nil {
		t.Fatalf("decode stdout JSON: %v; %s", err, out.String())
	}
	var saved previewCustomerMigrationReport
	savedBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(savedBytes, &saved); err != nil {
		t.Fatal(err)
	}
	if stdoutReport.Summary.Both != 1 || saved.Summary.Both != 1 || stdoutReport.Deployments[0].DeploymentID != migrationDeployment {
		t.Fatalf("unexpected tracker result: stdout=%+v saved=%+v", stdoutReport.Summary, saved.Summary)
	}
}

func TestPreviewCustomerMigrationAppIndexCountsConsumersAndTenantsSeparately(t *testing.T) {
	usage := migrationUsageFixture()
	usage.Routes[0].Customers = append(usage.Routes[0].Customers, api.RouteCustomerObservation{ConsumerID: rosterConsumerA, PlatformTenantID: rosterTenantY, Requests: 1, LastObservedAt: usage.Routes[0].LastObservedAt})
	usage.Routes[0].ConsumerCount = 2
	usage.Routes[0].PlatformTenantCount = 2
	app := indexPreviewCustomerMigrationUsage(previewCustomerMigrationAppReport{Status: "available"}, usage)
	counts := app.groups[routeLifecycleRouteKey{method: "GET", path: "/old"}]
	if counts.consumers != 2 || counts.tenants != 2 {
		t.Fatalf("pairwise consumer/tenant rows were counted as distinct identities: %+v", counts)
	}
	consumer := previewCustomerMigrationObserve(app, migrationEndpoint("api", "GET", "/old"), rosterConsumerA, "consumer")
	if !consumer.observed || consumer.requests != 11 {
		t.Fatalf("paired consumer rows should aggregate its observed requests: %+v", consumer)
	}
	tenant := previewCustomerMigrationObserve(app, migrationEndpoint("api", "GET", "/old"), rosterTenantX, "tenant")
	if !tenant.observed || tenant.requests != 20 {
		t.Fatalf("same tenant across consumers should aggregate its observed requests: %+v", tenant)
	}
}

func TestPreviewCustomerMigrationRenderersIncludeCurrentUsageEvidence(t *testing.T) {
	report, err := buildPreviewCustomerMigrationReport(migrationBuildInput())
	if err != nil {
		t.Fatal(err)
	}
	var textOutput bytes.Buffer
	renderPreviewCustomerMigrationText(&textOutput, report)
	if !strings.Contains(textOutput.String(), "old observed; 10 customer requests") || !strings.Contains(textOutput.String(), "12 requests") {
		t.Fatalf("text output omitted request evidence: %s", textOutput.String())
	}
	var markdown bytes.Buffer
	renderPreviewCustomerMigrationMarkdown(&markdown, report)
	if !strings.Contains(markdown.String(), "| Old requests |") || !strings.Contains(markdown.String(), "12 requests") {
		t.Fatalf("Markdown output omitted request evidence: %s", markdown.String())
	}
	var csvOutput bytes.Buffer
	if err := renderPreviewCustomerMigrationCSV(&csvOutput, report); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&csvOutput).ReadAll()
	if err != nil || len(rows) < 2 || rows[0][10] != "old_route_observed_requests" {
		t.Fatalf("CSV output is invalid: rows=%v err=%v", rows, err)
	}
	for _, row := range rows[1:] {
		if row[0] == rosterConsumerA && row[6] == "/old" {
			if row[10] != "10" || !strings.Contains(row[8], "12 requests") {
				t.Fatalf("CSV output omitted per-customer adoption volume: %v", row)
			}
			return
		}
	}
	t.Fatal("consumer /old migration row missing from CSV")
}
