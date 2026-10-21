package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Run the actual deploy command in separate processes. Each invocation packs
// source into a new temporary archive, and removes it before the next starts.
func TestDeployUploadRecoveryAcrossProcesses(t *testing.T) {
	for _, mode := range []string{"interrupted-upload", "transient-upload", "lost-commit", "uncertain-commit"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			sourceDir := t.TempDir()
			for name, contents := range map[string]string{"package.json": `{}`, "index.js": `console.log("hello")`} {
				if err := os.WriteFile(filepath.Join(sourceDir, name), []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fixture := &uploadRestartFixture{t: t, failure: mode}
			srv := httptest.NewServer(http.HandlerFunc(fixture.handle))
			defer srv.Close()
			runUploadRestartProcess(t, srv.URL, sourceDir, mode)

			dir, err := uploadStateDir()
			if err != nil {
				t.Fatal(err)
			}
			states, err := filepath.Glob(filepath.Join(dir, "*.json"))
			if err != nil || len(states) != 1 {
				t.Fatalf("recovery records = %v, error = %v", states, err)
			}
			state, found, err := loadResumableUploadState(states[0])
			if err != nil || !found {
				t.Fatalf("load recovery record: found=%v err=%v", found, err)
			}
			committed := mode == "lost-commit" || mode == "uncertain-commit"
			if state.CommitAttempted != committed {
				t.Fatalf("checkpoint commit attempt=%v, want %v", state.CommitAttempted, committed)
			}
			if _, err := os.Stat(state.ArchivePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("temporary archive should be removed after the first process, err=%v", err)
			}
			data, err := os.ReadFile(states[0])
			if err != nil || bytes.Contains(data, []byte("fp_restart_fixture")) {
				t.Fatalf("checkpoint must not store credentials, err=%v", err)
			}
			info, err := os.Stat(states[0])
			if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
				t.Fatalf("checkpoint permissions: info=%v err=%v", info, err)
			}

			fixture.mu.Lock()
			receivedBeforeRestart := len(fixture.contents)
			chunkSize, totalSize := fixture.chunkSize, fixture.totalSize
			fixture.failure = ""
			fixture.mu.Unlock()
			if !committed && receivedBeforeRestart != chunkSize*3 {
				t.Fatalf("received %d of %d bytes before interruption, want three chunks", receivedBeforeRestart, totalSize)
			}
			runUploadRestartProcess(t, srv.URL, sourceDir, "resume")
			fixture.mu.Lock()
			starts, deployments, cancels := fixture.starts, fixture.deployments, fixture.cancels
			received, resumedAt := len(fixture.contents), fixture.resumedAt
			fixture.mu.Unlock()
			if starts != 1 || deployments != 1 || cancels != 0 {
				t.Fatalf("starts=%d deployments=%d cancels=%d, want 1/1/0", starts, deployments, cancels)
			}
			if received != totalSize {
				t.Fatalf("received=%d total=%d", received, totalSize)
			}
			if resumedAt != receivedBeforeRestart {
				t.Fatalf("resumed at %d, want server offset %d", resumedAt, receivedBeforeRestart)
			}
			if _, err := os.Stat(states[0]); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("successful recovery left a checkpoint: %v", err)
			}
			// Even after successful cleanup, retrying the same logical intent
			// may replay StartUpload's original committed session.
			runUploadRestartProcess(t, srv.URL, sourceDir, "resume")
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.starts != 1 || fixture.deployments != 1 || fixture.cancels != 0 {
				t.Fatalf("replay starts=%d deployments=%d cancels=%d, want 1/1/0", fixture.starts, fixture.deployments, fixture.cancels)
			}
		})
	}
}

func runUploadRestartProcess(t *testing.T, origin, sourceDir, mode string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestDeployUploadRestartProcessHelper$")
	cmd.Dir = sourceDir
	cmd.Env = append(os.Environ(), "GREGALE_UPLOAD_RESTART_MODE="+mode, "GREGALE_UPLOAD_RESTART_STATE_HOME="+os.Getenv("XDG_STATE_HOME"), "FAAS_API="+origin, "FAAS_TOKEN=fp_restart_fixture")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("deploy process %s: %v\n%s", mode, err, output)
	}
}

