package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The shared SSE reader writes raw build logs to native stdout. Capture that
// too, so tests prove quiet launches do not leak logs outside the writer seam.
func startTestNativeOutput(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "native-output")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = file
	t.Cleanup(func() {
		os.Stdout = previous
		_ = file.Close()
	})
	return path
}

func TestStartSuccessResponsePreviewStaysShortAndSanitized(t *testing.T) {
	stdout, _ := startTestEnvironment(t)
	runner := startTestRunner(t, startTestSource(t), "")
	runner.startedAt = time.Now().Add(-3 * time.Second)
	result := startRequestResult{path: "/", appURL: "https://preview.example.test/", httpStatus: 200, body: strings.Repeat("line\x1b\n", 30), elapsed: 42 * time.Millisecond}
	if code := runner.verifyRequest(result, ""); code != 0 {
		t.Fatalf("code=%d stdout=%s", code, stdout)
	}
	got := stdout.String()
	if strings.Count(got, "    line") != 8 || !strings.Contains(got, "response shortened") || strings.Contains(got, "\x1b") || !strings.Contains(got, "Session") || !strings.Contains(got, "42ms") {
		t.Fatalf("response preview was not bounded and sanitized: %s", got)
	}
}

func TestStartProgressIgnoresReplayAndNeverInventsCompletion(t *testing.T) {
	var output bytes.Buffer
	p := newStartProgress(&output, false, "Waiting for deployment")
	defer p.Close()
	p.observeStage("future_stage", stageStatusCompleted, 0, "")
	p.observeStage("source_download", stageStatusPending, 0, "")
	p.observeStage("image_build", stageStatusInProgress, 0, "")
	p.observeStage("image_build", stageStatusCompleted, 1000, "")
	p.observeStage("snapshot_prepare", stageStatusInProgress, 0, "")
	p.observeStage("image_build", stageStatusInProgress, 0, "")
	p.observeStage("readiness", stageStatusCompleted, 1000, "")
	got := output.String()
	for _, label := range []string{"Building app", "Starting app", "Checking readiness"} {
		if strings.Count(got, label) != 1 {
			t.Errorf("replayed or missing phase %q: %s", label, got)
		}
	}
	for _, forbidden := range []string{"App ready", "future_stage", "100%", "\x1b"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("unsupported progress %q: %s", forbidden, got)
		}
	}
}

func TestStartProgressFailureAndCloseCannotBeOverwritten(t *testing.T) {
	var output safeBuffer
	p := newStartProgress(&output, true, "Waiting for deployment")
	p.observeStage("image_build", stageStatusFailed, 0, "private server detail")
	failed := output.String()
	p.observeStage("readiness", stageStatusCompleted, 1000, "")
	if output.String() != failed || strings.Contains(failed, "private server detail") {
		t.Fatalf("failure overwritten or raw reason leaked: %s", output.String())
	}
	p.Close()
	p.Close()
	select {
	case <-p.done:
	default:
		t.Fatal("clock still running after Close")
	}
	closed := output.String()
	p.observeStage("readiness", stageStatusCompleted, 1000, "")
	if output.String() != closed || !strings.HasSuffix(closed, "\n") {
		t.Fatalf("progress touched the next prompt: %s", output.String())
	}
}

func TestStartResumeCompactProgressWithLegacyPollingAndFailure(t *testing.T) {
	for _, mode := range []string{"legacy", "polling", "failed", "unreachable"} {
		t.Run(mode, func(t *testing.T) {
			stdout, stderr := startTestEnvironment(t)
			nativePath := startTestNativeOutput(t)
			reads := 0
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/first-app":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app"})
				case "/v1/deployments/d1":
					reads++
					dep := api.DeploymentResponse{ID: "d1", AppID: "app-1", Status: statusLive}
					if mode == "failed" {
						dep.Status, dep.Error = deploymentStatusFailed, "user_error"
					}
					if reads == 1 || mode == "unreachable" {
						dep.Status = "pending"
					}
					_ = json.NewEncoder(w).Encode(dep)
				case "/v1/deployments/d1/logs":
					if mode == "polling" || mode == "unreachable" {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "event: log\ndata: {\"line\":\"raw compiler output\"}\n\n")
					status := statusLive
					if mode == "failed" {
						_, _ = fmt.Fprint(w, "event: stage\ndata: {\"name\":\"image_build\",\"status\":\"failed\"}\n\n")
						status = deploymentStatusFailed
					}
					_, _ = fmt.Fprintf(w, "event: status\ndata: {\"status\":%q}\n\n", status)
				default:
					http.NotFound(w, r)
				}
			})
			runner := startTestRunner(t, startTestSource(t), "")
			runner.session.DeploymentID, runner.session.AppID = "d1", "app-1"
			code := runner.resume()
			ready := mode == "legacy" || mode == "polling"
			if (code == 0) != ready || strings.Contains(stdout.String(), "App ready") != ready {
				t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr(), stdout)
			}
			if mode == "failed" && !strings.Contains(stderr(), "Show deployment logs") {
				t.Errorf("missing recovery guidance: %s", stderr())
			}
			if mode == "unreachable" && code != 3 {
				t.Errorf("unreachable wait code=%d, want 3", code)
			}
			if mode != "failed" && (strings.Contains(stdout.String(), "Building app") || strings.Contains(stdout.String(), "Starting app")) {
				t.Errorf("legacy response invented phases: %s", stdout)
			}
			native, err := os.ReadFile(nativePath)
			if err != nil || strings.Contains(string(native), "raw compiler output") || strings.Contains(stdout.String(), "Deployed.") {
				t.Fatalf("compact output bypassed: native=%q stdout=%s err=%v", native, stdout, err)
			}
			if runner.progress != nil {
				t.Fatal("progress survived the session")
			}
		})
	}
}
