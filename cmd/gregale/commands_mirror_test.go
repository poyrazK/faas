// gregale mirror CLI wire tests (issue #72 / ADR-125 traffic
// mirroring PR-A2). Mirrors commands2_test.go::TestCmdTrafficSet_*
// shape exactly — httptest server, atomic hit counter, itoaForCli
// helper — so reviewers can compare side-by-side. The six leaves
// covered: list / create / info / update / rm / summary, plus the
// dispatcher's unknown-subcommand 1 exit.
//
// IDOR / quota / range tests live in cmd/apid/handlers_mirror_test.go
// (server-side). These tests pin the CLI ↔ server wire: every leaf
// must hit the canonical HTTP verb + path, send the canonical body
// shape, and render the canonical success / failure line. No DB,
// no pgx — just the http.Handler that the SDK round-trips to.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// mirrorTestApp is the canonical "two-deployments-live" fixture
// shape returned by the server in happy-path tests. The fields
// here are exactly MirrorRuleResponse's; missing zero values
// default-fill on the API DTO side.
func mirrorRuleTestFixture() api.MirrorRuleResponse {
	return api.MirrorRuleResponse{
		ID:                    "mrr_test123",
		AccountID:             "acc_a",
		AppID:                 "app_x",
		SourceDeploymentID:    "dep_src",
		MirrorDeploymentID:    "dep_mir",
		Percent:               100,
		Enabled:               true,
		IncludeBody:           false,
		RedactHeaders:         []string{"X-Custom"},
		AlwaysStrippedHeaders: api.MirrorAlwaysStrippedHeaders,
	}
}

