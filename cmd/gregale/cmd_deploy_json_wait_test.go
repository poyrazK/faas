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

	if code := cmdDeployTarball([]string{"--image", "registry.x/app@sha256:abc", "--name", "my-app"}); code != 1 {
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
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"})
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	jsonOutput = true

	var stdout, stderr bytes.Buffer
	oldOut, oldErr := osStdout, osStderr
	osStdout, osStderr = &stdout, &stderr
	defer func() { osStdout, osStderr = oldOut, oldErr }()

	started := time.Now()
	if code := cmdDeployTarball([]string{"--image", "registry.x/app@sha256:abc", "--name", "my-app", "--timeout", "1"}); code != 3 {
		t.Fatalf("cmdDeploy --json --timeout 1 exit = %d, want timeout exit 3", code)
	}
	elapsed := time.Since(started)
	if elapsed > 3*time.Second {
		t.Fatalf("--timeout 1 returned after %s, want no more than 3s", elapsed)
	}
	if deploymentReads.Load() == 0 {
		t.Fatal("default JSON deploy did not poll the deployment before timing out")
	}
	if !strings.Contains(stderr.String(), "wait deadline") {
		t.Fatalf("stderr missing timeout explanation: %s", stderr.String())
	}
	var receipt api.DeploymentResponse
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if receipt.ID != "d1" || receipt.Status != "pending" {
		t.Fatalf("timeout receipt = id %q status %q, want accepted d1 pending", receipt.ID, receipt.Status)
	}
}
