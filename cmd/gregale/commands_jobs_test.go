// Tests for `gregale jobs ...` cluster (ADR-099 PR-E). Mirrors
// commands_crons_info_test.go (smoke): httptest fake + t.Setenv +
// osStdout swap + jsonOutput swap. Three smoke levels live here:
//
//   - Dispatcher routes: each verb goes to the right SDK method on
//     the right HTTP verb, with the right path/body (Round-Trip pin
//     so a future wire-surface drift breaks the test).
//   - Local validation: bad ids / missing positionals fail with 1 and
//     zero server calls (Postel's-locally-strict posture).
//   - JSON envelope: --json emits the typed DTO on stdout so `jq`
//     pipelines stay stable.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// jobsInfoID is the 32-hex id used by every smoke test below.
// Matches jobIDPattern in commands_jobs.go.
const jobsInfoID = "0123456789abcdef0123456789abcde1"

// makeJobResponse builds the canned payload the SDK would emit for
// `GET /v1/jobs/<id>`. Pin every field so a future DTO drift breaks
// the test (otherwise the smoke grows stale).
func makeJobResponse() api.JobResponse {
	return api.JobResponse{
		ID:             jobsInfoID,
		Name:           "smoke-job",
		Kind:           "run_to_completion",
		ImageRef:       "sha256:" + strings.Repeat("a", 64),
		RAMMB:          512,
		TaskTimeoutS:   300,
		MaxParallelism: 10,
		RetryMax:       2,
		EnvOverrides:   map[string]string{"FOO": "bar"},
		Status:         "active",
		CreatedAt:      "2026-08-14T12:00:00Z",
		UpdatedAt:      "2026-08-14T12:00:00Z",
	}
}

// withJobsFakeServer swaps osStdout and FAAS_API/TOKEN for a fake
// HTTP server. The test function gets back the body it should have
// recorded for inspection. The atomic.Int32 counter is incremented
// for every recorded call so a future "no server call" assertion
// has zero false positives.
func withJobsFakeServer(t *testing.T, h http.HandlerFunc) (*atomic.Int32, *bytes.Buffer) {
	t.Helper()
	calls := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })
	return calls, &stdout
}

// --- dispatch / round-trip ------------------------------------------------

// TestCmdJobsInfo_RoundTrip pins the wire path: GET /v1/jobs/{id},
// JSON-or-human branch, renderJobInfo column shape.
func TestCmdJobsInfo_RoundTrip(t *testing.T) {
	var gotMethod, gotPath string
	calls, stdout := withJobsFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(makeJobResponse())
	})
	if code := cmdJobsInfo([]string{jobsInfoID}); code != 0 {
		t.Errorf("jobs info = %d, want 0", code)
	}
	if calls.Load() != 1 {
		t.Errorf("server calls = %d, want 1", calls.Load())
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/jobs/"+jobsInfoID {
		t.Errorf("path = %q, want /v1/jobs/<id>", gotPath)
	}
	for _, want := range []string{
		"job " + jobsInfoID,
		"  name:        smoke-job",
		"  kind:        run_to_completion",
		"  status:      active",
		"  image:       sha256:" + strings.Repeat("a", 64),
		"  ram_mb:      512",
		"  timeout_s:   300",
		"  parallelism: 10",
		"  retry_max:   2",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("body missing %q; got:\n%s", want, stdout.String())
		}
	}
}

