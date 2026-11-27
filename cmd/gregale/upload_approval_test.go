package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestApprovedUploadRetryRequiresOriginalCheckpoint(t *testing.T) {
	const upload = "upload-original"
	for _, tc := range []struct {
		name, expected, status string
		loseCheckpoint         bool
		wantCommit             bool
	}{
		{name: "approved upload", expected: upload, status: "open", wantCommit: true},
		{name: "missing checkpoint", expected: upload, loseCheckpoint: true},
		{name: "different upload", expected: "another-upload"},
		{name: "expired upload", expected: upload, status: "expired"},
		{name: "incomplete upload", expected: upload, status: "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, stateDir := t.TempDir(), t.TempDir()
			t.Chdir(dir)
			t.Setenv("XDG_STATE_HOME", stateDir)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("FAAS_TOKEN", "fp_live_fixture")
			if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var starts, chunks int
			var size int64
			var commitKeys []string
			app := api.AppResponse{ID: "a1", Slug: "demo", Type: "app", Status: "active"}
			stub := main.NewZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, _ *main.ZeroConfigStubServer) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/demo":
					_ = json.NewEncoder(w).Encode(app)
				case r.Method == http.MethodPost && r.URL.Path == "/v1/uploads":
					starts++
					var req api.UploadStartRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					size = req.TotalSize
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(api.UploadStartResponse{UploadID: upload, ChunkSize: 1 << 20, TotalSize: size})
				case r.Method == http.MethodPatch && r.URL.Path == "/v1/uploads/"+upload:
					chunks++
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Upload-Offset", strconv.FormatInt(size, 10))
				case r.Method == http.MethodGet && r.URL.Path == "/v1/uploads/"+upload:
					status, received := tc.status, size
					if status == "incomplete" {
						status, received = "open", size-1
					}
					_ = json.NewEncoder(w).Encode(api.UploadSessionResponse{
						UploadID: upload, AppSlug: app.Slug, Status: status, ChunkSize: 1 << 20, TotalSize: size, ReceivedBytes: received,
					})
				case r.Method == http.MethodPost && r.URL.Path == "/v1/uploads/"+upload+"/commit":
					commitKeys = append(commitKeys, r.Header.Get("Idempotency-Key"))
					if len(commitKeys) == 1 {
						problem := api.NewProblem(428, api.CodeApprovalRequired, "Approval required", "Review this upload")
						problem.AgentApproval = &api.AgentApprovalPendingInfo{ID: "approval-original", Category: api.AgentApprovalProduction}
						api.WriteProblem(w, problem)
						return
					}
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "approved-deployment", AppID: app.ID, Status: "pending"})
				default:
					if r.Method != http.MethodGet {
						t.Errorf("approval retry made an unexpected write: %s %s", r.Method, r.URL.Path)
					}
					http.NotFound(w, r)
				}
			})
			t.Setenv("FAAS_API", (*stub.Srv()).URL)
			main.WithResetJSONOutput(t, true)
			stdout, readStderr, restore := main.SwapIO(t)
			defer restore()
			args := []string{"--path", dir, "--name", app.Slug, "--app", "--yes", "--no-wait", "--no-doctor", "--idempotency-key", "original-key"}
			if status := main.CmdDeployTarball(args); status == 0 || !strings.Contains(readStderr(), api.CodeApprovalRequired) {
				t.Fatalf("initial deploy did not request approval: exit %d, %s", status, readStderr())
			}
			if tc.loseCheckpoint {
				if err := os.RemoveAll(filepath.Join(stateDir, "gregale", "uploads")); err != nil {
					t.Fatal(err)
				}
			}
			exit := main.CmdDeployTarball(append(slices.Clone(args), "--resume-upload-id", tc.expected))
			mu.Lock()
			defer mu.Unlock()
			if starts != 1 || chunks != 1 {
				t.Fatalf("approval retry started or transferred another upload: %d starts, %d chunks", starts, chunks)
			}
			if tc.wantCommit {
				if exit != 0 || len(commitKeys) != 2 || commitKeys[0] == "" || commitKeys[1] != commitKeys[0] ||
					!strings.Contains(stdout.String(), "approved-deployment") {
					t.Fatalf("approved retry exit %d, commit keys %v, stderr %s", exit, commitKeys, readStderr())
				}
			} else if exit == 0 || len(commitKeys) != 1 {
				t.Fatalf("approval retry used an invalid checkpoint: exit %d, commit keys %v", exit, commitKeys)
			}
		})
	}
}
