package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/routeimpact"
)

const routeLifecycleCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func routeLifecycleSpecFixture(t *testing.T) *openapidiff.Spec {
	t.Helper()
	spec, err := openapidiff.LoadBytes([]byte(`{
		"openapi":"3.1.0",
		"paths":{
			"/active/{userId}":{"get":{"responses":{"200":{"description":"ok"}}}},
			"/quiet/{orderId}":{"get":{"responses":{"200":{"description":"ok"}}}},
			"/protected":{"post":{"responses":{"200":{"description":"ok"}}}},
			"/source-present":{"get":{"responses":{"200":{"description":"ok"}}}}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func routeLifecycleUsageFixture() api.RouteCustomerUsageResponse {
	return api.RouteCustomerUsageResponse{
		Slug: "api", DeploymentID: customerBaseline,
		From: "2026-09-21T00:00:00Z", Until: "2026-10-05T00:00:00Z", AsOf: "2026-10-05T00:05:00Z",
		Coverage: "observed_only", RoutesLimit: api.RouteCustomerUsageMaxRoutes,
		CustomersLimit: api.RouteCustomerUsageMaxCustomers,
		Routes: []api.RouteCustomerUsage{{
			Route: "GET /active/{id}", Method: "GET", Requests: 18,
			IdentifiedRequests: 12, AnonymousRequests: 4, UnresolvedIdentityRequests: 2,
			ConsumerCount: 3, PlatformTenantCount: 2, LastObservedAt: "2026-10-04T23:58:00Z",
			Customers: []api.RouteCustomerObservation{{ConsumerID: customerID, PlatformTenantID: customerTenant, Requests: 12, LastObservedAt: "2026-10-04T23:58:00Z"}},
		}},
	}
}

func routeLifecycleObservationFixture() routeLifecycleObservation {
	return routeLifecycleObservation{
		Status: "available", From: "2026-09-21T00:00:00Z", Until: "2026-10-05T00:00:00Z",
		AsOf: "2026-10-05T00:05:00Z", Coverage: "observed_only", RequestedSince: "14d",
		RoutesScanned: 1, RouteLimit: api.RouteCustomerUsageMaxRoutes,
	}
}

func routeLifecycleRequirementsFixture() routeLifecycleRequirementEvidence {
	return routeLifecycleRequirementEvidence{
		status: "available", rev: 2, digest: strings.Repeat("b", 64),
		config: &api.RouteRequirementsConfig{Version: 2, Routes: []api.RouteRequirement{{Name: "critical-write", Method: "POST", Path: "/protected"}}},
	}
}

func routeLifecycleSourceFixture() routeLifecycleSourceEvidence {
	beforeQuiet := routeimpact.Route{Method: "GET", Path: "/quiet/{orderId}"}
	afterActive := routeimpact.Route{Method: "GET", Path: "/active/{id}"}
	afterSourcePresent := routeimpact.Route{Method: "GET", Path: "/source-present"}
	afterCodeOnly := routeimpact.Route{
		Method: "GET", Path: "/code-only/{accountID}", Handler: "getAccount",
		Source:       routeimpact.Location{File: "handlers/accounts.go", Line: 42},
		Registration: routeimpact.Location{File: "routes.go", Line: 18},
	}
	impact := routeimpact.Report{
		App: "api", Repository: "github.com/acme/api", SourceRoot: "services/api", Status: "complete",
		Candidate: routeimpact.Snapshot{Revision: routeLifecycleCommit},
		Routes: []routeimpact.Result{
			{Method: "GET", Path: beforeQuiet.Path, Before: &beforeQuiet},
			{Method: "GET", Path: afterActive.Path, After: &afterActive},
			{Method: "GET", Path: afterSourcePresent.Path, After: &afterSourcePresent},
			{Method: "GET", Path: afterCodeOnly.Path, After: &afterCodeOnly},
		},
	}
	deployment := api.DeploymentResponse{
		ID: customerBaseline, AppID: "app-id", SourceURL: "https://github.com/acme/api.git",
		CommitSHA: routeLifecycleCommit, SourceRoot: "services/api",
	}
	return buildRouteLifecycleSourceEvidence(&impact, strings.Repeat("c", 64), deployment, "api")
}

func routeLifecycleReviewFixture(t *testing.T) routeLifecycleReviewReport {
	t.Helper()
	usage := routeLifecycleUsageFixture()
	return buildRouteLifecycleReviewReport("api", customerBaseline, "14d",
		routeLifecycleInventory{Status: "available", Source: "captured_deployment_openapi", DocumentSHA256: strings.Repeat("d", 64)},
		routeLifecycleSpecFixture(t), routeLifecycleObservationFixture(), &usage,
		routeLifecycleRequirementsFixture(), routeLifecycleSourceFixture())
}

func findRouteLifecycleRoute(t *testing.T, report routeLifecycleReviewReport, method, path string) routeLifecycleRoute {
	t.Helper()
	for _, route := range report.Routes {
		if route.Method == method && route.Path == path {
			return route
		}
	}
	t.Fatalf("route %s %s not found in report", method, path)
	return routeLifecycleRoute{}
}

func TestBuildRouteLifecycleReviewClassifiesOnlySupportedQuietRouteAsCandidate(t *testing.T) {
	report := routeLifecycleReviewFixture(t)
	if report.Outcome != "review_required" || report.Summary.ReviewCandidates != 1 || report.Summary.UnknownRoutes != 0 {
		t.Fatalf("review outcome and summary = %s %+v", report.Outcome, report.Summary)
	}
	active := findRouteLifecycleRoute(t, report, "GET", "/active/{userId}")
	if active.Status != "active" || active.ObservationMatch != "parameter_shape" || active.Requests != 18 || active.SourceReference != "present" {
		t.Fatalf("active route evidence = %+v", active)
	}
	quiet := findRouteLifecycleRoute(t, report, "GET", "/quiet/{orderId}")
	if quiet.Status != "review_candidate" || quiet.SourceReference != "removed" {
		t.Fatalf("quiet route evidence = %+v", quiet)
	}
	protected := findRouteLifecycleRoute(t, report, "POST", "/protected")
	if protected.Status != "protected" || !slicesContains(protected.RequirementRefs, "route:critical-write") {
		t.Fatalf("requirement-protected route evidence = %+v", protected)
	}
	sourcePresent := findRouteLifecycleRoute(t, report, "GET", "/source-present")
	if sourcePresent.Status != "protected" || sourcePresent.SourceReference != "present" {
		t.Fatalf("source-protected route evidence = %+v", sourcePresent)
	}
	codeOnly := findRouteLifecycleRoute(t, report, "GET", "/code-only/{accountID}")
	if codeOnly.Status != "source_only" || !slicesContains(codeOnly.Surfaces, "current_source") ||
		slicesContains(codeOnly.Surfaces, "captured_contract") || codeOnly.SourceHandler != "getAccount" ||
		codeOnly.SourceFile != "handlers/accounts.go" || codeOnly.SourceLine != 42 ||
		codeOnly.RegistrationFile != "routes.go" || codeOnly.RegistrationLine != 18 ||
		!slicesContains(codeOnly.Reasons, "static_source_discovery_does_not_prove_runtime_presence") {
		t.Fatalf("source-only route drift lost source location or caveat: %+v", codeOnly)
	}
	if report.Summary.SourceOutsideContractRoutes != 1 || report.Summary.ContractMissingFromSourceRoutes != 2 || report.Summary.SourceRoutes != 3 {
		t.Fatalf("three-way route summary = %+v", report.Summary)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []string{customerID, customerTenant} {
		if strings.Contains(string(body), identity) {
			t.Fatalf("customer identity leaked into lifecycle report: %s", body)
		}
	}
}

func TestBuildRouteLifecycleReviewJoinsSourceAndObservedRouteOutsideContract(t *testing.T) {
	usage := routeLifecycleUsageFixture()
	usage.Routes = append(usage.Routes, api.RouteCustomerUsage{
		Route: "GET /code-only/{account}", Method: "GET", Requests: 7,
		PlatformTenantCount: 2, ConsumerCount: 1, LastObservedAt: "2026-10-04T23:59:00Z",
	})
	observation := routeLifecycleObservationFixture()
	observation.RoutesScanned = len(usage.Routes)
	report := buildRouteLifecycleReviewReport("api", customerBaseline, "14d",
		routeLifecycleInventory{Status: "available"}, routeLifecycleSpecFixture(t), observation, &usage,
		routeLifecycleRequirementsFixture(), routeLifecycleSourceFixture())
	joined := findRouteLifecycleRoute(t, report, "GET", "/code-only/{accountID}")
	if joined.Status != "source_and_observed_outside_contract" || joined.Requests != 7 ||
		!slicesContains(joined.Surfaces, "current_source") || !slicesContains(joined.Surfaces, "observed_traffic") ||
		slicesContains(joined.Surfaces, "captured_contract") || joined.SourceMatch != "" || joined.ObservationMatch != "parameter_shape" {
		t.Fatalf("source and traffic evidence were not joined: %+v", joined)
	}
	if report.Summary.SourceOutsideContractRoutes != 1 || report.Summary.ObservedOutsideContractRoutes != 1 ||
		report.Inventory.ObservedRoutesOutsideDoc != 1 || report.Summary.ObservedOnlyRoutes != 0 {
		t.Fatalf("source/traffic drift counts = summary %+v, inventory %+v", report.Summary, report.Inventory)
	}
	var rendered bytes.Buffer
	renderRouteLifecycleReview(&rendered, report)
	if !strings.Contains(rendered.String(), "handler getAccount") || !strings.Contains(rendered.String(), "handlers/accounts.go:42") {
		t.Fatalf("human review omitted source location: %s", rendered.String())
	}
}

func TestBuildRouteLifecycleReviewDoesNotClaimSourceOnlyWhenContractIsTruncated(t *testing.T) {
	usage := routeLifecycleUsageFixture()
	report := buildRouteLifecycleReviewReport("api", customerBaseline, "14d",
		routeLifecycleInventory{Status: "incomplete", RoutesOmitted: 3, Reason: "deployment_contract_route_limit_exceeded"},
		routeLifecycleSpecFixture(t), routeLifecycleObservationFixture(), &usage,
		routeLifecycleRequirementsFixture(), routeLifecycleSourceFixture())
	codeOnly := findRouteLifecycleRoute(t, report, "GET", "/code-only/{accountID}")
	if codeOnly.Status != "unknown" || slicesContains(codeOnly.Reasons, "static_source_route_not_in_captured_contract") ||
		!slicesContains(codeOnly.Reasons, "contract_inventory_incomplete") || report.Summary.SourceOutsideContractRoutes != 0 || report.Outcome != "incomplete" {
		t.Fatalf("truncated contract was treated as complete route coverage: route=%+v summary=%+v outcome=%s", codeOnly, report.Summary, report.Outcome)
	}
}

func TestBuildRouteLifecycleReviewKeepsUncontractedTrafficUnknownWithoutSourceInventory(t *testing.T) {
	usage := routeLifecycleUsageFixture()
	usage.Routes = append(usage.Routes, api.RouteCustomerUsage{
		Route: "GET /outside", Method: "GET", Requests: 2,
	})
	source := routeLifecycleSourceEvidence{status: "not_provided", refs: map[routeLifecycleRouteKey]routeLifecycleSourceRef{}, current: map[routeLifecycleRouteKey]routeimpact.Route{}}
	report := buildRouteLifecycleReviewReport("api", customerBaseline, "14d",
		routeLifecycleInventory{Status: "available"}, routeLifecycleSpecFixture(t), routeLifecycleObservationFixture(), &usage,
		routeLifecycleRequirementsFixture(), source)
	outside := findRouteLifecycleRoute(t, report, "GET", "/outside")
	if outside.Status != "unknown" || !slicesContains(outside.Reasons, "current_source_inventory_incomplete") ||
		!slicesContains(outside.Surfaces, "observed_traffic") || slicesContains(outside.Surfaces, "current_source") ||
		report.Summary.ObservedOutsideContractRoutes != 1 || report.Outcome != "incomplete" {
		t.Fatalf("missing source evidence was treated as a complete three-way match: route=%+v summary=%+v outcome=%s", outside, report.Summary, report.Outcome)
	}
}

func TestBuildRouteLifecycleReviewSurfacesObservedRouteMissingFromBoundSource(t *testing.T) {
	usage := routeLifecycleUsageFixture()
	source := routeLifecycleSourceFixture()
	delete(source.current, routeLifecycleRouteKey{method: "GET", path: "/active/{id}"})
	source.refs[routeLifecycleRouteKey{method: "GET", path: "/active/{id}"}] = routeLifecycleSourceRef{removed: true}
	report := buildRouteLifecycleReviewReport("api", customerBaseline, "14d",
		routeLifecycleInventory{Status: "available"}, routeLifecycleSpecFixture(t),
		routeLifecycleObservationFixture(), &usage, routeLifecycleRequirementsFixture(), source)
	active := findRouteLifecycleRoute(t, report, "GET", "/active/{userId}")
	if active.Status != "active" || !slicesContains(active.Reasons, "observed_route_removed_from_bound_source_snapshot") {
		t.Fatalf("source/traffic mismatch was not exposed: %+v", active)
	}
	var rendered bytes.Buffer
	renderRouteLifecycleReview(&rendered, report)
	if !strings.Contains(rendered.String(), "GET /active/{userId}: active") || !strings.Contains(rendered.String(), "source removed") {
		t.Fatalf("human review hid the source/traffic mismatch: %s", rendered.String())
	}
}

func TestBuildRouteLifecycleReviewWithholdsCandidatesWhenEvidenceIsInsufficient(t *testing.T) {
	type evidenceMutator func(*routeLifecycleInventory, *routeLifecycleObservation, *routeLifecycleRequirementEvidence, *routeLifecycleSourceEvidence)
	for name, mutate := range map[string]evidenceMutator{
		"clamped window": func(_ *routeLifecycleInventory, observation *routeLifecycleObservation, _ *routeLifecycleRequirementEvidence, _ *routeLifecycleSourceEvidence) {
			observation.WindowClamped = true
		},
		"truncated telemetry": func(_ *routeLifecycleInventory, observation *routeLifecycleObservation, _ *routeLifecycleRequirementEvidence, _ *routeLifecycleSourceEvidence) {
			observation.RoutesTruncated = true
		},
		"incomplete contract": func(inventory *routeLifecycleInventory, _ *routeLifecycleObservation, _ *routeLifecycleRequirementEvidence, _ *routeLifecycleSourceEvidence) {
			inventory.Status = "incomplete"
		},
		"unavailable requirements": func(_ *routeLifecycleInventory, _ *routeLifecycleObservation, requirements *routeLifecycleRequirementEvidence, _ *routeLifecycleSourceEvidence) {
			requirements.status = "unavailable"
			requirements.config = nil
		},
		"unbound source": func(_ *routeLifecycleInventory, _ *routeLifecycleObservation, _ *routeLifecycleRequirementEvidence, source *routeLifecycleSourceEvidence) {
			source.status, source.bound = "unbound", false
			source.refs = map[routeLifecycleRouteKey]routeLifecycleSourceRef{}
			source.current = map[routeLifecycleRouteKey]routeimpact.Route{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			usage := routeLifecycleUsageFixture()
			inventory := routeLifecycleInventory{Status: "available"}
			observation := routeLifecycleObservationFixture()
			requirements := routeLifecycleRequirementsFixture()
			source := routeLifecycleSourceFixture()
			mutate(&inventory, &observation, &requirements, &source)
			report := buildRouteLifecycleReviewReport("api", customerBaseline, "14d", inventory,
				routeLifecycleSpecFixture(t), observation, &usage, requirements, source)
			quiet := findRouteLifecycleRoute(t, report, "GET", "/quiet/{orderId}")
			if quiet.Status == "review_candidate" || report.Summary.ReviewCandidates != 0 || report.Outcome != "incomplete" {
				t.Fatalf("insufficient evidence produced a retirement candidate: %+v; summary=%+v", quiet, report.Summary)
			}
		})
	}
}

func TestBuildRouteLifecycleReviewMarksObservedContractDriftAndAmbiguousTemplatesUnknown(t *testing.T) {
	t.Run("observed route outside contract", func(t *testing.T) {
		usage := routeLifecycleUsageFixture()
		usage.Routes = append(usage.Routes, api.RouteCustomerUsage{
			Route: "GET /outside", Method: "GET", Requests: 1, LastObservedAt: "2026-10-04T23:59:00Z",
		})
		observation := routeLifecycleObservationFixture()
		observation.RoutesScanned = len(usage.Routes)
		report := buildRouteLifecycleReviewReport("api", customerBaseline, "14d",
			routeLifecycleInventory{Status: "available"}, routeLifecycleSpecFixture(t), observation, &usage,
			routeLifecycleRequirementsFixture(), routeLifecycleSourceFixture())
		quiet := findRouteLifecycleRoute(t, report, "GET", "/quiet/{orderId}")
		if quiet.Status != "unknown" || report.Inventory.Status != "incomplete" || report.Summary.ReviewCandidates != 0 || report.Outcome != "incomplete" {
			t.Fatalf("contract drift did not withhold candidates: quiet=%+v inventory=%+v summary=%+v outcome=%s", quiet, report.Inventory, report.Summary, report.Outcome)
		}
	})

	t.Run("ambiguous parameter shape", func(t *testing.T) {
		spec := routeLifecycleSpecFixture(t)
		spec.Paths["/quiet/{name}"] = spec.Paths["/quiet/{orderId}"]
		usage := routeLifecycleUsageFixture()
		report := buildRouteLifecycleReviewReport("api", customerBaseline, "14d",
			routeLifecycleInventory{Status: "available"}, spec, routeLifecycleObservationFixture(), &usage,
			routeLifecycleRequirementsFixture(), routeLifecycleSourceFixture())
		quiet := findRouteLifecycleRoute(t, report, "GET", "/quiet/{orderId}")
		alias := findRouteLifecycleRoute(t, report, "GET", "/quiet/{name}")
		if quiet.Status != "unknown" || alias.Status != "unknown" || report.Summary.ReviewCandidates != 0 || report.Outcome != "incomplete" {
			t.Fatalf("ambiguous route templates were not withheld: quiet=%+v alias=%+v summary=%+v outcome=%s", quiet, alias, report.Summary, report.Outcome)
		}
	})
}

func TestBuildRouteLifecycleSourceEvidenceRequiresDeploymentProvenance(t *testing.T) {
	source := routeLifecycleSourceFixture()
	if source.status != "complete" || !source.bound {
		t.Fatalf("valid source binding = %+v", source)
	}

	impact := routeimpact.Report{App: "api", Repository: "github.com/other/repo", SourceRoot: "services/api", Status: "complete", Candidate: routeimpact.Snapshot{Revision: routeLifecycleCommit}}
	deployment := api.DeploymentResponse{ID: customerBaseline, AppID: "app-id", SourceURL: "https://github.com/acme/api.git", CommitSHA: routeLifecycleCommit, SourceRoot: "services/api"}
	for name, mutate := range map[string]func(*routeimpact.Report, *api.DeploymentResponse){
		"repository mismatch":  func(report *routeimpact.Report, _ *api.DeploymentResponse) {},
		"source root mismatch": func(report *routeimpact.Report, _ *api.DeploymentResponse) { report.SourceRoot = "other/root" },
		"revision mismatch": func(report *routeimpact.Report, _ *api.DeploymentResponse) {
			report.Candidate.Revision = strings.Repeat("e", 40)
		},
		"app mismatch":                      func(report *routeimpact.Report, _ *api.DeploymentResponse) { report.App = "other" },
		"deployment repository unavailable": func(_ *routeimpact.Report, deployed *api.DeploymentResponse) { deployed.SourceURL = "" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := impact
			deployed := deployment
			if name != "repository mismatch" {
				candidate.Repository = "github.com/acme/api"
			}
			mutate(&candidate, &deployed)
			evidence := buildRouteLifecycleSourceEvidence(&candidate, strings.Repeat("c", 64), deployed, "api")
			if evidence.bound || evidence.status != "unbound" {
				t.Fatalf("unbound provenance produced evidence: %+v", evidence)
			}
		})
	}
}

func TestReadRouteLifecycleRequirementsDistinguishesMissingConfigFromUnknown404(t *testing.T) {
	for _, test := range []struct {
		detail string
		want   string
	}{
		{detail: "app, saved route requirements or deployment", want: "none"},
		{detail: "unknown route", want: "unavailable"},
	} {
		t.Run(test.detail, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/api/route-requirements" {
					t.Errorf("unexpected saved requirements request: %s %s", r.Method, r.URL.String())
				}
				writeJSONTestStatus(w, http.StatusNotFound, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", test.detail))
			}))
			defer server.Close()
			evidence := readRouteLifecycleRequirements(t.Context(), api.NewClient(server.URL, "token"), "api", "app-id")
			if evidence.status != test.want {
				t.Fatalf("404 detail %q classified as %q, want %q", test.detail, evidence.status, test.want)
			}
		})
	}
}

func TestRouteLifecyclePathShapeOnlyNormalizesWholeParameterSegments(t *testing.T) {
	for _, test := range []struct {
		path string
		want string
		ok   bool
	}{
		{path: "/users/{userId}/orders/{orderId}", want: "/users/{}/orders/{}", ok: true},
		{path: "/users/current", want: "/users/current", ok: true},
		{path: "/users/pre-{id}", ok: false},
		{path: "/users/{id}{name}", ok: false},
		{path: "/users/{}", ok: false},
		{path: "/users/{id}?verbose=true", ok: false},
	} {
		got, ok := routeLifecyclePathShape(test.path)
		if got != test.want || ok != test.ok {
			t.Errorf("routeLifecyclePathShape(%q) = (%q, %t), want (%q, %t)", test.path, got, ok, test.want, test.ok)
		}
	}
}

func TestCmdRoutesLifecycleReviewReadsBoundedEvidenceAndFailsInconclusive(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing auth header: %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v1/deployments/" + customerBaseline:
			if r.Method != http.MethodGet {
				t.Errorf("deployment request method = %s", r.Method)
			}
			writeJSONTest(w, api.DeploymentResponse{ID: customerBaseline, AppID: "app-id", CommitSHA: routeLifecycleCommit})
		case "/v1/apps/api/deployments/" + customerBaseline + "/openapi":
			writeJSONTest(w, api.OpenAPIDocResponse{
				DeploymentID: customerBaseline, AppID: "app-id", Source: "manual_upload", CapturedAt: "2026-10-04T00:00:00Z",
				Doc: map[string]any{"openapi": "3.1.0", "paths": map[string]any{
					"/quiet": map[string]any{"get": map[string]any{"responses": map[string]any{"200": map[string]any{"description": "ok"}}}},
				}},
			})
		case "/v1/apps/api/analytics/route-customers":
			if r.URL.Query().Get("deployment_id") != customerBaseline || r.URL.Query().Get("since") != "14d" {
				t.Errorf("wrong telemetry scope: %s", r.URL.RawQuery)
			}
			writeJSONTest(w, api.RouteCustomerUsageResponse{
				Slug: "api", DeploymentID: customerBaseline, From: "2026-09-21T00:00:00Z",
				Until: "2026-10-05T00:00:00Z", AsOf: "2026-10-05T00:05:00Z", Coverage: "observed_only",
				Routes: []api.RouteCustomerUsage{}, RoutesLimit: api.RouteCustomerUsageMaxRoutes,
				CustomersLimit: api.RouteCustomerUsageMaxCustomers,
			})
		case "/v1/apps/api/route-requirements":
			writeJSONTestStatus(w, http.StatusNotFound, map[string]any{
				"status": http.StatusNotFound, "code": "not_found", "title": "Not found",
				"detail": "app, saved route requirements or deployment",
			})
		default:
			t.Errorf("unexpected lifecycle API request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)

	var out bytes.Buffer
	oldOut, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	code := cmdRoutes([]string{"lifecycle", "review", "api", "--deployment", customerBaseline, "--since", "14d", "--fail-on-incomplete"})
	if code != 1 {
		t.Fatalf("lifecycle review returned %d, want incomplete exit 1: %s", code, out.String())
	}
	if reads != 4 {
		t.Fatalf("lifecycle API reads = %d, want deployment, contract, usage and requirements reads", reads)
	}
	var report routeLifecycleReviewReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode lifecycle report: %v (%s)", err, out.String())
	}
	if report.Outcome != "incomplete" || report.Requirements.Status != "none" || report.Summary.ReviewCandidates != 0 || report.Summary.UnknownRoutes != 1 {
		t.Fatalf("incomplete evidence was not surfaced safely: %+v", report)
	}
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
