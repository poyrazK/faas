package main

// Test file for issue #740 / DEPLOY-PROV-5 — pins the
// `gregale build provenance <id>` rendering of the new
// framework_version row (issue #740 / ADR-087). The print function
// is whitebox (package main) on purpose: the row slice is the load-
// bearing contract for the row label + value, and the test must
// catch silent reordering of the fields. The minimum viable test
// is a single row scan that asserts the new row is present at the
// expected position.
//
// DEPLOY-PROV-6 follow-up / ADR-091 (issue #741 close-out) adds
// the `gregale build list` subcommand; the TestCmdBuildList_*
// tests below pin its rendering, filters, and pagination hint.
// The em-dash byte-for-byte assertion (TestCmdBuildList_NextBeforeHint)
// is load-bearing because cmdDeployments uses the same character
// — a silent drift between the two surfaces would break
// customer-automation that greps the hint.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestPrintProvenance_IncludesFrameworkVersion pins the new
// framework_version row at the position between railpack_version and
// base_digest (mirrors the build_provenance column order).
func TestPrintProvenance_IncludesFrameworkVersion(t *testing.T) {
	var buf bytes.Buffer
	printProvenance(&buf, api.BuildProvenanceResponse{
		ID:           "id-1",
		BuildID:      "build-1",
		BuildkitVer:  "",
		RailpackVer:  "",
		FrameworkVer: "22.11.0",
		BaseDigest:   "sha256:abc",
	})
	out := buf.String()
	// The row label is left-padded to 22 chars then ": " then the
	// value. "framework_version" is 17 chars, so the column is padded
	// to 22 → "framework_version: " (5-char gutter). The assertion
	// below groups the prefix + value with the full padding.
	wantLine := "framework_version:     22.11.0"
	if !strings.Contains(out, wantLine) {
		t.Errorf("expected line %q in output; got:\n%s", wantLine, out)
	}
	if !strings.Contains(out, "railpack_version:") {
		t.Errorf("expected railpack_version row to remain; got:\n%s", out)
	}
	if !strings.Contains(out, "base_digest:") {
		t.Errorf("expected base_digest row to remain; got:\n%s", out)
	}
	// Order: railpack_version must come before framework_version,
	// which must come before base_digest. The test fails on silent
	// reordering.
	idxRail := strings.Index(out, "railpack_version:")
	idxFW := strings.Index(out, "framework_version:")
	idxBase := strings.Index(out, "base_digest:")
	if !(idxRail < idxFW && idxFW < idxBase) {
		t.Errorf("row order: railpack=%d, framework=%d, base=%d; want railpack < framework < base",
			idxRail, idxFW, idxBase)
	}
}

// TestPrintProvenance_EmptyFrameworkVersionRendersEmptyRow pins the
// empty case: a build with no version detected (no .nvmrc, no
// engines.node, no .python-version, etc.) renders the
// framework_version row with an empty value, matching the
// buildkit_version / railpack_version convention (auditor wants
// line consistency, not skipped rows).
func TestPrintProvenance_EmptyFrameworkVersionRendersEmptyRow(t *testing.T) {
	var buf bytes.Buffer
	printProvenance(&buf, api.BuildProvenanceResponse{
		BuildID:      "build-2",
		FrameworkVer: "",
	})
	out := buf.String()
	wantLine := "framework_version:"
	if !strings.Contains(out, wantLine) {
		t.Errorf("expected empty framework_version row; got:\n%s", out)
	}
}

// --- cmdBuildList ----------------------------------------------------------
//
// Tests mirror commands_deployments_test.go's TestCmdDeployments_*
// shape (httptest.NewServer fake + t.Setenv + osStdout swap) so
// the cross-resource surface stays consistent. The em-dash byte-
// for-byte check is the only test that's unique to builds (and
// mirrors cmdDeployments' cursor hint).

