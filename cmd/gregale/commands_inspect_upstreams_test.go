// Tests for `gregale inspect <slug> --upstreams` (issue #952,
// ADR-098 §9.A cluster follow-up). Mirrors commands_crons_info_test.go:
// httptest.NewServer fake + t.Setenv + osStdout swap + jsonOutput
// swap. The dispatch-from-main test lives at the bottom
// (TestRun_DispatchInspectUpstreams) so the run() switch routing
// is locked alongside the leaf's behaviour.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// inspectSlug is the only valid 3..40-char kebab slug the tests
// pass through. Stays inside the validCLISlug shape (commands5.go:51)
// so the local validator accepts and the request reaches the server.
const inspectSlug = "myapp"

// quotaCap is the plan's DataPlacementHintsPerApp the fake server
// stamps on the wrapper. Mirrors the apid-side constant at
// handlers_upstreams.go:112 (limits.DataPlacementHintsPerApp).
const quotaCap = 8

// makeUpstreamRow builds one DataUpstreamResponse with all fields
// populated. LastRTTMs is a pointer so we can pin the optional
// wire shape (json tag omitempty).
func makeUpstreamRow(id, kind, scope, last4 string, port int, rtt *int, probed string) api.DataUpstreamResponse {
	return api.DataUpstreamResponse{
		ID:               id,
		Source:           api.DataUpstreamSource("inferred"),
		Kind:             api.DataUpstreamKind(kind),
		HostRedactedHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HostLast4:        last4,
		Port:             port,
		Scope:            scope,
		LastRTTMs:        rtt,
		LastProbedAt:     probed,
		CreatedAt:        "2026-08-18T12:34:56Z",
		LastSeenAt:       "2026-08-18T12:34:56Z",
	}
}

// encodeUpstreamsList is the helper that stamps the wrapper the
// fake server returns. Mirrors handlers_upstreams.go:110-114.
func encodeUpstreamsList(t *testing.T, rows []api.DataUpstreamResponse) []byte {
	t.Helper()
	wrapper := api.DataUpstreamListResponse{
		Upstreams: rows,
		Quota:     quotaCap,
		Count:     len(rows),
	}
	b, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatalf("encode upstreams: %v", err)
	}
	return b
}

// --- happy path -----------------------------------------------------------

func TestCmdInspectUpstreams_HappyPath(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		rtt := 12
		rows := []api.DataUpstreamResponse{
			makeUpstreamRow("11111111-1111-1111-1111-111111111111", "postgres", "primary", "a1b2c3d4", 5432, &rtt, "2026-08-18T12:34:56Z"),
			makeUpstreamRow("22222222-2222-2222-2222-222222222222", "redis", "cache", "c3d4e5f6", 6379, nil, ""),
			makeUpstreamRow("33333333-3333-3333-3333-333333333333", "minio", "primary", "e5f6a7b8", 9000, &rtt, "2026-08-18T12:00:00Z"),
		}
		_, _ = w.Write(encodeUpstreamsList(t, rows))
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdInspect([]string{inspectSlug, "--upstreams"}); code != 0 {
		t.Fatalf("inspect upstreams = %d, want 0 (stderr=%s)", code, readStderr())
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/apps/"+inspectSlug+"/upstreams" {
		t.Errorf("path = %q, want /v1/apps/<slug>/upstreams", gotPath)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want empty (no --scope)", gotQuery)
	}
	body := stdout.String()
	// Quota stamp + header row + one row per upstream.
	for _, want := range []string{
		inspectSlug + ": 3/8 upstreams",
		"KIND", "SCOPE", "HOST_LAST4", "PORT", "SOURCE", "LAST_RTT_MS", "LAST_PROBED_AT",
		"postgres", "primary", "a1b2c3d4", "5432", "inferred", "12", "2026-08-18T12:34:56Z",
		"redis", "cache", "c3d4e5f6", "6379",
		"minio", "primary", "e5f6a7b8", "9000",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; got:\n%s", want, body)
		}
	}
	// The redis row has no RTT probe — the em-dash sentinel must surface.
	if !strings.Contains(body, "—") {
		t.Errorf("em-dash sentinel missing for unprobed rows; got:\n%s", body)
	}
}

// --- empty list -----------------------------------------------------------

func TestCmdInspectUpstreams_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(encodeUpstreamsList(t, nil))
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdInspect([]string{inspectSlug, "--upstreams"}); code != 0 {
		t.Fatalf("inspect upstreams (empty) = %d, want 0 (stderr=%s)", code, readStderr())
	}
	body := stdout.String()
	want := inspectSlug + ": no upstreams (0/8)\n"
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

// --- JSON output ----------------------------------------------------------

