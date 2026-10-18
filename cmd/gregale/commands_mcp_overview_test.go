package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

const overviewWebDeployment = "00000000-0000-4000-8000-000000000001"
const overviewHeldDeployment = "00000000-0000-4000-8000-000000000002"

type overviewFixture struct {
	scenario  string
	server    *httptest.Server
	mutations atomic.Int32
	toolCalls atomic.Int32
	pages     atomic.Int32
}

func newOverviewFixture(t *testing.T, scenario string) *overviewFixture {
	t.Helper()
	fixture := &overviewFixture{scenario: scenario}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fixture.serve(t, w, r) }))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func overviewRPC(body []byte) (string, int) {
	var req struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &req)
	var result any
	switch req.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": mcphosting.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}}
	case "notifications/initialized":
		return "", http.StatusAccepted
	case "server/discover":
		result = map[string]any{"supportedVersions": []string{mcphosting.ProtocolVersion}, "capabilities": map[string]any{"tools": map[string]any{}}}
	case "tools/list":
		result = map[string]any{"tools": []any{map[string]any{"name": "add", "inputSchema": map[string]any{"type": "object"}}}}
	default:
		result = map[string]any{}
	}
	encoded, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	return string(encoded), http.StatusOK
}

func (fixture *overviewFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.URL.Path == "/mcp" {
		if r.Header.Get("Authorization") != "Bearer CLIENT_TOKEN_SENTINEL" {
			t.Errorf("incorrect public MCP auth: %q", r.Header.Get("Authorization"))
		}
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		fixture.writeRPC(t, w, body)
		return
	}
	if r.Header.Get("Authorization") != "Bearer PLATFORM_TOKEN_SENTINEL" {
		t.Errorf("incorrect platform auth: %q", r.Header.Get("Authorization"))
	}
	path := r.URL.Path
	if strings.HasSuffix(path, "/mcp-probe") {
		var probe api.MCPInstanceProtocolProbeRequest
		if err := json.NewDecoder(r.Body).Decode(&probe); err != nil {
			t.Error(err)
		}
		if r.Header.Get("MCP-Probe-Authorization") != "CLIENT_TOKEN_SENTINEL" {
			t.Error("client token not used for replica probe")
		}
		method, valid := api.MCPProbeRPCMethod(probe.RPCRequest)
		if !valid {
			t.Fatalf("invalid probe %s", probe.RPCRequest)
		}
		if method == "tools/call" || method == "resources/read" || method == "prompts/get" {
			fixture.toolCalls.Add(1)
		}
		body, status := overviewRPC(probe.RPCRequest)
		parts := strings.Split(path, "/")
		writeJSONTest(w, api.MCPInstanceProtocolProbeResponse{AppID: "web-id", DeploymentID: overviewWebDeployment, InstanceID: parts[len(parts)-2], HTTPStatus: status, ContentType: "application/json", ProtocolVersion: mcphosting.ProtocolVersion, Body: body})
		return
	}
	if r.Method != http.MethodGet {
		fixture.mutations.Add(1)
		http.Error(w, "unexpected write", 500)
		return
	}
	switch path {
	case "/v1/apps/reports-web/policy/status":
		writeJSONTest(w, api.RuntimePolicyStatusResponse{AppID: "web-id", EdgeRules: api.RuntimePolicyComponentStatus{Scope: "account", State: "active", DesiredRevision: 42}, MCPConvergence: &api.MCPPolicyConvergenceSnapshot{State: "active", DesiredRevision: 42, ServingGateways: 2, DeploymentID: overviewWebDeployment}})
	case "/v1/apps/reports-web/alerts":
		writeJSONTest(w, []api.AlertRuleResponse{})
	case "/v1/deployments/" + overviewWebDeployment + "/mcp-admissions", "/v1/deployments/" + overviewHeldDeployment + "/mcp-admissions":
		writeJSONTest(w, api.MCPAdmissionHistoryResponse{Items: []api.MCPAdmissionEvidence{}, Limit: func() int {
			if r.URL.Query().Get("outcome") != "" {
				return 1
			}
			return 50
		}()})
	case "/v1/apps/reports-web":
		writeJSONTest(w, api.AppResponse{ID: "web-id", Slug: "reports-web", Status: "active"})
	case "/v1/apps/reports-worker":
		if fixture.scenario == "worker timeout" {
			<-r.Context().Done()
			return
		}
		writeJSONTest(w, overviewWorker())
	case "/v1/apps/reports-observer":
		writeJSONTest(w, api.AppResponse{ID: "observer-id", Slug: "reports-observer", Status: "active", WorkloadClass: "worker", DeploymentAvailability: api.AppDeploymentAvailabilityLive, Manifest: api.AppManifest{Entrypoint: []string{"node", "tasks-observer.js"}, WorkerReplicas: &api.WorkerScaling{Min: 1, Max: 1}, Env: map[string]string{"MCP_TASK_NAMESPACE": "reports", "MCP_TASKS_SCALING_APP_SLUG": "reports-worker"}}})
	case "/v1/apps/reports-web/deployments":
		fixture.pages.Add(1)
		if fixture.scenario == "held" && r.URL.Query().Get("before") == "older" {
			writeJSONTest(w, api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: overviewHeldDeployment, Status: "ready", PromotionHold: &api.DeploymentPromotionHold{Code: api.CodeMCPTaskCompatibilityGate, Detail: "PRIVATE_ERROR_BODY", HeldAt: time.Now().Add(-5 * time.Minute), RecoveryAttempts: 2}}}})
			return
		}
		page := api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: overviewWebDeployment, Status: "live", TrafficPercent: 100}}}
		if fixture.scenario == "held" {
			page.NextBefore = "older"
		}
		writeJSONTest(w, page)
	case "/v1/apps/reports-worker/deployments":
		writeJSONTest(w, api.DeploymentListResponse{Items: []api.DeploymentResponse{}})
	case "/v1/apps/reports-web/instances":
		writeJSONTest(w, []api.InstanceResponse{{ID: "replica-a", DeploymentID: overviewWebDeployment, State: "running"}, {ID: "replica-b", DeploymentID: overviewWebDeployment, State: "running"}})
	case "/v1/apps/reports-worker/instances":
		writeJSONTest(w, []api.InstanceResponse{{ID: "worker-a", State: "running"}})
	case "/v1/apps/reports-observer/instances":
		writeJSONTest(w, []api.InstanceResponse{{ID: "observer-a", State: "running"}})
	case "/v1/apps/reports-worker/custom-metrics":
		if fixture.scenario == "metrics denied" {
			api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Denied", "PRIVATE_ERROR_BODY"))
			return
		}
		metrics := []api.CustomMetricResponse{}
		for _, name := range []string{mcpTasksOutstandingMetric, mcpTasksOldestAgeMetric, mcpTasksWithoutCompatibleWorkerMetric, mcpTasksObserverHeartbeatMetric} {
			if fixture.scenario == "missing metric" && name == mcpTasksOutstandingMetric {
				continue
			}
			value := float64(0)
			if name == mcpTasksObserverHeartbeatMetric {
				value = 1
			}
			if fixture.scenario == "incompatible" && name == mcpTasksWithoutCompatibleWorkerMetric {
				value = 4
			}
			stale := fixture.scenario == "stale metric" && name == mcpTasksOutstandingMetric || fixture.scenario == "stale observer" && name == mcpTasksObserverHeartbeatMetric
			metrics = append(metrics, api.CustomMetricResponse{Name: name, Value: value, ObservedAt: time.Now(), Stale: stale})
		}
		writeJSONTest(w, api.CustomMetricListResponse{Metrics: metrics, FreshnessS: 45})
	case "/v1/apps/reports-worker/mcp-task-compatibility":
		writeJSONTest(w, api.MCPTaskCompatibilityReportResponse{Published: true, Handlers: []api.MCPTaskHandlerCompatibility{}, FreshnessSeconds: 45})
	case "/v1/deployments/" + overviewWebDeployment + "/url":
		writeJSONTest(w, api.DeploymentPreviewURL{DeploymentID: overviewWebDeployment, URL: fixture.server.URL, Alive: true})
	default:
		if strings.HasSuffix(path, "/mcp-health") {
			parts := strings.Split(path, "/")
			id := parts[len(parts)-2]
			digest := strings.Repeat("a", 64)
			if fixture.scenario == "catalog drift" && id == "replica-b" {
				digest = strings.Repeat("b", 64)
			}
			if fixture.scenario == "malformed health" {
				digest = "PRIVATE_ERROR_BODY"
			}
			body := fmt.Sprintf(`{"status":"ok","catalog_revision":1,"catalog_sha256":%q}`, digest)
			writeJSONTest(w, api.MCPInstanceHealthProbeResponse{AppID: "web-id", DeploymentID: overviewWebDeployment, InstanceID: id, HTTPStatus: 200, HealthBody: body})
			return
		}
		t.Errorf("unexpected read %s", path)
		http.NotFound(w, r)
	}
}