// TestCmdJobsCreate_RoundTrip pins create: POST /v1/jobs with the
// constructed CreateJobRequest body. Env flags default to plan-tier
// minimums (ram=256, timeout=300, parallelism=1, retry=0).
func TestCmdJobsCreate_RoundTrip(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody api.CreateJobRequest
	calls, stdout := withJobsFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		resp := makeJobResponse()
		_ = json.NewEncoder(w).Encode(resp)
	})
	img := "sha256:" + strings.Repeat("b", 64)
	if code := cmdJobsCreate([]string{
		"--name", "create-job",
		"--image", img,
		"--ram", "1024",
		"--timeout", "600",
		"--parallelism", "5",
		"--retry-max", "3",
		"--env", "FOO=bar",
		"--env", "BAZ=qux",
	}); code != 0 {
		t.Errorf("jobs create = %d, want 0", code)
	}
	if calls.Load() != 1 {
		t.Errorf("server calls = %d, want 1", calls.Load())
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/jobs" {
		t.Errorf("path = %q, want /v1/jobs", gotPath)
	}
	if gotBody.Name != "create-job" || gotBody.ImageRef != img ||
		gotBody.RAMMB != 1024 || gotBody.TaskTimeoutS != 600 ||
		gotBody.MaxParallelism != 5 || gotBody.RetryMax != 3 ||
		gotBody.EnvOverrides["FOO"] != "bar" || gotBody.EnvOverrides["BAZ"] != "qux" {
		t.Errorf("body drift: %+v", gotBody)
	}
	// Human-mode PrintOK line echoes whatever the server returned —
	// the fake server in this test returns makeJobResponse() with
	// name "smoke-job", so pin that against the renderer. The round-
	// trip above already pins the request body.
	if !strings.Contains(stdout.String(), "Created job") || !strings.Contains(stdout.String(), "smoke-job") {
		t.Errorf("stdout missing created signal; got:\n%s", stdout.String())
	}
}

// TestCmdJobsUpdate_RoundTrip pins partial-update: PATCH /v1/jobs/{id}
// with the UpdateJobRequest. fs.Visit semantics: only the flags the
// user set populate the request.
func TestCmdJobsUpdate_RoundTrip(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody api.UpdateJobRequest
	calls, _ := withJobsFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(makeJobResponse())
	})
	if code := cmdJobsUpdate([]string{jobsInfoID, "--ram", "2048", "--parallelism", "12"}); code != 0 {
		t.Errorf("jobs update = %d, want 0", code)
	}
	if calls.Load() != 1 {
		t.Errorf("server calls = %d, want 1", calls.Load())
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/v1/jobs/"+jobsInfoID {
		t.Errorf("path = %q, want /v1/jobs/<id>", gotPath)
	}
	if gotBody.RAMMB == nil || *gotBody.RAMMB != 2048 {
		t.Errorf("ram_mb not propagated: %+v", gotBody.RAMMB)
	}
	if gotBody.MaxParallelism == nil || *gotBody.MaxParallelism != 12 {
		t.Errorf("parallelism not propagated: %+v", gotBody.MaxParallelism)
	}
	// ImageRef + env_overrides must NOT have been set (not flagged).
	if gotBody.ImageRef != nil {
		t.Errorf("image_ref leaked into unset slot: %+v", gotBody.ImageRef)
	}
	if gotBody.EnvOverrides != nil {
		t.Errorf("env_overrides leaked into unset slot: %+v", gotBody.EnvOverrides)
	}
}

// TestCmdJobsRm_RoundTrip pins delete: DELETE /v1/jobs/{id}.
func TestCmdJobsRm_RoundTrip(t *testing.T) {
	var gotMethod, gotPath string
	calls, _ := withJobsFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	if code := cmdJobsRm([]string{jobsInfoID}); code != 0 {
		t.Errorf("jobs rm = %d, want 0", code)
	}
	if calls.Load() != 1 || gotMethod != http.MethodDelete || gotPath != "/v1/jobs/"+jobsInfoID {
		t.Errorf("rm round-trip drift: calls=%d method=%q path=%q", calls.Load(), gotMethod, gotPath)
	}
}

