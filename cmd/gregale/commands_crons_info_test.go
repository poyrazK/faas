// Tests for `gregale crons info <id>` (issue #791 PR-E / ADR-090
// closure). Mirrors commands_crons_runs_test.go: httptest.NewServer
// fake + t.Setenv + osStdout swap + jsonOutput swap. The dispatch
// placement lives in this file (TestRun_DispatchCronsInfo).
package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// cronsInfoID is the 32-hex id used by every test below. Matches
// cronIDPattern in commands2.go.
const cronsInfoID = "0123456789abcdef0123456789abcdef"

// makeCronResponse builds the canned payload the SDK would emit for
// `GET /v1/crons/<id>`. The LastFiredAt zero-vs-set branch is the
// primary UX test: zero → "—", set → RFC3339 stamp.
func makeCronResponse() api.CronResponse {
	return api.CronResponse{
		ID:          cronsInfoID,
		AppID:       "0123456789abcdef0123456789abcde0",
		Schedule:    "*/5 * * * *",
		Path:        "/cleanup",
		Enabled:     true,
		LastFiredAt: "2026-08-10T09:00:00Z",
		CreatedAt:   "2026-08-01T12:00:00Z",
	}
}

// --- happy path -----------------------------------------------------------

func TestCmdCronsInfo_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	var runsRead bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// crons info also reads the recent runs (H10-70); pin the cron read.
		if r.URL.Path == "/v1/crons/"+cronsInfoID+"/runs" {
			runsRead = true
			_ = json.NewEncoder(w).Encode(api.ListCronRunsResponse{})
			return
		}
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(makeCronResponse())
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &stdout
	defer func() { (*main.OsStdout) = oldOut }()

	if code := main.CmdCronsInfo([]string{cronsInfoID}); code != 0 {
		t.Errorf("crons info = %d, want 0", code)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/crons/"+cronsInfoID {
		t.Errorf("path = %q, want /v1/crons/<id>", gotPath)
	}
	if !runsRead {
		t.Error("crons info did not read the recent runs")
	}
	body := stdout.String()
	// Each identity + field column must surface in the rendered block.
	// Render invariant: a label is left-padded to 9 chars ("schedule:",
	// "path:", "enabled:", "app:", "last:") — pin a couple of the
	// lines verbatim so a future tab/space drift breaks the test.
	for _, want := range []string{
		"cron " + cronsInfoID,
		"  schedule: */5 * * * *",
		"  path:     /cleanup",
		"  enabled:  true",
		"  app:      0123456789abcdef0123456789abcde0",
		"  last scheduled fire: 2026-08-10T09:00:00Z",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; got:\n%s", want, body)
		}
	}
}

// TestCmdCronsInfo_NeverFired pins the "—" sentinel for crons that
// have never fired. The customer's mental model is "Last: never" —
// we render an em-dash so the column is non-empty even when the
// server returns an empty string.
func TestCmdCronsInfo_NeverFired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := makeCronResponse()
		resp.LastFiredAt = ""
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &stdout
	defer func() { (*main.OsStdout) = oldOut }()

	if code := main.CmdCronsInfo([]string{cronsInfoID}); code != 0 {
		t.Errorf("crons info (never fired) = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "  last scheduled fire: —") {
		t.Errorf("never-fired sentinel missing; got:\n%s", stdout.String())
	}
}

// --- JSON output -----------------------------------------------------------

func TestCmdCronsInfo_JSON_Envelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(makeCronResponse())
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &stdout
	defer func() { (*main.OsStdout) = oldOut }()

	main.ResetJSONOutput()
	t.Cleanup(main.ResetJSONOutput)
	(*main.JsonOutput) = true

	if code := main.CmdCronsInfo([]string{cronsInfoID}); code != 0 {
		t.Errorf("crons info json = %d, want 0", code)
	}
	var resp api.CronResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("JSON envelope parse failed: %v\nraw: %s", err, stdout.String())
	}
	// Pin every field so a future tag drift breaks the test.
	if resp.ID != cronsInfoID ||
		resp.AppID != "0123456789abcdef0123456789abcde0" ||
		resp.Schedule != "*/5 * * * *" ||
		resp.Path != "/cleanup" ||
		!resp.Enabled ||
		resp.LastFiredAt != "2026-08-10T09:00:00Z" ||
		resp.CreatedAt != "2026-08-01T12:00:00Z" {
		t.Errorf("cron response drift: %+v", resp)
	}
}

// --- client-side rejections (no server call) -------------------------------

func TestCmdCronsInfo_BadID_NoServerCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := main.CmdCronsInfo([]string{"not-hex"}); code != 1 {
		t.Errorf("crons info bad id = %d, want 1", code)
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0 (local validation only)", calls)
	}
}

func TestCmdCronsInfo_MissingID_NoServerCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := main.CmdCronsInfo([]string{}); code != 1 {
		t.Errorf("crons info no args = %d, want 1", code)
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0", calls)
	}
}

func TestCmdCronsInfo_ExtraPositional(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := main.CmdCronsInfo([]string{cronsInfoID, "extra"}); code != 1 {
		t.Errorf("crons info extra positional = %d, want 1", code)
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0", calls)
	}
}

// --- server error surfacing ------------------------------------------------

// TestCmdCronsInfo_ServerNotFound asserts the SDK's RFC 7807 problem
// reaches the operator verbatim. The handler returns a byte-identical
// 404 on missing or cross-account (handlers_ext.go::getCron), so the
// CLI never invents a local "cron not found" branch. This is the
// regression guard against the IDOR-safe two-step handler's 404
// message somehow leaking differently through the CLI.
func TestCmdCronsInfo_ServerNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/crons/"+cronsInfoID {
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

	if code := main.CmdCronsInfo([]string{cronsInfoID}); code == 0 {
		t.Errorf("crons info 404 = 0, want nonzero")
	}
}

// --- dispatch --------------------------------------------------------------

// TestRun_DispatchCronsInfo asserts the main run() switch routes
// `crons info <id>` into cmdCronsInfo rather than falling through to
// the "unknown crons subcommand" branch.
func TestRun_DispatchCronsInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/crons/"+cronsInfoID {
			http.Error(w, "no", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(makeCronResponse())
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := main.Run([]string{"crons", "info", cronsInfoID}); code != 0 {
		t.Errorf("run crons info = %d, want 0", code)
	}
}
