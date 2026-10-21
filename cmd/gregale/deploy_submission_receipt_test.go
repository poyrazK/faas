package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDeploySubmissionReceiptAcrossProcesses(t *testing.T) {
	for _, transport := range []string{"image", "multipart", "source-ref"} {
		t.Run(transport, func(t *testing.T) {
			f := newDeployResumeFixture()
			accepted := make(chan string, 1)
			var attemptID string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/apps/demo/deployments") {
					_, _ = io.Copy(io.Discard, r.Body)
					f.mu.Lock()
					f.submissions++
					attemptID = r.Header.Get(api.DeploymentAttemptHeader)
					id := attemptID
					f.mu.Unlock()
					accepted <- id
					// Acceptance is durable, but no reply reaches the child.
					<-r.Context().Done()
					return
				}
				if r.URL.Path == "/v1/uploads" {
					http.NotFound(w, r)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v1/apps/demo/deployment-submissions/") {
					f.mu.Lock()
					defer f.mu.Unlock()
					if r.Method != http.MethodGet || strings.TrimPrefix(r.URL.Path, "/v1/apps/demo/deployment-submissions/") != attemptID {
						t.Errorf("receipt request=%v", r)
						http.NotFound(w, r)
						return
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentSubmissionReceipt{AttemptID: attemptID, AppID: f.app.ID, DeploymentID: f.dep.ID, AcceptedAt: time.Now().UTC()})
					return
				}
				f.serve(w, r)
			}))
			defer srv.Close()
			stateDir, sourceDir := t.TempDir(), t.TempDir()
			for name, content := range map[string]string{"package.json": "{}", "index.js": "console.log('fixture')", "gregale.yaml": resumeManifest} {
				if err := os.WriteFile(filepath.Join(sourceDir, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			run := func(mode, dir string) *exec.Cmd {
				cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestDeploySubmissionReceiptProcessHelper$")
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "GREGALE_RECEIPT_MODE="+mode, "GREGALE_RECEIPT_TRANSPORT="+transport, "GREGALE_DEPLOY_RESUME_STATE="+stateDir, "FAAS_API="+srv.URL, "FAAS_TOKEN=fp_receipt_fixture")
				return cmd
			}
			cmd := run("submit", sourceDir)
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			select {
			case id := <-accepted:
				if !api.ValidDeploymentAttemptID(id) {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatalf("attempt was not sent: %q output=%s", id, &output)
				}
			case <-time.After(20 * time.Second):
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("submission was not accepted: %s", &output)
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			files, err := filepath.Glob(filepath.Join(stateDir, "gregale", "deploys", "*", "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("sessions=%v err=%v", files, err)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var record deploySessionRecord
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			if record.AttemptID != attemptID || record.DeploymentID != "" || record.Outcome != deploySubmissionUnknown || record.Upload != nil {
				t.Fatalf("missing pre-submission journal: %+v", record)
			}
			if bytes.Contains(data, []byte("fp_receipt_fixture")) || bytes.Contains(data, []byte(sourceDir)) {
				t.Fatalf("private metadata leaked: %s", data)
			}
			if err := os.RemoveAll(sourceDir); err != nil {
				t.Fatal(err)
			}
			if data, err := run("resume", t.TempDir()).CombinedOutput(); err != nil {
				t.Fatalf("resume: %v output=%s", err, data)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.submissions != 1 || f.starts != 0 {
				t.Fatalf("resume resubmitted: submissions=%d starts=%d", f.submissions, f.starts)
			}
		})
	}
}

func TestDeploySubmissionReceiptProcessHelper(t *testing.T) {
	mode := os.Getenv("GREGALE_RECEIPT_MODE")
	if mode == "" {
		t.Skip("process helper")
	}
	t.Setenv("XDG_STATE_HOME", os.Getenv("GREGALE_DEPLOY_RESUME_STATE"))
	jsonOutput = true
	var output bytes.Buffer
	osStdout, osStderr = &output, &output
	if mode == "submit" {
		var code int
		if os.Getenv("GREGALE_RECEIPT_TRANSPORT") == "source-ref" {
			code = cmdDeployRepoSourceRefContext(context.Background(), "demo", "o/r", "main", api.DeployAnnotations{})
		} else {
			args := []string{"--name", "demo", "--no-doctor", "--secret-scan", "off"}
			if os.Getenv("GREGALE_RECEIPT_TRANSPORT") == "image" {
				args = append(args, "--image", "example.test/image@sha256:"+strings.Repeat("a", 64))
			} else {
				args = append(args, "--path", ".")
			}
			code = cmdDeployTarballToExisting(context.Background(), args, true, deployExecution{})
		}
		t.Fatalf("submission returned before process death: code=%d output=%s", code, &output)
	}
	if code := cmdDeployResume(context.Background(), []string{"--app", "demo"}); code != 0 {
		t.Fatalf("resume exit=%d output=%s", code, &output)
	}
	var receipt DeployReceipt
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.ID != "dep-1" || receipt.ConfigurationComplete == nil || !*receipt.ConfigurationComplete {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestDeploySubmissionReceiptResumeFailsClosed(t *testing.T) {
	for _, mode := range []string{"missing", "unavailable", "wrong-attempt", "wrong-app", "empty-deployment", "empty-time"} {
		t.Run(mode, func(t *testing.T) {
			f := newDeployResumeFixture()
			var expected string
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/v1/apps/demo/deployment-submissions/") {
					if mode == "missing" {
						http.NotFound(w, r)
						return
					}
					if mode == "unavailable" {
						api.WriteProblem(w, api.ErrCapacity("fixture"))
						return
					}
					receipt := api.DeploymentSubmissionReceipt{AttemptID: expected, AppID: f.app.ID, DeploymentID: f.dep.ID, AcceptedAt: time.Now()}
					switch mode {
					case "wrong-attempt":
						receipt.AttemptID = strings.Repeat("f", 32)
					case "wrong-app":
						receipt.AppID = "app-other"
					case "empty-deployment":
						receipt.DeploymentID = ""
					case "empty-time":
						receipt.AcceptedAt = time.Time{}
					}
					_ = json.NewEncoder(w).Encode(receipt)
					return
				}
				f.serve(w, r)
			})
			c, err := authedClient()
			if err != nil {
				t.Fatal(err)
			}
			s, lock, err := newSavedDeploySession(t.Context(), c, "demo", f.app.ID, "", "intent", nil, false, false)
			if err != nil {
				t.Fatal(err)
			}
			_ = lock.Close()
			expected = s.record.AttemptID
			if code := cmdDeployResume(t.Context(), []string{s.record.ID}); code == 0 {
				t.Fatal("unverified receipt succeeded")
			}
			if f.submissions != 0 || f.starts != 0 || f.patches != 0 {
				t.Fatalf("unverified receipt mutated fixture: %+v", f)
			}
			pending, err := loadSavedDeploySession(c, s.path)
			if err != nil || pending.record.Outcome != deploySubmissionUnknown || pending.record.DeploymentID != "" {
				t.Fatalf("pending=%+v err=%v", pending, err)
			}
		})
	}
}

func TestDeploySubmissionReceiptConflictsRemainUncertain(t *testing.T) {
	for _, code := range []string{api.CodeDeploymentAttemptAccepted, api.CodeDeploymentAttemptPending} {
		err := &api.APIError{Problem: *api.NewProblem(409, code, "Attempt conflict", "fixture")}
		if got := submissionOutcome(api.DeploymentResponse{}, err); got != deploySubmissionUnknown {
			t.Fatalf("%s outcome=%s", code, got)
		}
	}
}
