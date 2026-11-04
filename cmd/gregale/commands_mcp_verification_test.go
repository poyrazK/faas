package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpVerificationFixture struct {
	endpoint, control                     *httptest.Server
	promoted                              atomic.Bool
	promotions, parks, exact, initializes atomic.Int64
	badInitialize                         bool
	badReplica                            bool
	badPing                               bool
	statefulReplica                       bool
	cancelOnProbe                         context.CancelFunc
	inFlight, peak                        atomic.Int64
	latency                               time.Duration
}

func (f *mcpVerificationFixture) trackRequest() func() {
	current := f.inFlight.Add(1)
	for peak := f.peak.Load(); current > peak; peak = f.peak.Load() {
		if f.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	return func() { f.inFlight.Add(-1) }
}
func mcpVerificationRPCResult(raw []byte) (string, json.RawMessage) {
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	_ = json.Unmarshal(raw, &req)
	var result any = map[string]any{}
	switch req.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": mcphosting.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}}
	case "server/discover":
		result = map[string]any{"supportedVersions": []string{mcphosting.ProtocolVersion}, "capabilities": map[string]any{"tools": map[string]any{}}}
	case "tools/list":
		result = map[string]any{"tools": []any{}}
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	return req.Method, body
}

func writeMCPVerificationProbe(t *testing.T, w http.ResponseWriter, r *http.Request, app, deployment, instance string) {
	t.Helper()
	var request api.MCPInstanceProtocolProbeRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Errorf("decode protocol probe: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	method, body := mcpVerificationRPCResult(request.RPCRequest)
	response := api.MCPInstanceProtocolProbeResponse{AppID: app, DeploymentID: deployment, InstanceID: instance, HTTPStatus: http.StatusOK, ContentType: "application/json", ProtocolVersion: request.ProtocolVersion, Body: string(body)}
	if method == "notifications/initialized" {
		response.HTTPStatus, response.ContentType, response.Body = http.StatusAccepted, "", ""
	}
	_ = json.NewEncoder(w).Encode(response)
}
func waitMCPVerificationFixture(ctx context.Context, d time.Duration) bool {
	if d == 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func newMCPVerificationFixture(t *testing.T, badInitialize bool, latency time.Duration) *mcpVerificationFixture {
	f := &mcpVerificationFixture{badInitialize: badInitialize, latency: latency}
	f.endpoint = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer f.trackRequest()()
		if !waitMCPVerificationFixture(r.Context(), f.latency) {
			return
		}
		if r.Header.Get("Origin") != "" {
			w.WriteHeader(403)
			return
		}
		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		method, body := mcpVerificationRPCResult(raw)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("MCP-Protocol-Version", mcphosting.ProtocolVersion)
		if method == "initialize" {
			f.initializes.Add(1)
			if f.badInitialize {
				http.Error(w, "initialization unavailable", 500)
				return
			}
		}
		if method == "ping" && f.badPing {
			http.Error(w, "ping unavailable", 500)
			return
		}
		if method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(f.endpoint.Close)
	f.control = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		candidate := api.DeploymentResponse{ID: "candidate", AppID: "app", Status: main.StatusLive, Scope: "default"}
		stable := api.DeploymentResponse{ID: "stable", AppID: "app", Status: main.StatusLive}
		if f.promoted.Load() {
			candidate.TrafficPercent = 100
		} else {
			stable.TrafficPercent = 100
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(api.CapabilitiesResponse{ConditionalParking: true})
		case "/v1/apps/web":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app", Slug: "web", URL: f.endpoint.URL, StreamingEnabled: true, PublicAuth: api.PublicAuthStatus{Mode: api.AppPublicAuthModeOpen}})
		case "/v1/apps/web/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{candidate, stable}})
		case "/v1/deployments/candidate":
			_ = json.NewEncoder(w).Encode(candidate)
		case "/v1/deployments/candidate/url":
			_ = json.NewEncoder(w).Encode(api.DeploymentPreviewURL{URL: f.endpoint.URL, Alive: true})
		case "/v1/apps/web/instances":
			instances := []api.InstanceResponse{{ID: "instance-a", AppID: "app", DeploymentID: "candidate", State: "running"}}
			if f.badReplica || f.statefulReplica {
				instances = append(instances, api.InstanceResponse{ID: "instance-b", AppID: "app", DeploymentID: "candidate", State: "running"})
			}
			_ = json.NewEncoder(w).Encode(instances)
		case "/v1/apps/web/deployments/candidate/instances/instance-a/mcp-health":
			_ = json.NewEncoder(w).Encode(api.MCPInstanceHealthProbeResponse{AppID: "app", DeploymentID: "candidate", InstanceID: "instance-a", HTTPStatus: 200, HealthBody: fmt.Sprintf(`{"status":"ok","catalog_revision":1,"catalog_sha256":%q}`, strings.Repeat("a", 64))})
		case "/v1/apps/web/deployments/candidate/instances/instance-a/mcp-probe", "/v1/apps/web/deployments/candidate/instances/instance-b/mcp-probe":
			defer f.trackRequest()()
			if f.cancelOnProbe != nil {
				f.cancelOnProbe()
			}
			if !waitMCPVerificationFixture(r.Context(), f.latency) {
				return
			}
			f.exact.Add(1)
			var request api.MCPInstanceProtocolProbeRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			method, body := mcpVerificationRPCResult(request.RPCRequest)
			result := api.MCPInstanceProtocolProbeResponse{AppID: "app", DeploymentID: "candidate", InstanceID: "instance-a", HTTPStatus: 200, ContentType: "application/json", ProtocolVersion: mcphosting.ProtocolVersion, Body: string(body)}
			if strings.Contains(r.URL.Path, "/instance-b/") {
				result.InstanceID = "instance-b"
			}
			if method == "notifications/initialized" {
				result.HTTPStatus = 202
				result.Body = ""
				result.ContentType = ""
			}
			if method == "initialize" && (f.badInitialize || (f.badReplica && result.InstanceID == "instance-b")) {
				result.HTTPStatus = 500
				result.Body = "initialization unavailable"
			}
			if f.statefulReplica && result.InstanceID == "instance-b" {
				result.SessionIDPresent = true
				result.SessionID = "unexpected-session"
			}
			_ = json.NewEncoder(w).Encode(result)
		case "/v1/apps/web/mcp-readiness-policy":
			_ = json.NewEncoder(w).Encode(api.MCPReadinessPolicy{Enabled: false})
		case "/v1/apps/observer":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "observer", Slug: "observer", WorkloadClass: "worker", Manifest: api.AppManifest{WorkerReplicas: &api.WorkerScaling{Min: 1}, Env: mcpNativeObserverFixtureEnv()}})
		case "/v1/apps/observer/instances":
			_ = json.NewEncoder(w).Encode([]api.InstanceResponse{{ID: "observer-instance", State: "running"}})
		case "/v1/apps/metrics/custom-metrics":
			_ = json.NewEncoder(w).Encode(api.CustomMetricListResponse{Metrics: []api.CustomMetricResponse{{Name: "mcp_tasks_observer_heartbeat", Value: 1, Stale: false, ObservedAt: time.Now()}}})
		case "/v1/apps/previous/deployments/latest":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "old", AppID: "previous", Status: main.StatusLive})
		case "/v1/deployments/candidate/traffic":
			f.promotions.Add(1)
			f.promoted.Store(true)
			candidate.TrafficPercent = 100
			_ = json.NewEncoder(w).Encode(candidate)
		case "/v1/apps/previous/park/conditional":
			f.parks.Add(1)
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.control.Close)
	return f
}
func writeMCPVerificationConfig(t *testing.T, count int) (string, string, mcphosting.Config) {
	t.Helper()
	root := t.TempDir()
	body, err := os.ReadFile("templates/mcp-node/gregale-mcp.json")
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), `"legacy": true`, `"legacy": false`, 1))
	if err = os.WriteFile(filepath.Join(root, "gregale-mcp.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := mcphosting.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools"}, Tools: []mcphosting.Tool{}})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := mcphosting.MarshalContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "baseline.json"), baseline, 0600); err != nil {
		t.Fatal(err)
	}
	roles := []map[string]string{}
	for i := 0; i < count; i++ {
		roles = append(roles, map[string]string{"name": fmt.Sprintf("role-%d", i), "baseline": "baseline.json"})
	}
	policy, _ := json.Marshal(map[string]any{"version": 1, "roles": roles})
	path := filepath.Join(root, "release.json")
	if err = os.WriteFile(path, policy, 0600); err != nil {
		t.Fatal(err)
	}
	return root, path, cfg
}

