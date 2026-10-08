package main

import (
	"context"
	"encoding/json"
	"errors"
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

func TestMCPNativeRestoreTrafficAndRecovery(t *testing.T) {
	original := mcpNativeCheckWorkers
	defer func() { mcpNativeCheckWorkers = original }()
	for _, tc := range []struct {
		name                                                   string
		changed, parked, incompatible, race, lost, postFailure bool
	}{
		{name: "healthy"}, {name: "traffic changed", changed: true}, {name: "worker parked", parked: true},
		{name: "worker incompatible", incompatible: true}, {name: "CAS race", race: true},
		{name: "lost response", lost: true}, {name: "canonical unhealthy", postFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mcpNativeCheckWorkers = func(context.Context, *Client, mcpNativeReleasePlan, *mcpNativeReleaseState) error {
				if tc.incompatible {
					return errors.New("not ready")
				}
				return nil
			}
			root := t.TempDir()
			config, err := os.ReadFile("templates/mcp-node/gregale-mcp.json")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "gregale-mcp.json"), []byte(strings.Replace(string(config), `"legacy": true`, `"legacy": false`, 1)), 0600); err != nil {
				t.Fatal(err)
			}
			current := "candidate"
			if tc.changed {
				current = "external"
			}
			patches := 0
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("control credential leaked to MCP endpoint")
				}
				if tc.postFailure && current == "stable" {
					w.WriteHeader(503)
					return
				}
				if r.Header.Get("Origin") != "" {
					w.WriteHeader(403)
					return
				}
				var request struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				json.NewDecoder(r.Body).Decode(&request)
				w.Header().Set("Content-Type", "application/json")
				if request.Method == "server/discover" {
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":["%s"],"capabilities":{"tools":{}}}}`, request.ID, mcphosting.ProtocolVersion)
				} else {
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[]}}`, request.ID)
				}
			}))
			defer endpoint.Close()
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/web/deployments":
					json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: current, AppID: "web-id", Status: statusLive, TrafficPercent: 100}}})
				case "/v1/apps/web":
					json.NewEncoder(w).Encode(api.AppResponse{ID: "web-id", Slug: "web", URL: endpoint.URL, StreamingEnabled: true, PublicAuth: api.PublicAuthStatus{Mode: api.AppPublicAuthModeOpen}})
				case "/v1/deployments/stable":
					traffic := 0
					if current == "stable" {
						traffic = 100
					}
					json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "stable", AppID: "web-id", Status: statusLive, TrafficPercent: traffic})
				case "/v1/deployments/stable/url":
					json.NewEncoder(w).Encode(api.DeploymentPreviewURL{Alive: true, URL: endpoint.URL})
				case "/v1/apps/observer":
					json.NewEncoder(w).Encode(api.AppResponse{ID: "obs", WorkloadClass: "worker", Manifest: api.AppManifest{WorkerReplicas: &api.WorkerScaling{Min: 1}}})
				case "/v1/apps/observer/instances":
					json.NewEncoder(w).Encode([]api.InstanceResponse{{ID: "observer", State: "running"}})
				case "/v1/apps/metrics/custom-metrics":
					json.NewEncoder(w).Encode(api.CustomMetricListResponse{Metrics: []api.CustomMetricResponse{{Name: "mcp_tasks_observer_heartbeat", Value: 1, ObservedAt: time.Now()}}})
				case "/v1/apps/previous/deployments/latest":
					json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "old", AppID: "previous-id", Status: statusLive})
				case "/v1/apps/previous/instances":
					json.NewEncoder(w).Encode([]api.InstanceResponse{{ID: "old-instance", DeploymentID: "old", State: "running"}})
				case "/v1/apps/previous/logs":
					fmt.Fprint(w, "event: log\ndata: {\"instance\":\"old-instance\",\"line\":\"{\\\"event\\\":\\\"mcp_task_worker_started\\\",\\\"workerID\\\":\\\"12345678-1234-1234-1234-123456789abc\\\"}\"}\n\n")
				case "/v1/deployments/stable/traffic":
					if r.Method != "PATCH" {
						t.Error("unexpected write method")
					}
					var body api.UpdateDeploymentTrafficRequest
					json.NewDecoder(r.Body).Decode(&body)
					if body.ExpectedServingDeploymentID == nil || *body.ExpectedServingDeploymentID != "candidate" {
						t.Error("restoration lacks CAS")
					}
					patches++
					if tc.race {
						current = "external"
						w.WriteHeader(409)
						return
					}
					current = "stable"
					if tc.lost {
						w.WriteHeader(500)
						return
					}
					json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "stable", AppID: "web-id", Status: statusLive, TrafficPercent: 100})
				default:
					t.Errorf("unexpected route: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer control.Close()
			p := mcpNativeReleasePlan{WebApp: "web", WebPath: root, WorkerPath: root, PreviousWorkerApps: []string{"previous"}, ObserverApp: "observer", ObserverMetricApp: "metrics"}
			s := mcpNativeReleaseState{Version: 1, Fingerprint: "source", Stage: "web_promoted", ServingCaptured: true, ServingDeployment: "stable", WebDeployment: "candidate", Promoted: true, PreviousDeployments: map[string]string{"previous": "old"}, Parked: map[string]bool{"previous": tc.parked}}
			path := filepath.Join(t.TempDir(), "state.json")
			client := NewClient(control.URL, "operator")
			err = restoreMCPNativeWeb(context.Background(), client, p, &s, path)
			wantError := tc.changed || tc.parked || tc.incompatible || tc.race || tc.postFailure
			if (err != nil) != wantError {
				t.Fatalf("err=%v state=%+v", err, s)
			}
			if tc.changed || tc.parked || tc.incompatible {
				if patches != 0 {
					t.Fatal("unsafe traffic write")
				}
				return
			}
			if tc.postFailure {
				if s.Stage != "restore_pending" {
					t.Fatal("failed canonical check lost recovery stage")
				}
				return
			}
			if !wantError {
				if s.Stage != "web_restored" || s.Promoted || current != "stable" {
					t.Fatalf("bad restoration %+v", s)
				}
				if err := restoreMCPNativeWeb(context.Background(), client, p, &s, path); err != nil {
					t.Fatal(err)
				}
				if patches != 1 {
					t.Fatal("resume repeated traffic write")
				}
			}
		})
	}
}

func TestMCPNativeRecoveryInspectionDoesNotConfigureIngress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("inspection changed control plane: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v1/apps/web/deployments":
			json.NewEncoder(w).Encode(api.DeploymentListResponse{})
		case "/v1/apps/observer":
			json.NewEncoder(w).Encode(api.AppResponse{})
		case "/v1/account":
			json.NewEncoder(w).Encode(api.AccountResponse{Plan: "pro"})
		default:
			t.Errorf("inspection attempted candidate ingress preparation: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_inspection")
	_, _, restore := swapIO(t)
	defer restore()
	if code := recoverMCPNativeRelease(mcpNativeReleasePlan{WebApp: "web", ObserverApp: "observer", TimeoutSeconds: 1}, mcpNativeReleaseState{ServingCaptured: true, WebDeployment: "candidate"}, "unused", "unused", false); code != 0 {
		t.Fatalf("inspection exit %d", code)
	}
}
