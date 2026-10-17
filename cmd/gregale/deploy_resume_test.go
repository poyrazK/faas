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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const resumeManifest = `lifecycle:
  request_timeout_s: 30
scaling:
  min_instances: 1
  max_instances: 3
  target:
    metric: concurrent_requests
    value: 2
`

type deployResumeFixture struct {
	mu                           sync.Mutex
	app                          api.AppResponse
	dep                          api.DeploymentResponse
	submissions, starts, patches int
	total                        int64
	uploadStatus                 string
	losePatch                    bool
	loseCommit                   bool
	beforePatch                  func(*deployResumeFixture)
	patchAttempts                int
	rejectConditions             bool
	newer                        bool
}

func newDeployResumeFixture() *deployResumeFixture {
	return &deployResumeFixture{
		app:          api.AppResponse{ID: "app-1", Slug: "demo"},
		dep:          api.DeploymentResponse{ID: "dep-1", AppID: "app-1", Status: "live"},
		uploadStatus: "committed",
	}
}

func (f *deployResumeFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	encode := func(value any) { _ = json.NewEncoder(w).Encode(value) }
	switch {
	case r.URL.Path == "/v1/apps/demo" && r.Method == http.MethodGet:
		encode(f.app)
	case r.URL.Path == "/v1/apps/demo" && r.Method == http.MethodPatch:
		var update api.UpdateAppRequest
		_ = json.NewDecoder(r.Body).Decode(&update)
		f.patchAttempts++
		if f.beforePatch != nil {
			f.beforePatch(f)
			f.beforePatch = nil
		}
		if condition := update.DeploySettingsCondition; condition != nil {
			if f.rejectConditions {
				api.WriteProblem(w, api.NewProblem(400, "validation_failed", "Unsupported guarded update", "deploy_settings_condition is not supported"))
				return
			}
			if f.newer || condition.ExpectedAppID != f.app.ID || condition.ExpectedDeploymentID != f.dep.ID {
				api.WriteProblem(w, api.NewProblem(409, api.CodeDeploySettingsConflict, "Deploy settings changed", "deployment_changed"))
				return
			}
			current := api.DeploySettingsSnapshot(f.app)
			for field, expected := range condition.ExpectedSettings {
				if !api.DeploySettingEqual(field, current[field], expected) {
					api.WriteProblem(w, api.NewProblem(409, api.CodeDeploySettingsConflict, "Deploy settings changed", "targeted field changed: "+field))
					return
				}
			}
		}
		f.patches++
		if update.RequestTimeoutS != nil {
			f.app.Manifest.RequestTimeoutS = *update.RequestTimeoutS
		}
		if update.ScalingPolicy != nil {
			f.app.ScalingPolicy = update.ScalingPolicy
		}
		if f.losePatch {
			f.losePatch = false
			api.WriteProblem(w, api.NewProblem(503, "unavailable", "Reply lost", "settings were applied"))
			return
		}
		encode(f.app)
	case r.URL.Path == "/v1/apps/demo/deployments" && r.Method == http.MethodGet:
		dep := f.dep
		if f.newer {
			dep.ID = "dep-newer"
		}
		encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{dep}})
	case r.URL.Path == "/v1/apps/demo/deployments" && r.Method == http.MethodPost:
		f.submissions++
		encode(f.dep)
	case r.URL.Path == "/v1/uploads" && r.Method == http.MethodPost:
		var request api.UploadStartRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		f.total = request.TotalSize
		f.starts++
		encode(api.UploadStartResponse{UploadID: "upload-1", TotalSize: f.total, ChunkSize: 1048576})
	case r.URL.Path == "/v1/uploads/upload-1" && r.Method == http.MethodPatch:
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Upload-Offset", strconv.FormatInt(f.total, 10))
	case r.URL.Path == "/v1/uploads/upload-1" && r.Method == http.MethodGet:
		id := f.dep.ID
		encode(api.UploadSessionResponse{UploadID: "upload-1", AppSlug: "demo", TotalSize: f.total, ChunkSize: 1048576, ReceivedBytes: f.total, Status: f.uploadStatus, DeploymentID: &id})
	case r.URL.Path == "/v1/uploads/upload-1/commit" && r.Method == http.MethodPost:
		f.submissions++
		if f.loseCommit {
			_, _ = io.Copy(io.Discard, r.Body)
			f.mu.Unlock()
			<-r.Context().Done()
			f.mu.Lock()
			return
		}
		encode(f.dep)
	case r.URL.Path == "/v1/deployments/dep-1" && r.Method == http.MethodGet:
		encode(f.dep)
	default:
		http.NotFound(w, r)
	}
}