func TestMCPNativeReleaseRequiresProtocolQualification(t *testing.T) {
	t.Setenv("MCP_TASK_NAMESPACE", mcpNativeTestNamespace)
	old := *main.McpNativeCheckWorkers
	defer func() { *main.McpNativeCheckWorkers = old }()
	*main.McpNativeCheckWorkers = func(context.Context, *main.Client, main.McpNativeReleasePlan, *main.McpNativeReleaseState) error {
		return nil
	}
	for _, test := range []struct {
		name                                                   string
		badInitialize, badReplica, badPing, stateful, noPolicy bool
	}{
		{name: "healthy"}, {name: "healthy without release policy", noPolicy: true},
		{name: "initialization fails", badInitialize: true}, {name: "one replica fails", badReplica: true},
		{name: "public ping fails", badPing: true}, {name: "one replica creates a session", stateful: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, restore := main.SwapIO(t)
			defer restore()
			f := newMCPVerificationFixture(t, test.badInitialize, 0)
			f.badReplica, f.badPing, f.statefulReplica = test.badReplica, test.badPing, test.stateful
			root, policy, _ := writeMCPVerificationConfig(t, 1)
			if test.noPolicy {
				policy = ""
			}
			p := main.McpNativeReleasePlan{WebApp: "web", WebPath: root, WorkerPath: root, WorkerApp: "worker", ObserverApp: "observer", ObserverMetricApp: "metrics", PreviousWorkerApps: []string{"previous"}, ReleasePolicy: policy}
			s := main.McpNativeReleaseState{Version: 1, WebDeployment: "candidate", WorkerDeployment: "worker-candidate", ServingDeployment: "stable", ServingCaptured: true, PreviousDeployments: map[string]string{"previous": "old"}, WorkerIDs: []string{"12345678-1234-4234-8234-123456789abc"}, Parked: map[string]bool{}}
			t.Setenv("MCP_TASK_RELEASE_INPUT", `{"replacementWorkerIDs":["12345678-1234-4234-8234-123456789abc"]}`)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err := main.DrainMCPNativeRelease(ctx, main.NewClient(f.control.URL, "operator"), p, &s, filepath.Join(t.TempDir(), "state.json"))
			wantFailure := test.badInitialize || test.badReplica || test.badPing || test.stateful
			if wantFailure {
				if err == nil || f.promotions.Load() != 0 || f.parks.Load() != 0 {
					t.Fatalf("invalid endpoint changed serving state: error=%v promotions=%d parks=%d", err, f.promotions.Load(), f.parks.Load())
				}
			} else if err != nil || f.promotions.Load() != 1 || f.parks.Load() != 1 || f.initializes.Load() == 0 || f.exact.Load() == 0 {
				t.Fatalf("healthy native release failed: error=%v promotions=%d parks=%d public_initializations=%d exact_probes=%d", err, f.promotions.Load(), f.parks.Load(), f.initializes.Load(), f.exact.Load())
			}
		})
	}
}

