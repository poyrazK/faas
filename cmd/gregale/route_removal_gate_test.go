package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
)

func removalGateFixture(t *testing.T, now time.Time) (map[string]any, map[string]any, *openapidiff.Spec, *openapidiff.Spec, routeRemovalGateEvidence) {
	t.Helper()
	baseDoc := routeMigrationContractDocument("/old", "name")
	propDoc := routeMigrationContractDocument("/new", "name")
	basePaths, propPaths := baseDoc["paths"].(map[string]any), propDoc["paths"].(map[string]any)
	delete(basePaths["/old"].(map[string]any), "parameters")
	delete(propPaths["/new"].(map[string]any), "parameters")
	basePaths["/new"] = propPaths["/new"]
	baseBytes, _ := json.Marshal(baseDoc)
	propBytes, _ := json.Marshal(propDoc)
	base, err := openapidiff.LoadBytes(baseBytes)
	if err != nil {
		t.Fatal(err)
	}
	prop, err := openapidiff.LoadBytes(propBytes)
	if err != nil {
		t.Fatal(err)
	}
	baseSHA, propSHA := fmtRemovalSHA(baseBytes), fmtRemovalSHA(propBytes)
	ready := buildCutoverFixture(t, "no_supported_breaks", now)
	ready.GeneratedAt, ready.ContractReviewGeneratedAt = now.Add(-time.Minute), now.Add(-2*time.Minute)
	ready.FromDeployments = []routeMigrationDeploymentEvidence{{App: "api", DeploymentID: routeMigrationFromDeployment, Status: "available", ContractSHA: baseSHA}}
	ready.ToDeployments = append([]routeMigrationDeploymentEvidence{}, ready.FromDeployments...)
	mapping := previewCustomerMigrationMapping{From: migrationEndpoint("api", "GET", "/old"), Successors: []previewCustomerMigrationEndpoint{migrationEndpoint("api", "GET", "/new")}}
	evidence := routeRemovalGateEvidence{readiness: &ready, mappings: map[previewCustomerMigrationRouteKey]previewCustomerMigrationMapping{previewCustomerMigrationEndpointKey(mapping.From): mapping}, readinessSHA: strings.Repeat("a", 64), mappingSHA: strings.Repeat("b", 64)}
	evidence.approval = &routeRemovalOwnerApproval{Version: 1, ApprovedBy: "route-owner", ApprovedAt: now.Add(-30 * time.Second), App: "api", BaselineDeployment: routeMigrationFromDeployment, CandidateDeployment: routeMigrationToDeployment, BaselineContractSHA: baseSHA, CandidateContractSHA: propSHA, ReadinessSHA: evidence.readinessSHA, MappingSHA: evidence.mappingSHA}
	return baseDoc, propDoc, base, prop, evidence
}

func fmtRemovalSHA(body []byte) string { return fmt.Sprintf("%x", openapidiff.SumSHA256(body)) }

func fixtureRemovalGate(base, prop *openapidiff.Spec, evidence routeRemovalGateEvidence, now time.Time) routeRemovalGateReport {
	return buildRouteRemovalGate("api", routeMigrationFromDeployment, routeMigrationToDeployment, evidence.approval.BaselineContractSHA, evidence.approval.CandidateContractSHA, "enforce", base, prop, evidence, 72*time.Hour, now)
}