// TestCmdJobsRun_RoundTrip pins run-creation: POST /v1/jobs/{id}/runs
// with the constructed CreateRunRequest.
func TestCmdJobsRun_RoundTrip(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody api.CreateRunRequest
	calls, _ := withJobsFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(api.JobRunResponse{ID: "0123456789abcdef0123456789abcde2", JobID: jobsInfoID, Tasks: 11})
	})
	if code := cmdJobsRun([]string{jobsInfoID, "--tasks", "11", "--parallelism", "4"}); code != 0 {
		t.Errorf("jobs run = %d, want 0", code)
	}
	if calls.Load() != 1 || gotMethod != http.MethodPost || gotPath != "/v1/jobs/"+jobsInfoID+"/runs" {
		t.Errorf("run create round-trip drift: calls=%d method=%q path=%q", calls.Load(), gotMethod, gotPath)
	}
	if gotBody.Tasks != 11 {
		t.Errorf("tasks body drift: %+v", gotBody.Tasks)
	}
	if gotBody.Parallelism == nil || *gotBody.Parallelism != 4 {
		t.Errorf("parallelism body drift: %+v", gotBody.Parallelism)
	}
}

// TestCmdJobsRuns_RoundTrip pins run-list: GET /v1/jobs/{id}/runs.
func TestCmdJobsRuns_RoundTrip(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	calls, _ := withJobsFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(api.ListRunsResponse{
			Runs: []api.JobRunResponse{
				{ID: "0123456789abcdef0123456789abcde2", JobID: jobsInfoID, AggregateStatus: "queued"},
			},
		})
	})
	if code := cmdJobsRuns([]string{"--limit", "20", jobsInfoID}); code != 0 {
		t.Errorf("jobs runs = %d, want 0", code)
	}
	if calls.Load() != 1 {
		t.Errorf("server calls = %d, want 1", calls.Load())
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/jobs/"+jobsInfoID+"/runs" {
		t.Errorf("path = %q, want /v1/jobs/<id>/runs", gotPath)
	}
	if !strings.Contains(gotQuery, "limit=20") {
		t.Errorf("query missing limit: %q", gotQuery)
	}
}

// TestCmdJobsList_RoundTrip pins list: GET /v1/jobs?before=...&limit=...
func TestCmdJobsList_RoundTrip(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	calls, _ := withJobsFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(api.ListJobsResponse{
			Jobs:  []api.JobResponse{makeJobResponse()},
			Quota: 25,
			Count: 1,
		})
	})
	if code := cmdJobsList([]string{"--limit", "10"}); code != 0 {
		t.Errorf("jobs list = %d, want 0", code)
	}
	if calls.Load() != 1 || gotMethod != http.MethodGet || gotPath != "/v1/jobs" {
		t.Errorf("list round-trip drift: calls=%d method=%q path=%q", calls.Load(), gotMethod, gotPath)
	}
	if !strings.Contains(gotQuery, "limit=10") {
		t.Errorf("query missing limit: %q", gotQuery)
	}
}

// TestCmdJobsCancel_RoundTrip pins cancel: POST /v1/runs/{run_id}/cancel.
func TestCmdJobsCancel_RoundTrip(t *testing.T) {
	var gotMethod, gotPath string
	calls, _ := withJobsFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(api.JobRunResponse{ID: jobsInfoID, JobID: jobsInfoID, AggregateStatus: "cancelled"})
	})
	if code := cmdJobsCancel([]string{jobsInfoID}); code != 0 {
		t.Errorf("jobs cancel = %d, want 0", code)
	}
	if calls.Load() != 1 || gotMethod != http.MethodPost || gotPath != "/v1/runs/"+jobsInfoID+"/cancel" {
		t.Errorf("cancel round-trip drift: calls=%d method=%q path=%q", calls.Load(), gotMethod, gotPath)
	}
}

// --- dispatcher routing ---------------------------------------------------

// TestCmdJobsDispatcher_UnknownVerb_NoServerCall asserts the
// dispatcher rejects unknown positionals with 1 + zero server
// traffic (mirrors cmdCrons).
func TestCmdJobsDispatcher_UnknownVerb_NoServerCall(t *testing.T) {
	calls, _ := withJobsFakeServer(t, func(http.ResponseWriter, *http.Request) {})
	if code := cmdJobs([]string{"wibble"}); code == 0 {
		t.Errorf("jobs wibble = 0, want non-zero")
	}
	if calls.Load() != 0 {
		t.Errorf("server calls = %d, want 0", calls.Load())
	}
}