func TestDeployUploadRestartProcessHelper(t *testing.T) {
	mode := os.Getenv("GREGALE_UPLOAD_RESTART_MODE")
	if mode == "" {
		t.Skip("only used by the restart regression test")
	}
	// TestMain isolates standard config paths for each test binary. Restore the
	// shared checkpoint directory explicitly for these child processes.
	t.Setenv("XDG_STATE_HOME", os.Getenv("GREGALE_UPLOAD_RESTART_STATE_HOME"))
	jsonOutput = true
	var output bytes.Buffer
	osStdout, osStderr = &output, &output
	ctx := context.Background()
	if mode == "interrupted-upload" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	code := cmdDeployTarballToExisting(ctx, []string{"--name", "demo", "--path", ".", "--no-doctor", "--no-wait", "--secret-scan", "off"}, true)
	if mode == "resume" {
		if code != 0 || !strings.Contains(output.String(), "dep-recovered") {
			t.Fatalf("resumed deploy exit=%d output=%s", code, &output)
		}
	} else if code == 0 {
		t.Fatalf("expected interrupted deploy to fail, output=%s", &output)
	}
}

type uploadRestartFixture struct {
	t            *testing.T
	mu           sync.Mutex
	failure      string
	contents     []byte
	totalSize    int
	chunkSize    int
	starts       int
	startKey     string
	deployments  int
	cancels      int
	resumedAt    int
	commitCalled bool
}