func TestRouteRemovalGateBindsEvidenceAndRechecksCandidate(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	_, _, base, prop, evidence := removalGateFixture(t, now)
	report := fixtureRemovalGate(base, prop, evidence, now)
	if report.Status != "passed" || len(report.Routes) != 1 || report.Routes[0].Status != "approved" {
		t.Fatalf("complete staged cutover should pass: %+v", report)
	}
	for _, test := range []struct {
		name   string
		change func(*routeRemovalGateEvidence)
		code   string
	}{
		{"candidate-only telemetry", func(e *routeRemovalGateEvidence) {
			e.readiness.ToDeployments[0].DeploymentID = routeMigrationToDeployment
		}, "readiness_serving_contract_mismatch"},
		{"capture replaced", func(e *routeRemovalGateEvidence) {
			e.readiness.FromDeployments[0].ContractSHA = strings.Repeat("c", 64)
		}, "readiness_serving_contract_mismatch"},
		{"mapping bytes changed", func(e *routeRemovalGateEvidence) { e.mappingSHA = strings.Repeat("d", 64) }, "owner_approval_binding_mismatch"},
		{"readiness bytes changed", func(e *routeRemovalGateEvidence) { e.readinessSHA = strings.Repeat("d", 64) }, "owner_approval_binding_mismatch"},
		{"future readiness", func(e *routeRemovalGateEvidence) { e.readiness.GeneratedAt = now.Add(time.Hour) }, "readiness_stale_or_future"},
		{"expired approval", func(e *routeRemovalGateEvidence) { e.approval.ApprovedAt = now.Add(-73 * time.Hour) }, "owner_approval_invalid_or_stale"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, base, prop, e := removalGateFixture(t, now)
			test.change(&e)
			r := fixtureRemovalGate(base, prop, e, now)
			if r.Status != "blocked" || !slices.Contains(r.Blockers, test.code) {
				t.Fatalf("must block: %+v", r)
			}
		})
	}
	brokenDoc := routeMigrationContractDocument("/new", "name")
	item := brokenDoc["paths"].(map[string]any)["/new"].(map[string]any)
	delete(item, "parameters")
	schema := item["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	schema["required"] = []any{}
	body, _ := json.Marshal(brokenDoc)
	broken, err := openapidiff.LoadBytes(body)
	if err != nil {
		t.Fatal(err)
	}
	evidence.approval.CandidateContractSHA = fmtRemovalSHA(body)
	report = fixtureRemovalGate(base, broken, evidence, now)
	if report.Status != "blocked" || !slices.Contains(report.Routes[0].Blockers, "candidate_successor_contract_breaking") {
		t.Fatalf("old readiness cannot hide a new successor break: %+v", report)
	}
}

func TestRouteRemovalGateRejectsIncompleteCoverageAndActiveCustomers(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, change := range []func(*previewCustomerCutoverReviewReport){
		func(r *previewCustomerCutoverReviewReport) { r.Snapshots[1].Status = "incomplete" },
		func(r *previewCustomerCutoverReviewReport) {
			r.Snapshots[1].From = now.Add(-19 * 24 * time.Hour).Format(time.RFC3339Nano)
		},
		func(r *previewCustomerCutoverReviewReport) {
			r.Snapshots[1].Until = now.Add(-73 * time.Hour).Format(time.RFC3339Nano)
		},
		func(r *previewCustomerCutoverReviewReport) {
			r.Routes[0].Customers[0].MigrationEvidence = "old_route_active"
		},
		func(r *previewCustomerCutoverReviewReport) { r.Routes[0].OldRouteTrafficWindows = 1 },
		func(r *previewCustomerCutoverReviewReport) { r.Routes[0].Customers = nil },
		func(r *previewCustomerCutoverReviewReport) { r.Routes[0].CohortCustomers = 0 },
		func(r *previewCustomerCutoverReviewReport) {
			r.Routes[0].GracePeriodStart = now.Add(-90 * 24 * time.Hour).Format(time.RFC3339Nano)
		},
	} {
		_, _, base, prop, e := removalGateFixture(t, now)
		change(e.readiness)
		if r := fixtureRemovalGate(base, prop, e, now); r.Status != "blocked" {
			t.Fatalf("incomplete/contradictory evidence must block: %+v", r)
		}
	}
}

func TestRouteRemovalGateDoesNotRequireApprovalWithoutRemoval(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	_, _, base, _, e := removalGateFixture(t, now)
	r := buildRouteRemovalGate("api", routeMigrationFromDeployment, routeMigrationToDeployment, e.approval.BaselineContractSHA, e.approval.BaselineContractSHA, "enforce", base, base, routeRemovalGateEvidence{}, 72*time.Hour, now)
	if r.Status != "not_required" || len(r.Routes) != 0 {
		t.Fatalf("no removal requires no migration evidence: %+v", r)
	}
	r = buildRouteRemovalGate("api", routeMigrationFromDeployment, routeMigrationToDeployment, "", "", "enforce", nil, nil, routeRemovalGateEvidence{}, 72*time.Hour, now)
	if r.Status != "blocked" {
		t.Fatal("missing contracts cannot establish no removal")
	}
}