func savedResumeFixture(t *testing.T, f *deployResumeFixture) (*savedDeploySession, *bytes.Buffer) {
	t.Helper()
	deployReliabilityCLI(t, f.serve)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(resumeManifest), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := authedClient()
	if err != nil {
		t.Fatal(err)
	}
	s, lock, err := newSavedDeploySession(context.Background(), c, "demo", "app-1", dir, "intent", nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.submitted(f.dep, nil); err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	osStdout = out
	return s, out
}

func TestDeployResumeCompletesSettingsWithoutSource(t *testing.T) {
	f := newDeployResumeFixture()
	s, out := savedResumeFixture(t, f)
	for i := 0; i < 2; i++ {
		out.Reset()
		if code := cmdDeployTarball([]string{"resume", s.record.ID}); code != 0 {
			t.Fatalf("resume exit=%d", code)
		}
		var receipt DeployReceipt
		if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
			t.Fatal(err)
		}
		if receipt.ID != "dep-1" || receipt.SessionID != s.record.ID || receipt.ConfigurationComplete == nil || !*receipt.ConfigurationComplete {
			t.Fatalf("receipt=%+v", receipt)
		}
	}
	if f.submissions != 0 || f.patches != 2 || f.app.Manifest.RequestTimeoutS != 30 || f.app.ScalingPolicy.MaxInstances != 3 {
		t.Fatalf("fixture=%+v", f)
	}
}

func TestDeployResumeRejectsConflicts(t *testing.T) {
	for _, mode := range []string{"setting", "newer", "app", "deployment", "failed", "superseded", "rollout", "dark"} {
		t.Run(mode, func(t *testing.T) {
			f := newDeployResumeFixture()
			s, out := savedResumeFixture(t, f)
			switch mode {
			case "setting":
				f.app.Manifest.RequestTimeoutS = 45
			case "newer":
				f.newer = true
			case "app":
				f.app.ID = "app-replacement"
			case "deployment":
				f.dep.AppID = "other-app"
			case "failed", "superseded":
				f.dep.Status = mode
			case "rollout":
				s.record.WaitForRollout = true
				f.dep.RolloutState = rolloutStateAborted
				f.dep.CanaryTotalSteps = 3
			case "dark":
				s.record.DarkDeploy = true
				f.dep.TrafficPercent = 100
			}
			if err := s.save(); err != nil {
				t.Fatal(err)
			}
			if code := cmdDeployResume(context.Background(), []string{"--app", "demo"}); code == 0 {
				t.Fatalf("unexpected success: %s", out)
			}
			if f.patches != 0 || f.submissions != 0 {
				t.Fatalf("writes after conflict: patches=%d submissions=%d", f.patches, f.submissions)
			}
		})
	}
}

func TestDeployResumeRecoversLostSettingsReply(t *testing.T) {
	f := newDeployResumeFixture()
	s, out := savedResumeFixture(t, f)
	f.losePatch = true
	if code := cmdDeployResume(context.Background(), []string{s.record.ID}); code == 0 {
		t.Fatal("expected lost reply error")
	}
	var receipt DeployReceipt
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.ConfigurationComplete == nil || *receipt.ConfigurationComplete || receipt.WaitStage != "configuration" {
		t.Fatalf("false success receipt: %+v", receipt)
	}
	out.Reset()
	if code := cmdDeployResume(context.Background(), []string{s.record.ID}); code != 0 {
		t.Fatalf("resume exit=%d", code)
	}
	if f.patches != 2 {
		t.Fatalf("replayed PATCH: count=%d", f.patches)
	}
}

