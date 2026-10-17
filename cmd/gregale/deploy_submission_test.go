package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

const submissionManifest = "profiling:\n  enabled: true\ntracing:\n  enabled: true\ntriggers:\n  - kind: cron\n    app: probe-app\n    schedule: '0 3 * * *'\n    path: /run\n"

// Exercise the full command so receipt handling and deferred compensation are
// tested together, including the accepted-ID callback used by guided sessions.
func TestDeploySubmissionPreservesConfigurationUnlessRejected(t *testing.T) {
	for _, tc := range []struct {
		mode          string
		outcome       deploySubmissionOutcome
		deploymentID  string
		exit          int
		discoveries   int
		commitAttempt bool
	}{
		{"confirmed-read-fails", deploySubmissionAccepted, "dep-accepted", 3, 0, true},
		{"committed-without-id", deploySubmissionAccepted, "", 5, 0, true},
		{"lost-reply", deploySubmissionAccepted, "dep-accepted", 1, 1, true},
		{"uncertain-open", deploySubmissionUnknown, "", 1, 1, true},
		{"uncertain-missing", deploySubmissionUnknown, "", 4, 1, true},
		{"uncertain-unavailable", deploySubmissionUnknown, "", 1, 1, true},
		{"mismatched-receipt", deploySubmissionUnknown, "", 1, 1, true},
		{"commit-rejected", deploySubmissionRejected, "", 1, 0, false},
		{"upload-rejected", deploySubmissionRejected, "", 1, 0, false},
		{"legacy-unavailable", deploySubmissionUnknown, "", 3, 0, false},
		{"legacy-rejected", deploySubmissionRejected, "", 1, 0, false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Chdir(t.TempDir())
			fixture := &submissionFixture{t: t, mode: tc.mode, app: api.AppResponse{ID: "app-1", Slug: "probe-app"}}
			deployReliabilityCLI(t, fixture.handle)
			archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}", "Dockerfile": "FROM scratch\n", "gregale.yaml": submissionManifest})
			args := []string{"--name", "probe-app", "--tarball", archive, "--dockerfile", "--no-wait", "--no-doctor"}
			var queued []string
			errCalls := 0
			execution := deployExecution{
				onQueued: func(dep api.DeploymentResponse) { queued = append(queued, dep.ID) },
				onError:  func(error) { errCalls++ },
			}
			stderr, restore := captureStderr(t)
			code := cmdDeployTarballToExisting(t.Context(), args, false, execution)
			restore()
			assertOneProblem(t, stderr.String())
			var problem struct {
				Recovery deployRecovery `json:"recovery"`
			}
			if err := json.Unmarshal([]byte(stderr.String()), &problem); err != nil {
				t.Fatal(err)
			}
			if code != tc.exit || errCalls != 1 || problem.Recovery.SubmissionOutcome != tc.outcome || problem.Recovery.DeploymentID != tc.deploymentID {
				t.Fatalf("exit=%d error callbacks=%d recovery=%+v stderr=%s", code, errCalls, problem.Recovery, stderr.String())
			}
			if strings.Join(queued, ",") != tc.deploymentID || (tc.deploymentID == "" && len(queued) != 0) {
				t.Fatalf("accepted ID callbacks=%v", queued)
			}
			fixture.assertConfiguration(t, tc.outcome != deploySubmissionRejected)
			if fixture.starts != 1 || fixture.discoveries != tc.discoveries {
				t.Fatalf("upload starts=%d discoveries=%d", fixture.starts, fixture.discoveries)
			}
			if !strings.HasPrefix(tc.mode, "legacy") && tc.mode != "upload-rejected" {
				state := readSubmissionCheckpoint(t)
				if state.CommitAttempted != tc.commitAttempt {
					t.Fatalf("persisted commit attempt=%v, want %v", state.CommitAttempted, tc.commitAttempt)
				}
			}
			if tc.mode == "lost-reply" {
				// Recover the same accepted operation, with no new start or build.
				if code := cmdDeployTarballToExisting(t.Context(), args, false); code != 0 || fixture.accepted != 1 || fixture.starts != 1 || fixture.commits != 3 {
					t.Fatalf("retry exit=%d builds=%d starts=%d commits=%d", code, fixture.accepted, fixture.starts, fixture.commits)
				}
			}
		})
	}
}

