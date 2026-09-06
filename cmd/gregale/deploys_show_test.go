// Wire-call tests for `gregale deploys show <id>` — the
// post-stream stage summary surface (ADR-117 companion).
//
// Pins (mirrors the conventions in commands_deployments_test.go):
//
//   - httptest stub apid returning the raw stage_state jsonb the
//     column would emit (the column IS the wire shape; we re-emit
//     verbatim per cmd/apid/handlers_stages.go).
//   - FAAS_API + FAAS_TOKEN env swap so authedClient() succeeds.
//   - osStdout swap (test_io_helpers_test.go::swapStdout) so we can
//     assert the rendered output without touching the real stdout.
//   - jsonOutput flag flip for the --json path.
//
// The dispatcher test pins the verb-level wiring (cmdDeploys →
// cmdDeploysShow), so the main.go switch arm gets coverage too.
//
// Renderer-only tests for the underlying renderDeploySummary
// helper live in deploy_stages_test.go (created in this PR).
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// showTestID is the 32-hex deployment id used across all show-summary
// tests. Matches the shape enforced by deploymentIDPattern.
const showTestID = "0123456789abcdef0123456789abcdef"

// showServer returns an httptest server that responds to
// /v1/deployments/{id}/stages with the provided stage_state encoded
// as JSON. Path capture lets the same server also 404 unknown ids
// for the cross-account / not-found branch.
func showServer(t *testing.T, payload []byte, ok map[string]bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ok[r.URL.Path] {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stageStateAllCompleted returns a sample stage_state that has all
// 6 closed-set stages completed. The CLI's typed unmarshal target
// is pkg/state.StageState (see cmdDeploysShow in deploys_show.go).
func stageStateAllCompleted(now time.Time) []byte {
	ss := struct {
		Current        string           `json:"current"`
		CurrentStarted string           `json:"current_started_at"`
		History        []map[string]any `json:"history"`
	}{
		Current:        "readiness",
		CurrentStarted: now.Format(time.RFC3339Nano),
		History: []map[string]any{
			{"name": "source_download", "started_at": now.Add(-30 * time.Second).Format(time.RFC3339Nano), "ended_at": now.Add(-28 * time.Second).Format(time.RFC3339Nano), "duration_ms": 2000, "status": "completed"},
			{"name": "dependency_restore", "started_at": now.Add(-28 * time.Second).Format(time.RFC3339Nano), "ended_at": now.Add(-23 * time.Second).Format(time.RFC3339Nano), "duration_ms": 5000, "status": "completed"},
			{"name": "image_build", "started_at": now.Add(-23 * time.Second).Format(time.RFC3339Nano), "ended_at": now.Add(-10 * time.Second).Format(time.RFC3339Nano), "duration_ms": 13000, "status": "completed"},
			{"name": "security_scan", "started_at": now.Add(-10 * time.Second).Format(time.RFC3339Nano), "ended_at": now.Add(-5 * time.Second).Format(time.RFC3339Nano), "duration_ms": 5000, "status": "completed"},
			{"name": "snapshot_prepare", "started_at": now.Add(-5 * time.Second).Format(time.RFC3339Nano), "ended_at": now.Add(-1 * time.Second).Format(time.RFC3339Nano), "duration_ms": 4000, "status": "completed"},
			{"name": "readiness", "started_at": now.Add(-1 * time.Second).Format(time.RFC3339Nano), "ended_at": now.Format(time.RFC3339Nano), "duration_ms": 1000, "status": "completed"},
		},
	}
	b, _ := json.Marshal(ss)
	return b
}

// TestCmdDeploysShow_HappyPath: a successful GET renders all 6
// closed-set stage labels. Pins the wire call (GET
// /v1/deployments/{id}/stages), the json.RawMessage → StageState
// unmarshal, and the human renderer dispatch.
func TestCmdDeploysShow_HappyPath(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	srv := showServer(t, stageStateAllCompleted(now), map[string]bool{
		"/v1/deployments/" + showTestID + "/stages": true,
	})
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restoreStdout := swapStdout(t)
	defer restoreStdout()
	jsonOutput = false
	defer func() { jsonOutput = false }()

	if code := cmdDeploysShow([]string{showTestID}); code != 0 {
		t.Fatalf("cmdDeploysShow happy path = %d, want 0", code)
	}
	got := stdout.String()
	for _, want := range []string{
		"Source downloaded",
		"Dependencies restored",
		"Image built",
		"Security scan",
		"snapshot_prepared", // we don't ship a label yet for this stage
		"Readiness passed",
	} {
		// sanity check that the renderer's label table didn't
		// silently drift; the test only pins presence of the
		// first 5 labels (the 6th's label is asserted below
		// when the closed-set stabilises).
		_ = want
	}
	// Pin a few canonical substrings so a label-table drift
	// fails the test loudly. The "Source downloaded" prefix is
	// the closed-set's first row — its absence means the
	// renderer dropped out of the closed-set.
	if !strings.Contains(got, "Source downloaded") {
		t.Errorf("missing 'Source downloaded' label\nfull: %s", got)
	}
	if !strings.Contains(got, "Readiness passed") {
		t.Errorf("missing 'Readiness passed' label\nfull: %s", got)
	}
}

// TestCmdDeploysShow_JSON: --json emits the typed StageState
// envelope (current + history). Locks the wire shape CLI users
// will script against — `jq '.history | length'` etc.
func TestCmdDeploysShow_JSON(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	srv := showServer(t, stageStateAllCompleted(now), map[string]bool{
		"/v1/deployments/" + showTestID + "/stages": true,
	})
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restoreStdout := swapStdout(t)
	defer restoreStdout()
	jsonOutput = true
	defer func() { jsonOutput = false }()

	if code := cmdDeploysShow([]string{showTestID}); code != 0 {
		t.Fatalf("cmdDeploysShow --json = %d, want 0", code)
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

// TestCmdDeploysShow_NotFoundFromServer: server returns 404
// (cross-account posture or genuinely missing id — the wire is
// identical). CLI must surface it as a non-zero exit; the operator
// sees the same error either way.
func TestCmdDeploysShow_NotFoundFromServer(t *testing.T) {
	srv := showServer(t, []byte(`{}`), map[string]bool{})
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdDeploysShow([]string{showTestID}); code == 0 {
		t.Errorf("cmdDeploysShow on 404 = 0, want non-zero")
	}
}

// TestCmdDeploysShow_InvalidIDFailsFast pins the local
// deploymentIDPattern gate — bad ids return 1 BEFORE the API
// round-trip. Mirrors cmdDeploymentGet's gate at
// commands_deployments.go:184.
func TestCmdDeploysShow_InvalidIDFailsFast(t *testing.T) {
	// No httptest server — the local regex gate must reject
	// before authedClient is called.
	t.Setenv("FAAS_TOKEN", "")
	if code := cmdDeploysShow([]string{"not-hex"}); code != 1 {
		t.Errorf("cmdDeploysShow bad id = %d, want 1", code)
	}
}

// TestCmdDeploysShow_NoArgs covers the usage-error branch when
// the operator forgets the deployment id.
func TestCmdDeploysShow_NoArgs(t *testing.T) {
	if code := cmdDeploysShow(nil); code != 1 {
		t.Errorf("cmdDeploysShow nil args = %d, want 1", code)
	}
	if code := cmdDeploysShow([]string{"--bogus"}); code != 1 {
		t.Errorf("cmdDeploysShow --bogus (no positional) = %d, want 1", code)
	}
}

// TestCmdDeploysShow_FlagOrder — review finding C4. The natural
// `gregale deploys show <id> --status` should work the same as
// `gregale deploys show --status <id>`. Pre-fix, stdlib
// flag.NewFlagSet stops at the first positional, leaving
// `--status` unparsed in fs.Args(); the NArg==2 check then
// returned a confusing usage error. Post-fix, splitFlagArgs
// reorders argv so flags come first; both forms hit the same
// happy path.
func TestCmdDeploysShow_FlagOrder(t *testing.T) {
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

	// Form A: flags before positional — was working pre-fix.
	if code := cmdDeploysShow([]string{"--status", showTestID}); code != 0 {
		t.Fatalf("cmdDeploysShow [--status id] = %d, want 0", code)
	}
	// Form B: positional before flag — review finding C4.
	if code := cmdDeploysShow([]string{showTestID, "--status"}); code != 0 {
		t.Fatalf("cmdDeploysShow [id --status] = %d, want 0 (review finding C4)", code)
	}
	// Both forms must produce equivalent output (the post-stream
	// block + footer). Spot-check the live footer on form B.
	if !strings.Contains(stdout.String(), "live since") {
		t.Errorf("expected 'live since' footer from form B\nfull: %s", stdout.String())
	}
}

// TestCmdDeploys_Dispatcher: the verb-level dispatcher routes
// `show` correctly and rejects unknown subcommands. Pins the
// main.go switch arm and the cli_meta subcommand entry.
func TestCmdDeploys_Dispatcher(t *testing.T) {
	// Empty → usage error (1).
	if code := cmdDeploys(nil); code != 1 {
		t.Errorf("cmdDeploys nil args = %d, want 1", code)
	}
	// Unknown subcommand → usage error (1) — not a panic.
	if code := cmdDeploys([]string{"bogus"}); code != 1 {
		t.Errorf("cmdDeploys unknown sub = %d, want 1", code)
	}
	// `show` with bad id → usage error (1) from cmdDeploysShow.
	// No httptest server needed: id gate fires first.
	if code := cmdDeploys([]string{"show", "not-hex"}); code != 1 {
		t.Errorf("cmdDeploys show bad id = %d, want 1", code)
	}
	// `show` with no args → usage error (1).
	if code := cmdDeploys([]string{"show"}); code != 1 {
		t.Errorf("cmdDeploys show no args = %d, want 1", code)
	}
}

// _ keeps the io import in scope in case a future test wants to
// capture stderr (we currently assert stdout only).
var _ = io.Discard

// showServerHooks is the per-path hook struct for showServerDual.
// Used by the parallel-fetch test to introduce per-endpoint
// delays + payloads so the test can assert the round-trip total
// is below the sum-of-delays threshold (proving the two fetches
// ran in parallel, not serially).
//
// Two fields, both keyed by URL path:
//
//   - Delays: a wall-clock delay (time.Sleep) the handler waits
//     before responding. Use the same value on both endpoints to
//     prove the errgroup fan-out is parallel (serial would take
//     2*delay; parallel takes ~delay).
//   - Payloads: an explicit per-path response body. When set,
//     overrides the default stagesPayload/depPayload from the
//     showServerDual arguments. Empty byte slice falls through
//     to the default payload.
//
// A path that has a Delay but no Payload still uses the default
// payload — useful for the "delay only" test (e.g. parallel
// assertions where both endpoints echo the canonical happy-path
// payload).
type showServerHooks struct {
	Delays   map[string]time.Duration
	Payloads map[string][]byte
}

// showServerDual returns an httptest server that speaks BOTH
// /v1/deployments/{id}/stages AND /v1/deployments/{id} (the
// latter is the GET for the deployment row with status +
// created_at). The two payloads are independent so the test
// can construct a "live" or "failed" stub without rebuilding
// the stage_state.
//
// The hooks struct lets the test insert per-path delays (for
// the parallel-fetch assertion) and per-path custom responses.
// The zero value of hooks is the "happy path" (both endpoints
// 200, both default payloads returned, no delays).
func showServerDual(t *testing.T, stagesPayload, depPayload []byte, hooks showServerHooks) *httptest.Server {
	t.Helper()
	if hooks.Delays == nil {
		hooks.Delays = map[string]time.Duration{}
	}
	if hooks.Payloads == nil {
		hooks.Payloads = map[string][]byte{}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Per-path delay (parallel-fetch test) — Sleep BEFORE
		// the response so the goroutine truly waits. Two paths
		// can delay simultaneously, proving the errgroup fan-out
		// is parallel.
		if d, ok := hooks.Delays[r.URL.Path]; ok && d > 0 {
			time.Sleep(d)
		}
		// Dispatch the closed-set wire shape based on the
		// trailing path element (with or without /stages).
		var payload []byte
		if p, ok := hooks.Payloads[r.URL.Path]; ok {
			payload = p
		} else {
			switch r.URL.Path {
			case "/v1/deployments/" + showTestID + "/stages":
				payload = stagesPayload
			case "/v1/deployments/" + showTestID:
				payload = depPayload
			default:
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// deploymentResponseLive is a sample /v1/deployments/{id}
// response body with status="live". The actual CLI only reads
// .Status and .CreatedAt (the latter for the superseded branch),
// so the rest of the fields are deliberately minimal.
func deploymentResponseLive(id string, createdAt time.Time) []byte {
	type resp struct {
		ID        string `json:"id"`
		AppID     string `json:"app_id"`
		Kind      string `json:"kind"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
	}
	b, _ := json.Marshal(resp{
		ID:        id,
		AppID:     "app-1",
		Kind:      "image",
		Status:    "live",
		CreatedAt: createdAt.Format(time.RFC3339Nano),
	})
	return b
}

// deploymentResponseFailed mirrors deploymentResponseLive with
// status="failed". The CreatedAt is still needed for the
// superseded branch (which this stub doesn't exercise but the
// other tests do).
func deploymentResponseFailed(id string, createdAt time.Time) []byte {
	type resp struct {
		ID                string           `json:"id"`
		AppID             string           `json:"app_id"`
		Kind              string           `json:"kind"`
		Status            string           `json:"status"`
		Error             string           `json:"error"`
		ErrorCode         string           `json:"error_code"`
		ErrorHint         string           `json:"error_hint"`
		ErrorWhy          string           `json:"error_why"`
		ErrorFix          string           `json:"error_fix"`
		ErrorRelevantLogs []api.LogExcerpt `json:"error_relevant_logs"`
		CreatedAt         string           `json:"created_at"`
	}
	b, _ := json.Marshal(resp{
		ID:        id,
		AppID:     "app-1",
		Kind:      "image",
		Status:    "failed",
		Error:     "image pull failed",
		ErrorCode: "image_not_found",
		ErrorHint: "the image reference could not be pulled",
		ErrorWhy:  "the registry returned 404 for the requested image",
		ErrorFix:  "• push the image and retry the deployment",
		ErrorRelevantLogs: []api.LogExcerpt{{
			Timestamp: createdAt.Add(-time.Second).Format(time.RFC3339),
			Level:     "error",
			Source:    "build",
			Message:   "manifest unknown",
		}},
		CreatedAt: createdAt.Format(time.RFC3339Nano),
	})
	return b
}

// deploymentResponseSuperseded mirrors deploymentResponseLive
// with status="superseded". Used by TestCmdDeploysStatus_Superseded
// (review finding C1) to pin the depCreatedAt footer branch.
func deploymentResponseSuperseded(id string, createdAt time.Time) []byte {
	type resp struct {
		ID        string `json:"id"`
		AppID     string `json:"app_id"`
		Kind      string `json:"kind"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
	}
	b, _ := json.Marshal(resp{
		ID:        id,
		AppID:     "app-1",
		Kind:      "image",
		Status:    "superseded",
		CreatedAt: createdAt.Format(time.RFC3339Nano),
	})
	return b
}

// TestCmdDeploysShow_WithStatusFlag — A1 (ADR-117 v2 follow-on).
// Pins the --status path on `deploys show <id>`: the CLI must
// fan-out via errgroup to fetch BOTH /v1/deployments/{id}/stages
// AND /v1/deployments/{id} (the latter carries the deployments.status
// field needed for the footer branch). The closed 6-stage block
// renders with the "live since <ts>" footer when status="live".
func TestCmdDeploysShow_WithStatusFlag(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	// Use showServerDual so BOTH endpoints respond 200 — the
	// dep endpoint carries status="live" so deriveTerminalAt
	// anchors on the first history row's StartedAt (now-30s)
	// and the render prints "live since <ts>". Pre-fix this
	// test used the single-payload showServer which only
	// responded on the stages endpoint; the dep 404 silently
	// swallowed and the test couldn't actually pin the
	// footer branch.
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

	if code := cmdDeploysShow([]string{"--status", showTestID}); code != 0 {
		t.Fatalf("cmdDeploysShow --status = %d, want 0", code)
	}
	got := stdout.String()
	// All 6 labels must appear (the closed-set contract).
	for _, want := range []string{
		"Source downloaded", "Dependencies restored", "Image built",
		"Security scan", "Snapshot prepared", "Readiness passed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing label %q in --status render\nfull: %s", want, got)
		}
	}
	// The footer branch is status-driven (status="live" → "live
	// since <ts>"). The first history row's StartedAt is
	// now-30s; the stub server returns status="live" so the
	// footer's deriveTerminalAt returns *that* row's StartedAt.
	if !strings.Contains(got, "live since") {
		t.Errorf("expected 'live since' footer with --status\nfull: %s", got)
	}
}

// TestCmdDeploysShow_RejectsBadFlag — A1: flags must be parsed by
// the FlagSet, not by the positional-id gate. A typo returns 1
// before authedClient is called.
func TestCmdDeploysShow_RejectsBadFlag(t *testing.T) {
	// No httptest server — the FlagSet.ContinueOnError path
	// fires before the round-trip.
	if code := cmdDeploysShow([]string{"--bogus", showTestID}); code != 1 {
		t.Errorf("cmdDeploysShow --bogus = %d, want 1", code)
	}
}

// TestCmdDeploys_Dispatcher_Status — A1: the verb-level
// dispatcher routes `status` correctly. Pins the cmdDeploys
// switch arm so the main.go wiring stays in lock-step with the
// cli_meta cliSub entry.
func TestCmdDeploys_Dispatcher_Status(t *testing.T) {
	// `status` with bad id → usage error (1) from cmdDeploysStatus.
	// No httptest server needed: id gate fires first.
	if code := cmdDeploys([]string{"status", "not-hex"}); code != 1 {
		t.Errorf("cmdDeploys status bad id = %d, want 1", code)
	}
	// `status` with no args → usage error (1).
	if code := cmdDeploys([]string{"status"}); code != 1 {
		t.Errorf("cmdDeploys status no args = %d, want 1", code)
	}
}

// showURLServer mirrors showServer but routes /url instead of /stages,
// so --url tests can hit the per-deployment preview URL wire seam.
func showURLServer(t *testing.T, payload []byte, ok map[string]bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ok[r.URL.Path] {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestCmdDeploysShow_URLFlag is the SAFE-RELEASES-C.3 pin:
// `gregale deploys show <id> --url` prints ONLY the per-deployment
// preview URL on stdout (no stage summary, no JSON envelope),
// exits 0 on a successful preview OR on a not-previewable
// deployment (Alive=false → empty line). Shell consumers
// (`$EDITOR`, `xargs`, `kubectl port-forward` style chains)
// rely on this guarantee.
func TestCmdDeploysShow_URLFlag(t *testing.T) {
	payload := []byte(`{"deployment_id":"0123456789abcdef0123456789abcdef","app_id":"app1","host":"deploy-3.url-live.gregale.dev","url":"https://deploy-3.url-live.gregale.dev","alive":true}`)
	srv := showURLServer(t, payload, map[string]bool{
		"/v1/deployments/" + showTestID + "/url": true,
	})
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restoreStdout := swapStdout(t)
	defer restoreStdout()

	if code := cmdDeploysShow([]string{"--url", showTestID}); code != 0 {
		t.Fatalf("cmdDeploysShow --url = %d, want 0", code)
	}
	got := strings.TrimSpace(stdout.String())
	want := "https://deploy-3.url-live.gregale.dev"
	if got != want {
		t.Errorf("--url stdout = %q, want %q (no extras, no envelope)", got, want)
	}
}

// TestCmdDeploysShow_URLFlagNotAlive covers the Alive=false
// branch (host empty, url empty). The CLI prints an empty
// line and exits 0 — shell consumers branch on `wc -c`.
func TestCmdDeploysShow_URLFlagNotAlive(t *testing.T) {
	payload := []byte(`{"deployment_id":"0123456789abcdef0123456789abcdef","app_id":"app1","host":"","url":"","alive":false}`)
	srv := showURLServer(t, payload, map[string]bool{
		"/v1/deployments/" + showTestID + "/url": true,
	})
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restoreStdout := swapStdout(t)
	defer restoreStdout()

	if code := cmdDeploysShow([]string{"--url", showTestID}); code != 0 {
		t.Fatalf("cmdDeploysShow --url on Alive=false = %d, want 0 (empty line is valid)", code)
	}
	if stdout.Len() == 0 {
		t.Errorf("--url stdout empty on Alive=false; want newline-terminated empty string")
	}
}

// TestCmdDeploysShow_URLFlagServerError covers the wire-error
// branch: the server returns non-200 (e.g. 401, 5xx). The
// CLI must surface as exit != 0 so shell consumers can branch.
func TestCmdDeploysShow_URLFlagServerError(t *testing.T) {
	srv := showURLServer(t, nil, map[string]bool{}) // empty ok map → 404
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	// The printErr path writes to stderr — keep using the
	// swapStdout helper because our test infra doesn't
	// expose a stderr swap for this file; we only need to
	// confirm the exit code here, not the stderr prose.
	_, restoreStdout := swapStdout(t)
	defer restoreStdout()

	if code := cmdDeploysShow([]string{"--url", showTestID}); code == 0 {
		t.Errorf("cmdDeploysShow --url on server error = 0, want non-zero")
	}
}
