// Tests for `gregale jobs <list|add|info|update|rm|run|runs|cancel|
// tasks|logs>` (Mega-1 M12 CLI surface). Mirrors the
// commands_crons_*_test.go shape: each leaf verifies its
// positional-arg + flag-validation path WITHOUT a live server
// (authedClient returns an error so the leaves that hit the
// network abort early). The flag-validation surface is the
// load-bearing assertion — a customer typo must surface
// locally (exit 1 + usage) instead of round-tripping to apid
// for a 400.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestRenderJobStateShowsImageFailure(t *testing.T) {
	var out bytes.Buffer
	renderJobState(&out, api.JobResponse{
		Name: "nightly", ImageRef: "registry.example/nightly:v1",
		ImageMaterializationStatus: "failed", ImageMaterializationError: "registry image not found",
	})
	if !strings.Contains(out.String(), "image status: failed") || !strings.Contains(out.String(), "image error: registry image not found") {
		t.Fatalf("image failure missing from human output: %q", out.String())
	}
}

func TestRenderJobTasksTableShowsOutcomeAndDecision(t *testing.T) {
	var out bytes.Buffer
	renderJobTasksTable(&out, []api.JobTaskResponse{{
		TaskIndex: 2, Status: "failed", Attempt: 1, ErrorClass: "user_error",
		OutcomeCode: "invalid_record",
		WorkDecision: &workpolicy.Decision{
			Classification: "permanent", Action: "fail_partition",
		},
	}})
	for _, want := range []string{"outcome_code", "decision", "invalid_record", "permanent/fail_partition"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("job task output missing %q: %q", want, out.String())
		}
	}
}

// runWithStderr swaps both os.Stderr and the gregale package's
// osStderr writer for a pipe, runs fn, drains the pipe, and
// returns (exit code, captured stderr). PrintUsage writes to
// os.Stderr; printErr writes to osStderr — both must be
// captured for a complete flag-validation assertion.
func runWithStderr(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	oldStderr := os.Stderr
	oldPkgStderr := osStderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	osStderr = w
	code := fn()
	w.Close()
	os.Stderr = oldStderr
	osStderr = oldPkgStderr
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	r.Close()
	return code, buf.String()
}

// TestCmdJobs_NoArgs verifies the dispatcher usage on bare
// `gregale jobs`. Mirrors the crons dispatcher pattern at
// commands2.go:1743 — `PrintUsage(os.Stderr, ...)` + exit 1
// when args[0] is empty.
func TestCmdJobs_NoArgs(t *testing.T) {
	code, captured := runWithStderr(t, func() int { return cmdJobs([]string{}) })
	if code != 1 {
		t.Errorf("cmdJobs(no args) = %d, want 1", code)
	}
	if !strings.Contains(captured, "gregale jobs") {
		t.Errorf("usage must mention 'gregale jobs'; got: %s", captured)
	}
}

// TestCmdJobs_UnknownSubcommand pins the dispatcher reject
// path. Unknown leaf → "unknown jobs subcommand %q" + exit 1.
func TestCmdJobs_UnknownSubcommand(t *testing.T) {
	code, captured := runWithStderr(t, func() int { return cmdJobs([]string{"bogus-leaf"}) })
	if code != 1 {
		t.Errorf("cmdJobs(bogus) = %d, want 1", code)
	}
	if !strings.Contains(captured, "unknown jobs subcommand") {
		t.Errorf("usage must say 'unknown jobs subcommand'; got: %s", captured)
	}
}

func TestCmdJobsList_RejectsNegativePaginationLocally(t *testing.T) {
	cases := [][]string{
		{"list", "--limit", "-1"},
		{"list", "--offset", "-1"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			code, captured := runWithStderr(t, func() int { return cmdJobsList(args[1:]) })
			if code != 1 {
				t.Errorf("cmdJobsList(%v) = %d, want 1", args, code)
			}
			if !strings.Contains(captured, "gregale jobs list") {
				t.Errorf("usage must mention 'gregale jobs list'; got: %s", captured)
			}
		})
	}
}

