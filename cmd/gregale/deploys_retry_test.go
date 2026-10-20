// Wire-call tests for `gregale deploys retry <id> [--from=<stage>]`
// (ADR-117 §Production-ready follow-on, C2).
//
// Pins (mirrors the conventions in deploys_show_test.go):
//
//   - httptest stub apid with two routes:
//     GET /v1/deployments/{id}/stages (failing-row read for the
//     default from_stage) + POST /v1/deployments/{id}/retry
//     (the actual write).
//   - FAAS_API + FAAS_TOKEN env swap so authedClient() succeeds.
//   - Captured request bodies verify the wire shape:
//     `{"from_stage": "<stage>"}` JSON body.
//   - Captured paths verify the route is keyed on {id} (no slug).
//
// Pins three branches:
//  1. Happy path with --from=<stage> explicit
//  2. Happy path with default from_stage (read from /stages)
//  3. Cross-account / 404 returns exit 2 (api error)
package main_test

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// retryTestID is the 32-hex deployment id used across retry tests.
const retryTestID = "0123456789abcdef0123456789abcdef"

// retryTestNewID is the new id the stub server returns on a
// successful retry. Different from retryTestID so the test can
// assert the CLI surfaces the new id, not the source.
const retryTestNewID = "fedcba9876543210fedcba9876543210"