type submissionFixture struct {
	t            *testing.T
	mode         string
	app          api.AppResponse
	total        int64
	starts       int
	commits      int
	accepted     int
	discoveries  int
	removed      bool
	cancelCommit context.CancelFunc
}

func (f *submissionFixture) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/apps":
		_ = json.NewEncoder(w).Encode(f.app)
	case "/v1/apps/probe-app":
		if r.Method == http.MethodPatch {
			var req api.UpdateAppRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				f.t.Error(err)
			}
			if req.Profiling != nil {
				f.app.Manifest.Profiling = req.Profiling
			}
			if req.Tracing != nil {
				f.app.Manifest.Tracing = req.Tracing
			}
		}
		_ = json.NewEncoder(w).Encode(f.app)
	case "/v1/account":
		_ = json.NewEncoder(w).Encode(api.AccountResponse{Plan: "pro"})
	case "/v1/crons":
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]api.CronResponse{})
			return
		}
		_ = json.NewEncoder(w).Encode(api.CronResponse{ID: "cron-1", AppID: "app-1", Schedule: "0 3 * * *", Path: "/run", Enabled: true})
	case "/v1/crons/cron-1":
		f.removed = r.Method == http.MethodDelete
		w.WriteHeader(http.StatusNoContent)
	case "/v1/uploads":
		f.starts++
		if strings.HasPrefix(f.mode, "legacy") {
			http.NotFound(w, r)
			return
		}
		var req api.UploadStartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			f.t.Error(err)
		}
		f.total = req.TotalSize
		_ = json.NewEncoder(w).Encode(api.UploadStartResponse{UploadID: "upload-probe", ChunkSize: int32(f.total), TotalSize: f.total})
	case "/v1/uploads/upload-probe":
		if r.Method == http.MethodGet {
			f.discover(w)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		if f.mode == "upload-rejected" {
			api.WriteProblem(w, api.NewProblem(422, "invalid_source", "Invalid source", "Upload rejected"))
			return
		}
		if f.mode == "interrupted-upload" {
			f.cancelCommit()
		}
		w.Header().Set("Upload-Offset", strconv.FormatInt(f.total, 10))
	case "/v1/uploads/upload-probe/commit":
		f.commit(w, r)
	case "/v1/apps/probe-app/deployments":
		_, _ = io.Copy(io.Discard, r.Body)
		status := 422
		if f.mode == "legacy-unavailable" {
			status = 503
		}
		api.WriteProblem(w, api.NewProblem(status, "submission_failed", "Submission failed", "Multipart submission failed"))
	case "/v1/deployments/dep-accepted":
		if f.mode == "confirmed-read-fails" {
			api.WriteProblem(w, api.NewProblem(503, "unavailable", "Unavailable", "Deployment read is unavailable"))
			return
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-accepted", AppID: "app-1", Status: "pending"})
	default:
		f.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}
}