func TestCmdJobsList_ForwardsPaginationAndKeepsEnvelope(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jobs":[],"limit":1,"offset":20,"next_offset":-1,"total":20}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	stdout, restore := captureStdout(t)
	code := cmdJobsList([]string{"--limit", "1", "--offset", "20"})
	restore()
	if code != 0 {
		t.Fatalf("cmdJobsList = %d, want 0", code)
	}
	if gotQuery != "limit=1&offset=20" {
		t.Fatalf("query = %q, want limit=1&offset=20", gotQuery)
	}
	out := stdout.String()
	if !strings.Contains(out, `"next_offset": -1`) || !strings.Contains(out, `"total": 20`) {
		t.Fatalf("JSON output lost pagination envelope: %s", out)
	}
}

func TestCmdJobsRm_JSONHandlesNoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/jobs/test-job" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	stdout, restore := captureStdout(t)
	code := cmdJobsRm([]string{"test-job"})
	restore()
	if code != 0 {
		t.Fatalf("cmdJobsRm = %d, want 0", code)
	}
	if got := stdout.String(); !strings.Contains(got, `"name":"test-job"`) || !strings.Contains(got, `"deleted":true`) {
		t.Fatalf("JSON output = %q", got)
	}
}

// TestCmdJobsAdd_NoImage verifies that omitting --image fails
// locally with the per-leaf usage line. The handler-side
// validSlug + buildJob pipeline is exercised in
// handlers_jobs_test.go; this CLI test only pins that the
// flag-validation path catches a missing --image before the
// network round-trip.
func TestCmdJobsAdd_NoImage(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdJobsAdd([]string{"valid-slug-name"})
	})
	if code != 1 {
		t.Errorf("cmdJobsAdd(valid-slug, no --image) = %d, want 1", code)
	}
	if !strings.Contains(captured, "--image") {
		t.Errorf("usage must mention --image; got: %s", captured)
	}
}

// TestCmdJobsAdd_InvalidSlug verifies the local pattern check
// runs BEFORE the --image check. A customer typo on the slug
// must surface locally as "name is 3..40 lowercase / digits /
// hyphens" rather than hitting apid and getting a 400.
func TestCmdJobsAdd_InvalidSlug(t *testing.T) {
	cases := []string{
		"Bad Slug", // uppercase + space
		"bad_slug", // underscore
		"x",        // too short
		"a-very-long-slug-that-exceeds-the-forty-char-cap-and-some", // too long
	}
	for _, slug := range cases {
		code, captured := runWithStderr(t, func() int {
			return cmdJobsAdd([]string{slug, "--image", "x"})
		})
		if code != 1 {
			t.Errorf("cmdJobsAdd(%q) = %d, want 1 (slug must be rejected locally)", slug, code)
		}
		if !strings.Contains(captured, "lowercase") {
			t.Errorf("usage must mention 'lowercase'; got: %s", captured)
		}
	}
}

// TestCmdJobsUpdate_NoPatchFields verifies the "at least one
// patch field is required" guard at the CLI layer. Mirrors
// the cmdCronsUpdate pattern at commands2.go:1868 — passing no
// patch flags must fail locally rather than silently
// no-op-ing server-side.
func TestCmdJobsUpdate_NoPatchFields(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdJobsUpdate([]string{"valid-slug"})
	})
	if code != 1 {
		t.Errorf("cmdJobsUpdate(slug, no flags) = %d, want 1", code)
	}
	if !strings.Contains(captured, "patch field") {
		t.Errorf("usage must say 'patch field'; got: %s", captured)
	}
}