func TestMCPReleaseVerificationScalesAcrossRoles(t *testing.T) {
	out, _, restore := main.SwapIO(t)
	defer restore()
	oldJSON := *main.JsonOutput
	*main.JsonOutput = true
	defer func() { *main.JsonOutput = oldJSON }()
	f := newMCPVerificationFixture(t, false, 50*time.Millisecond)
	_, path, cfg := writeMCPVerificationConfig(t, api.MCPReleaseMaxRoles)
	roles, err := main.LoadMCPReleasePolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	code := main.FinishMCPDeployWithSmoke(ctx, main.NewClient(f.control.URL, "operator"), "web", "candidate", cfg, "", roles, nil, nil)
	if code != 0 || f.promotions.Load() != 1 {
		t.Fatalf("healthy multi-role deployment rejected: %s", out.String())
	}
	if peak := f.peak.Load(); peak < 2 || peak > 4 {
		t.Fatalf("verification concurrency must be bounded: peak=%d", peak)
	}
	var receipt struct {
		Readiness []struct {
			Role string `json:"role"`
		} `json:"readiness"`
	}
	if err = json.Unmarshal(out.Bytes(), &receipt); err != nil || len(receipt.Readiness) != len(roles) {
		t.Fatalf("incomplete readiness receipt: %s error=%v", out.String(), err)
	}
	for index, role := range receipt.Readiness {
		if role.Role != fmt.Sprintf("role-%d", index) {
			t.Fatalf("unstable role order at %d: %s", index, role.Role)
		}
	}
}

func TestMCPReleaseVerificationCancellationPreservesServingState(t *testing.T) {
	_, _, restore := main.SwapIO(t)
	defer restore()
	f := newMCPVerificationFixture(t, false, 10*time.Millisecond)
	_, path, cfg := writeMCPVerificationConfig(t, 8)
	roles, err := main.LoadMCPReleasePolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	f.cancelOnProbe = cancel
	started := time.Now()
	code := main.FinishMCPDeployWithSmoke(ctx, main.NewClient(f.control.URL, "operator"), "web", "candidate", cfg, "", roles, nil, nil)
	if code == 0 || f.promotions.Load() != 0 || f.parks.Load() != 0 || ctx.Err() == nil {
		t.Fatalf("cancellation changed serving state: code=%d promotions=%d parks=%d error=%v", code, f.promotions.Load(), f.parks.Load(), ctx.Err())
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("cancelled probes did not stop promptly: %s", elapsed)
	}
}