func (fixture *overviewFixture) writeRPC(t *testing.T, w http.ResponseWriter, body []byte) {
	t.Helper()
	var request struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &request)
	if request.Method == "tools/call" || request.Method == "resources/read" || request.Method == "prompts/get" {
		fixture.toolCalls.Add(1)
	}
	result, status := overviewRPC(body)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("MCP-Protocol-Version", mcphosting.ProtocolVersion)
	w.WriteHeader(status)
	_, _ = fmt.Fprint(w, result)
}

func overviewWorker() api.AppResponse {
	return api.AppResponse{ID: "worker-id", Slug: "reports-worker", Status: "active", WorkloadClass: "worker", Manifest: api.AppManifest{WorkerReplicas: &api.WorkerScaling{Min: 0, Max: 10, Metric: api.ScalingMetricCustom, Name: mcpTasksOutstandingMetric, Target: 1, ObserverAppSlug: "reports-observer"}, Env: map[string]string{"MCP_TASK_NAMESPACE": "reports"}}, ScalingPolicy: &api.ScalingPolicy{MinInstances: 0, MaxInstances: 10, Target: &api.ScalingTarget{Metric: api.ScalingMetricCustom, Name: mcpTasksOutstandingMetric, Value: 1}}}
}

func TestMCPOverviewCLIReadOnlyAndPartialEvidence(t *testing.T) {
	for _, tc := range []struct {
		scenario, health string
		exit             int
	}{
		{"healthy", "healthy", 0}, {"missing metric", "unknown", 1}, {"stale metric", "unknown", 1}, {"incompatible", "degraded", 1}, {"stale observer", "unknown", 1}, {"catalog drift", "degraded", 1}, {"malformed health", "degraded", 1}, {"held", "degraded", 1}, {"metrics denied", "unknown", 1},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			fixture := newOverviewFixture(t, tc.scenario)
			t.Setenv("FAAS_API", fixture.server.URL)
			t.Setenv("FAAS_TOKEN", "PLATFORM_TOKEN_SENTINEL")
			t.Setenv("MCP_OVERVIEW_TOKEN", "CLIENT_TOKEN_SENTINEL")
			out, stderr, restore := swapIO(t)
			defer restore()
			oldJSON := jsonOutput
			jsonOutput = true
			defer func() { jsonOutput = oldJSON }()
			code := cmdMCP([]string{"overview", "--app", "reports-web", "--worker-app", "reports-worker", "--token-env", "MCP_OVERVIEW_TOKEN"})
			if code != tc.exit {
				t.Fatalf("exit %d, want %d: %s\n%s", code, tc.exit, out.String(), stderr())
			}
			var report mcpOverviewReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Health != tc.health {
				t.Fatalf("health %q, want %q: %s", report.Health, tc.health, out.String())
			}
			for _, private := range []string{"PLATFORM_TOKEN_SENTINEL", "CLIENT_TOKEN_SENTINEL", "PRIVATE_ERROR_BODY"} {
				if strings.Contains(out.String(), private) {
					t.Fatalf("leaked %s", private)
				}
			}
			if fixture.mutations.Load() != 0 || fixture.toolCalls.Load() != 0 {
				t.Fatal("overview changed state or executed a tool")
			}
			if tc.scenario == "healthy" && (report.Serving.Catalog.CheckedReplicas != 2 || report.Tasks.Observer == nil || !report.Tasks.Observer.HeartbeatFresh || report.Tasks.Summary.Health != "healthy") {
				t.Fatalf("missing joined evidence: %s", out.String())
			}
			if tc.scenario == "held" && (fixture.pages.Load() != 2 || len(report.Releases.Holds) != 1 || report.Releases.Holds[0].AgeSeconds == nil || !report.Releases.Holds[0].Retry.Mutating) {
				t.Fatalf("missing paginated hold evidence: %s", out.String())
			}
			if tc.scenario == "missing metric" && report.Tasks.Summary.Metrics[0].Present {
				t.Fatal("missing gauge became a known zero")
			}
		})
	}
}