func (f *uploadRestartFixture) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/v1/account":
		_ = json.NewEncoder(w).Encode(api.AccountResponse{Plan: "pro"})
	case r.URL.Path == "/v1/apps/demo":
		_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "demo"})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/uploads":
		var request api.UploadStartRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			f.t.Error(err)
		}
		key := r.Header.Get("Idempotency-Key")
		if f.starts == 0 || key != f.startKey {
			f.starts++
			f.startKey = key
		}
		f.totalSize = int(request.TotalSize)
		f.chunkSize = (f.totalSize + 4) / 5
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.UploadStartResponse{UploadID: "upload-1", ChunkSize: int32(f.chunkSize), TotalSize: request.TotalSize})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/uploads/upload-1":
		f.resumedAt = len(f.contents)
		if f.failure == "uncertain-commit" {
			api.WriteProblem(w, api.NewProblem(503, "unavailable", "Try again", "Commit acceptance cannot be checked"))
			return
		}
		status := "open"
		var deploymentID *string
		if f.commitCalled {
			status = "committed"
			id := "dep-recovered"
			deploymentID = &id
		}
		_ = json.NewEncoder(w).Encode(api.UploadSessionResponse{UploadID: "upload-1", AppSlug: "demo", ChunkSize: int32(f.chunkSize), TotalSize: int64(f.totalSize), ReceivedBytes: int64(len(f.contents)), Status: status, DeploymentID: deploymentID})
	case r.Method == http.MethodPatch && r.URL.Path == "/v1/uploads/upload-1":
		if f.commitCalled {
			api.WriteProblem(w, api.NewProblem(409, uploadSessionAlreadyCommittedCode, "Committed", "upload session already committed as deployment dep-recovered"))
			return
		}
		if f.failure == "transient-upload" && len(f.contents) >= f.chunkSize*3 {
			api.WriteProblem(w, api.NewProblem(503, "unavailable", "Try again", "Temporary outage"))
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			f.t.Error(err)
		}
		if r.Header.Get("Upload-Offset") != strconv.Itoa(len(f.contents)) {
			f.t.Errorf("append offset=%s, want %d", r.Header.Get("Upload-Offset"), len(f.contents))
		}
		f.contents = append(f.contents, body...)
		if (f.failure == "interrupted-upload" || f.failure == "request-timeout-upload") && len(f.contents) >= f.chunkSize*3 {
			// Accept the chunk, then lose its acknowledgment during shutdown.
			f.mu.Unlock()
			<-r.Context().Done()
			f.mu.Lock()
			return
		}
		w.Header().Set("Upload-Offset", strconv.Itoa(len(f.contents)))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/uploads/upload-1/commit":
		if !f.commitCalled {
			f.deployments++
			f.commitCalled = true
		}
		if f.failure == "lost-commit" || f.failure == "uncertain-commit" {
			api.WriteProblem(w, api.NewProblem(503, "unavailable", "Try again", "Commit response lost"))
			return
		}
		fallthrough
	case r.Method == http.MethodGet && r.URL.Path == "/v1/deployments/dep-recovered":
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-recovered", AppID: "app-1", Status: "pending"})
	case r.Method == http.MethodDelete:
		f.cancels++
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func TestUploadRecoveryIdentityIsolatesDeployments(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	base := api.NewClient("https://api.example.test", "fp_fixture")
	ctx := api.ContextWithIdempotencyKey(context.Background(), "logical-key")
	identity := uploadRecoveryIdentity(base, ctx, "demo", "source-digest", "options-digest")
	for _, tc := range []struct {
		name, origin, token, app, source, options, key string
	}{
		{"credential", base.BaseURL(), "fp_another", "demo", "source-digest", "options-digest", "logical-key"},
		{"endpoint", "https://other.example.test", base.Token(), "demo", "source-digest", "options-digest", "logical-key"},
		{"app", base.BaseURL(), base.Token(), "other", "source-digest", "options-digest", "logical-key"},
		{"source", base.BaseURL(), base.Token(), "demo", "changed-source", "options-digest", "logical-key"},
		{"options", base.BaseURL(), base.Token(), "demo", "source-digest", "changed-options", "logical-key"},
		{"intent", base.BaseURL(), base.Token(), "demo", "source-digest", "options-digest", "new-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := uploadRecoveryIdentity(api.NewClient(tc.origin, tc.token), api.ContextWithIdempotencyKey(context.Background(), tc.key), tc.app, tc.source, tc.options)
			if got == identity {
				t.Fatal("different deployment can find the original checkpoint")
			}
		})
	}
	path, err := uploadStatePath(identity)
	if err != nil || strings.Contains(path, base.Token()) {
		t.Fatalf("invalid checkpoint path %q: %v", path, err)
	}
}

func TestDeployResumableTarballRestartsExpiredSession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(path, []byte("abcdefghij"), 0o600); err != nil {
		t.Fatal(err)
	}
	var startKeys []string
	var starts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/uploads":
			starts++
			startKeys = append(startKeys, r.Header.Get("Idempotency-Key"))
			if starts == 2 {
				api.WriteProblem(w, api.NewProblem(503, "unavailable", "Try again", "Temporary outage"))
				return
			}
			id := "upload-1"
			if starts > 1 {
				id = "upload-2"
			}
			_ = json.NewEncoder(w).Encode(api.UploadStartResponse{UploadID: id, ChunkSize: 10, TotalSize: 10})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/uploads/upload-1":
			_ = json.NewEncoder(w).Encode(api.UploadSessionResponse{UploadID: "upload-1", AppSlug: "demo", ChunkSize: 10, TotalSize: 10, Status: "expired"})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/uploads/upload-1":
			api.WriteProblem(w, api.NewProblem(410, uploadSessionExpiredCode, "Expired", "Upload expired"))
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/uploads/upload-2":
			w.Header().Set("Upload-Offset", "10")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/uploads/upload-2/commit":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-recovered"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := api.ContextWithIdempotencyKey(context.Background(), "logical-key")
	client := api.NewClient(srv.URL, "fp_fixture")
	if _, _, _, err := DeployResumableTarball(client, ctx, "demo", path, nil); err == nil {
		t.Fatal("expected the replacement start to fail")
	}
	dep, _, _, err := DeployResumableTarball(client, ctx, "demo", path, nil)
	if err != nil || dep.ID != "dep-recovered" {
		t.Fatalf("replacement upload: dep=%+v err=%v", dep, err)
	}
	if len(startKeys) != 3 || startKeys[0] == startKeys[1] || startKeys[1] != startKeys[2] {
		t.Fatalf("replacement replayed the expired start key: %v", startKeys)
	}
}

