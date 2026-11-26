package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdDeploy_JSONDefaultWaitPropagatesTerminalFailure(t *testing.T) {
	resetJSONOutput()
	defer resetJSONOutput()

	var deploymentReads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "my-app"})
		case "/v1/apps/my-app/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"})
		case "/v1/deployments/d1":
			deploymentReads.Add(1)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "failed"})
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	jsonOutput = true

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdDeployTarball([]string{"--image", "registry.x/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--name", "my-app"}); code != 1 {
		t.Fatalf("cmdDeploy --json exit = %d, want terminal failure exit 1", code)
	}
	if deploymentReads.Load() == 0 {
		t.Fatal("default JSON deploy returned without reading the terminal deployment")
	}
	var receipt api.DeploymentResponse
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if receipt.Status != "failed" {
		t.Fatalf("receipt status = %q, want failed", receipt.Status)
	}
}

func TestCmdDeploy_JSONDefaultWaitHonorsTimeout(t *testing.T) {
	t.Chdir(t.TempDir())
	appID, deploymentID, instanceID := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
	var submissions atomic.Int32
	var deploymentReads atomic.Int32
	var live atomic.Bool
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps", "/v1/apps/my-app":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: appID, Slug: "my-app"})
		case "/v1/apps/my-app/deployments":
			submissions.Add(1)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: deploymentID, AppID: appID, Status: "pending"})
		case "/v1/deployments/" + deploymentID:
			deploymentReads.Add(1)
			status := "pending"
			if live.Load() {
				status = statusLive
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: deploymentID, AppID: appID, Scope: "default", Status: status})
		case "/v1/deployments/response-check/capabilities":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponseCheckCapabilities{ProtocolVersion: api.DeploymentResponseCheckProtocolVersion, Available: true})
		case "/v1/apps/my-app/deployments/" + deploymentID + "/response-check":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponseCheckResult{AppID: appID, DeploymentID: deploymentID, InstanceID: instanceID, Scope: "default", HTTPStatus: 200, Body: "ready"})
		default:
			http.NotFound(w, r)
		}
	})
	t.Setenv("FAAS_JSON", "0")
	stdout := osStdout.(*bytes.Buffer)
	stderr, restoreErr := captureStderr(t)
	defer restoreErr()

	started := time.Now()
	if code := run([]string{"--profile", "default", "--non-interactive", "--json", "deploy", "--image", "registry.x/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--name", "my-app", "--yes", "--timeout", "1"}); code != 3 {
		t.Fatalf("deploy exit=%d, want timeout exit 3, stderr=%s", code, stderr)
	}
	elapsed := time.Since(started)
	if elapsed > 3*time.Second {
		t.Fatalf("--timeout 1 returned after %s, want no more than 3s", elapsed)
	}
	if deploymentReads.Load() == 0 {
		t.Fatal("default JSON deploy did not poll the deployment before timing out")
	}
	problem := assertOneProblem(t, stderr.String())
	// A wait deadline is not a network failure (CLI audit 2026-10-31).
	if problem.Code != "deployment_wait_stopped" || !strings.Contains(problem.Detail, "timed out") || !strings.Contains(problem.Hint, "saved deploy") {
		t.Fatalf("stderr missing timeout recovery: %+v", problem)
	}
	var receipt DeployReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if receipt.ID != deploymentID || receipt.Status != "pending" {
		t.Fatalf("timeout receipt = id %q status %q, want accepted %s pending", receipt.ID, receipt.Status, deploymentID)
	}
	if !receipt.TimedOut {
		t.Fatal("timeout receipt timed_out = false, want true")
	}
	if !validDeploySessionID(receipt.SessionID) {
		t.Fatalf("timeout receipt missing valid session ID: %q", receipt.SessionID)
	}
	wantCommand := "gregale --profile default --non-interactive --json release resume " + receipt.SessionID
	assertDeployRecoveryCommand(t, receipt.Recovery, wantCommand)
	if receipt.ResumeCommand != wantCommand {
		t.Fatalf("timeout receipt resume_command = %q, want %q", receipt.ResumeCommand, wantCommand)
	}
	var problemDetails struct {
		Recovery *deployRecovery `json:"recovery"`
	}
	if err := json.Unmarshal([]byte(stderr.String()), &problemDetails); err != nil {
		t.Fatal(err)
	}
	assertDeployRecoveryCommand(t, problemDetails.Recovery, wantCommand)

	live.Store(true)
	stdout.Reset()
	runEmittedDeployCommand(t, problemDetails.Recovery.NextCommand)
	var resumed DeployReceipt
	if err := json.Unmarshal(stdout.Bytes(), &resumed); err != nil || resumed.ID != deploymentID || resumed.Status != statusLive || resumed.SessionID != receipt.SessionID {
		t.Fatalf("resume stdout=%s err=%v", stdout, err)
	}
	verifyResumedDeployment(t, "my-app", resumed.ID)
	if submissions.Load() != 1 {
		t.Fatalf("submitted %d builds, want 1", submissions.Load())
	}
}

func assertDeployRecoveryCommand(t *testing.T, recovery *deployRecovery, want string) {
	t.Helper()
	if recovery == nil || recovery.ResumeCommand != want || recovery.NextCommand != want {
		t.Fatalf("recovery=%+v, want next_command and resume_command to both be %q", recovery, want)
	}
}

func runEmittedDeployCommand(t *testing.T, command string) {
	t.Helper()
	args := strings.Fields(command) // Test data uses shell-safe profiles and session IDs.
	if len(args) == 0 || args[0] != "gregale" {
		t.Fatalf("invalid Gregale command: %q", command)
	}
	if code := run(args[1:]); code != 0 {
		t.Fatalf("%s exited %d, stderr=%s", command, code, osStderr)
	}
}

func verifyResumedDeployment(t *testing.T, slug, deploymentID string) {
	t.Helper()
	stdout := osStdout.(*bytes.Buffer)
	stdout.Reset()
	if code := run([]string{"--non-interactive", "--json", "release", "verify", deploymentID, "--app", slug}); code != 0 {
		t.Fatalf("verify exited %d, stderr=%s", code, osStderr)
	}
	var result struct {
		api.DeploymentResponseCheckResult
		Verified bool `json:"verified"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Verified || result.DeploymentID != deploymentID || result.HTTPStatus != http.StatusOK || result.Body != "ready" {
		t.Fatalf("verify stdout=%s err=%v", stdout, err)
	}
}