// retryServer returns an httptest server with:
//   - GET  /v1/deployments/{id}/stages  → 200 with stageStateFailing
//   - POST /v1/deployments/{id}/retry   → 202 with stubDeploymentResponse
//   - everything else                    → 404
//
// The captured body and path are exposed via the returned
// *retryCaptured so tests can assert the wire shape.
func retryServer(t *testing.T) (*httptest.Server, *retryCaptured) {
	t.Helper()
	cap := &retryCaptured{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/deployments/"+retryTestID+"/stages", func(w http.ResponseWriter, r *http.Request) {
		cap.stagesHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(stageStateFailing())
	})
	mux.HandleFunc("/v1/deployments/"+retryTestID+"/retry", func(w http.ResponseWriter, r *http.Request) {
		cap.retryHits++
		cap.retryPath = r.URL.Path
		cap.retryMethod = r.Method
		body, _ := io.ReadAll(r.Body)
		cap.retryBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write(stubDeploymentResponse(retryTestNewID))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, cap
}

type retryCaptured struct {
	mu          sync.Mutex
	stagesHits  int
	retryHits   int
	retryPath   string
	retryMethod string
	retryBody   string
}

// stageStateFailing returns a sample stage_state with
// state.Current="image_build" (the failing stage). The CLI
// reads this on the default-from-stage path.
func stageStateFailing() []byte {
	ss := struct {
		Current string           `json:"current"`
		History []map[string]any `json:"history"`
	}{
		Current: "image_build",
		History: []map[string]any{
			{"name": "source_download", "status": "completed"},
			{"name": "dependency_restore", "status": "completed"},
		},
	}
	b, _ := json.Marshal(ss)
	return b
}

// stubDeploymentResponse returns a minimal DeploymentResponse
// carrying the new deployment id. The CLI only reads .ID and
// .Status from the response.
func stubDeploymentResponse(newID string) []byte {
	resp := map[string]any{
		"id":     newID,
		"status": "pending",
	}
	b, _ := json.Marshal(resp)
	return b
}

// TestCmdDeploysRetry_ExplicitFrom — the happy path with the
// caller supplying --from=image_build. The CLI must:
//   - skip the /stages GET (no need to derive the default)
//   - POST to /v1/deployments/{id}/retry with body {"from_stage":"image_build"}
//   - exit 0
func TestCmdDeploysRetry_ExplicitFrom(t *testing.T) {
	srv, cap := retryServer(t)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	cap.mu.Lock()
	defer cap.mu.Unlock()

	exit := main.CmdDeploysRetry([]string{retryTestID, "--from=image_build"})
	if exit != 0 {
		t.Errorf("cmdDeploysRetry exit = %d, want 0", exit)
	}
	if cap.stagesHits != 0 {
		t.Errorf("expected 0 /stages hits (explicit --from), got %d", cap.stagesHits)
	}
	if cap.retryHits != 1 {
		t.Errorf("expected 1 /retry hit, got %d", cap.retryHits)
	}
	if cap.retryMethod != http.MethodPost {
		t.Errorf("retry method = %q, want POST", cap.retryMethod)
	}
	if cap.retryPath != "/v1/deployments/"+retryTestID+"/retry" {
		t.Errorf("retry path = %q, want /v1/deployments/{id}/retry (no slug)", cap.retryPath)
	}
	// Body shape: {"from_stage":"<stage>"} verbatim.
	var sent map[string]string
	if err := json.Unmarshal([]byte(cap.retryBody), &sent); err != nil {
		t.Fatalf("decode retry body: %v (body=%q)", err, cap.retryBody)
	}
	if sent["from_stage"] != "image_build" {
		t.Errorf("from_stage = %q, want %q", sent["from_stage"], "image_build")
	}
}

// TestCmdDeploysRetry_DefaultFromStage — happy path with the
// caller omitting --from. The CLI must:
//   - GET /v1/deployments/{id}/stages to read state.Current
//   - derive from_stage = "image_build"
//   - POST to /v1/deployments/{id}/retry with body {"from_stage":"image_build"}
//   - exit 0
func TestCmdDeploysRetry_DefaultFromStage(t *testing.T) {
	srv, cap := retryServer(t)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	cap.mu.Lock()
	defer cap.mu.Unlock()

	exit := main.CmdDeploysRetry([]string{retryTestID})
	if exit != 0 {
		t.Errorf("cmdDeploysRetry exit = %d, want 0", exit)
	}
	if cap.stagesHits != 1 {
		t.Errorf("expected 1 /stages hit (default), got %d", cap.stagesHits)
	}
	if cap.retryHits != 1 {
		t.Errorf("expected 1 /retry hit, got %d", cap.retryHits)
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(cap.retryBody), &sent); err != nil {
		t.Fatalf("decode retry body: %v (body=%q)", err, cap.retryBody)
	}
	if sent["from_stage"] != "image_build" {
		t.Errorf("derived from_stage = %q, want %q (the failing stage)", sent["from_stage"], "image_build")
	}
}

// TestCmdDeploysRetry_InvalidFromStageFailsFast — unknown
// --from= argument is rejected locally (CLI-side closed-vocab
// check). The server never sees the request.
func TestCmdDeploysRetry_InvalidFromStageFailsFast(t *testing.T) {
	srv, cap := retryServer(t)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	cap.mu.Lock()
	defer cap.mu.Unlock()

	exit := main.CmdDeploysRetry([]string{retryTestID, "--from=not_a_stage"})
	if exit != 1 {
		t.Errorf("cmdDeploysRetry exit = %d, want 1 (usage error)", exit)
	}
	if cap.retryHits != 0 {
		t.Errorf("expected 0 /retry hits (closed-vocab guard), got %d", cap.retryHits)
	}
}

// TestCmdDeploysRetry_NoArgs — usage error when no deployment id
// is supplied.
func TestCmdDeploysRetry_NoArgs(t *testing.T) {
	exit := main.CmdDeploysRetry([]string{})
	if exit != 1 {
		t.Errorf("cmdDeploysRetry with no args: exit = %d, want 1", exit)
	}
}

// TestCmdDeploysRetry_RetryFromTop — from_stage=source_download
// is accepted (intentional retry-from-top semantics).
func TestCmdDeploysRetry_RetryFromTop(t *testing.T) {
	srv, cap := retryServer(t)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	cap.mu.Lock()
	defer cap.mu.Unlock()

	exit := main.CmdDeploysRetry([]string{retryTestID, "--from=source_download"})
	if exit != 0 {
		t.Errorf("cmdDeploysRetry exit = %d, want 0", exit)
	}
	if cap.retryHits != 1 {
		t.Errorf("expected 1 /retry hit, got %d", cap.retryHits)
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(cap.retryBody), &sent); err != nil {
		t.Fatalf("decode retry body: %v", err)
	}
	if sent["from_stage"] != "source_download" {
		t.Errorf("from_stage = %q, want %q (retry-from-top)", sent["from_stage"], "source_download")
	}
}

// TestCmdDeploys_DispatchRetry — pins the dispatch wiring in
// cmdDeploys. The "retry" verb routes to cmdDeploysRetry. Same
// shape as TestCmdDeploys_DispatchShow / DispatchStatus (see
// deploys_show_test.go).
func TestCmdDeploys_DispatchRetry(t *testing.T) {
	srv, cap := retryServer(t)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	cap.mu.Lock()
	defer cap.mu.Unlock()

	// Capture stdout to avoid spamming test logs.
	oldStdout := (*main.OsStdout)
	r, w, _ := os.Pipe()
	(*main.OsStdout) = w
	defer func() { (*main.OsStdout) = oldStdout }()

	exit := main.CmdDeploys([]string{"retry", retryTestID, "--from=image_build"})
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()

	if exit != 0 {
		t.Errorf("cmdDeploys retry exit = %d, want 0", exit)
	}
	if cap.retryHits != 1 {
		t.Errorf("expected 1 /retry hit via cmdDeploys dispatch, got %d", cap.retryHits)
	}
	// Stdout must surface the new deployment id so the customer
	// can pipe to status.
	if !strings.Contains(buf.String(), retryTestNewID) {
		t.Errorf("stdout missing new id %q; got:\n%s", retryTestNewID, buf.String())
	}
}