func TestDeployResumeRecognizesClearedRetryPolicy(t *testing.T) {
	f := newDeployResumeFixture()
	s, out := savedResumeFixture(t, f)
	s.record.Settings = []deploySettingsStep{{Name: "scaling",
		Before:  map[string]json.RawMessage{"retry_policy": json.RawMessage(`{"max_attempts":3}`)},
		Desired: map[string]json.RawMessage{"retry_policy": json.RawMessage(`{"max_attempts":0,"base_seconds":0,"max_seconds":0,"jitter_seconds":0}`)},
	}}
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	if code := cmdDeployResume(context.Background(), []string{s.record.ID}); code != 0 {
		t.Fatalf("resume cleared retry policy exit=%d output=%s", code, out)
	}
	if f.patches != 0 {
		t.Fatal("already-cleared retry policy was applied again")
	}
}

func TestDeployResumeGuardRejectsChangesAfterRead(t *testing.T) {
	for _, mode := range []string{"target", "new-deployment", "other-field-in-step", "unsupported-server"} {
		t.Run(mode, func(t *testing.T) {
			f := newDeployResumeFixture()
			s, out := savedResumeFixture(t, f)
			if mode == "other-field-in-step" {
				f.app.Manifest.StopSignal = "SIGTERM"
				s.record.Settings[0].Before["stop_signal"] = json.RawMessage(`""`)
				s.record.Settings[0].Desired["stop_signal"] = json.RawMessage(`"SIGTERM"`)
				if err := s.save(); err != nil {
					t.Fatal(err)
				}
			}
			f.beforePatch = func(current *deployResumeFixture) {
				switch mode {
				case "target":
					current.app.Manifest.RequestTimeoutS = 45
				case "new-deployment":
					current.newer = true
				case "other-field-in-step":
					current.app.Manifest.StopSignal = "SIGQUIT"
				case "unsupported-server":
					current.rejectConditions = true
				}
			}
			if code := cmdDeployResume(context.Background(), []string{s.record.ID}); code == 0 {
				t.Fatalf("stale update succeeded: %s", out)
			}
			if f.patches != 0 || f.patchAttempts != 1 || f.submissions != 0 {
				t.Fatalf("writes=%d attempts=%d deployments=%d", f.patches, f.patchAttempts, f.submissions)
			}
			if mode == "target" && f.app.Manifest.RequestTimeoutS != 45 {
				t.Fatal("concurrent edit overwritten")
			}
			c, err := authedClient()
			if err != nil {
				t.Fatal(err)
			}
			pending, err := loadSavedDeploySession(c, s.path)
			if err != nil || pending.record.Settings[0].Done {
				t.Fatalf("conflict did not preserve pending step: err=%v", err)
			}
			var receipt DeployReceipt
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.ConfigurationComplete == nil || *receipt.ConfigurationComplete || receipt.WaitStage != "configuration" {
				t.Fatalf("receipt=%+v", receipt)
			}
		})
	}
}

func TestDeployResumeUncertainUploadReadsOnly(t *testing.T) {
	for _, status := range []string{"open", "expired", "cancelled", "committed", "mismatch"} {
		t.Run(status, func(t *testing.T) {
			f := newDeployResumeFixture()
			s, _ := savedResumeFixture(t, f)
			f.total = 10
			f.uploadStatus = status
			s.record.DeploymentID = ""
			s.record.Outcome = deploySubmissionUnknown
			s.record.Upload = &deploySessionUpload{ID: "upload-1", TotalSize: 10, ChunkSize: 1048576}
			if status == "mismatch" {
				f.total = 11
				f.uploadStatus = "committed"
			}
			if err := s.save(); err != nil {
				t.Fatal(err)
			}
			code := cmdDeployResume(context.Background(), []string{s.record.ID})
			if (code == 0) != (status == "committed") {
				t.Fatalf("status=%s exit=%d", status, code)
			}
			if f.submissions != 0 || f.starts != 0 {
				t.Fatal("resume submitted source")
			}
		})
	}
}