func (f *submissionFixture) commit(w http.ResponseWriter, r *http.Request) {
	f.commits++
	switch f.mode {
	case "confirmed-read-fails", "committed-without-id":
		f.accepted++
		detail := "upload session already committed"
		if f.mode == "confirmed-read-fails" {
			detail += " as deployment dep-accepted"
		}
		api.WriteProblem(w, api.NewProblem(409, uploadSessionAlreadyCommittedCode, "Already committed", detail))
	case "commit-rejected":
		api.WriteProblem(w, api.NewProblem(422, "invalid_source", "Invalid source", "Commit rejected"))
	case "interrupted-commit":
		f.accepted++
		_, _ = io.Copy(io.Discard, r.Body)
		f.cancelCommit()
		<-r.Context().Done()
	case "lost-reply":
		if f.commits >= 3 {
			api.WriteProblem(w, api.NewProblem(409, uploadSessionAlreadyCommittedCode, "Already committed", "upload session already committed as deployment dep-accepted"))
			return
		}
		if f.commits == 1 {
			f.accepted++
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				f.t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		api.WriteProblem(w, api.NewProblem(422, "invalid_source", "Invalid source", "Confirmation rejected"))
	default:
		if f.commits == 1 {
			api.WriteProblem(w, api.NewProblem(503, "unavailable", "Unavailable", "Commit reply unavailable"))
			return
		}
		status := 422
		if f.mode == "uncertain-missing" {
			status = 410
		}
		api.WriteProblem(w, api.NewProblem(status, "commit_failed", "Commit failed", "Later commit attempt rejected"))
	}
}

func (f *submissionFixture) discover(w http.ResponseWriter) {
	f.discoveries++
	if f.mode == "uncertain-unavailable" || f.mode == "uncertain-missing" {
		status := 503
		if f.mode == "uncertain-missing" {
			status = 404
		}
		api.WriteProblem(w, api.NewProblem(status, "discovery_failed", "Discovery failed", "Upload session unavailable"))
		return
	}
	response := api.UploadSessionResponse{UploadID: "upload-probe", AppSlug: "probe-app", ChunkSize: int32(f.total), TotalSize: f.total, ReceivedBytes: f.total, Status: "open"}
	if f.mode == "lost-reply" || f.mode == "interrupted-commit" || f.mode == "mismatched-receipt" {
		id := "dep-accepted"
		response.Status, response.DeploymentID = "committed", &id
	}
	if f.mode == "mismatched-receipt" {
		response.AppSlug = "other-app"
	}
	if f.mode == "uncertain-expired" {
		response.Status = "expired"
	}
	_ = json.NewEncoder(w).Encode(response)
}

func (f *submissionFixture) assertConfiguration(t *testing.T, preserved bool) {
	t.Helper()
	profiling := f.app.Manifest.Profiling != nil && f.app.Manifest.Profiling.Enabled
	tracing := f.app.Manifest.Tracing != nil && f.app.Manifest.Tracing.Enabled
	if profiling != preserved || tracing != preserved || f.removed {
		t.Fatalf("preserve=%v profiling=%v tracing=%v cron removed=%v", preserved, profiling, tracing, f.removed)
	}
}

func readSubmissionCheckpoint(t *testing.T) resumableUploadState {
	t.Helper()
	dir, err := uploadStateDir()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("checkpoint paths=%v err=%v", paths, err)
	}
	state, found, err := loadResumableUploadState(paths[0])
	if !found || err != nil {
		t.Fatalf("checkpoint found=%v err=%v", found, err)
	}
	return state
}

func TestDeploySubmissionReconcilesAfterCancellation(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fixture := &submissionFixture{t: t, mode: "interrupted-commit", app: api.AppResponse{ID: "app-1", Slug: "probe-app"}, cancelCommit: cancel}
	deployReliabilityCLI(t, fixture.handle)
	archive := writeDeploySourceArchive(t, map[string]string{"Dockerfile": "FROM scratch\n", "gregale.yaml": submissionManifest})
	var deploymentID string
	var submissionErr error
	execution := deployExecution{
		onQueued: func(dep api.DeploymentResponse) { deploymentID = dep.ID },
		onError:  func(err error) { submissionErr = err },
	}
	code := cmdDeployTarballToExisting(ctx, []string{"--name", "probe-app", "--tarball", archive, "--dockerfile", "--no-wait", "--no-doctor"}, false, execution)
	if code != 130 || deploymentID != "dep-accepted" || fixture.discoveries != 1 || !errors.Is(submissionErr, context.Canceled) {
		t.Fatalf("exit=%d deployment=%s discoveries=%d err=%v", code, deploymentID, fixture.discoveries, submissionErr)
	}
	fixture.assertConfiguration(t, true)
}

