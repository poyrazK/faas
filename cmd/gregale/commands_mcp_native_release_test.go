package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPNativeReleasePlanAndJournal(t *testing.T) {
	root := t.TempDir()
	web := filepath.Join(root, "web")
	worker := filepath.Join(root, "worker")
	for _, dir := range []string{web, worker} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := mcpNativeReleasePlan{WebApp: "web", WorkerApp: "candidate", ObserverApp: "observer", ObserverMetricApp: "candidate", WebPath: "web", WorkerPath: "worker", PreviousWorkerApps: []string{"previous"}}
	body, _ := json.Marshal(p)
	plan := filepath.Join(root, "plan.json")
	os.WriteFile(plan, body, 0600)
	loaded, err := readMCPNativePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WorkerPath != worker || loaded.TimeoutSeconds != 300 {
		t.Fatalf("plan=%+v", loaded)
	}
	fingerprint, err := mcpNativeFingerprint(loaded)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_API", "https://another-control-plane.example")
	changedAPI, err := mcpNativeFingerprint(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if changedAPI == fingerprint {
		t.Fatal("release fingerprint omitted effective control-plane API")
	}
	t.Setenv("FAAS_API", "https://third-control-plane.example")
	// Continue with a consistent API context for journal/source checks.
	fingerprint, err = mcpNativeFingerprint(loaded)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "release.json")
	s, err := loadMCPNativeState(statePath, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	s.WorkerDeployment = "pinned"
	s.PendingSubmission = "web"
	if err := saveMCPNativeState(statePath, s); err != nil {
		t.Fatal(err)
	}
	restored, err := loadMCPNativeState(statePath, fingerprint)
	if err != nil || restored.WorkerDeployment != "pinned" || restored.PendingSubmission != "web" {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	info, _ := os.Stat(statePath)
	if info.Mode().Perm() != 0600 {
		t.Fatal("journal permissions")
	}
	os.WriteFile(filepath.Join(worker, "server.js"), []byte("changed"), 0600)
	changed, _ := mcpNativeFingerprint(loaded)
	if _, err = loadMCPNativeState(statePath, changed); err == nil {
		t.Fatal("changed source resumed old release")
	}
	p.PreviousWorkerApps = []string{"candidate"}
	body, _ = json.Marshal(p)
	os.WriteFile(plan, body, 0600)
	if _, err = readMCPNativePlan(plan); err == nil {
		t.Fatal("in-place worker replacement accepted")
	}
}
func TestMCPNativeWorkerLogsRequireCurrentDeploymentInstances(t *testing.T) {
	id := "12345678-1234-1234-1234-123456789abc"
	event := func(instance, worker string) string {
		line, _ := json.Marshal(map[string]string{"event": "mcp_task_worker_started", "workerID": worker})
		envelope, _ := json.Marshal(map[string]string{"instance": instance, "line": string(line)})
		return "event: log\ndata: " + string(envelope) + "\n\n"
	}
	instances := []api.InstanceResponse{{ID: "current", DeploymentID: "candidate", State: "running"}, {ID: "old", DeploymentID: "previous", State: "running"}}
	ids, err := mcpNativeWorkerLogs(strings.NewReader(event("old", id)+event("current", id)), instances, "candidate")
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	if _, err = mcpNativeWorkerLogs(strings.NewReader(event("old", id)), instances, "candidate"); err == nil {
		t.Fatal("old startup satisfied candidate")
	}
	instances = append(instances, api.InstanceResponse{ID: "missing", DeploymentID: "candidate", State: "running"})
	if _, err = mcpNativeWorkerLogs(strings.NewReader(event("current", id)), instances, "candidate"); err == nil {
		t.Fatal("missing replica startup accepted")
	}
}
func TestMCPNativeReleaseResumesPinnedDeploymentWithoutSubmitting(t *testing.T) {
	submissions := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			submissions++
		}
		switch r.URL.Path {
		case "/v1/deployments/pinned":
			json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "pinned", AppID: "app", Status: statusLive})
		case "/v1/apps/worker":
			json.NewEncoder(w).Encode(api.AppResponse{ID: "app", Slug: "worker"})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	s := mcpNativeReleaseState{WorkerDeployment: "pinned"}
	if err := mcpNativeDeploy(context.Background(), NewClient(server.URL, "operator"), "unused", "worker", false, &s, "unused"); err != nil {
		t.Fatal(err)
	}
	if submissions != 0 {
		t.Fatal("resume submitted another deployment")
	}
}