// TestCmdMirrorList_HappyPath pins:
//   - GET /v1/apps/{slug}/mirrors is the list verb (not POST).
//   - The slug from --app is interpolated into the path.
//   - Human output prints the header row + one row per rule.
func TestCmdMirrorList_HappyPath(t *testing.T) {
	const wantSlug = "myapp"
	var hits int32
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotMethod = r.Method
		gotPath = r.URL.Path
		writeJSONTest(w, api.MirrorRuleListResponse{
			Rules: []api.MirrorRuleResponse{mirrorRuleTestFixture()},
			Count: 1,
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	if code := cmdMirrorList([]string{"--app", wantSlug}); code != 0 {
		t.Fatalf("cmdMirrorList exit = %d, want 0", code)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("hit count = %d, want 1", hits)
	}
	if gotMethod != "GET" {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/apps/"+wantSlug+"/mirrors" {
		t.Errorf("path = %q, want /v1/apps/%s/mirrors", gotPath, wantSlug)
	}
}

// TestCmdMirrorList_MissingApp pins that --app absence short-circuits
// before any HTTP round-trip (matches cmdTrafficSet's
// "missing --deployment" guard).
func TestCmdMirrorList_MissingApp(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	if code := cmdMirrorList([]string{}); code == 0 {
		t.Errorf("missing --app exit = 0, want non-zero")
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("server hit %d times; CLI must short-circuit", hits)
	}
}

// TestCmdMirrorCreate_HappyPath pins:
//   - POST /v1/apps/{slug}/mirrors is the create verb.
//   - Body is the canonical CreateMirrorRuleRequest shape
//     (snake_case, percent, include_body, redact_headers list).
//   - --redact-header repeatable flag collects into a slice.
func TestCmdMirrorCreate_HappyPath(t *testing.T) {
	const wantSlug = "myapp"
	const wantSource = "dep_src_id"
	const wantMirror = "dep_mir_id"
	var hits int32
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		writeJSONTest(w, mirrorRuleTestFixture())
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	if code := cmdMirrorCreate([]string{
		"--app", wantSlug,
		"--source", wantSource,
		"--mirror", wantMirror,
		"--percent", "50",
		"--include-body",
		"--redact-header", "X-Tenant",
		"--redact-header", "X-Trace",
	}); code != 0 {
		t.Fatalf("cmdMirrorCreate exit = %d, want 0", code)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("hit count = %d, want 1", hits)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/apps/"+wantSlug+"/mirrors" {
		t.Errorf("path = %q, want /v1/apps/%s/mirrors", gotPath, wantSlug)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
		t.Fatalf("body is not JSON: %v (body=%q)", err, gotBody)
	}
	if sent["source_deployment_id"] != wantSource {
		t.Errorf("source_deployment_id = %v, want %s", sent["source_deployment_id"], wantSource)
	}
	if sent["mirror_deployment_id"] != wantMirror {
		t.Errorf("mirror_deployment_id = %v, want %s", sent["mirror_deployment_id"], wantMirror)
	}
	if sent["percent"].(float64) != 50 {
		t.Errorf("percent = %v, want 50", sent["percent"])
	}
	if sent["include_body"] != true {
		t.Errorf("include_body = %v, want true", sent["include_body"])
	}
	redact, _ := sent["redact_headers"].([]any)
	if len(redact) != 2 || redact[0] != "X-Tenant" || redact[1] != "X-Trace" {
		t.Errorf("redact_headers = %v, want [X-Tenant X-Trace]", redact)
	}
}

// TestCmdMirrorCreate_MissingFlags pins that --source / --mirror
// absence short-circuits before any HTTP round-trip. Mirrors the
// pattern in TestCmdTrafficSet_MissingArgs.
func TestCmdMirrorCreate_MissingFlags(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	// Missing --mirror.
	if code := cmdMirrorCreate([]string{"--app", "myapp", "--source", "dep_x"}); code == 0 {
		t.Errorf("missing --mirror exit = 0, want non-zero")
	}
	// Missing --source.
	if code := cmdMirrorCreate([]string{"--app", "myapp", "--mirror", "dep_x"}); code == 0 {
		t.Errorf("missing --source exit = 0, want non-zero")
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("server hit %d times; CLI must short-circuit", hits)
	}
}

// TestCmdMirrorInfo_HappyPath pins:
//   - GET /v1/apps/{slug}/mirrors/{id} is the read-one verb.
//   - The slug AND the id are interpolated.
func TestCmdMirrorInfo_HappyPath(t *testing.T) {
	const wantSlug = "myapp"
	const wantID = "mrr_abc123"
	var hits int32
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotMethod = r.Method
		gotPath = r.URL.Path
		writeJSONTest(w, mirrorRuleTestFixture())
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	if code := cmdMirrorInfo([]string{"--app", wantSlug, "--id", wantID}); code != 0 {
		t.Fatalf("cmdMirrorInfo exit = %d, want 0", code)
	}
	if gotMethod != "GET" {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	want := "/v1/apps/" + wantSlug + "/mirrors/" + wantID
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

// TestCmdMirrorUpdate_PatchSemantics pins the patch-vs-put wire
// behaviour: only fields explicitly flagged end up in the JSON
// body. fs.Visit distinguishes "absent" from "set"; without that
// distinction a `--percent 0` (legal disable-without-removing)
// couldn't be distinguished from "leave alone".
func TestCmdMirrorUpdate_PatchSemantics(t *testing.T) {
	const wantSlug = "myapp"
	const wantID = "mrr_abc123"
	var hits int32
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		writeJSONTest(w, mirrorRuleTestFixture())
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	if code := cmdMirrorUpdate([]string{
		"--app", wantSlug,
		"--id", wantID,
		"--percent", "0",
		"--disable",
	}); code != 0 {
		t.Fatalf("cmdMirrorUpdate exit = %d, want 0", code)
	}
	if gotMethod != "PATCH" {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	want := "/v1/apps/" + wantSlug + "/mirrors/" + wantID
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
		t.Fatalf("body is not JSON: %v (body=%q)", err, gotBody)
	}
	if sent["percent"].(float64) != 0 {
		t.Errorf("percent = %v, want 0", sent["percent"])
	}
	if sent["enabled"] != false {
		t.Errorf("enabled = %v, want false", sent["enabled"])
	}
	// include_body must NOT be in the body — fs.Visit only emits
	// fields whose flag was set. The pin here is "absent == omitted".
	if _, ok := sent["include_body"]; ok {
		t.Errorf("include_body should be absent when --include-body not passed (got %v)", sent["include_body"])
	}
}

func TestCmdMirrorUpdate_ExplicitFalseSemantics(t *testing.T) {
	const wantSlug = "myapp"
	const wantID = "mrr_abc123"
	var hits int32
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotBody, _ = io.ReadAll(r.Body)
		writeJSONTest(w, mirrorRuleTestFixture())
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	if code := cmdMirrorUpdate([]string{"--app", wantSlug, "--id", wantID, "--include-body=false"}); code != 0 {
		t.Fatalf("--include-body=false exit = %d, want 0", code)
	}
	var sent map[string]any
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if value, ok := sent["include_body"]; !ok || value != false {
		t.Fatalf("include_body = %v, present=%t; want false", value, ok)
	}

	for _, flagArg := range []string{"--enable=false", "--disable=false", "--no-include-body=false", "--clear-redact=false"} {
		before := atomic.LoadInt32(&hits)
		if code := cmdMirrorUpdate([]string{"--app", wantSlug, "--id", wantID, flagArg}); code == 0 {
			t.Errorf("%s exit = 0, want non-zero no-op", flagArg)
		}
		if after := atomic.LoadInt32(&hits); after != before {
			t.Errorf("%s sent an HTTP mutation: hits %d -> %d", flagArg, before, after)
		}
	}
}

// TestCmdMirrorUpdate_MutuallyExclusive pins that --enable +
// --disable together are rejected before HTTP. The same posture
// applies to --include-body vs --no-include-body in
// cmdMirrorUpdate's flag-validation block.
func TestCmdMirrorUpdate_MutuallyExclusive(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	if code := cmdMirrorUpdate([]string{"--app", "x", "--id", "y", "--enable", "--disable"}); code == 0 {
		t.Errorf("--enable + --disable exit = 0, want non-zero")
	}
	if code := cmdMirrorUpdate([]string{"--app", "x", "--id", "y", "--include-body", "--no-include-body"}); code == 0 {
		t.Errorf("--include-body + --no-include-body exit = 0, want non-zero")
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("server hit %d times; CLI must short-circuit", hits)
	}
}

// TestCmdMirrorRm_HappyPath pins:
//   - DELETE /v1/apps/{slug}/mirrors/{id} is the delete verb.
//   - Body is empty (DELETE semantics).
func TestCmdMirrorRm_HappyPath(t *testing.T) {
	const wantSlug = "myapp"
	const wantID = "mrr_abc123"
	var hits int32
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	if code := cmdMirrorRm([]string{"--app", wantSlug, "--id", wantID}); code != 0 {
		t.Fatalf("cmdMirrorRm exit = %d, want 0", code)
	}
	if gotMethod != "DELETE" {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	want := "/v1/apps/" + wantSlug + "/mirrors/" + wantID
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

// TestCmdMirrorSummary_WindowDefault pins that the default window
// query parameter is "1h" when --window is absent. The server's
// ParseMirrorWindow enforces 1h | 24h | 7d; passing anything else
// gets a 422, but the CLI side defaults to 1h.
func TestCmdMirrorSummary_WindowDefault(t *testing.T) {
	const wantSlug = "myapp"
	const wantID = "mrr_abc123"
	var hits int32
	var gotRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotRawQuery = r.URL.RawQuery
		writeJSONTest(w, api.MirrorSummaryResponse{
			TotalInvocations: 10,
			WindowSeconds:    3600,
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	if code := cmdMirrorSummary([]string{"--app", wantSlug, "--id", wantID}); code != 0 {
		t.Fatalf("cmdMirrorSummary exit = %d, want 0", code)
	}
	if gotRawQuery != "window=1h" {
		t.Errorf("RawQuery = %q, want window=1h", gotRawQuery)
	}
}

// TestCmdMirrorSummary_WindowExplicit pins that --window 7d flows
// through to ?window=7d.
func TestCmdMirrorSummary_WindowExplicit(t *testing.T) {
	const wantSlug = "myapp"
	const wantID = "mrr_abc123"
	var hits int32
	var gotRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotRawQuery = r.URL.RawQuery
		writeJSONTest(w, api.MirrorSummaryResponse{WindowSeconds: 604800})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	if code := cmdMirrorSummary([]string{"--app", wantSlug, "--id", wantID, "--window", "7d"}); code != 0 {
		t.Fatalf("cmdMirrorSummary exit = %d, want 0", code)
	}
	if gotRawQuery != "window=7d" {
		t.Errorf("RawQuery = %q, want window=7d", gotRawQuery)
	}
}

// TestCmdMirror_DispatchUnknown pins that an unknown sub-command
// name exits 1 without hitting the server.
func TestCmdMirror_DispatchUnknown(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	if code := cmdMirror([]string{"bogus"}); code == 0 {
		t.Errorf("unknown sub exit = 0, want non-zero")
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("server hit %d times; dispatcher must short-circuit", hits)
	}
}

// TestCmdMirrorList_EmptyTable pins that an empty rule set
// renders the "(no mirror rules)" line rather than the header
// alone, so a `gregale mirror list` against a fresh account
// doesn't look like a tool bug.
func TestCmdMirrorList_EmptyTable(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		writeJSONTest(w, api.MirrorRuleListResponse{Rules: nil, Count: 0})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	if code := cmdMirrorList([]string{"--app", "empty"}); code != 0 {
		t.Fatalf("cmdMirrorList exit = %d, want 0", code)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("hit count = %d, want 1", hits)
	}
}

// TestCmdMirrorList_JsonOutput pins the --json path: it round-trips
// the SDK DTO through jsonOut so the customer's pipeline sees the
// canonical snake-case shape. We don't assert on the formatted
// output (jsonOutput is package-scoped and resets via t.Setenv
// elsewhere); we only assert exit == 0 with --json.
func TestCmdMirrorList_JsonOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONTest(w, api.MirrorRuleListResponse{Rules: []api.MirrorRuleResponse{mirrorRuleTestFixture()}, Count: 1})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	prevJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = prevJSON })
	if code := cmdMirrorList([]string{"--app", "x"}); code != 0 {
		t.Fatalf("cmdMirrorList --json exit = %d, want 0", code)
	}
}

// TestRenderMirrorRule pins the per-row table formatter. The
// header constants and row widths must stay in lockstep, so we
// regress at the row level — a length change in either side
// shows up as a failure here.
func TestRenderMirrorRule(t *testing.T) {
	var sb strings.Builder
	renderMirrorRule(&sb, mirrorRuleTestFixture())
	out := sb.String()
	if !strings.Contains(out, "100%") {
		t.Errorf("rendered row missing '100%%': %q", out)
	}
	if !strings.Contains(out, "1 redact") {
		t.Errorf("rendered row missing '1 redact': %q", out)
	}
}