func TestDeploySubmissionCancellationBeforeCommitRestoresConfiguration(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fixture := &submissionFixture{t: t, mode: "interrupted-upload", app: api.AppResponse{ID: "app-1", Slug: "probe-app"}, cancelCommit: cancel}
	deployReliabilityCLI(t, fixture.handle)
	archive := writeDeploySourceArchive(t, map[string]string{"Dockerfile": "FROM scratch\n", "gregale.yaml": submissionManifest})
	var submissionErr error
	code := cmdDeployTarballToExisting(ctx, []string{"--name", "probe-app", "--tarball", archive, "--dockerfile", "--no-wait", "--no-doctor"}, false, deployExecution{onError: func(err error) { submissionErr = err }})
	if code != 130 || fixture.commits != 0 || submissionOutcome(api.DeploymentResponse{}, submissionErr) != deploySubmissionRejected || readSubmissionCheckpoint(t).CommitAttempted {
		t.Fatalf("exit=%d commits=%d err=%v", code, fixture.commits, submissionErr)
	}
	fixture.assertConfiguration(t, false)
}

func TestDeploySubmissionUnknownSurvivesNewInvocation(t *testing.T) {
	for _, remote := range []string{"uncertain-open", "uncertain-missing", "uncertain-expired"} {
		t.Run(remote, func(t *testing.T) {
			t.Chdir(t.TempDir())
			fixture := &submissionFixture{t: t, mode: "uncertain-open", app: api.AppResponse{ID: "app-1", Slug: "probe-app"}}
			deployReliabilityCLI(t, fixture.handle)
			archive := writeDeploySourceArchive(t, map[string]string{"Dockerfile": "FROM scratch\n", "gregale.yaml": submissionManifest})
			args := []string{"--name", "probe-app", "--tarball", archive, "--dockerfile", "--no-wait", "--no-doctor"}
			if code := cmdDeployTarballToExisting(t.Context(), args, false); code == 0 {
				t.Fatal("uncertain commit reported success")
			}
			if !readSubmissionCheckpoint(t).CommitAttempted {
				t.Fatal("commit uncertainty was not saved")
			}
			fixture.mode = remote
			var retryErr error
			if code := cmdDeployTarballToExisting(t.Context(), args, false, deployExecution{onError: func(err error) { retryErr = err }}); code == 0 {
				t.Fatal("uncertain retry reported success")
			}
			if fixture.starts != 1 || submissionOutcome(api.DeploymentResponse{}, retryErr) != deploySubmissionUnknown || !readSubmissionCheckpoint(t).CommitAttempted {
				t.Fatalf("restart starts=%d err=%v", fixture.starts, retryErr)
			}
			fixture.assertConfiguration(t, true)
		})
	}
}

func TestDeploySubmissionLocalFailureIsRejected(t *testing.T) {
	client := api.NewClient("https://unused.example.test", "fp_fixture")
	path := filepath.Join(t.TempDir(), "missing.tar.gz")
	for _, submit := range []func() (api.DeploymentResponse, error){
		func() (api.DeploymentResponse, error) {
			return DeployTarball(client, t.Context(), "demo", path, "", "", false, api.DeployAnnotations{})
		},
		func() (api.DeploymentResponse, error) {
			return deployDeveloperSource(client, t.Context(), "demo", path, "", "", false, "", api.DeployAnnotations{}, &devSourceSyncState{})
		},
	} {
		dep, err := submit()
		if !errors.Is(err, os.ErrNotExist) || submissionOutcome(dep, err) != deploySubmissionRejected {
			t.Fatalf("local failure outcome=%s err=%v", submissionOutcome(dep, err), err)
		}
	}
}