// TestCmdJobsUpdate_PauseResumeConflict verifies the
// mutual-exclusion check on --pause / --resume. The handler
// enforces the same mutex in pkg/api/limits.go; this CLI test
// pins that the local surface catches the conflict BEFORE
// hitting the network.
func TestCmdJobsUpdate_PauseResumeConflict(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdJobsUpdate([]string{"valid-slug", "--pause", "--resume"})
	})
	if code != 1 {
		t.Errorf("cmdJobsUpdate(slug, --pause, --resume) = %d, want 1", code)
	}
	if !strings.Contains(captured, "mutually exclusive") {
		t.Errorf("usage must say 'mutually exclusive'; got: %s", captured)
	}
}

// TestCmdJobsRun_NoTasks verifies the --tasks > 0 check.
func TestCmdJobsRun_NoTasks(t *testing.T) {
	code, _ := runWithStderr(t, func() int {
		return cmdJobsRun([]string{"valid-slug", "--tasks", "0"})
	})
	if code != 1 {
		t.Errorf("cmdJobsRun(--tasks 0) = %d, want 1", code)
	}
}

func TestCmdJobsRun_ForwardsExplicitZeroRetries(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		body = string(payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"00000000-0000-4000-8000-000000000000","job_id":"00000000-0000-4000-8000-000000000001","account_id":"00000000-0000-4000-8000-000000000002","trigger_kind":"manual","tasks":1,"parallelism":1,"retry_max":0,"task_timeout_sec":60,"aggregate_status":"pending","created_at":"2026-09-19T00:00:00Z"}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	if code := cmdJobsRun([]string{"valid-slug", "--tasks", "1", "--retries", "0"}); code != 0 {
		t.Fatalf("cmdJobsRun = %d, want 0", code)
	}
	if !strings.Contains(body, `"retry_max":0`) {
		t.Fatalf("request body = %s, want explicit retry_max zero", body)
	}
}

func TestCmdJobsRun_ExternalManifest(t *testing.T) {
	var got api.CreateJobRunRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/jobs/valid-slug/runs" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"00000000-0000-4000-8000-000000000000","tasks":2,"parallelism":1}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	if code := cmdJobsRun([]string{"valid-slug", "--input-manifest-uri", "obj://app/bucket/inputs.json", "--input-manifest-sha256", "sha256:" + strings.Repeat("a", 64)}); code != 0 {
		t.Fatalf("manifest run code = %d", code)
	}
	if got.Tasks != 0 || len(got.Inputs) != 0 || got.InputManifestURI != "obj://app/bucket/inputs.json" || got.InputManifestSHA256 != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("manifest request = %+v", got)
	}
	code, _ := runWithStderr(t, func() int {
		return cmdJobsRun([]string{"valid-slug", "--input-manifest-uri", "obj://app/bucket/inputs.json"})
	})
	if code != 1 {
		t.Fatal("unpaired manifest flags must fail locally")
	}
}

func TestCmdJobsOccurrencesUsesCursorPage(t *testing.T) {
	const cursor = "01234567-89ab-cdef-0123-456789abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/jobs/customer-sync/occurrences" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("before") != cursor {
			t.Errorf("query = %s, want limit=2 and before cursor", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"occurrences":[],"limit":2,"before":"`+cursor+`"}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)

	stdout, restore := captureStdout(t)
	code := cmdJobsOccurrences([]string{"customer-sync", "--limit", "2", "--before", cursor})
	restore()
	if code != 0 {
		t.Fatalf("cmdJobsOccurrences = %d", code)
	}
	if !strings.Contains(stdout.String(), "(no scheduled occurrences)") {
		t.Fatalf("stdout = %q, want empty-page output", stdout.String())
	}
}

func TestPolicyJSONCLIParsingValidatesVersions(t *testing.T) {
	if _, err := parseSchedulePolicyJSON(`{"version":1,"overlap":"skip","missed_runs":"skip"}`); err != nil {
		t.Fatalf("valid schedule policy: %v", err)
	}
	if _, err := parseSchedulePolicyJSON(`{"version":2,"overlap":"skip","missed_runs":"skip"}`); err == nil {
		t.Fatal("unsupported schedule policy version was accepted")
	}
	if _, err := parseFailureRulesJSON(`{"version":1,"rules":[{"exit_codes":[2],"action":"fail_partition"}],"unmatched_failure":"retry","uncertain_outcome":"hold"}`); err != nil {
		t.Fatalf("valid failure rules: %v", err)
	}
}

