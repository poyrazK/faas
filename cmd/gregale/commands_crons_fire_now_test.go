// Tests for `gregale crons run <id>` and `gregale crons fire-now
// <request-id>` (issue #791 / PR-D). Mirrors commands_crons_runs_test.go:
// httptest.NewServer fake + t.Setenv + osStdout swap + jsonOutput
// swap. The dispatch placement lives in main_test.go (see
// TestRun_DispatchCronsRun / TestRun_DispatchCronsFireNow below).
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cronsFireNowRunID is the 32-hex id used by `crons run` tests.
const cronsFireNowRunID = "0123456789abcdef0123456789abcdef"

// cronsFireNowRequestID is the 32-hex id used by `crons fire-now`
// tests. Matches fireNowRequestIDPattern in commands_crons_fire_now.go.
const cronsFireNowRequestID = "fedcba9876543210fedcba9876543210"

// makeFireNowResponse builds the canned payload the SDK would emit
// for `GET /v1/cron-fire-now-requests/<id>`. Tests edit specific
// fields (Status, FinishedAt, InvocationID, Error) as needed.
func makeFireNowResponse() api.FireCronRequestResponse {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	return api.FireCronRequestResponse{
		RequestID:    cronsFireNowRequestID,
		CronID:       cronsFireNowRunID,
		Status:       "succeeded",
		RequestedAt:  now.UTC().Format(time.RFC3339Nano),
		FinishedAt:   stringPtr(now.Add(2 * time.Second).UTC().Format(time.RFC3339Nano)),
		InvocationID: stringPtr("aabbccddeeff00112233445566778899"),
		AccountID:    "11223344556677889900aabbccddeeff",
	}
}

func stringPtr(s string) *string { return &s }

// --- cmdCronsRun ----------------------------------------------------------

func TestCmdCronsRun_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(api.FireCronResponse{
			RequestID: cronsFireNowRequestID,
			CronID:    cronsFireNowRunID,
			Status:    "pending",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdCronsRun([]string{cronsFireNowRunID}); code != 0 {
		t.Errorf("crons run = %d, want 0", code)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/crons/"+cronsFireNowRunID+"/run" {
		t.Errorf("path = %q, want /v1/crons/<id>/run", gotPath)
	}
	body := stdout.String()
	if !strings.Contains(body, "Fire-now request enqueued") {
		t.Errorf("body missing 'Fire-now request enqueued'; got:\n%s", body)
	}
	if !strings.Contains(body, cronsFireNowRequestID) {
		t.Errorf("body missing request_id %q; got:\n%s", cronsFireNowRequestID, body)
	}
}

func TestCmdCronsRun_JSON_Envelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.FireCronResponse{
			RequestID: cronsFireNowRequestID,
			CronID:    cronsFireNowRunID,
			Status:    "pending",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	if code := cmdCronsRun([]string{cronsFireNowRunID}); code != 0 {
		t.Errorf("crons run json = %d, want 0", code)
	}
	var resp api.FireCronResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("JSON envelope parse failed: %v\nraw: %s", err, stdout.String())
	}
	if resp.RequestID != cronsFireNowRequestID {
		t.Errorf("request_id = %q, want %q", resp.RequestID, cronsFireNowRequestID)
	}
}

func TestCmdCronsRun_BadID_NoServerCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsRun([]string{"not-hex"}); code != 1 {
		t.Errorf("crons run bad id = %d, want 1", code)
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0 (local validation only)", calls)
	}
}

func TestCmdCronsRun_MissingID_NoServerCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsRun([]string{}); code != 1 {
		t.Errorf("crons run no args = %d, want 1", code)
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0", calls)
	}
}

// --- cmdCronsFireNowGet ---------------------------------------------------

func TestCmdCronsFireNowGet_HappyPath_Terminal(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(makeFireNowResponse())
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdCronsFireNowGet([]string{cronsFireNowRequestID}); code != 0 {
		t.Errorf("crons fire-now = %d, want 0", code)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/cron-fire-now-requests/"+cronsFireNowRequestID {
		t.Errorf("path = %q, want /v1/cron-fire-now-requests/<id>", gotPath)
	}
	body := stdout.String()
	// Render invariant: succeeded → "succeeded" + invocation tag.
	if !strings.Contains(body, "succeeded") {
		t.Errorf("body missing 'succeeded'; got:\n%s", body)
	}
	if !strings.Contains(body, "invocation: aabbccddeeff00112233445566778899") {
		t.Errorf("body missing invocation tag; got:\n%s", body)
	}
}

