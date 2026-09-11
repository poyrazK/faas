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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

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
