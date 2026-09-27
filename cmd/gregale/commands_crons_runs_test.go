// Tests for `gregale crons runs <id>` (issue #791 / PR B). Mirrors
// commands_crons_update_test.go: httptest.NewServer fake + t.Setenv
// + osStdout swap + jsonOutput swap. The dispatch placement lives in
// main_test.go (see TestRun_DispatchCronsRuns below).
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

// cronsRunsID is the 32-hex id used by every test below. Matches
// cronIDPattern in commands2.go.
const cronsRunsID = "0123456789abcdef0123456789abcdef"

// ptr helpers — DurationMs / CompletedAt are *int64 / *time.Time.
func int64Ptr(v int64) *int64        { return &v }
func timePtr(t time.Time) *time.Time { return &t }

// makeRunsResponse builds the canned payload the SDK would emit for
// `GET /v1/crons/<id>/runs`. Tests edit specific fields as needed.
func makeRunsResponse() api.ListCronRunsResponse {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	return api.ListCronRunsResponse{
		Runs: []api.CronRun{
			{
				ID:          "0123456789abcdef0123456789abcdef",
				StartedAt:   now,
				CompletedAt: timePtr(now.Add(1200 * time.Millisecond)),
				DurationMs:  int64Ptr(1200),
				Outcome:     api.CronRunSuccess,
				Attempts:    1,
			},
			{
				ID:          "fedcba9876543210fedcba9876543210",
				StartedAt:   now.Add(-12 * time.Hour),
				CompletedAt: timePtr(now.Add(-12*time.Hour + 30*time.Second)),
				DurationMs:  int64Ptr(30000),
				Outcome:     api.CronRunTimeout,
				Attempts:    1,
				Error:       "invoke: gateway timeout",
			},
		},
	}
}

// --- formatCronDuration direct ---------------------------------------------