func writeRemovalFixtureFiles(t *testing.T, e routeRemovalGateEvidence) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	readyPath, mappingPath, approvalPath := filepath.Join(dir, "readiness.json"), filepath.Join(dir, "mapping.json"), filepath.Join(dir, "approval.json")
	body, _ := json.Marshal(e.readiness)
	if err := os.WriteFile(readyPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	e.approval.ReadinessSHA = fmtRemovalSHA(body)
	values := []previewCustomerMigrationMapping{}
	for _, m := range e.mappings {
		values = append(values, m)
	}
	body, _ = json.Marshal(previewCustomerMigrationMappingFile{Version: 1, Mappings: values})
	if err := os.WriteFile(mappingPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	e.approval.MappingSHA = fmtRemovalSHA(body)
	body, _ = json.Marshal(e.approval)
	if err := os.WriteFile(approvalPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return readyPath, mappingPath, approvalPath
}

func TestRouteRemovalPromotionBlocksBeforeWriteAndPreservesServingGuard(t *testing.T) {
	for _, test := range []struct {
		name, mode                         string
		missingApproval, active, truncated bool
		wantCode, wantWrites               int
	}{
		{"approved", "enforce", false, false, false, 0, 1},
		{"no approval", "enforce", true, false, false, 1, 0},
		{"new activity", "enforce", false, true, false, 1, 0},
		{"truncated usage", "enforce", false, false, true, 1, 0},
		{"older window", "enforce", false, false, false, 1, 0},
		{"narrow window", "enforce", false, false, false, 1, 0},
		{"missing usage", "enforce", false, false, false, 1, 0},
		{"bindings", "enforce", false, false, false, 0, 1},
		{"advisory", "report", true, false, false, 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetJSONOut(t)
			now := time.Now().UTC()
			baseDoc, propDoc, _, _, e := removalGateFixture(t, now)
			rp, mp, ap := writeRemovalFixtureFiles(t, e)
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/deployments/" + routeMigrationFromDeployment:
					writeJSONTest(w, api.DeploymentResponse{ID: routeMigrationFromDeployment, AppID: "api-id", Status: "live", TrafficPercent: 100})
				case "/v1/deployments/" + routeMigrationToDeployment:
					writeJSONTest(w, api.DeploymentResponse{ID: routeMigrationToDeployment, AppID: "api-id", Status: "live", TrafficPercent: 0})
				case "/v1/apps/api/deployments/" + routeMigrationFromDeployment + "/openapi":
					writeJSONTest(w, api.OpenAPIDocResponse{AppID: "api-id", DeploymentID: routeMigrationFromDeployment, Source: "manual_upload", Doc: baseDoc})
				case "/v1/apps/api/deployments/" + routeMigrationToDeployment + "/openapi":
					writeJSONTest(w, api.OpenAPIDocResponse{AppID: "api-id", DeploymentID: routeMigrationToDeployment, Source: "manual_upload", Doc: propDoc})
				case "/v1/apps/api/analytics/route-customers":
					if test.name == "missing usage" {
						http.NotFound(w, r)
						return
					}
					if r.URL.Query().Get("deployment_id") != routeMigrationFromDeployment {
						t.Error("usage must refer to serving deployment")
					}
					until, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("until"))
					if err != nil {
						t.Error(err)
						http.Error(w, "invalid until", http.StatusBadRequest)
						return
					}
					usage := api.RouteCustomerUsageResponse{Slug: "api", DeploymentID: routeMigrationFromDeployment, From: until.Add(-72 * time.Hour).Format(time.RFC3339Nano), Until: until.Format(time.RFC3339Nano), AsOf: until.Format(time.RFC3339Nano), Coverage: "observed_only", RoutesLimit: api.RouteCustomerUsageMaxRoutes, CustomersLimit: api.RouteCustomerUsageMaxCustomers, Routes: []api.RouteCustomerUsage{}, RoutesTruncated: test.truncated}
					if test.name == "older window" {
						usage.Until = until.Add(-7 * 24 * time.Hour).Format(time.RFC3339Nano)
						usage.From = until.Add(-10 * 24 * time.Hour).Format(time.RFC3339Nano)
					}
					if test.name == "narrow window" {
						usage.From = until.Add(-time.Hour).Format(time.RFC3339Nano)
					}
					if test.active {
						usage.Routes = []api.RouteCustomerUsage{{Method: "GET", Route: "GET /old", Requests: 1, AnonymousRequests: 1, LastObservedAt: until.Add(-time.Minute).Format(time.RFC3339Nano)}}
					}
					writeJSONTest(w, usage)
				case "/v1/deployments/" + routeMigrationToDeployment + "/traffic":
					writes++
					if r.Method != "PATCH" {
						t.Errorf("unexpected mutation %s", r.Method)
					}
					var req api.UpdateDeploymentTrafficRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
						http.Error(w, "invalid traffic request", http.StatusBadRequest)
						return
					}
					if req.ExpectedServingDeploymentID == nil || *req.ExpectedServingDeploymentID != routeMigrationFromDeployment || req.TrafficPercent != 100 {
						t.Errorf("lost atomic serving guard: %+v", req)
					}
					writeJSONTest(w, api.DeploymentResponse{ID: routeMigrationToDeployment, AppID: "api-id", Status: "live", TrafficPercent: 100})
				case "/v1/deployments/" + routeMigrationToDeployment + "/promote":
					writes++
					if test.name != "bindings" || r.Method != "POST" {
						t.Errorf("unexpected bindings mutation %s", r.Method)
					}
					var req api.BindingPromotionRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
						http.Error(w, "invalid promotion request", http.StatusBadRequest)
						return
					}
					if req.ExpectedServingDeploymentID == nil || *req.ExpectedServingDeploymentID != routeMigrationFromDeployment {
						t.Errorf("bindings promotion lost serving guard: %+v", req)
					}
					writeJSONTest(w, api.BindingPromotionResponse{Deployment: api.DeploymentResponse{ID: routeMigrationToDeployment, AppID: "api-id", Status: "live", TrafficPercent: 100}, ToPercent: 100, BindingsCheck: &api.BindingCheckReport{Passed: true, DeploymentID: routeMigrationToDeployment, ExpectedDeploymentID: routeMigrationToDeployment, Scope: "default", CheckedAt: time.Now(), MaxVerificationAge: api.DefaultBindingVerificationAge.String()}})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			var output bytes.Buffer
			oldOut, oldJSON := osStdout, jsonOutput
			osStdout, jsonOutput = &output, true
			t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
			if test.name == "approved" {
				newApproval := filepath.Join(t.TempDir(), "owner-approval.json")
				approveArgs := []string{"approve", "--app", "api", "--baseline-deployment", routeMigrationFromDeployment, "--candidate-deployment", routeMigrationToDeployment, "--readiness", rp, "--mapping", mp, "--approved-by", "route-owner", "--out", newApproval}
				if code := cmdRoutesMigration(approveArgs); code != 0 {
					t.Fatalf("owner attestation exit %d: %s", code, output.String())
				}
				var attestation routeRemovalOwnerApproval
				if err := json.Unmarshal(output.Bytes(), &attestation); err != nil || attestation.ReadinessSHA == "" || attestation.MappingSHA == "" || attestation.ApprovedBy != "route-owner" {
					t.Fatalf("missing approval binding: %s", output.String())
				}
				output.Reset()
				if code := cmdRoutesMigration(approveArgs); code != 1 {
					t.Fatal("owner approval must not overwrite an existing file")
				}
				output.Reset()
				ap = newApproval
			}
			args := []string{"--app", "api", "--deployment", routeMigrationToDeployment, "--if-serving", routeMigrationFromDeployment, "--route-removal-mode", test.mode, "--route-readiness", rp, "--route-mapping", mp}
			if !test.missingApproval {
				args = append(args, "--route-owner-approval", ap)
			}
			if test.name == "bindings" {
				args = append(args, "--require-bindings")
			}
			gateArgs := []string{"gate", "--app", "api", "--baseline-deployment", routeMigrationFromDeployment, "--candidate-deployment", routeMigrationToDeployment, "--readiness", rp, "--mapping", mp, "--mode", test.mode}
			if !test.missingApproval {
				gateArgs = append(gateArgs, "--owner-approval", ap)
			}
			if code := cmdRoutesMigration(gateArgs); code != test.wantCode || writes != 0 {
				t.Fatalf("standalone gate exit %d writes %d: %s", code, writes, output.String())
			}
			output.Reset()
			if code := cmdTrafficPromote(args); code != test.wantCode || writes != test.wantWrites {
				t.Fatalf("exit=%d writes=%d: %s", code, writes, output.String())
			}
			var receipt struct {
				RouteRemovalGate *routeRemovalGateReport `json:"route_removal_gate"`
			}
			if err := json.Unmarshal(output.Bytes(), &receipt); err != nil || receipt.RouteRemovalGate == nil {
				t.Fatalf("one JSON gate receipt required: %s (%v)", output.String(), err)
			}
		})
	}
}

