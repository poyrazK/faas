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

// A worker declared with gregale.yaml's worker: section was created as a
// request-mode HTTP app, so its first boot failed the HTTP readiness check
// and the saved worker settings never applied; a plain redeploy also sent
// execution_mode=request to an existing worker (Hunt 12 H12-28).
func TestDeployManifestExecutionModeReachesTheAppBeforeTheBuild(t *testing.T) {
	const worker = "worker:\n  drain_timeout: 10s\n  stop_signal: SIGTERM\n  scale:\n    min: 1\n    max: 1\n"
	tests := []struct {
		name       string
		manifest   string
		existing   string // execution mode of an existing app; "" = new app
		args       []string
		wantCreate string // execution_mode in POST /v1/apps
		wantPatch  string // execution_mode in a pre-build PATCH; "" = none
	}{
		{name: "new worker", manifest: worker, wantCreate: api.ExecutionModeWorker},
		{name: "new app without manifest", wantCreate: api.ExecutionModeRequest},
		{name: "new app with lifecycle mode", manifest: "lifecycle:\n  execution_mode: service\n", wantCreate: api.ExecutionModeService},
		{name: "explicit flag wins", manifest: worker, args: []string{"--execution-mode", "request"}, wantCreate: api.ExecutionModeRequest},
		{name: "existing worker, plain redeploy", existing: api.ExecutionModeWorker},
		{name: "existing worker with worker section", manifest: worker, existing: api.ExecutionModeWorker, wantPatch: api.ExecutionModeWorker},
		{name: "existing request app gains a worker section", manifest: worker, existing: api.ExecutionModeRequest, wantPatch: api.ExecutionModeWorker},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := main.InitZeroConfigRepo(t)
			main.MustGit(t, repo, "remote", "remove", "origin")
			if tt.manifest != "" {
				if err := os.WriteFile(filepath.Join(repo, "gregale.yaml"), []byte(tt.manifest), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			main.WithCwd(t, repo)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			var mu sync.Mutex
			exists := tt.existing != ""
			mode := tt.existing
			var created, patched *string
			committed := false
			stub := main.NewZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, _ *main.ZeroConfigStubServer) {
				mu.Lock()
				defer mu.Unlock()
				app := func() api.AppResponse {
					return api.AppResponse{ID: "a1", Slug: "demo", Type: "app", Manifest: api.AppManifest{ExecutionMode: mode}}
				}
				switch {
				case r.URL.Path == "/v1/apps/demo" && r.Method == http.MethodGet:
					if !exists {
						http.Error(w, `{"status":404,"title":"Not found"}`, http.StatusNotFound)
						return
					}
					_ = json.NewEncoder(w).Encode(app())
				case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
					var req api.CreateAppRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					created, exists, mode = &req.ExecutionMode, true, req.ExecutionMode
					_ = json.NewEncoder(w).Encode(app())
				case r.URL.Path == "/v1/apps/demo" && r.Method == http.MethodPatch:
					var req api.UpdateAppRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if req.ExecutionMode != nil && patched == nil && !committed {
						patched = req.ExecutionMode
						mode = *req.ExecutionMode
					}
					_ = json.NewEncoder(w).Encode(app())
				case r.URL.Path == "/v1/uploads" && r.Method == http.MethodPost:
					var req api.UploadStartRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(api.UploadStartResponse{UploadID: "wk-upload", ChunkSize: 8 * 1024 * 1024, TotalSize: req.TotalSize})
				case r.URL.Path == "/v1/uploads/wk-upload" && r.Method == http.MethodPatch:
					raw, _ := io.ReadAll(r.Body)
					w.Header().Set("Upload-Offset", strconv.Itoa(len(raw)))
				case r.URL.Path == "/v1/uploads/wk-upload/commit":
					committed = true
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"})
				default:
					http.NotFound(w, r)
				}
			})
			t.Setenv("FAAS_API", (*stub.Srv()).URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			main.WithResetJSONOutput(t, false)
			_, _, restore := main.SwapIO(t)
			code := main.CmdDeployTarball(append([]string{"--name", "demo", "--app", "--no-wait"}, tt.args...))
			restore()
			if code != 0 {
				t.Fatalf("deploy exit %d", code)
			}
			mu.Lock()
			defer mu.Unlock()
			if got := deref(created); got != tt.wantCreate {
				t.Errorf("created execution_mode = %q, want %q", got, tt.wantCreate)
			}
			if got := deref(patched); got != tt.wantPatch {
				t.Errorf("pre-build PATCH execution_mode = %q, want %q", got, tt.wantPatch)
			}
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