func TestMCPOverviewDeadlinePreservesServingEvidence(t *testing.T) {
	fixture := newOverviewFixture(t, "worker timeout")
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	report := collectMCPOverview(ctx, NewClient(fixture.server.URL, "PLATFORM_TOKEN_SENTINEL"), mcpOverviewOptions{App: "reports-web", Worker: "reports-worker", Endpoint: "/mcp", Timeout: time.Second, Roles: []mcpReleaseRole{{Name: "default", token: "CLIENT_TOKEN_SENTINEL"}}})
	if time.Since(start) > 2*time.Second || report.Tasks.State != "unknown" || report.Serving.State != "healthy" || report.Releases.Complete {
		t.Fatalf("lost partial evidence or deadline: %+v", report)
	}
}

func TestMCPOverviewWithoutWorkerDoesNotClaimTasksAreDisabled(t *testing.T) {
	fixture := newOverviewFixture(t, "healthy")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	report := collectMCPOverview(ctx, NewClient(fixture.server.URL, "PLATFORM_TOKEN_SENTINEL"), mcpOverviewOptions{App: "reports-web", Endpoint: "/mcp", Timeout: time.Second, Roles: []mcpReleaseRole{{Name: "default", token: "CLIENT_TOKEN_SENTINEL"}}})
	if report.Health != "healthy" || report.Tasks.State != "not_requested" || report.Tasks.Summary != nil || report.Tasks.Observer != nil || !report.Releases.Complete {
		t.Fatalf("incorrect inspection scope: %+v", report)
	}
}

func TestMCPOverviewHoldsHideRetiredAndActiveRetryActions(t *testing.T) {
	rows := []api.DeploymentResponse{{ID: overviewHeldDeployment, Status: "ready", PromotionHold: &api.DeploymentPromotionHold{Code: api.CodeMCPTaskCompatibilityGate, RecoveryInProgress: true}}, {ID: "retired", Status: "retired", PromotionHold: &api.DeploymentPromotionHold{}}}
	holds := mcpOverviewHolds("reports-web", rows, time.Now())
	if len(holds) != 1 || holds[0].Retry != nil || holds[0].AgeSeconds != nil {
		t.Fatalf("unsafe retry or fabricated hold age: %+v", holds)
	}
}

func TestMCPOverviewRejectsInvalidSelectorsBeforeReads(t *testing.T) {
	_, _, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{{}, {"--app", "reports-web", "--worker-app", "reports-web"}, {"--app", "reports-web", "--observer-app", "reports-observer"}, {"--app", "reports-web", "--timeout", "0"}, {"--app", "reports-web", "--token-env", "MCP_TOKEN", "--release-policy", "policy.json"}} {
		if cmdMCPOverview(args) == 0 {
			t.Fatalf("invalid flags accepted: %v", args)
		}
	}
}