func TestMCPNativeReleaseHealthAndDrainRecovery(t *testing.T) {
	oldCheck := mcpNativeCheckWorkers
	mcpNativeCheckWorkers = func(context.Context, *Client, mcpNativeReleasePlan, *mcpNativeReleaseState) error { return nil }
	defer func() { mcpNativeCheckWorkers = oldCheck }()

	for _, tc := range []struct {
		name                                                                   string
		stale, changed, badEndpoint, committed, workerLost, start, postFailure bool
		wantError                                                              bool
	}{{name: "healthy"}, {name: "resume start", start: true}, {name: "stale observer", stale: true, wantError: true}, {name: "old generation changed", changed: true, wantError: true}, {name: "bad MCP endpoint", badEndpoint: true, wantError: true}, {name: "lost promotion response", committed: true}, {name: "replacement lost", workerLost: true, wantError: true}, {name: "canonical fails after promotion", postFailure: true, wantError: true}} {
		t.Run(tc.name, func(t *testing.T) {
			mcpNativeCheckWorkers = func(context.Context, *Client, mcpNativeReleasePlan, *mcpNativeReleaseState) error {
				if tc.workerLost {
					return fmt.Errorf("replacement lost")
				}
				return nil
			}
			cfgDir := t.TempDir()
			config, err := os.ReadFile("templates/mcp-node/gregale-mcp.json")
			if err != nil {
				t.Fatal(err)
			}
			config = []byte(strings.Replace(string(config), `"legacy": true`, `"legacy": false`, 1))
			if err := os.WriteFile(filepath.Join(cfgDir, "gregale-mcp.json"), config, 0600); err != nil {
				t.Fatal(err)
			}
			promoted := tc.committed
			parks := 0
			promotions := 0
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("operator key sent to MCP endpoint")
				}
				if tc.badEndpoint || (tc.postFailure && promoted) {
					w.WriteHeader(404)
					return
				}
				if r.Header.Get("Origin") != "" {
					w.WriteHeader(403)
					return
				}
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				json.NewDecoder(r.Body).Decode(&req)
				w.Header().Set("Content-Type", "application/json")
				switch req.Method {
				case "server/discover":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":["%s"],"capabilities":{"tools":{}}}}`, req.ID, mcphosting.ProtocolVersion)
				case "tools/list":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[]}}`, req.ID)
				default:
					t.Errorf("unexpected MCP call %s", req.Method)
				}
			}))
			defer endpoint.Close()
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer operator" {
					t.Error("missing control credential")
				}
				dep := api.DeploymentResponse{ID: "candidate", AppID: "web-id", Status: statusLive}
				stable := api.DeploymentResponse{ID: "stable", AppID: "web-id", Status: statusLive}
				if promoted {
					dep.TrafficPercent = 100
				} else {
					stable.TrafficPercent = 100
				}
				switch r.URL.Path {
				case "/v1/account":
					json.NewEncoder(w).Encode(api.AccountResponse{Plan: "pro"})
				case "/v1/apps/worker":
					json.NewEncoder(w).Encode(api.AppResponse{ID: "worker-id", Slug: "worker", WorkloadClass: "worker", Manifest: api.AppManifest{WorkerReplicas: &api.WorkerScaling{Min: 1}}})
				case "/v1/deployments/worker-candidate":
					json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "worker-candidate", AppID: "worker-id", Status: statusLive})
				case "/v1/apps/worker/instances":
					json.NewEncoder(w).Encode([]api.InstanceResponse{{ID: "worker-instance", DeploymentID: "worker-candidate", State: "running"}})
				case "/v1/apps/worker/logs":
					fmt.Fprint(w, "event: log\ndata: {\"instance\":\"worker-instance\",\"line\":\"{\\\"event\\\":\\\"mcp_task_worker_started\\\",\\\"workerID\\\":\\\"12345678-1234-1234-1234-123456789abc\\\"}\"}\n\n")
				case "/v1/apps/web":
					json.NewEncoder(w).Encode(api.AppResponse{ID: "web-id", Slug: "web", URL: endpoint.URL, StreamingEnabled: true, PublicAuth: api.PublicAuthStatus{Mode: api.AppPublicAuthModeOpen}})
				case "/v1/apps/web/deployments":
					json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{dep, stable}})
				case "/v1/deployments/candidate":
					json.NewEncoder(w).Encode(dep)
				case "/v1/deployments/candidate/url":
					json.NewEncoder(w).Encode(api.DeploymentPreviewURL{URL: endpoint.URL, Alive: true})
				case "/v1/apps/observer":
					json.NewEncoder(w).Encode(api.AppResponse{ID: "observer-id", Slug: "observer", WorkloadClass: "worker", Manifest: api.AppManifest{WorkerReplicas: &api.WorkerScaling{Min: 1}}})
				case "/v1/apps/observer/instances":
					json.NewEncoder(w).Encode([]api.InstanceResponse{{ID: "obs", State: "running"}})
				case "/v1/apps/metrics/custom-metrics":
					json.NewEncoder(w).Encode(api.CustomMetricListResponse{Metrics: []api.CustomMetricResponse{{Name: "mcp_tasks_observer_heartbeat", Value: 1, Stale: tc.stale, ObservedAt: time.Now()}}})
				case "/v1/apps/previous/deployments/latest":
					id := "old"
					if tc.changed {
						id = "unrelated"
					}
					json.NewEncoder(w).Encode(api.DeploymentResponse{ID: id, AppID: "previous-id", Status: statusLive})
				case "/v1/deployments/candidate/traffic":
					var body api.UpdateDeploymentTrafficRequest
					json.NewDecoder(r.Body).Decode(&body)
					if body.ExpectedServingDeploymentID == nil || *body.ExpectedServingDeploymentID != "stable" {
						t.Error("promotion omitted expected serving")
					}
					promotions++
					promoted = true
					json.NewEncoder(w).Encode(dep)
				case "/v1/apps/previous/park":
					parks++
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer control.Close()
			p := mcpNativeReleasePlan{WebApp: "web", WebPath: cfgDir, WorkerPath: cfgDir, WorkerApp: "worker", ObserverApp: "observer", ObserverMetricApp: "metrics", PreviousWorkerApps: []string{"previous"}}
			s := mcpNativeReleaseState{Version: 1, WebDeployment: "candidate", WorkerDeployment: "worker-candidate", ServingDeployment: "stable", ServingCaptured: true, PreviousDeployments: map[string]string{"previous": "old"}, WorkerIDs: []string{"id"}, Parked: map[string]bool{}}
			t.Setenv("MCP_TASK_RELEASE_INPUT", `{"replacementWorkerIDs":["id"]}`)
			if tc.start {
				err = startMCPNativeRelease(context.Background(), NewClient(control.URL, "operator"), p, &s, filepath.Join(t.TempDir(), "state.json"))
				if err != nil || s.Stage != "replacements_started" || len(s.WorkerIDs) != 1 || parks != 0 || promotions != 0 {
					t.Fatalf("resumed start state=%+v err=%v", s, err)
				}
				return
			}
			err = drainMCPNativeRelease(context.Background(), NewClient(control.URL, "operator"), p, &s, filepath.Join(t.TempDir(), "state.json"))
			if (err != nil) != tc.wantError {
				t.Fatalf("err=%v", err)
			}
			if tc.wantError && (parks != 0 || (!tc.postFailure && promotions != 0)) {
				t.Fatalf("failed health changed rollout: parks=%d promotions=%d", parks, promotions)
			}
			if !tc.wantError && (parks != 1 || !s.Promoted || (tc.committed && promotions != 0)) {
				t.Fatalf("recovery parks=%d promotions=%d state=%+v", parks, promotions, s)
			}
		})
	}
}