func TestDeploySubmissionReconciliationDropsExpiredRequestBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := "dep-accepted"
		_ = json.NewEncoder(w).Encode(api.UploadSessionResponse{UploadID: "upload-1", AppSlug: "demo", TotalSize: 10, ChunkSize: 10, ReceivedBytes: 10, Status: "committed", DeploymentID: &id})
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	ctx = reqbudget.NewContext(ctx, reqbudget.Budget{Total: time.Second, Started: time.Now().Add(-time.Minute)})
	dep, err := reconcileUploadCommit(ctx, api.NewClient(server.URL, "fp_fixture"), resumableUploadState{UploadID: "upload-1", AppSlug: "demo", TotalSize: 10, ChunkSize: 10}, context.Canceled)
	if dep.ID != "dep-accepted" || submissionOutcome(dep, err) != deploySubmissionAccepted || !errors.Is(err, context.Canceled) {
		t.Fatalf("reconciliation dep=%+v err=%v", dep, err)
	}
}

type submissionCheckTransport func(*http.Request) (*http.Response, error)

func (f submissionCheckTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestDeploySubmissionReconciliationIsBounded(t *testing.T) {
	client := api.NewClient("https://unused.example.test", "fp_fixture")
	requests := 0
	client.HTTPClient().Transport = submissionCheckTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		deadline, bounded := r.Context().Deadline()
		remaining := time.Until(deadline)
		if !bounded || remaining <= 0 || remaining > 2*time.Second {
			t.Errorf("confirmation request is not bounded to two seconds: deadline=%v remaining=%v", deadline, remaining)
		}
		return nil, errors.New("discovery unavailable")
	})
	dep, err := reconcileUploadCommit(t.Context(), client, resumableUploadState{UploadID: "upload-1"}, context.Canceled)
	if requests != 1 || submissionOutcome(dep, err) != deploySubmissionUnknown || !errors.Is(err, context.Canceled) {
		t.Fatalf("requests=%d dep=%+v err=%v", requests, dep, err)
	}
}

func TestDeploySubmissionImageRecoveryOutcome(t *testing.T) {
	for _, status := range []int{422, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Chdir(t.TempDir())
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/probe-app/deployments" {
					api.WriteProblem(w, api.NewProblem(status, "submission_failed", "Submission failed", "Image submission failed"))
					return
				}
				_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "probe-app"})
			})
			stderr, restore := captureStderr(t)
			code := cmdDeployTarballToExisting(t.Context(), []string{"--name", "probe-app", "--image", "example.test/image@sha256:" + strings.Repeat("a", 64), "--no-wait", "--no-doctor"}, false)
			restore()
			var problem struct {
				Recovery deployRecovery `json:"recovery"`
			}
			if err := json.Unmarshal([]byte(stderr.String()), &problem); err != nil {
				t.Fatal(err)
			}
			want := deploySubmissionRejected
			if status == 503 {
				want = deploySubmissionUnknown
			}
			if code == 0 || problem.Recovery.SubmissionOutcome != want {
				t.Fatalf("image exit=%d recovery=%+v stderr=%s", code, problem.Recovery, stderr.String())
			}
		})
	}
}

func TestDeploySubmissionStopsWhenCommitCheckpointCannotBeUpdated(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	commits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if strings.HasSuffix(r.URL.Path, "/commit") {
				commits++
				_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "unexpected"})
				return
			}
			_ = json.NewEncoder(w).Encode(api.UploadStartResponse{UploadID: "upload-1", ChunkSize: 10, TotalSize: 10})
		case http.MethodPatch:
			w.Header().Set("Upload-Offset", "10")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(path, []byte("abcdefghij"), 0600); err != nil {
		t.Fatal(err)
	}
	progress := func(_, _ int64) {
		dir, err := uploadStateDir()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(dir, dir+"-original"); err != nil {
			t.Fatal(err)
		}
		// Turn the parent into a file to simulate a checkpoint storage error.
		if err := os.WriteFile(dir, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	dep, _, _, err := DeployResumableTarball(api.NewClient(server.URL, "fp_fixture"), t.Context(), "demo", path, progress)
	if err == nil || commits != 0 || submissionOutcome(dep, err) != deploySubmissionRejected {
		t.Fatalf("checkpoint failure: commits=%d dep=%+v err=%v", commits, dep, err)
	}
}
