// Wire-call tests for `gregale deploys status <id>` — A1 in the
// ADR-117 v2 follow-on mega-PR. Pins the errgroup parallel
// fetch, the footer branch rendering, and the cross-account 404
// posture.
//
// Conventions mirror deploys_show_test.go: the same FAAS_API +
// FAAS_TOKEN env swap, the same swapStdout test helper, the
// same showTestID 32-hex fixture. The fan-out stub
// (showServerDual) lives in deploys_show_test.go so the two
// test files share the helper.
package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestCmdDeploysStatus_HappyPath — A1: a successful status fetch
// renders the closed 6-stage block with the "live since <ts>"
// footer. Pins the errgroup parallel fetch (both endpoints
// hit), the footer branch (status="live"), and the
// deriveTerminalAt logic (first history row's StartedAt).
func TestCmdDeploysStatus_HappyPath(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	srv := showServerDual(t,
		stageStateAllCompleted(now),
		deploymentResponseLive(showTestID, now),
		showServerHooks{},
	)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restoreStdout := swapStdout(t)
	defer restoreStdout()
	jsonOutput = false
	defer func() { jsonOutput = false }()

	if code := cmdDeploysStatus([]string{showTestID}); code != 0 {
		t.Fatalf("cmdDeploysStatus happy path = %d, want 0", code)
	}
	got := stdout.String()
	// All 6 closed-set labels must render — the closed-set
	// invariant doesn't depend on the status branch.
	for _, want := range []string{
		"Source downloaded", "Dependencies restored", "Image built",
		"Security scan", "Snapshot prepared", "Readiness passed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing label %q in status render\nfull: %s", want, got)
		}
	}
	// The "live since" footer branch is taken when status="live"
	// and the first history row's StartedAt is non-nil (which
	// stageStateAllCompleted seeds with now-30s).
	if !strings.Contains(got, "live since") {
		t.Errorf("expected 'live since' footer for status=live\nfull: %s", got)
	}
	// Pin the timestamp matches the first history row's
	// StartedAt (now-30s) rather than the column's CreatedAt,
	// so a future refactor that picks the wrong row's
	// StartedAt fails this test loudly.
	wantTs := now.Add(-30 * time.Second).UTC().Format(time.RFC3339)
	if !strings.Contains(got, wantTs) {
		t.Errorf("expected 'live since %s' footer\nfull: %s", wantTs, got)
	}
}

// TestCmdDeploysStatus_Failed — A1: the failed branch picks the
// "failed at <ts>" footer. The footer anchor is the failed row's
// EndedAt, NOT the deployment's CreatedAt.
func TestCmdDeploysStatus_Failed(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	// stageStateAllCompleted seeds all 6 stages as completed;
	// we override the third row to failed with a specific
	// EndedAt so the footer assertion is deterministic.
	bytes := stageStateAllCompleted(now)
	// Mark the IMAGE_BUILD row as failed with a known reason+ts.
	var ss map[string]any
	if err := json.Unmarshal(bytes, &ss); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	hist := ss["history"].([]any)
	imageBuildRow := hist[2].(map[string]any)
	imageBuildRow["status"] = "failed"
	imageBuildRow["reason"] = "out of memory"
	imageBuildRow["ended_at"] = now.Add(-10 * time.Second).Format(time.RFC3339Nano)
	imageBuildRow["duration_ms"] = 13000
	failedSS, _ := json.Marshal(ss)

	srv := showServerDual(t, failedSS, deploymentResponseFailed(showTestID, now), showServerHooks{})
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restoreStdout := swapStdout(t)
	defer restoreStdout()
	jsonOutput = false
	defer func() { jsonOutput = false }()

	if code := cmdDeploysStatus([]string{showTestID}); code != 0 {
		t.Fatalf("cmdDeploysStatus failed path = %d, want 0", code)
	}
	got := stdout.String()
	// The failed row's duration column carries the failure
	// reason (NOT the duration value).
	if !strings.Contains(got, "failed: out of memory") {
		t.Errorf("expected 'failed: out of memory' duration column\nfull: %s", got)
	}
	// The "failed at" footer is status-driven.
	if !strings.Contains(got, "failed at") {
		t.Errorf("expected 'failed at' footer for status=failed\nfull: %s", got)
	}
	wantTs := now.Add(-10 * time.Second).UTC().Format(time.RFC3339)
	if !strings.Contains(got, wantTs) {
		t.Errorf("expected 'failed at %s' footer\nfull: %s", wantTs, got)
	}
	// A status lookup should answer the next question too: what
	// failed and what should I change before retrying? The deployment
	// row already carries the persisted explanation, so status must
	// render it without a second command or a log grep.
	for _, want := range []string{
		"image_not_found",
		"the image reference could not be pulled",
		"why: the registry returned 404",
		"fix: • push the image and retry the deployment",
		"manifest unknown",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing actionable failure detail %q\nfull: %s", want, got)
		}
	}
	// The "live since" footer must NOT appear for a failed deploy.
	if strings.Contains(got, "live since") {
		t.Errorf("failed render must NOT contain 'live since'\nfull: %s", got)
	}
}