func TestCmdInspectUpstreams_JSON_Envelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rtt := 12
		rows := []api.DataUpstreamResponse{
			makeUpstreamRow("11111111-1111-1111-1111-111111111111", "postgres", "primary", "a1b2c3d4", 5432, &rtt, "2026-08-18T12:34:56Z"),
		}
		_, _ = w.Write(encodeUpstreamsList(t, rows))
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	stdout, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdInspect([]string{inspectSlug, "--upstreams"}); code != 0 {
		t.Fatalf("inspect upstreams (json) = %d, want 0 (stderr=%s)", code, readStderr())
	}
	var env struct {
		Upstreams []api.DataUpstreamResponse `json:"upstreams"`
		Count     int                        `json:"count"`
		Quota     int                        `json:"quota_max"`
		Scope     string                     `json:"scope"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("JSON envelope parse: %v\nraw: %s", err, stdout.String())
	}
	if env.Count != 1 {
		t.Errorf("count = %d, want 1", env.Count)
	}
	if env.Quota != quotaCap {
		t.Errorf("quota_max = %d, want %d", env.Quota, quotaCap)
	}
	if env.Scope != "" {
		t.Errorf("scope = %q, want empty (no --scope flag)", env.Scope)
	}
	if len(env.Upstreams) != 1 {
		t.Fatalf("upstreams = %d, want 1", len(env.Upstreams))
	}
	if env.Upstreams[0].Kind != api.DataUpstreamKindPostgres ||
		env.Upstreams[0].Scope != "primary" ||
		env.Upstreams[0].HostLast4 != "a1b2c3d4" ||
		env.Upstreams[0].Port != 5432 {
		t.Errorf("upstreams[0] drift: %+v", env.Upstreams[0])
	}
}

// --- scope forwarding -----------------------------------------------------

func TestCmdInspectUpstreams_ScopeFilter_Forwarded(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write(encodeUpstreamsList(t, nil))
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdInspect([]string{inspectSlug, "--upstreams", "--scope=primary"}); code != 0 {
		t.Fatalf("inspect upstreams (scope) = %d, want 0 (stderr=%s)", code, readStderr())
	}
	if gotQuery != "scope=primary" {
		t.Errorf("query = %q, want scope=primary", gotQuery)
	}
	// Empty list body is the right stamp regardless of scope.
	if !strings.Contains(stdout.String(), "no upstreams (0/8)") {
		t.Errorf("body missing empty stamp; got:\n%s", stdout.String())
	}
}

// --- plan-feature-gated 403 ----------------------------------------------

// TestCmdInspectUpstreams_PlanFeatureGated_403 verifies the
// server-side plan gate (handlers_upstreams.go:55-58) surfaces
// verbatim as a non-zero CLI exit with the SDK's RFC 7807
// problem reaching the operator. The handler returns 403
// + api.ErrPlanFeatureGated when FAAS_DATA_PLACEMENT is OFF
// or the plan doesn't allow data placement. The CLI never
// invents a local branch — the SDK error round-trips through
// printErr/renderAPIError (commands.go:495-540), which prints
// the problem's Title + Detail + docs row verbatim.
func TestCmdInspectUpstreams_PlanFeatureGated_403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(api.Problem{
			Type:   "about:blank",
			Status: 403,
			Title:  "plan_feature_gated",
			Detail: "data_upstreams is not enabled on your plan",
			Code:   "plan_feature_gated",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	_, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdInspect([]string{inspectSlug, "--upstreams"}); code == 0 {
		t.Errorf("inspect upstreams (gated) = 0, want nonzero")
	}
	// renderAPIError prints the problem Title + Detail. Pin both
	// so a future drift in renderAPIError trips this test.
	stderr := readStderr()
	if !strings.Contains(stderr, "plan_feature_gated") {
		t.Errorf("stderr missing problem code; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "data_upstreams is not enabled on your plan") {
		t.Errorf("stderr missing problem detail; got:\n%s", stderr)
	}
}

// --- client-side rejections (no server call) -----------------------------

// TestCmdInspectUpstreams_BadSlug_NoServerCall confirms the local
// slug regex (validCLISlug, commands5.go:51) rejects before any
// network round-trip. Mirrors TestCmdCronsInfo_BadID_NoServerCall
// (commands_crons_info_test.go:153-169). The slug "Ab" fails on
// two counts — too short (<3) and contains an uppercase letter
// (lowercase alnum only).
func TestCmdInspectUpstreams_BadSlug_NoServerCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdInspect([]string{"Ab", "--upstreams"}); code == 0 {
		t.Errorf("inspect upstreams (bad slug) = 0, want nonzero")
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0 (local validation only)", calls)
	}
}

// TestCmdInspectUpstreams_NoLeafFlag asserts that a bare
// `gregale inspect <slug>` (no --upstreams) exits 1 with the
// usage line and zero server calls. The verb without a leaf
// flag is ambiguous today; future leaves (--env, --crons) will
// add their own gates here.
func TestCmdInspectUpstreams_NoLeafFlag(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	_, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdInspect([]string{inspectSlug}); code == 0 {
		t.Errorf("inspect (no leaf) = 0, want nonzero")
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0", calls)
	}
	if !strings.Contains(readStderr(), "usage: gregale inspect") {
		t.Errorf("stderr missing usage line; got:\n%s", readStderr())
	}
}

// --- dispatch -------------------------------------------------------------

// TestRun_DispatchInspectUpstreams asserts the main run() switch
// routes `inspect <slug> --upstreams` into cmdInspect rather than
// falling through to the default branch. The CLI is the operator's
// first stop for "why is schedd placing my app there?" — a
// miss-routed dispatch would silently no-op with no diagnostic.
func TestRun_DispatchInspectUpstreams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/"+inspectSlug+"/upstreams" {
			http.Error(w, "no", 404)
			return
		}
		_, _ = w.Write(encodeUpstreamsList(t, nil))
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := run([]string{"inspect", inspectSlug, "--upstreams"}); code != 0 {
		t.Errorf("run inspect upstreams = %d, want 0", code)
	}
}
