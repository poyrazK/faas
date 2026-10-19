// `gregale apps routes <slug>` CLI tests — ADR-093 Tier B item #2.
//
// Coverage matrix (mirrors commands_tier_b_test.go's leaf-shape
// pattern):
//   - happy path (live source, routes + cap_hit=false)
//   - happy path (live source, routes + cap_hit=true)
//   - empty list (live source, no admitted routes)
//   - source:unavailable (gatewayd-internal dial failed)
//   - missing slug exits 1 (rejects `gregale apps routes`)
//   - not-logged-in exits 1 (auth gate)
//   - unknown app exits 1 (404 from apid → wrapErr → exit 1)
//
// The --json path is asserted by TestCmdAppsRoutes_JSONShape (one
// fixture is enough — the renderer + the JSON marshaller both
// read the same SDK response, so duplicating across fixtures
// would only retest the SDK).
//
// stdout-content assertions use the existing osStdout-seam
// pattern from cli_test.go (assign a *bytes.Buffer, restore
// afterwards). Renderer drift is otherwise covered by the
// existing output_test.go family.

package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// --- happy path: live source with cap_hit=false ---

func TestCmdAppsRoutes_HappyPath(t *testing.T) {
	main.ResetJSONOut(t)
	body := `{"slug":"demo","app_id":"app-uuid-1","routes":["GET /users","POST /orders"],"source":"live","cap_hit":false}`
	f := main.AuthedFakeAPI(t, body, http.StatusOK)
	if code := main.CmdAppsRoutes("demo", nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if (*f.SawMethod()) != "GET" {
		t.Errorf("method = %q, want GET", (*f.SawMethod()))
	}
	if (*f.SawPath()) != "/v1/apps/demo/routes" {
		t.Errorf("path = %q, want /v1/apps/demo/routes", (*f.SawPath()))
	}
}

// --- happy path: live source with cap_hit=true (the load-bearing flag) ---

func TestCmdAppsRoutes_CapHitTrueRendersOverflowHint(t *testing.T) {
	main.ResetJSONOut(t)
	body := `{"slug":"demo","app_id":"app-uuid-1","routes":["GET /users","__route_other__"],"source":"live","cap_hit":true}`
	main.AuthedFakeAPI(t, body, http.StatusOK)
	var buf bytes.Buffer
	prev := (*main.OsStdout)
	(*main.OsStdout) = &buf
	t.Cleanup(func() { (*main.OsStdout) = prev })
	if code := main.CmdAppsRoutes("demo", nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	out := buf.String()
	if !strings.Contains(out, "cap_hit: true") {
		t.Errorf("stdout missing cap_hit:true; got:\n%s", out)
	}
	if !strings.Contains(out, "hit the 50-route cap") {
		t.Errorf("stdout missing overflow hint; got:\n%s", out)
	}
	if !strings.Contains(out, "__route_other__") {
		t.Errorf("stdout missing __route_other__ entry; got:\n%s", out)
	}
}

// --- empty list (live source, no admitted routes) ---

func TestCmdAppsRoutes_EmptyList(t *testing.T) {
	main.ResetJSONOut(t)
	body := `{"slug":"demo","app_id":"app-uuid-1","routes":[],"source":"live","cap_hit":false}`
	f := main.AuthedFakeAPI(t, body, http.StatusOK)
	if code := main.CmdAppsRoutes("demo", nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if (*f.SawPath()) != "/v1/apps/demo/routes" {
		t.Errorf("path = %q, want /v1/apps/demo/routes", (*f.SawPath()))
	}
}

// --- --json output round-trips the wire shape ---

func TestCmdAppsRoutes_JSONShape(t *testing.T) {
	main.ResetJSONOut(t)
	body := `{"slug":"demo","app_id":"app-uuid-1","routes":["GET /users"],"source":"live","cap_hit":true}`
	main.AuthedFakeAPI(t, body, http.StatusOK)
	var buf bytes.Buffer
	prevOut := (*main.OsStdout)
	(*main.OsStdout) = &buf
	t.Cleanup(func() { (*main.OsStdout) = prevOut })
	prevJSON := (*main.JsonOutput)
	(*main.JsonOutput) = true
	t.Cleanup(func() { (*main.JsonOutput) = prevJSON })
	if code := main.CmdAppsRoutes("demo", nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	out := strings.TrimSpace(buf.String())
	var got api.AppRoutesResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode stdout JSON: %v\noutput:\n%s", err, out)
	}
	if got.Slug != "demo" {
		t.Errorf("slug = %q, want demo", got.Slug)
	}
	if !got.CapHit {
		t.Errorf("cap_hit = false, want true (upstream reported cap_hit=true)")
	}
	if got.Source != "live" {
		t.Errorf("source = %q, want live", got.Source)
	}
}

// --- source: unavailable (gatewayd-internal dial failed) ---

func TestCmdAppsRoutes_SourceUnavailable(t *testing.T) {
	main.ResetJSONOut(t)
	body := `{"slug":"demo","routes":[],"source":"unavailable"}`
	main.AuthedFakeAPI(t, body, http.StatusOK)
	var buf bytes.Buffer
	prev := (*main.OsStdout)
	(*main.OsStdout) = &buf
	t.Cleanup(func() { (*main.OsStdout) = prev })
	if code := main.CmdAppsRoutes("demo", nil); code != 0 {
		t.Fatalf("exit = %d, want 0 (unavailable is a renderable state, not an error)", code)
	}
	out := buf.String()
	if !strings.Contains(out, "source: unavailable") {
		t.Errorf("stdout missing source:unavailable; got:\n%s", out)
	}
	if !strings.Contains(out, "cap_hit unknown") {
		t.Errorf("stdout missing cap_hit-unknown hint; got:\n%s", out)
	}
}

// --- arg-validation: missing slug exits 1 ---

func TestCmdAppsRoutes_NoSlugExitsOne(t *testing.T) {
	// Uses exit code 1 (PrintUsage path) — distinct from the
	// auth-error path which uses exit code 2. The two are
	// intentionally separate so a CI script can distinguish
	// "fix your command" from "log in first".
	main.ResetJSONOut(t)
	main.AuthedFakeAPI(t, "", http.StatusOK)
	if code := main.CmdAppsRoutes("", nil); code != 1 {
		t.Fatalf("exit = %d, want 1 (missing slug — usage error, not auth)", code)
	}
}

// --- auth-gate: no FAAS_TOKEN exits with auth-error code (2 per CLI §3.2) ---

func TestCmdAppsRoutes_RequiresLogin(t *testing.T) {
	main.ResetJSONOut(t)
	// Use newFakeAPI (not authedFakeAPI) so FAAS_TOKEN is
	// empty — authedClient() should bail before the round-trip.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "")
	// printErr returns 2 for auth-shaped errors (per CLI §3.2 —
	// "the auth gate is distinct from a request-side error so
	// callers can distinguish 'log in first' from 'the call
	// failed because the token is wrong'"). The memory entry
	// "Middleware AuthLimit shared bucket" cites this exact
	// contract; the tests in commands_tier_b_test.go and
	// commands_mfa_test.go pin it. cmdAppsRoutes inherits the
	// shared printErr → auth-error exit code 2.
	if code := main.CmdAppsRoutes("demo", nil); code != 2 {
		t.Fatalf("exit = %d, want 2 (auth error per CLI §3.2)", code)
	}
}

// --- unknown app: the SDK returns an error which printErr surfaces as exit 1 ---

func TestCmdAppsRoutes_UnknownApp(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.NewFakeAPI(t, `{"code":"not_found","title":"App not found"}`, http.StatusNotFound)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	// newFakeAPI already wires FAAS_API; re-set to be explicit.
	t.Setenv("FAAS_API", (*f.Srv()).URL)
	if code := main.CmdAppsRoutes("ghost-app", nil); code == 0 {
		t.Fatalf("exit = 0, want non-zero (unknown app)")
	}
}