// TestCmdDeploysStatus_ParallelFetches — A1: the errgroup fan-out
// over GetDeployment + GetDeploymentStages runs in parallel.
// Proves the parallelization by inserting a 50ms delay on BOTH
// endpoints and asserting the total round-trip latency is below
// the sum-of-delays threshold (90ms < 100ms = 2*50ms serial).
//
// Review finding C5: the pre-fix version only delayed ONE
// endpoint (50ms depPath). A serial fan-out also finishes in
// ~51ms and the test passes — the assertion was a false
// positive. Post-fix, both endpoints carry 50ms, so:
//
//   - parallel fan-out: ~50ms (max of the two, fired concurrently)
//   - serial fan-out:   ~100ms (50ms + 50ms sequential)
//
// The 90ms gate fails serial cleanly while keeping the test
// robust to CI scheduler jitter (50ms + 40ms scheduler overhead
// stays comfortably under).
func TestCmdDeploysStatus_ParallelFetches(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	stagesPath := "/v1/deployments/" + showTestID + "/stages"
	depPath := "/v1/deployments/" + showTestID
	hooks := showServerHooks{
		Delays: map[string]time.Duration{
			stagesPath: 50 * time.Millisecond,
			depPath:    50 * time.Millisecond,
		},
	}
	srv := showServerDual(t, stageStateAllCompleted(now), deploymentResponseLive(showTestID, now), hooks)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restoreStdout := swapStdout(t)
	defer restoreStdout()
	jsonOutput = false
	defer func() { jsonOutput = false }()

	start := time.Now()
	if code := cmdDeploysStatus([]string{showTestID}); code != 0 {
		t.Fatalf("cmdDeploysStatus parallel = %d, want 0", code)
	}
	elapsed := time.Since(start)
	// Parallel fan-out: ~50ms (both endpoints fire concurrently;
	// the wall clock is the max of the two). Serial fan-out:
	// ~100ms (50ms + 50ms sequential). Threshold 90ms fails
	// serial cleanly while tolerating CI scheduler jitter.
	if elapsed > 90*time.Millisecond {
		t.Errorf("parallel fetch took %v, want < 90ms (proves errgroup fan-out; serial would be ~100ms)", elapsed)
	}
	// Output must still render — the parallel fetch succeeded.
	if !strings.Contains(stdout.String(), "Source downloaded") {
		t.Errorf("missing 'Source downloaded' label after parallel fetch\nfull: %s", stdout.String())
	}
}