func TestDeployResumeConnectionIsolationAndLock(t *testing.T) {
	f := newDeployResumeFixture()
	s, _ := savedResumeFixture(t, f)
	lock, err := lockDeploySession(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if code := cmdDeployResume(context.Background(), []string{s.record.ID}); code == 0 {
		t.Fatal("resume ignored session lock")
	}
	_ = lock.Close()
	t.Setenv("FAAS_TOKEN", "different-credential")
	if code := cmdDeployResume(context.Background(), []string{s.record.ID}); code == 0 {
		t.Fatal("resume crossed credential scope")
	}
	if f.patches != 0 {
		t.Fatal("unexpected settings write")
	}
}

func TestDeployResumeAcrossProcesses(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		name := "accepted"
		if uncertain {
			name = "unknown"
		}
		t.Run(name, func(t *testing.T) { testDeployResumeAcrossProcesses(t, uncertain) })
	}
}

func testDeployResumeAcrossProcesses(t *testing.T, uncertain bool) {
	t.Helper()
	f := newDeployResumeFixture()
	f.loseCommit = uncertain
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	defer srv.Close()
	stateDir := t.TempDir()
	sourceDir := t.TempDir()
	for name, data := range map[string]string{"package.json": "{}", "index.js": "console.log('fixture')", "gregale.yaml": resumeManifest} {
		if err := os.WriteFile(filepath.Join(sourceDir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(mode, dir string) {
		t.Helper()
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestDeployResumeProcessHelper$")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GREGALE_DEPLOY_RESUME_MODE="+mode, "GREGALE_DEPLOY_RESUME_STATE="+stateDir, "FAAS_API="+srv.URL, "FAAS_TOKEN=fp_resume_fixture")
		if mode == "submit" {
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			accepted := filepath.Join(stateDir, "accepted")
			limit := time.Now().Add(10 * time.Second)
			for {
				_, err := os.Stat(accepted)
				ready := err == nil
				if uncertain {
					f.mu.Lock()
					ready = f.submissions == 1
					f.mu.Unlock()
				}
				if ready {
					break
				}
				if time.Now().After(limit) {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatalf("deployment acceptance was not saved: %s", &output)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err == nil {
				t.Fatal("deploy process was not killed")
			}
			return
		}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s process: %v\n%s", mode, err, output)
		}
	}
	run("submit", sourceDir)
	files, err := filepath.Glob(filepath.Join(stateDir, "gregale", "deploys", "*", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("sessions=%v err=%v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil || bytes.Contains(data, []byte("fp_resume_fixture")) || bytes.Contains(data, []byte(sourceDir)) {
		t.Fatalf("private recovery metadata: %s, err=%v", data, err)
	}
	var record deploySessionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if uncertain && (record.DeploymentID != "" || record.Outcome != deploySubmissionUnknown || record.Upload == nil) {
		t.Fatalf("missing durable upload recovery before commit response: %+v", record)
	}
	info, err := os.Stat(files[0])
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions=%v err=%v", info, err)
	}
	if err := os.RemoveAll(sourceDir); err != nil {
		t.Fatal(err)
	}
	run("resume", t.TempDir())
	if f.submissions != 1 || f.starts != 1 || f.patches != 2 || f.app.Manifest.RequestTimeoutS != 30 || f.app.ScalingPolicy.MaxInstances != 3 {
		t.Fatalf("fixture=%+v", f)
	}
}

func TestDeployResumeProcessHelper(t *testing.T) {
	mode := os.Getenv("GREGALE_DEPLOY_RESUME_MODE")
	if mode == "" {
		t.Skip("restart helper")
	}
	t.Setenv("XDG_STATE_HOME", os.Getenv("GREGALE_DEPLOY_RESUME_STATE"))
	jsonOutput = true
	var output bytes.Buffer
	osStdout, osStderr = &output, &output
	if mode == "submit" {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		code := cmdDeployTarballToExisting(ctx, []string{"--name", "demo", "--path", ".", "--no-doctor", "--secret-scan", "off"}, true, deployExecution{onQueued: func(api.DeploymentResponse) {
			if err := os.WriteFile(filepath.Join(os.Getenv("GREGALE_DEPLOY_RESUME_STATE"), "accepted"), []byte("accepted"), 0600); err != nil {
				t.Fatal(err)
			}
			// The parent kills this process after acceptance is durably saved.
			// No deferred cleanup or orderly cancellation runs in this process.
			timer := time.NewTimer(time.Minute)
			defer timer.Stop()
			<-timer.C
			cancel()
		}})
		if code != 130 {
			t.Fatalf("interrupted deploy exit=%d output=%s", code, &output)
		}
	} else {
		if code := cmdDeployResume(context.Background(), []string{"--app", "demo"}); code != 0 {
			t.Fatalf("resume exit=%d output=%s", code, &output)
		}
		var receipt DeployReceipt
		if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
			t.Fatal(err)
		}
		if receipt.ID != "dep-1" || receipt.ConfigurationComplete == nil || !*receipt.ConfigurationComplete || !strings.Contains(receipt.ResumeCommand, "deploy resume") {
			t.Fatalf("receipt=%+v", receipt)
		}
	}
}

func TestDeployResumeRejectsMissingOrUnsafeSession(t *testing.T) {
	f := newDeployResumeFixture()
	s, _ := savedResumeFixture(t, f)
	for _, args := range [][]string{nil, {"../escape"}, {s.record.ID, "--timeout", "0"}, {s.record.ID, "--app", "other-app"}} {
		if code := cmdDeployResume(context.Background(), args); code == 0 {
			t.Fatalf("accepted invalid args %v", args)
		}
	}
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	if code := cmdDeployResume(context.Background(), []string{s.record.ID}); code == 0 {
		t.Fatal("accepted missing session")
	}
	if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing session recreated: %v", err)
	}
}

func TestDeployResumeKeepsEarlierSessionAndSelectsLatestAttempt(t *testing.T) {
	f := newDeployResumeFixture()
	f.app.Manifest.Env = map[string]string{"TOKEN": "fixture-secret-value"}
	first, out := savedResumeFixture(t, f)
	c, err := authedClient()
	if err != nil {
		t.Fatal(err)
	}
	second, lock, err := newSavedDeploySession(context.Background(), c, "demo", "app-1", "", "new-intent", nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.submitted(api.DeploymentResponse{}, submissionError(deploySubmissionRejected, errors.New("rejected"))); err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()
	if code := cmdDeployResume(context.Background(), []string{"--app", "demo"}); code == 0 {
		t.Fatal("latest rejected attempt fell back to an older deployment")
	}
	if f.patches != 0 {
		t.Fatal("latest rejected attempt changed settings")
	}
	out.Reset()
	if code := cmdDeployResume(context.Background(), []string{first.record.ID}); code != 0 {
		t.Fatalf("earlier saved session exit=%d", code)
	}
	data, err := os.ReadFile(first.path)
	if err != nil || bytes.Contains(data, []byte("fixture-secret-value")) {
		t.Fatalf("session stored environment values: err=%v", err)
	}
	if _, err := os.Stat(second.path); err != nil {
		t.Fatalf("second record was removed: %v", err)
	}
}

func TestDeploySessionWriteFailureStopsSubmission(t *testing.T) {
	f := newDeployResumeFixture()
	deployReliabilityCLI(t, f.serve)
	t.Chdir(t.TempDir())
	stateFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(stateFile, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", stateFile)
	if code := cmdDeployTarballToExisting(context.Background(), []string{"--name", "demo", "--image", "registry.example/demo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--no-wait", "--no-doctor"}, true); code == 0 {
		t.Fatal("submission continued without durable recovery")
	}
	if f.submissions != 0 {
		t.Fatal("deployment submitted despite recovery write failure")
	}
}

func TestDeployResumeHelpIsLocalAndHumanRecovery(t *testing.T) {
	f := newDeployResumeFixture()
	s, out := savedResumeFixture(t, f)
	jsonOutput = false
	if code := run([]string{"deploy", "resume", "--help"}); code != 0 {
		t.Fatalf("resume help exit=%d", code)
	}
	if !strings.Contains(out.String(), "--app") || !strings.Contains(out.String(), "--timeout") {
		t.Fatalf("help=%s", out)
	}
	out.Reset()
	if code := cmdDeployResume(context.Background(), []string{s.record.ID}); code != 0 {
		t.Fatalf("human resume exit=%d", code)
	}
	if !strings.Contains(out.String(), "Deployed") || f.patches != 2 {
		t.Fatalf("output=%s patches=%d", out, f.patches)
	}
}