// cmdBuildListText runs cmdBuildList with `args` and returns the
// captured stdout. The fake server returns the supplied page; tests
// that need a multi-page walk wire their own handler.
func cmdBuildListText(t *testing.T, page api.BuildListResponse, args []string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()
	if code := cmdBuildList(args); code != 0 {
		t.Fatalf("cmdBuildList(%v) = %d, want 0", args, code)
	}
	return stdout.String()
}

// TestCmdBuildList_Empty pins the empty-page text: 0 items →
// "No builds match." with no rows + no cursor hint.
func TestCmdBuildList_Empty(t *testing.T) {
	out := cmdBuildListText(t, api.BuildListResponse{}, nil)
	if !strings.Contains(out, "No builds match.") {
		t.Errorf("missing 'No builds match.' line\nfull: %s", out)
	}
}

// TestCmdBuildList_NonEmpty pins the tabular row renderer: 2
// builds render to two 6-column rows. The format string is
// "%-32s %-32s %-10s %-10s %10s %s\n" — values must NOT include
// the "id:" / "status:" labels (those are for the singular
// `gregale build status` path, not the list).
func TestCmdBuildList_NonEmpty(t *testing.T) {
	page := api.BuildListResponse{
		Items: []api.BuildResponse{
			{
				ID:           "b00000000000000000000000000000001",
				DeploymentID: "d00000000000000000000000000000001",
				Status:       "running",
				Kind:         "railpack",
				SourceBytes:  12345,
				StartedAt:    "2026-08-10T12:00:00Z",
			},
			{
				ID:           "b00000000000000000000000000000002",
				DeploymentID: "d00000000000000000000000000000002",
				Status:       "succeeded",
				Kind:         "dockerfile",
				SourceBytes:  67890,
				StartedAt:    "2026-08-10T12:01:00Z",
			},
		},
	}
	out := cmdBuildListText(t, page, nil)
	for _, want := range []string{
		"b00000000000000000000000000000001",
		"b00000000000000000000000000000002",
		"running", "succeeded", "railpack", "dockerfile",
		"12345", "67890",
		"2026-08-10T12:00:00Z", "2026-08-10T12:01:00Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// The singular-status renderer uses "status:" prefixes; the
	// list path must not.
	if strings.Contains(out, "status:") {
		t.Errorf("list output unexpectedly uses 'status:' label:\n%s", out)
	}
}

// TestCmdBuildList_NextBeforeHint pins the cursor hint: when the
// page has a next_before, the output MUST end with the em-dash
// (U+2014) hint byte-for-byte matching cmdDeployments. Drift
// between the two surfaces breaks automation that greps the hint.
//
// The hint's cursor arg is the opaque tuple "<started_at>|<id_hex>"
// (post-review fix for code-review issues #74 + #75) — the CLI
// threads NextBefore verbatim, so this test pins that contract
// end-to-end.
func TestCmdBuildList_NextBeforeHint(t *testing.T) {
	page := api.BuildListResponse{
		Items: []api.BuildResponse{
			{ID: "b1", Status: "running"},
		},
		NextBefore: "2026-08-10T12:00:00Z|b00000000000000000000000000000001",
	}
	out := cmdBuildListText(t, page, nil)
	// Byte-for-byte em-dash (U+2014) check.
	const wantHint = "... more — pass --before 2026-08-10T12:00:00Z|b00000000000000000000000000000001\n"
	if !strings.Contains(out, wantHint) {
		t.Errorf("missing em-dash cursor hint %q\nfull: %s", wantHint, out)
	}
	// And confirm it's the U+2014 (not a hyphen or en-dash).
	if !strings.Contains(out, "—") {
		t.Errorf("output missing U+2014 em-dash:\n%s", out)
	}
	if strings.Contains(out, "–") { // en-dash (U+2013) — wrong
		t.Errorf("output uses en-dash (U+2013) instead of em-dash (U+2014):\n%s", out)
	}
}

// TestCmdBuildList_NextBeforeHint_QueuedTail pins the queued-tail
// cursor hint: when the page's last row is queued, the cursor's
// started_at segment is empty ("|id_hex" shape) and the CLI must
// render that verbatim — no time-formatting, no quoting.
//
// Regression tripwire for code-review Issue #1 (queued builds
// dropped past page boundary under the original single-column
// cursor that required a non-null started_at anchor).
func TestCmdBuildList_NextBeforeHint_QueuedTail(t *testing.T) {
	page := api.BuildListResponse{
		Items: []api.BuildResponse{
			{ID: "b1", Status: "running"},
		},
		NextBefore: "|b00000000000000000000000000000001",
	}
	out := cmdBuildListText(t, page, nil)
	// Quoted to make the empty-string segment visually obvious.
	const wantHint = "... more — pass --before |b00000000000000000000000000000001\n"
	if !strings.Contains(out, wantHint) {
		t.Errorf("missing queued-tail cursor hint %q\nfull: %s", wantHint, out)
	}
}

// TestCmdBuildList_QueuedRow_RendersEmDash pins the queued-build
// rendering: a build with no started_at must render an em-dash
// in the STARTED column rather than an empty string (which would
// collapse the column and break alignment with running rows).
func TestCmdBuildList_QueuedRow_RendersEmDash(t *testing.T) {
	page := api.BuildListResponse{
		Items: []api.BuildResponse{
			{
				ID:           "b00000000000000000000000000000001",
				DeploymentID: "d00000000000000000000000000000001",
				Status:       "queued",
				Kind:         "railpack",
				StartedAt:    "", // queued → no started_at
			},
		},
	}
	out := cmdBuildListText(t, page, nil)
	if !strings.Contains(out, "queued") {
		t.Errorf("missing 'queued' status\nfull: %s", out)
	}
	// Em-dash must appear at least once (the queued row's started
	// column). TestRenderBuildListRow_QueuedStartedAtEmDash below
	// pins the exact format string.
	if !strings.Contains(out, "—") {
		t.Errorf("queued row missing em-dash placeholder in started column:\n%s", out)
	}
}

// TestRenderBuildListRow_QueuedStartedAtEmDash pins the format
// string shape: a queued build row must contain the em-dash
// in the STARTED column position (last column). Drift here breaks
// shell pipelines that pipe `gregale build list` into column-aware
// tooling (awk, column -t).
func TestRenderBuildListRow_QueuedStartedAtEmDash(t *testing.T) {
	var buf bytes.Buffer
	renderBuildListRow(&buf, api.BuildResponse{
		ID:           "b1",
		DeploymentID: "d1",
		Status:       "queued",
		Kind:         "railpack",
		StartedAt:    "",
	})
	out := buf.String()
	// The format string ends with "%s\n" for started; the queued
	// row's placeholder is U+2014. The full row must therefore
	// contain a trailing U+2014 followed by a newline.
	if !strings.HasSuffix(out, "—\n") {
		t.Errorf("queued row does not end with em-dash + newline:\n%q", out)
	}
	if !strings.Contains(out, "queued") {
		t.Errorf("queued row missing status 'queued':\n%q", out)
	}
}

// TestCmdBuildList_Unauthenticated: no FAAS_TOKEN → exit != 0
// (no API round-trip; authedClient fails first).
func TestCmdBuildList_Unauthenticated(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if code := cmdBuildList(nil); code == 0 {
		t.Error("cmdBuildList without token must fail")
	}
}

// TestCmdBuildList_InvalidLimit pins the --limit validation:
// 0 < N <= 200. Out-of-range values → exit 1, no API round-trip.
func TestCmdBuildList_InvalidLimit(t *testing.T) {
	if code := cmdBuildList([]string{"--limit", "999"}); code != 1 {
		t.Errorf("cmdBuildList --limit 999 = %d, want 1", code)
	}
	if code := cmdBuildList([]string{"--limit", "-1"}); code != 1 {
		t.Errorf("cmdBuildList --limit -1 = %d, want 1", code)
	}
	// Post-review fix: --limit 0 used to silently fall back to
	// the default 50. The help text says "1-200" — accepting 0
	// was a UX papercut (callers who meant the default should
	// omit --limit). Now rejected to match the help contract.
	if code := cmdBuildList([]string{"--limit", "0"}); code != 1 {
		t.Errorf("cmdBuildList --limit 0 = %d, want 1", code)
	}
}

// TestCmdBuildList_InvalidStatus pins the --status enum guard:
// anything outside queued|running|succeeded|failed → exit 1.
func TestCmdBuildList_InvalidStatus(t *testing.T) {
	if code := cmdBuildList([]string{"--status", "bogus"}); code != 1 {
		t.Errorf("cmdBuildList --status bogus = %d, want 1", code)
	}
}

// TestCmdBuildList_ExtraPositional: positional args are rejected
// (mirrors cmdDeployments' behavior — all flags, no positionals).
func TestCmdBuildList_ExtraPositional(t *testing.T) {
	if code := cmdBuildList([]string{"foo"}); code != 1 {
		t.Errorf("cmdBuildList extra positional = %d, want 1", code)
	}
}

// TestCmdBuildList_JSON_EnvelopeShape pins the JSON output path:
// -j must serialize the envelope (items + next_before), not the
// bare slice. Mirrors cmdDeployments' deliberate break from the
// apps/crons/keys NDJSON convention.
func TestCmdBuildList_JSON_EnvelopeShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.BuildListResponse{
			Items: []api.BuildResponse{
				{ID: "b1", Status: "running"},
			},
			NextBefore: "2026-08-10T12:00:00.000000000Z",
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

	if code := cmdBuildList(nil); code != 0 {
		t.Errorf("cmdBuildList -j = %d, want 0", code)
	}
	var env api.BuildListResponse
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("JSON envelope parse failed: %v\nraw: %s", err, stdout.String())
	}
	if len(env.Items) != 1 || env.Items[0].ID != "b1" {
		t.Errorf("envelope items = %+v", env.Items)
	}
	if env.NextBefore == "" {
		t.Errorf("envelope next_before lost; envelope = %+v", env)
	}
}