// TestCmdDeploysStatus_Superseded — review finding C1:
// deriveTerminalAt (cmd/gregale/deploys_show.go) handles the
// "superseded" branch by anchoring on depCreatedAt (the
// deployment row's insert timestamp). The pre-fix code returned
// time.Time{} for any status other than "live"/"failed", which
// left the operator looking at a stage table with no terminal
// anchor even though deployments.status said "superseded". This
// test pins the fix.
//
// Pins:
//   - the footer renders "superseded at <depCreatedAt>"
//   - the footer does NOT carry "live since" or "failed at"
//   - the footer anchor matches the dep row's CreatedAt (NOT
//     any history-row timestamp)
func TestCmdDeploysStatus_Superseded(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	srv := showServerDual(t,
		stageStateAllCompleted(now),
		deploymentResponseSuperseded(showTestID, now),
		showServerHooks{},
	)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restoreStdout := swapStdout(t)
	defer restoreStdout()
	jsonOutput = false
	defer func() { jsonOutput = false }()

	if code := cmdDeploysStatus([]string{showTestID}); code != 0 {
		t.Fatalf("cmdDeploysStatus superseded = %d, want 0", code)
	}
	got := stdout.String()
	wantTs := now.UTC().Format(time.RFC3339)
	if !strings.Contains(got, "superseded at "+wantTs) {
		t.Errorf("expected 'superseded at %s' footer for status=superseded\nfull: %s", wantTs, got)
	}
	if strings.Contains(got, "live since") {
		t.Errorf("superseded render must NOT contain 'live since'\nfull: %s", got)
	}
	if strings.Contains(got, "failed at") {
		t.Errorf("superseded render must NOT contain 'failed at'\nfull: %s", got)
	}
}

// TestCmdDeploysStatus_CrossAccount404 — A1: the cross-account
// posture (404) is symmetric across both endpoints. A 404 on
// either side surfaces as a non-zero exit with the same
// "Could not fetch deployment status" wrapper the operator
// sees for a missing deploy. This is the IDOR-safe branch: the
// wire is identical for cross-account and missing-id, so the
// CLI does not distinguish.
//
// We use the original showServer (single-payload, 404 on
// unmapped paths) so the deployment-row endpoint also 404s —
// errgroup then surfaces whichever error fires first and the
// CLI exits 1.
func TestCmdDeploysStatus_CrossAccount404(t *testing.T) {
	srv := showServer(t, []byte(`{}`), map[string]bool{})
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdDeploysStatus([]string{showTestID}); code == 0 {
		t.Errorf("cmdDeploysStatus on 404 = 0, want non-zero")
	}
}

// TestCmdDeploysStatus_NoArgs — A1: usage-error branch when the
// operator forgets the deployment id.
func TestCmdDeploysStatus_NoArgs(t *testing.T) {
	if code := cmdDeploysStatus(nil); code != 1 {
		t.Errorf("cmdDeploysStatus nil args = %d, want 1", code)
	}
}

// TestCmdDeploysStatus_InvalidIDFailsFast — A1: the local
// deploymentIDPattern gate fires before any HTTP round-trip.
func TestCmdDeploysStatus_InvalidIDFailsFast(t *testing.T) {
	// No httptest server — the regex gate rejects before
	// authedClient is called.
	t.Setenv("FAAS_TOKEN", "")
	if code := cmdDeploysStatus([]string{"not-hex"}); code != 1 {
		t.Errorf("cmdDeploysStatus bad id = %d, want 1", code)
	}
}

// TestCmdDeploysStatus_JSON — A1: --json emits the typed
// StageState envelope (current + history). Mirrors
// cmdDeploysShow_JSON so the two subcommands share the same
// wire-shape contract.
func TestCmdDeploysStatus_JSON(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	srv := showServerDual(t,
		stageStateAllCompleted(now),
		deploymentResponseLive(showTestID, now),
		showServerHooks{},
	)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restoreStdout := swapStdout(t)
	defer restoreStdout()
	jsonOutput = true
	defer func() { jsonOutput = false }()

	if code := cmdDeploysStatus([]string{showTestID}); code != 0 {
		t.Fatalf("cmdDeploysStatus --json = %d, want 0", code)
	}
	var got struct {
		Current string           `json:"current"`
		History []map[string]any `json:"history"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal --json output: %v\nraw: %s", err, stdout.String())
	}
	if got.Current != "readiness" {
		t.Errorf("current: got %q, want %q", got.Current, "readiness")
	}
	if len(got.History) != 6 {
		t.Errorf("history len: got %d, want 6 (closed set)", len(got.History))
	}
}