func TestRoutesMigrationReadinessRefreshesContractsAndBindsSnapshotDeployment(t *testing.T) {
	resetJSONOut(t)
	now := time.Now().UTC()
	baseDoc, _, _, _, _ := removalGateFixture(t, now)
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != "GET" {
			t.Errorf("readiness mutated state: %s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/deployments/" + routeMigrationFromDeployment:
			writeJSONTest(w, api.DeploymentResponse{ID: routeMigrationFromDeployment, AppID: "api-id", Status: "live", TrafficPercent: 100})
		case "/v1/apps/api/deployments/" + routeMigrationFromDeployment + "/openapi":
			writeJSONTest(w, api.OpenAPIDocResponse{AppID: "api-id", DeploymentID: routeMigrationFromDeployment, Source: "manual_upload", CapturedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), Doc: baseDoc})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	args := []string{"readiness", "--mapping", "", "--from-deployment", "api=" + routeMigrationFromDeployment, "--to-deployment", "api=" + routeMigrationFromDeployment}
	dir := t.TempDir()
	mappingPath := filepath.Join(dir, "mapping.json")
	mapping := previewCustomerMigrationMappingFile{Version: 1, Mappings: []previewCustomerMigrationMapping{{From: migrationEndpoint("api", "GET", "/old"), Successors: []previewCustomerMigrationEndpoint{migrationEndpoint("api", "GET", "/new")}}}}
	body, _ := json.Marshal(mapping)
	if err := os.WriteFile(mappingPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	args[2] = mappingPath
	for i, snapshot := range cutoverProgressFixtures(t, now) {
		snapshot.report.Deployments[0].DeploymentID = routeMigrationFromDeployment
		body, _ := json.Marshal(snapshot.report)
		path := filepath.Join(dir, fmt.Sprintf("snapshot-%d.json", i))
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--snapshot", path)
	}
	var output bytes.Buffer
	oldOut, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	if code := cmdRoutesMigration(args); code != 0 {
		t.Fatalf("readiness exit %d: %s", code, output.String())
	}
	var report previewCustomerCutoverReviewReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "owner_review_ready" || !report.OwnerReviewRequired || report.FromDeployments[0].ContractSHA == "" || report.FromDeployments[0].ContractSHA != report.ToDeployments[0].ContractSHA || reads != 4 {
		t.Fatalf("fresh, bound readiness required: reads=%d report=%+v", reads, report)
	}
}

func TestPreviewReportRouteRemovalGateUsesBoundCapturesWithoutDuplicateReads(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	baseDoc, propDoc, _, _, _ := removalGateFixture(t, time.Now().UTC())
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != "GET" {
			t.Errorf("preview made a mutation: %s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/preview/pr-42-api":
			writeJSONTest(w, api.PreviewResourceResponse{App: api.AppResponse{ID: "preview-id", Slug: "pr-42-api", PreviewOfSlug: "api"}, Parent: &api.AppResponse{ID: "api-id", Slug: "api"}, LatestDeployment: &api.DeploymentResponse{ID: routeMigrationToDeployment, AppID: "preview-id", Status: "live"}, ProductionDeployment: &api.DeploymentResponse{ID: routeMigrationFromDeployment, AppID: "api-id", Status: "live", TrafficPercent: 100}})
		case "/v1/apps/api/deployments/" + routeMigrationFromDeployment + "/openapi":
			writeJSONTest(w, api.OpenAPIDocResponse{DeploymentID: routeMigrationFromDeployment, AppID: "api-id", Source: "manual_upload", Doc: baseDoc})
		case "/v1/apps/pr-42-api/deployments/" + routeMigrationToDeployment + "/openapi":
			writeJSONTest(w, api.OpenAPIDocResponse{DeploymentID: routeMigrationToDeployment, AppID: "preview-id", Source: "manual_upload", Doc: propDoc})
		case "/v1/apps/pr-42-api/openapi/preview":
			writeJSONTest(w, api.AppOpenAPIPolicyPreviewResponse{Routes: []api.AppOpenAPIPolicyPreviewRoute{}})
		case "/v1/apps/api/deployments/" + routeMigrationFromDeployment + "/route-policy":
			writePreviewPolicySnapshotTest(w, routeMigrationFromDeployment, "api-id", []api.EdgeRuleResponse{})
		case "/v1/apps/pr-42-api/deployments/" + routeMigrationToDeployment + "/route-policy":
			writePreviewPolicySnapshotTest(w, routeMigrationToDeployment, "preview-id", []api.EdgeRuleResponse{})
		case "/v1/apps/pr-42-api/edge-rules":
			writeJSONTest(w, []api.EdgeRuleResponse{})
		case "/v1/apps/api/analytics":
			writeJSONTest(w, previewReportAnalytics(routeMigrationFromDeployment, 0, 0))
		case "/v1/apps/pr-42-api/analytics":
			writeJSONTest(w, previewReportAnalytics(routeMigrationToDeployment, 0, 0))
		case "/v1/apps/api/analytics/route-customers":
			until, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("until"))
			if err != nil {
				t.Error(err)
				http.Error(w, "invalid until", http.StatusBadRequest)
				return
			}
			writeJSONTest(w, api.RouteCustomerUsageResponse{Slug: "api", DeploymentID: routeMigrationFromDeployment, From: until.Add(-24 * time.Hour).Format(time.RFC3339Nano), Until: until.Format(time.RFC3339Nano), AsOf: until.Format(time.RFC3339Nano), Coverage: "observed_only", RoutesLimit: api.RouteCustomerUsageMaxRoutes, CustomersLimit: api.RouteCustomerUsageMaxCustomers, Routes: []api.RouteCustomerUsage{}})
		default:
			t.Errorf("unexpected read %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	oldOut, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	for _, test := range []struct {
		mode string
		code int
	}{{"report", 0}, {"enforce", 1}} {
		output.Reset()
		reads = 0
		if code := cmdPreviewReport([]string{"pr-42-api", "--route-removal-mode", test.mode}); code != test.code {
			t.Fatalf("preview %s exit %d: %s", test.mode, code, output.String())
		}
		var report previewRouteReport
		if err := json.Unmarshal(output.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		gate := report.RouteRemovalGate
		if gate == nil || gate.Status != "blocked" || len(gate.Routes) != 1 || gate.Routes[0].From.Path != "/old" || !slices.Contains(gate.Blockers, "owner_approval_missing") || reads != 10 {
			t.Fatalf("bound removal preview should show blockers using existing reads: reads=%d gate=%+v", reads, gate)
		}
	}
}