func TestFormatCronDuration_Bands(t *testing.T) {
	cases := []struct {
		in   *int64
		want string
	}{
		{nil, "—"},
		{int64Ptr(0), "0ms"},
		{int64Ptr(500), "500ms"},
		{int64Ptr(999), "999ms"},
		{int64Ptr(1000), "1.0s"},
		{int64Ptr(1200), "1.2s"},
		{int64Ptr(30000), "30.0s"},
		{int64Ptr(59999), "60.0s"},
		{int64Ptr(60000), "60s"},
		{int64Ptr(90000), "90s"},
		{int64Ptr(3600000), "3600s"},
	}
	for _, c := range cases {
		got := formatCronDuration(c.in)
		if got != c.want {
			t.Errorf("formatCronDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- happy paths -----------------------------------------------------------

func TestCmdCronsRuns_HappyPath_MultiRow(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(makeRunsResponse())
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdCronsRuns([]string{cronsRunsID}); code != 0 {
		t.Errorf("crons runs = %d, want 0", code)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/crons/"+cronsRunsID+"/runs" {
		t.Errorf("path = %q, want /v1/crons/<id>/runs", gotPath)
	}
	// Default --limit is 10, no --before, so query should be exactly "limit=10".
	if gotQuery != "limit=10" {
		t.Errorf("query = %q, want limit=10", gotQuery)
	}
	body := stdout.String()
	// Render invariant: each row has the 5 columns (run_id, started_at,
	// outcome, duration, error) joined by tabs. With the TTY gate
	// off (default in tests), glyphs are stripped so the line
	// starts at the run id.
	if !strings.Contains(body, "0123456789abcdef0123456789abcdef\t2026-08-10T09:00:00Z\tsuccess\t1.2s") {
		t.Errorf("body missing success row; got:\n%s", body)
	}
	if !strings.Contains(body, "fedcba9876543210fedcba9876543210\t2026-08-09T21:00:00Z\ttimeout\t30.0s\tinvoke: gateway timeout") {
		t.Errorf("body missing timeout row; got:\n%s", body)
	}
}

func TestCmdCronsRuns_DetailShowsTaskOutput(t *testing.T) {
	const runID = "fedcba98-7654-3210-fedc-ba9876543210"
	zero := 0
	task := api.AppTaskResponse{
		ID: runID, DeploymentID: "deployment-1", Kind: api.AppTaskKindCron,
		Status: api.AppTaskStatusSucceeded, AttemptCount: 2, RetryMax: 2,
		ExitCode: &zero, StdoutTail: "daily job complete\n", StderrTail: "notice: cache cold\n",
		OutputTruncated: true, MaxOutputBytes: 4096,
	}
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewEncoder(w).Encode(task)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout, stderr bytes.Buffer
	oldOut, oldErr := osStdout, osStderr
	osStdout, osStderr = &stdout, &stderr
	defer func() { osStdout, osStderr = oldOut, oldErr }()
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)

	if code := cmdCronsRuns([]string{cronsRunsID, "--run", runID}); code != 0 {
		t.Fatalf("crons runs detail = %d, want 0; stderr=%s", code, stderr.String())
	}
	if gotMethod != http.MethodGet || gotPath != "/v1/crons/"+cronsRunsID+"/runs/"+runID {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	for _, want := range []string{"status: succeeded", "attempts: 2/3", "exit_code: 0", "stdout:\ndaily job complete", "stderr:\nnotice: cache cold"} {
		if !strings.Contains(stdout.String(), want) && !strings.Contains(stderr.String(), want) {
			t.Errorf("detail output missing %q; stdout=%q stderr=%q", want, stdout.String(), stderr.String())
		}
	}
	if !strings.Contains(stderr.String(), "captured output was truncated at 4096 bytes") {
		t.Errorf("truncation warning missing: %q", stderr.String())
	}
}

func TestCmdCronsRuns_DetailRejectsBadOrMixedFlagsBeforeRequest(t *testing.T) {
	for _, args := range [][]string{
		{cronsRunsID, "--run", "not-a-task-id"},
		{cronsRunsID, "--run", "fedcba98-7654-3210-fedc-ba9876543210", "--before", "cursor"},
	} {
		var stdout, stderr bytes.Buffer
		oldOut, oldErr := osStdout, osStderr
		osStdout, osStderr = &stdout, &stderr
		if code := cmdCronsRuns(args); code != 1 {
			t.Errorf("cmdCronsRuns(%v) = %d, want 1", args, code)
		}
		osStdout, osStderr = oldOut, oldErr
	}
}

func TestCmdCronsRuns_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.ListCronRunsResponse{Runs: []api.CronRun{}})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdCronsRuns([]string{cronsRunsID}); code != 0 {
		t.Errorf("crons runs (empty) = %d, want 0", code)
	}
	if got := strings.TrimSpace(stdout.String()); got != "(no runs)" {
		t.Errorf("empty sentinel = %q, want \"(no runs)\"", got)
	}
}

// --- JSON output -----------------------------------------------------------

func TestCmdCronsRuns_JSON_Envelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(makeRunsResponse())
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

	if code := cmdCronsRuns([]string{cronsRunsID}); code != 0 {
		t.Errorf("crons runs json = %d, want 0", code)
	}
	var resp api.ListCronRunsResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("JSON envelope parse failed: %v\nraw: %s", err, stdout.String())
	}
	if len(resp.Runs) != 2 {
		t.Fatalf("len(runs) = %d, want 2", len(resp.Runs))
	}
	// Pin every field on the first row so a future tag drift breaks
	// the test (same posture as crons_update JSON test).
	r0 := resp.Runs[0]
	if r0.ID != "0123456789abcdef0123456789abcdef" ||
		r0.Outcome != api.CronRunSuccess ||
		r0.Attempts != 1 ||
		r0.DurationMs == nil || *r0.DurationMs != 1200 ||
		r0.CompletedAt == nil ||
		r0.Error != "" {
		t.Errorf("runs[0] drift: %+v", r0)
	}
	// In-flight assertion: a *int64 duration that round-trips
	// through JSON and back as *int64 (not 0). The peer Explore
	// agent flagged that the duration column is the disambiguator
	// vs. invocations list; pin both branches here.
	if resp.Runs[1].DurationMs == nil || *resp.Runs[1].DurationMs != 30000 {
		t.Errorf("runs[1].duration_ms drift: %+v", resp.Runs[1])
	}
}