func TestCmdJobs_ResultOperations(t *testing.T) {
	const runID = "00000000-0000-4000-8000-000000000000"
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/jobs/valid-slug/runs/" + runID + "/replay-failed":
			_, _ = io.WriteString(w, `{"id":"00000000-0000-4000-8000-000000000001","tasks":1,"source_run_id":"`+runID+`"}`)
		case "/v1/jobs/valid-slug/runs/" + runID + "/tasks/0/attempts":
			_, _ = io.WriteString(w, `{"attempts":[{"task_index":0,"attempt":1,"status":"failed","input_id":"shard-a","finished_at":"2026-09-29T00:00:00Z"}],"limit":50,"offset":0,"next_offset":-1}`)
		case "/v1/jobs/valid-slug/runs/" + runID + "/tasks/0/artifacts/result/download":
			_, _ = io.WriteString(w, `{"name":"result","size_bytes":3,"sha256":"sha256:abc","download":{"url":"https://example.test/file","method":"GET","headers":{},"expires_at":"2026-09-29T00:05:00Z"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true
	for _, args := range [][]string{
		{"replay-failed", "valid-slug", runID},
		{"attempts", "valid-slug", runID, "0"},
		{"artifact-url", "valid-slug", runID, "0", "result"},
	} {
		stdout, restore := captureStdout(t)
		code := cmdJobs(args)
		restore()
		if code != 0 || !json.Valid(stdout.Bytes()) {
			t.Fatalf("%v: code=%d output=%s", args, code, stdout.String())
		}
	}
	if len(requests) != 3 || requests[0] != "POST /v1/jobs/valid-slug/runs/"+runID+"/replay-failed" ||
		requests[1] != "GET /v1/jobs/valid-slug/runs/"+runID+"/tasks/0/attempts" ||
		requests[2] != "GET /v1/jobs/valid-slug/runs/"+runID+"/tasks/0/artifacts/result/download" {
		t.Fatalf("requests = %v", requests)
	}
}

// TestCmdJobsCancel_BadUUID verifies the run-id pattern check.
func TestCmdJobsCancel_BadUUID(t *testing.T) {
	code, captured := runWithStderr(t, func() int {
		return cmdJobsCancel([]string{"valid-slug", "not-a-uuid"})
	})
	if code != 1 {
		t.Errorf("cmdJobsCancel(bad uuid) = %d, want 1", code)
	}
	if !strings.Contains(captured, "uuid") {
		t.Errorf("usage must mention 'uuid'; got: %s", captured)
	}
}

// TestCmdJobsLogs_BadTaskIndex verifies negative and malformed indexes are
// rejected while zero remains a valid persisted task index.
func TestCmdJobsLogs_BadTaskIndex(t *testing.T) {
	cases := []string{"-1", "alpha"}
	for _, idx := range cases {
		code, _ := runWithStderr(t, func() int {
			return cmdJobsLogs([]string{"valid-slug", "00000000-0000-0000-0000-000000000000", idx})
		})
		if code != 1 {
			t.Errorf("cmdJobsLogs(idx=%q) = %d, want 1", idx, code)
		}
	}
}

func TestCmdJobsLogs_AllowsZeroBasedIndex(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"task_status":"queued","log_content":"","truncated":false,"max_bytes":65536}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	if code := cmdJobsLogs([]string{"valid-slug", "00000000-0000-0000-0000-000000000000", "0"}); code != 0 {
		t.Fatalf("cmdJobsLogs(task-index 0) = %d, want 0", code)
	}
	if gotPath != "/v1/jobs/valid-slug/runs/00000000-0000-0000-0000-000000000000/tasks/0/logs" {
		t.Fatalf("request path = %q, want zero-based task path", gotPath)
	}
}

func TestCmdJobsLogs_ForwardsMaxBytesAfterPositionals(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"task_status":"succeeded","log_content":"tail","truncated":true,"max_bytes":9}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	if code := cmdJobsLogs([]string{"valid-slug", "00000000-0000-0000-0000-000000000000", "0", "--max-bytes", "9"}); code != 0 {
		t.Fatalf("cmdJobsLogs = %d, want 0", code)
	}
	if gotQuery != "max_bytes=9" {
		t.Fatalf("query = %q, want max_bytes=9", gotQuery)
	}
}

func TestCmdJobsLogs_ForwardsMaxBytesBeforePositionals(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"task_status":"succeeded","log_content":"tail","truncated":true,"max_bytes":10}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	if code := cmdJobsLogs([]string{"--max-bytes=10", "valid-slug", "00000000-0000-0000-0000-000000000000", "0"}); code != 0 {
		t.Fatalf("cmdJobsLogs = %d, want 0", code)
	}
	if gotQuery != "max_bytes=10" {
		t.Fatalf("query = %q, want max_bytes=10", gotQuery)
	}
}

func TestCmdJobsLogs_RejectsMaxBytesOutOfRange(t *testing.T) {
	for _, value := range []string{"0", "1048577", "not-a-number"} {
		code, captured := runWithStderr(t, func() int {
			return cmdJobsLogs([]string{"valid-slug", "00000000-0000-0000-0000-000000000000", "0", "--max-bytes", value})
		})
		if code != 1 || !strings.Contains(captured, "max-bytes") {
			t.Errorf("max-bytes=%q: code=%d stderr=%q", value, code, captured)
		}
	}
}

// TestJobSlugPattern_Exhaustive verifies the regex accepts the
// canonical valid slugs and rejects the canonical invalid
// ones. Pinning this table-driven sweep means a future
// regex tweak surfaces here, not in production.
func TestJobSlugPattern_Exhaustive(t *testing.T) {
	valid := []string{
		"abc", // minimum length (3)
		"a-valid-slug",
		"job123",
		"a-thirty-nine-char-slug-with-padding-yyy", // 40 chars (max)
		"0-9-mixed-digits-and-letters",
	}
	for _, s := range valid {
		if !jobSlugPattern.MatchString(s) {
			t.Errorf("jobSlugPattern rejected valid slug %q", s)
		}
	}
	invalid := []string{
		"a",       // too short (2)
		"ab",      // too short
		"abc-",    // trailing hyphen
		"-abc",    // leading hyphen
		"ABC",     // uppercase
		"abc_def", // underscore
		"abc def", // space
		"abc.def", // dot
	}
	for _, s := range invalid {
		if jobSlugPattern.MatchString(s) {
			t.Errorf("jobSlugPattern accepted invalid slug %q", s)
		}
	}
}

// TestJobRunIDPattern_Exhaustive mirrors the slug table for
// the uuid v4 shape.
func TestJobRunIDPattern_Exhaustive(t *testing.T) {
	valid := []string{
		"00000000-0000-0000-0000-000000000000",
		"12345678-1234-1234-1234-123456789abc",
		"abcdef01-2345-6789-abcd-ef0123456789",
	}
	for _, s := range valid {
		if !jobRunIDPattern.MatchString(s) {
			t.Errorf("jobRunIDPattern rejected valid uuid %q", s)
		}
	}
	invalid := []string{
		"not-a-uuid",
		"00000000-0000-0000-0000-00000000000",   // 11 in last group
		"00000000-0000-0000-0000-0000000000000", // 13 in last group
		"00000000.0000.0000.0000.000000000000",  // dot separators
		"00000000000000000000000000000000",      // no hyphens (32-hex variant is the cron id, NOT run id)
	}
	for _, s := range invalid {
		if jobRunIDPattern.MatchString(s) {
			t.Errorf("jobRunIDPattern accepted invalid uuid %q", s)
		}
	}
}
