package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A source deploy that waited for the deployment in human mode never applied
// gregale.yaml's saved settings: h12-wk went live without its worker
// replicas, stop signal or drain timeout (Hunt 12 H12-30).
func TestWaitedSourceDeployAppliesSavedSettings(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"human wait", nil},
		{"json wait", []string{"--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := main.InitZeroConfigRepo(t)
			main.MustGit(t, repo, "remote", "remove", "origin")
			manifest := "worker:\n  drain_timeout: 10s\n  stop_signal: SIGTERM\n  scale:\n    min: 1\n    max: 2\n"
			if err := os.WriteFile(filepath.Join(repo, "gregale.yaml"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			main.WithCwd(t, repo)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			var mu sync.Mutex
			app := api.AppResponse{ID: "a1", Slug: "demo", Type: "app", Manifest: api.AppManifest{ExecutionMode: api.ExecutionModeWorker}}
			var settled *api.UpdateAppRequest
			committed := false
			live := api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "live", TrafficPercent: 100, RolloutState: "complete"}
			stub := main.NewZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, _ *main.ZeroConfigStubServer) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.URL.Path == "/v1/apps/demo" && r.Method == http.MethodGet:
					_ = json.NewEncoder(w).Encode(app)
				case r.URL.Path == "/v1/apps/demo" && r.Method == http.MethodPatch:
					var req api.UpdateAppRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if committed && req.WorkerReplicas != nil {
						settled = &req
						app.Manifest.WorkerReplicas = req.WorkerReplicas
					}
					_ = json.NewEncoder(w).Encode(app)
				case r.URL.Path == "/v1/uploads" && r.Method == http.MethodPost:
					var req api.UploadStartRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(api.UploadStartResponse{UploadID: "st-upload", ChunkSize: 8 * 1024 * 1024, TotalSize: req.TotalSize})
				case r.URL.Path == "/v1/uploads/st-upload" && r.Method == http.MethodPatch:
					raw, _ := io.ReadAll(r.Body)
					w.Header().Set("Upload-Offset", strconv.Itoa(len(raw)))
				case r.URL.Path == "/v1/uploads/st-upload/commit":
					committed = true
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"})
				case r.URL.Path == "/v1/deployments/d1" && r.Method == http.MethodGet:
					_ = json.NewEncoder(w).Encode(live)
				default:
					http.NotFound(w, r)
				}
			})
			t.Setenv("FAAS_API", (*stub.Srv()).URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			main.WithResetJSONOutput(t, false)
			_, _, restore := main.SwapIO(t)
			code := main.Run(append(append([]string{}, tt.args...), "deploy", "--name", "demo", "--app", "--timeout", "20s"))
			restore()
			mu.Lock()
			defer mu.Unlock()
			if code != 0 {
				t.Fatalf("deploy exit %d", code)
			}
			if settled == nil || settled.WorkerReplicas.Max != 2 || settled.StopSignal == nil || *settled.StopSignal != "SIGTERM" || settled.StopGracePeriodS == nil || *settled.StopGracePeriodS != 10 {
				t.Fatalf("saved worker settings not applied after the deployment went live: %+v", settled)
			}
		})
	}
}