// --- query pass-through ----------------------------------------------------

func TestCmdCronsRuns_LimitAndBeforePassThrough(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(api.ListCronRunsResponse{})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdCronsRuns([]string{"--limit", "5", "--before", "fedcba9876543210fedcba9876543210", cronsRunsID}); code != 0 {
		t.Errorf("crons runs with flags = %d, want 0", code)
	}
	// url.Values.Encode() orders keys alphabetically: before, limit.
	want := "before=fedcba9876543210fedcba9876543210&limit=5"
	if gotQuery != want {
		t.Errorf("query = %q, want %q", gotQuery, want)
	}
}

// --- client-side rejections (no server call) -------------------------------

func TestCmdCronsRuns_BadID_NoServerCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsRuns([]string{"not-hex"}); code != 1 {
		t.Errorf("crons runs bad id = %d, want 1", code)
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0 (local validation only)", calls)
	}
}

func TestCmdCronsRuns_MissingID_NoServerCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsRuns([]string{}); code != 1 {
		t.Errorf("crons runs no args = %d, want 1", code)
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0", calls)
	}
}

func TestCmdCronsRuns_ExtraPositional(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsRuns([]string{cronsRunsID, "extra"}); code != 1 {
		t.Errorf("crons runs extra positional = %d, want 1", code)
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0", calls)
	}
}

func TestCmdCronsRuns_Unauthenticated(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_TOKEN", "")

	if code := cmdCronsRuns([]string{cronsRunsID}); code == 0 {
		t.Errorf("crons runs unauth = 0, want nonzero")
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0", calls)
	}
}

// --- server error surfacing ------------------------------------------------

// TestCmdCronsRuns_ServerNotFound asserts the SDK's RFC 7807 problem
// reaches the operator verbatim — we never invent a local "cron not
// found" branch (peer Explore agent's plan-review note). This is a
// regression guard against the IDOR-safe two-step handler's 404
// message somehow leaking differently through the CLI.
func TestCmdCronsRuns_ServerNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/crons/"+cronsRunsID+"/runs" {
			http.Error(w, "wrong path", 500)
			return
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(api.Problem{
			Type:   "about:blank",
			Status: 404,
			Title:  "not_found",
			Detail: "no such cron",
			Code:   "not_found",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsRuns([]string{cronsRunsID}); code == 0 {
		t.Errorf("crons runs 404 = 0, want nonzero")
	}
}

// --- dispatch --------------------------------------------------------------

// TestRun_DispatchCronsRuns asserts the main run() switch routes
// `crons runs <id>` into cmdCronsRuns rather than falling through to
// the "unknown crons subcommand" branch.
func TestRun_DispatchCronsRuns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/crons/"+cronsRunsID+"/runs" {
			http.Error(w, "no", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(makeRunsResponse())
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := run([]string{"crons", "runs", cronsRunsID}); code != 0 {
		t.Errorf("run crons runs = %d, want 0", code)
	}
}

// --- glyph stripping in piped output ---------------------------------------

// TestCmdCronsRuns_NoGlyphsInCapturedBuffer is the regression guard for
// the output.go:55-63 gate. With the TTY seam forced off (the
// non-interactive case the package default would not catch), the
// captured buffer must not contain ✓/✗/→ literals — otherwise a pipe
// would leak them and break grep. The plan-review agent flagged this
// as the most common landmine in this codebase.
//
// TestMain (output_test.go:35) arms testOnlyTTY = &true so existing
// tests with `✓ `-prefixed assertions don't go red under the §3.2 gate.
// We override that with withTTYForTest(false) so this test exercises
// the strip path specifically.
func TestCmdCronsRuns_NoGlyphsInCapturedBuffer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(makeRunsResponse())
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()
	defer withTTYForTest(false)()

	if code := cmdCronsRuns([]string{cronsRunsID}); code != 0 {
		t.Errorf("crons runs = %d, want 0", code)
	}
	body := stdout.String()
	for _, g := range []string{"✓", "✗", "→"} {
		if strings.Contains(body, g) {
			t.Errorf("body leaked glyph %q under non-TTY stdout; output.go gate failed\nbody:\n%s", g, body)
		}
	}
}