// TestCmdJobsDispatcher_NoVerb asserts that `gregale jobs` with no
// args prints usage + returns 1.
func TestCmdJobsDispatcher_NoVerb(t *testing.T) {
	calls, _ := withJobsFakeServer(t, func(http.ResponseWriter, *http.Request) {})
	if code := cmdJobs([]string{}); code == 0 {
		t.Errorf("jobs (no args) = 0, want 1")
	}
	if calls.Load() != 0 {
		t.Errorf("server calls = %d, want 0", calls.Load())
	}
}

// --- local validation gates -----------------------------------------------

// TestCmdJobsUpdate_NoFieldsSet_NoServerCall mirrors cmdCronsUpdate:
// the server would no-op + emit a notify — the CLI catches it.
func TestCmdJobsUpdate_NoFieldsSet_NoServerCall(t *testing.T) {
	calls, _ := withJobsFakeServer(t, func(http.ResponseWriter, *http.Request) {})
	if code := cmdJobsUpdate([]string{jobsInfoID}); code == 0 {
		t.Errorf("jobs update (no fields) = 0, want 1")
	}
	if calls.Load() != 0 {
		t.Errorf("server calls = %d, want 0", calls.Load())
	}
}

// TestCmdJobsInfo_BadID_NoServerCall mirrors cmdCronsInfo: local id
// validation pre-checks the regex so a 32-hex-violating string
// returns 1 with zero network round-trips.
func TestCmdJobsInfo_BadID_NoServerCall(t *testing.T) {
	calls, _ := withJobsFakeServer(t, func(http.ResponseWriter, *http.Request) {})
	if code := cmdJobsInfo([]string{"not-hex"}); code == 0 {
		t.Errorf("jobs info bad id = 0, want 1")
	}
	if calls.Load() != 0 {
		t.Errorf("server calls = %d, want 0", calls.Load())
	}
}

// --- JSON output ---------------------------------------------------------

// TestCmdJobsInfo_JSON_Envelope pins the JSON path: --json emits the
// typed DTO on stdout so `jq .name` pipelines stay stable.
func TestCmdJobsInfo_JSON_Envelope(t *testing.T) {
	_, stdout := withJobsFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(makeJobResponse())
	})
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true
	if code := cmdJobsInfo([]string{jobsInfoID}); code != 0 {
		t.Errorf("jobs info json = %d, want 0", code)
	}
	var resp api.JobResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("JSON envelope parse failed: %v\nraw: %s", err, stdout.String())
	}
	if resp.Name != "smoke-job" || resp.Kind != "run_to_completion" || resp.Status != "active" {
		t.Errorf("jobs info JSON drift: %+v", resp)
	}
}

// TestParseEnvOverrides_Pin locks the env-flag semantics so a future
// drift breaks the test (kubectl-style --env k=v).
func TestParseEnvOverrides_Pin(t *testing.T) {
	got := parseEnvOverrides([]string{"FOO=bar", "BAZ=", "qux"})
	want := map[string]string{"FOO": "bar", "BAZ": "", "qux": ""}
	if len(got) != len(want) {
		t.Fatalf("len drift: got %d, want %d (%+v)", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("env key %q drift: got %q, want %q", k, got[k], v)
		}
	}
	// Empty input -> nil (server treats nil vs empty-map distinctly
	// in CreateJobRequest; the SDK encodes non-nil maps with `{}`).
	if got := parseEnvOverrides(nil); got != nil {
		t.Errorf("parseEnvOverrides(nil) = %+v, want nil", got)
	}
}

// Force the context import to be used so gofmt/vet don't complain
// when no other test happens to reference it.
var _ = context.Background