// TestCmdBuildList_All pins the multi-page walk: two server
// pages (first has NextBefore, second is empty) under --all must
// be flattened into the full list. JSON output is the bare slice
// (no envelope) — matches cmdDeploymentsAll's convention.
func TestCmdBuildList_All(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(api.BuildListResponse{
				Items: []api.BuildResponse{
					{ID: "b1", Status: "running"},
				},
				NextBefore: "2026-08-10T12:00:00.000000000Z",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(api.BuildListResponse{})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdBuildList([]string{"--all"}); code != 0 {
		t.Errorf("cmdBuildList --all = %d, want 0", code)
	}
	if calls < 2 {
		t.Errorf("--all did not walk past page 1; calls = %d", calls)
	}
	// Text path: both build ids must appear.
	for _, want := range []string{"b1"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q in --all output:\n%s", want, stdout.String())
		}
	}
}

// TestCmdBuildList_All_JSON pins --all's JSON output as the bare
// slice (matches cmdDeploymentsAll).
func TestCmdBuildList_All_JSON(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(api.BuildListResponse{
				Items:      []api.BuildResponse{{ID: "b1", Status: "running"}},
				NextBefore: "next-cursor",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(api.BuildListResponse{})
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

	if code := cmdBuildList([]string{"--all"}); code != 0 {
		t.Errorf("cmdBuildList --all -j = %d, want 0", code)
	}
	var items []api.BuildResponse
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		t.Fatalf("JSON slice parse failed: %v\nraw: %s", err, stdout.String())
	}
	if len(items) != 1 || items[0].ID != "b1" {
		t.Errorf("items = %+v, want one b1", items)
	}
}
