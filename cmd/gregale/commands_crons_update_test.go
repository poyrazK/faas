// Tests for the `gregale crons update <id>` subcommand. Mirrors the
// `commands_deployments_test.go` shape: httptest.NewServer fake + t.Setenv
// + osStdout swap. The dispatch placement lives in main_test.go.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// cronsUpdateID is the 32-hex id used by every test below. Matches
// the cronIDPattern in commands2.go; the update path runs the same
// regex before the server round-trip.
const cronsUpdateID = "0123456789abcdef0123456789abcdef"

// --- happy paths ------------------------------------------------------------

func TestCmdCronsUpdate_HappyPath_ScheduleOnly(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody api.UpdateCronRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(api.CronResponse{
			ID: cronsUpdateID, AppID: "a1", Schedule: "*/15 * * * *", Path: "/", Enabled: true,
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsUpdate([]string{cronsUpdateID, "--schedule", "*/15 * * * *"}); code != 0 {
		t.Errorf("update schedule-only = %d, want 0", code)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/v1/crons/"+cronsUpdateID {
		t.Errorf("path = %q, want /v1/crons/<id>", gotPath)
	}
	if gotBody.Schedule == nil || *gotBody.Schedule != "*/15 * * * *" {
		t.Errorf("body.schedule = %v, want */15 * * * *", gotBody.Schedule)
	}
	if gotBody.Path != nil {
		t.Errorf("body.path = %v, want nil (unset)", *gotBody.Path)
	}
	if gotBody.Enabled != nil {
		t.Errorf("body.enabled = %v, want nil (unset)", *gotBody.Enabled)
	}
}

func TestCmdCronsUpdate_HappyPath_Enable(t *testing.T) {
	var gotBody api.UpdateCronRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(api.CronResponse{
			ID: cronsUpdateID, AppID: "a1", Schedule: "*/5 * * * *", Path: "/", Enabled: true,
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsUpdate([]string{cronsUpdateID, "--enable"}); code != 0 {
		t.Errorf("update --enable = %d, want 0", code)
	}
	if gotBody.Enabled == nil || *gotBody.Enabled != true {
		t.Errorf("body.enabled = %v, want true", gotBody.Enabled)
	}
	if gotBody.Schedule != nil || gotBody.Path != nil {
		t.Errorf("expected only Enabled to be set; got schedule=%v path=%v", gotBody.Schedule, gotBody.Path)
	}
}

func TestCmdCronsUpdate_HappyPath_Disable(t *testing.T) {
	var gotBody api.UpdateCronRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(api.CronResponse{
			ID: cronsUpdateID, AppID: "a1", Schedule: "*/5 * * * *", Path: "/", Enabled: false,
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsUpdate([]string{cronsUpdateID, "--disable"}); code != 0 {
		t.Errorf("update --disable = %d, want 0", code)
	}
	if gotBody.Enabled == nil || *gotBody.Enabled != false {
		t.Errorf("body.enabled = %v, want false (pointer-to-false path)", gotBody.Enabled)
	}
}

func TestCmdCronsUpdate_HappyPath_RetryPolicy(t *testing.T) {
	var gotBody api.UpdateCronRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %q, want PATCH", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(api.CronResponse{
			ID: cronsUpdateID, AppID: "a1", Kind: "command", Schedule: "*/5 * * * *",
			Command: []string{"bin/maintenance"}, RetryMax: 3, RetryBackoffSeconds: 25, Enabled: true,
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsUpdate([]string{cronsUpdateID, "--retry-max", "3", "--retry-backoff-seconds", "25"}); code != 0 {
		t.Fatalf("update retry policy = %d", code)
	}
	if gotBody.RetryMax == nil || *gotBody.RetryMax != 3 ||
		gotBody.RetryBackoffSeconds == nil || *gotBody.RetryBackoffSeconds != 25 {
		t.Fatalf("retry patch = %+v", gotBody)
	}
}

func TestCmdCronsUpdate_HappyPath_PathEmpty(t *testing.T) {
	var gotBody api.UpdateCronRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(api.CronResponse{
			ID: cronsUpdateID, AppID: "a1", Schedule: "*/5 * * * *", Path: "", Enabled: true,
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	// Pins the explicit-zero path: --path "" must set Path to *string("")
	// in the wire body, not drop the flag (which would silently leave
	// the path at "/"). fs.Visit is what makes this work.
	if code := cmdCronsUpdate([]string{cronsUpdateID, "--path", ""}); code != 0 {
		t.Errorf("update --path '' = %d, want 0", code)
	}
	if gotBody.Path == nil {
		t.Errorf("body.path = nil; fs.Visit should have flagged --path '' as explicit-empty")
	} else if *gotBody.Path != "" {
		t.Errorf("body.path = %q, want \"\"", *gotBody.Path)
	}
}

// --- client-side rejections (no server call) -------------------------------

func TestCmdCronsUpdate_MissingID(t *testing.T) {
	if code := cmdCronsUpdate(nil); code != 1 {
		t.Errorf("update no args = %d, want 1", code)
	}
}

func TestCmdCronsUpdate_ExtraPositional(t *testing.T) {
	if code := cmdCronsUpdate([]string{cronsUpdateID, "extra"}); code != 1 {
		t.Errorf("update extra positional = %d, want 1", code)
	}
}

func TestCmdCronsUpdate_BadID(t *testing.T) {
	if code := cmdCronsUpdate([]string{"not-hex"}); code != 1 {
		t.Errorf("update invalid id = %d, want 1", code)
	}
	if code := cmdCronsUpdate([]string{"0123456789abcdef"}); code != 1 { // 30 chars
		t.Errorf("update short id = %d, want 1", code)
	}
}

func TestCmdCronsUpdate_InvalidSchedule(t *testing.T) {
	// 4-token schedule: rejected locally without a server call.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(api.CronResponse{})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsUpdate([]string{cronsUpdateID, "--schedule", "garbage"}); code != 1 {
		t.Errorf("update bad schedule = %d, want 1", code)
	}
	if calls != 0 {
		t.Errorf("server was hit %d times; client-side rejection should not call API", calls)
	}
}

func TestCmdCronsUpdate_EnableDisableConflict(t *testing.T) {
	if code := cmdCronsUpdate([]string{cronsUpdateID, "--enable", "--disable"}); code != 1 {
		t.Errorf("update --enable --disable = %d, want 1", code)
	}
}

func TestCmdCronsUpdate_NoFields(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(api.CronResponse{})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdCronsUpdate([]string{cronsUpdateID}); code != 1 {
		t.Errorf("update no fields = %d, want 1", code)
	}
	if calls != 0 {
		t.Errorf("server was hit %d times; no-fields should not call API", calls)
	}
}

func TestCmdCronsUpdate_Unauthenticated(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if code := cmdCronsUpdate([]string{cronsUpdateID, "--enable"}); code == 0 {
		t.Error("update without token must fail")
	}
}

// --- server-side error surfacing -------------------------------------------

func TestCmdCronsUpdate_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"type":"...","title":"Not found","code":"not_found","status":404}`, http.StatusNotFound)
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdCronsUpdate([]string{cronsUpdateID, "--enable"}); code == 0 {
		t.Error("update 404 must fail")
	}
}

func TestCmdCronsUpdate_InvalidCronServerSide(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(api.Problem{
			Type:   "about:blank",
			Title:  "Bad Request",
			Code:   api.CodeCronInvalid,
			Status: http.StatusBadRequest,
			Detail: "expected 5-field cron expression",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdCronsUpdate([]string{cronsUpdateID, "--schedule", "*/5 * * * *"}); code == 0 {
		t.Error("update 400 cron_invalid must fail")
	}
}

// --- JSON output ------------------------------------------------------------

func TestCmdCronsUpdate_JSON_SingleRecord(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.CronResponse{
			ID:          cronsUpdateID,
			AppID:       "fedcba9876543210fedcba9876543210",
			Schedule:    "*/15 * * * *",
			Path:        "/webhook",
			Enabled:     true,
			CreatedAt:   "2026-07-25T10:00:00Z",
			LastFiredAt: "2026-07-25T09:00:00Z",
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

	if code := cmdCronsUpdate([]string{cronsUpdateID, "--schedule", "*/15 * * * *"}); code != 0 {
		t.Errorf("update json = %d, want 0", code)
	}
	var c api.CronResponse
	if err := json.Unmarshal(stdout.Bytes(), &c); err != nil {
		t.Fatalf("JSON single-record parse failed: %v\nraw: %s", err, stdout.String())
	}
	// Pin every DTO field so a future rename or JSON-tag drift breaks
	// the test (same shape as TestCmdDeployment_JSON_SingleRecord).
	if c.ID != cronsUpdateID ||
		c.AppID != "fedcba9876543210fedcba9876543210" ||
		c.Schedule != "*/15 * * * *" ||
		c.Path != "/webhook" ||
		!c.Enabled ||
		c.CreatedAt != "2026-07-25T10:00:00Z" ||
		c.LastFiredAt != "2026-07-25T09:00:00Z" {
		t.Errorf("JSON shape drift on CronResponse; got %+v", c)
	}
}

// --- dispatch --------------------------------------------------------------

// TestRun_DispatchCronsUpdate asserts the main run() switch routes
// `crons update <id>` into cmdCronsUpdate rather than falling through
// to the "unknown crons subcommand" branch.
func TestRun_DispatchCronsUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/crons/"+cronsUpdateID {
			http.Error(w, "no", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(api.CronResponse{
			ID: cronsUpdateID, AppID: "a1", Schedule: "*/15 * * * *", Path: "/", Enabled: true,
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := run([]string{"crons", "update", cronsUpdateID, "--schedule", "*/15 * * * *"}); code != 0 {
		t.Errorf("run crons update = %d, want 0", code)
	}
}

// --- human output ----------------------------------------------------------

// TestCmdCronsUpdate_HappyPath_DetailRendered pins the human multi-line
// state block against the osStdout seam so a future refactor doesn't
// silently route the body through fmt.Printf (which bypasses the seam;
// same finding as PR #202 on the deployments list).
func TestCmdCronsUpdate_HappyPath_DetailRendered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.CronResponse{
			ID: cronsUpdateID, AppID: "a1", Schedule: "*/15 * * * *", Path: "/webhook", Enabled: true,
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdCronsUpdate([]string{cronsUpdateID, "--schedule", "*/15 * * * *"}); code != 0 {
		t.Errorf("update = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"Updated cron",
		cronsUpdateID,
		"schedule:",
		"*/15 * * * *",
		"path:",
		"/webhook",
		"enabled:",
		"true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("human state block missing %q\nfull: %s", want, out)
		}
	}
}

// TestCmdCronsUpdate_Disable_PrintsFalse pins the pointer-to-false
// branch in the human output (the schedule/path labels reflect the
// server's response, which carries enabled=false when --disable was
// the only change).
func TestCmdCronsUpdate_Disable_PrintsFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.CronResponse{
			ID: cronsUpdateID, AppID: "a1", Schedule: "*/5 * * * *", Path: "/", Enabled: false,
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdCronsUpdate([]string{cronsUpdateID, "--disable"}); code != 0 {
		t.Errorf("update --disable = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "enabled:") || !strings.Contains(stdout.String(), "false") {
		t.Errorf("expected 'enabled: false' in human output\nfull: %s", stdout.String())
	}
}

// TestRenderCronState_PinsColumnLayout pins the multi-line state
// block against the writer seam (io.Writer). Same shape as
// TestRenderDeploymentRow_PinsColumnLayout — if the field set on
// the CronResponse grows (e.g. last_fired_at gains a long token),
// the column count or widths have to shift and this test surfaces
// that.
func TestRenderCronState_PinsColumnLayout(t *testing.T) {
	var buf bytes.Buffer
	renderCronState(&buf, api.CronResponse{
		ID:       cronsUpdateID,
		AppID:    "a1",
		Schedule: "*/15 * * * *",
		Path:     "/webhook",
		Enabled:  true,
	})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	// 3 labels → schedule / path / enabled.
	if len(lines) != 3 {
		t.Fatalf("line count = %d, want 3\nraw: %s", len(lines), buf.String())
	}
	for _, want := range []string{"schedule:", "*/15 * * * *", "path:", "/webhook", "enabled:", "true"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("rendered output missing %q\nfull: %s", want, buf.String())
		}
	}
}