func TestDeployResumableTarballPreservesRequestTimeout(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(path, []byte("abcdefghij"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := &uploadRestartFixture{t: t, failure: "request-timeout-upload"}
	srv := httptest.NewServer(http.HandlerFunc(fixture.handle))
	defer srv.Close()
	client := api.NewClient(srv.URL, "fp_restart_fixture")
	client.HTTPClient().Timeout = 100 * time.Millisecond
	ctx := context.Background()
	if _, _, _, err := DeployResumableTarball(client, ctx, "demo", path, nil); !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("expected request timeout with live deploy context: %v", err)
	}
	fixture.mu.Lock()
	fixture.failure = ""
	fixture.mu.Unlock()
	dep, _, _, err := DeployResumableTarball(api.NewClient(srv.URL, "fp_restart_fixture"), ctx, "demo", path, nil)
	if err != nil || dep.ID != "dep-recovered" {
		t.Fatalf("resume after request timeout: dep=%+v err=%v", dep, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.resumedAt != 6 || len(fixture.contents) != 10 || fixture.starts != 1 || fixture.deployments != 1 || fixture.cancels != 0 {
		t.Fatalf("timeout recovery: received=%d resumed=%d starts=%d deployments=%d cancels=%d", len(fixture.contents), fixture.resumedAt, fixture.starts, fixture.deployments, fixture.cancels)
	}
}

func TestDeployUploadRecoveryRefusesCredentialBearingEndpoint(t *testing.T) {
	for _, origin := range []string{"https://user:private-secret@example.test", "https://example.test?token=private-secret", "https://example.test#private-secret"} {
		t.Run(origin, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			_, _, _, err := DeployResumableTarball(api.NewClient(origin, "fp_fixture"), context.Background(), "demo", "unused.tar.gz", nil)
			if err == nil || strings.Contains(err.Error(), "private-secret") {
				t.Fatalf("credential-bearing endpoint reached upload or leaked: %v", err)
			}
			dir, _ := uploadStateDir()
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid endpoint wrote a checkpoint: %v", err)
			}
		})
	}
}

func TestDeployUploadRecoveryDoesNotReuseChangedSourceOrOptions(t *testing.T) {
	for _, change := range []string{"source", "options"} {
		t.Run(change, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			path := filepath.Join(t.TempDir(), "source.tar.gz")
			if err := os.WriteFile(path, []byte("abcdefghij"), 0o600); err != nil {
				t.Fatal(err)
			}
			var starts int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodPost:
					starts++
					_ = json.NewEncoder(w).Encode(api.UploadStartResponse{UploadID: "upload-" + strconv.Itoa(starts), ChunkSize: 2, TotalSize: 10})
				case http.MethodPatch:
					w.Header().Set("Upload-Offset", "2")
				default:
					t.Errorf("changed deployment touched the original session: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			client := api.NewClient(srv.URL, "fp_fixture")
			interrupt := func(options api.UploadDeployOptions) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				_, _, _, err := DeployResumableTarball(client, ctx, "demo", path, func(_, _ int64) { cancel() }, options)
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("interrupt upload: %v", err)
				}
			}
			interrupt(api.UploadDeployOptions{})
			var options api.UploadDeployOptions
			if change == "source" {
				// Same size and pathname, different bytes.
				if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				options.NoTriggers = true
			}
			interrupt(options)
			dir, _ := uploadStateDir()
			states, err := filepath.Glob(filepath.Join(dir, "*.json"))
			if err != nil || len(states) != 2 || starts != 2 {
				t.Fatalf("changed deployment: starts=%d checkpoints=%d err=%v", starts, len(states), err)
			}
		})
	}
}