func TestCmdCronsFireNowGet_Pending(t *testing.T) {
	resp := makeFireNowResponse()
	resp.Status = "pending"
	resp.FinishedAt = nil
	resp.InvocationID = nil
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdCronsFireNowGet([]string{cronsFireNowRequestID}); code != 0 {
		t.Errorf("crons fire-now pending = %d, want 0", code)
	}
	body := stdout.String()
	if !strings.Contains(body, "pending") {
		t.Errorf("body missing 'pending'; got:\n%s", body)
	}
	// Pending row has no terminal stamp — must NOT render the
	// invocation column.
	if strings.Contains(body, "invocation:") {
		t.Errorf("body leaked invocation column on pending row; got:\n%s", body)
	}
}

func TestCmdCronsFireNowGet_Failed(t *testing.T) {
	resp := makeFireNowResponse()
	resp.Status = "failed"
	resp.InvocationID = nil
	errMsg := "cron disabled"
	resp.Error = &errMsg
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdCronsFireNowGet([]string{cronsFireNowRequestID}); code != 0 {
		t.Errorf("crons fire-now failed = %d, want 0", code)
	}
	body := stdout.String()
	if !strings.Contains(body, "failed") {
		t.Errorf("body missing 'failed'; got:\n%s", body)
	}
	if !strings.Contains(body, "cron disabled") {
		t.Errorf("body missing error text; got:\n%s", body)
	}
}

func TestRenderFireNowStatusShowsCommandTaskReceipt(t *testing.T) {
	response := makeFireNowResponse()
	response.Status = fireNowStatusSucceeded
	taskID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	response.TaskID = &taskID
	var output bytes.Buffer
	renderFireNowStatus(&output, response)
	if !strings.Contains(output.String(), "task: "+taskID) {
		t.Fatalf("command fire-now status = %q; want task receipt", output.String())
	}
}

func TestCmdCronsFireNowGet_JSON_Envelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(makeFireNowResponse())
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	if code := cmdCronsFireNowGet([]string{cronsFireNowRequestID}); code != 0 {
		t.Errorf("crons fire-now json = %d, want 0", code)
	}
	var got api.FireCronRequestResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("JSON envelope parse failed: %v\nraw: %s", err, stdout.String())
	}
	if got.RequestID != cronsFireNowRequestID {
		t.Errorf("request_id = %q, want %q", got.RequestID, cronsFireNowRequestID)
	}
	if got.Status != "succeeded" {
		t.Errorf("status = %q, want succeeded", got.Status)
	}
}

func TestCmdCronsFireNowGet_BadID_NoServerCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsFireNowGet([]string{"not-hex"}); code != 1 {
		t.Errorf("crons fire-now bad id = %d, want 1", code)
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0 (local validation only)", calls)
	}
}

func TestCmdCronsFireNowGet_MissingID_NoServerCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsFireNowGet([]string{}); code != 1 {
		t.Errorf("crons fire-now no args = %d, want 1", code)
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0", calls)
	}
}

func TestCmdCronsFireNowGet_ServerNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(api.Problem{
			Type:   "about:blank",
			Status: 404,
			Title:  "not_found",
			Detail: "no such fire-now request",
			Code:   "not_found",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsFireNowGet([]string{cronsFireNowRequestID}); code == 0 {
		t.Errorf("crons fire-now 404 = 0, want nonzero")
	}
}

// --- dispatch --------------------------------------------------------------

func TestRun_DispatchCronsRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/crons/"+cronsFireNowRunID+"/run" {
			http.Error(w, "no", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(api.FireCronResponse{
			RequestID: cronsFireNowRequestID,
			CronID:    cronsFireNowRunID,
			Status:    "pending",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := run([]string{"crons", "run", cronsFireNowRunID}); code != 0 {
		t.Errorf("run crons run = %d, want 0", code)
	}
}

func TestRun_DispatchCronsFireNow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/cron-fire-now-requests/"+cronsFireNowRequestID {
			http.Error(w, "no", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(makeFireNowResponse())
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := run([]string{"crons", "fire-now", cronsFireNowRequestID}); code != 0 {
		t.Errorf("run crons fire-now = %d, want 0", code)
	}
}

// --- glyph stripping -------------------------------------------------------

// TestCmdCronsFireNowGet_NoGlyphsInCapturedBuffer is the regression
// guard for the output.go:55-63 gate. Pending rows render →; a
// captured buffer under the non-TTY gate must not contain ✓/✗/→ —
// otherwise a pipe would leak them and break grep.
func TestCmdCronsFireNowGet_NoGlyphsInCapturedBuffer(t *testing.T) {
	resp := makeFireNowResponse()
	resp.Status = "pending"
	resp.FinishedAt = nil
	resp.InvocationID = nil
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()
	defer withTTYForTest(false)()

	if code := cmdCronsFireNowGet([]string{cronsFireNowRequestID}); code != 0 {
		t.Errorf("crons fire-now = %d, want 0", code)
	}
	body := stdout.String()
	for _, g := range []string{"✓", "✗", "→"} {
		if strings.Contains(body, g) {
			t.Errorf("body leaked glyph %q under non-TTY stdout; output.go gate failed\nbody:\n%s", g, body)
		}
	}
}
