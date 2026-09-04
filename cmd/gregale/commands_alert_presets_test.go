// commands_alert_presets_test.go — CLI tests for
// gregale alerts preset <list|enable> (issue #1233 / ADR-123).
//
// Covers:
//
//   - the dispatcher's "no args" + "unknown subcommand" usage exits
//     (cmdAlertsPreset).
//   - cmdAlertPresetList happy path (no --app required; route hits
//     /v1/alert-presets).
//   - cmdAlertPresetList happy path under --json mode (raw response
//     emitted).
//   - cmdAlertPresetEnable validation gates:
//   - missing --app / missing positional preset name
//   - missing webhook-url / webhook-secret
//   - out-of-band --cooldown-minutes
//     All fire BEFORE the HTTP round-trip (the fake server's hit
//     counter must stay at 0).
//   - cmdAlertPresetEnable happy path (route hits
//     POST /v1/apps/{slug}/alert-presets/{name}/enable; body
//     shape matches the EnableAlertPresetRequest wire contract).
package main

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
)

// TestCmdAlertsPreset_NoArgsIsUsage pins the dispatcher's
// "no args" branch — a bare `gregale alerts preset` returns 1
// with a usage line and never reaches the leaves.
func TestCmdAlertsPreset_NoArgsIsUsage(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdAlertsPreset(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

// TestCmdAlertsPreset_UnknownSubcommandExitsOne pins the dispatcher's
// unknown-subcommand branch — the suggestion helper stays local
// (we don't assert its output, only the exit code).
func TestCmdAlertsPreset_UnknownSubcommandExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdAlertsPreset([]string{"wat"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

// TestCmdAlertPresetList_HappyPath pins the catalog GET route
// (no --app required; the catalog is global). The fake API must
// see GET /v1/alert-presets verbatim.
func TestCmdAlertPresetList_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `[{"id":"0123456789abcdef0123456789abcdef","name":"error_rate_2pct","display_name":"Error rate exceeds 2%","description":"desc","category":"reliability","metric":"error_rate_pct","comparison":"gt","threshold":2,"window_spec":"15m","default_cooldown_minutes":15,"minimum_plan":"hobby","enabled_in_catalog":true}]`
	f := authedFakeAPI(t, body, 200)
	if code := cmdAlertPresetList(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/alert-presets" {
		t.Errorf("route = %s %s, want GET /v1/alert-presets", f.sawMethod, f.sawPath)
	}
}

// TestCmdAlertPresetList_JSONMode pins the --json output path:
// the raw response slice is emitted verbatim (not the table form).
// The body must parse as JSON with the same top-level shape the
// server returned.
func TestCmdAlertPresetList_JSONMode(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = false })

	body := `[{"id":"0123456789abcdef0123456789abcdef","name":"p95_latency_1s","display_name":"p95 exceeds one second","description":"desc","category":"reliability","metric":"latency_p95_ms","comparison":"gt","threshold":1000,"window_spec":"15m","default_cooldown_minutes":15,"minimum_plan":"hobby","enabled_in_catalog":true}]`
	authedFakeAPI(t, body, 200)
	if code := cmdAlertPresetList(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
}

// TestCmdAlertPresetEnable_MissingAppAndName pins the dual
// validation gate — both --app and the positional preset name are
// required; either one missing → exit 1 with no HTTP round-trip.
func TestCmdAlertPresetEnable_MissingAppAndName(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", 200)
	// Missing --app.
	if code := cmdAlertPresetEnable([]string{"--webhook-url", "https://x", "--webhook-secret", "s", "error_rate_2pct"}); code != 1 {
		t.Errorf("missing --app exit = %d, want 1", code)
	}
	// Missing positional name.
	if code := cmdAlertPresetEnable([]string{"--app", "demo", "--webhook-url", "https://x", "--webhook-secret", "s"}); code != 1 {
		t.Errorf("missing preset name exit = %d, want 1", code)
	}
}

// TestCmdAlertPresetEnable_MissingWebhook pins the webhook side:
// both --webhook-url and --webhook-secret are required; either
// missing → exit 1. Both checks fire before the auth round-trip.
func TestCmdAlertPresetEnable_MissingWebhook(t *testing.T) {
	resetJSONOut(t)
	var hits int32
	srv := newFakeAPI(t, "", 200)
	srv.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	})
	// Missing --webhook-url.
	if code := cmdAlertPresetEnable([]string{"--app", "demo", "--webhook-secret", "s", "error_rate_2pct"}); code != 1 {
		t.Errorf("missing --webhook-url exit = %d, want 1", code)
	}
	// Missing --webhook-secret.
	if code := cmdAlertPresetEnable([]string{"--app", "demo", "--webhook-url", "https://x", "error_rate_2pct"}); code != 1 {
		t.Errorf("missing --webhook-secret exit = %d, want 1", code)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("server was hit %d times; CLI must short-circuit before HTTP", got)
	}
}

// TestCmdAlertPresetEnable_BadCooldown pins the cooldown-band
// check: a value outside [AlertRuleCooldownMinMinutes,
// AlertRuleCooldownMaxMinutes] returns exit 1 and never reaches
// the HTTP layer. The 0 sentinel is reserved for "use preset
// default" so it's NOT tested here — see
// TestCmdAlertPresetEnable_HappyPath for the 0→omitted body shape.
func TestCmdAlertPresetEnable_BadCooldown(t *testing.T) {
	resetJSONOut(t)
	var hits int32
	srv := newFakeAPI(t, "", 200)
	srv.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	})
	// Too low (the floor is AlertRuleCooldownMinMinutes=5).
	if code := cmdAlertPresetEnable([]string{"--app", "demo", "--webhook-url", "https://x", "--webhook-secret", "s", "--cooldown-minutes", "1", "error_rate_2pct"}); code != 1 {
		t.Errorf("cooldown=1 exit = %d, want 1", code)
	}
	// Too high (the ceiling is AlertRuleCooldownMaxMinutes=1440).
	if code := cmdAlertPresetEnable([]string{"--app", "demo", "--webhook-url", "https://x", "--webhook-secret", "s", "--cooldown-minutes", "9999", "error_rate_2pct"}); code != 1 {
		t.Errorf("cooldown=9999 exit = %d, want 1", code)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("server was hit %d times; CLI must short-circuit before HTTP", got)
	}
}

// TestCmdAlertPresetEnable_HappyPath pins the round-trip:
// the route POSTs to /v1/apps/{slug}/alert-presets/{name}/enable
// and the body matches the EnableAlertPresetRequest wire shape
// (webhook_url + webhook_secret set, cooldown_minutes omitted
// when --cooldown-minutes=0, enabled defaults to true).
func TestCmdAlertPresetEnable_HappyPath(t *testing.T) {
	resetJSONOut(t)
	respBody := `{"id":"0123456789abcdef0123456789abcdef","name":"Error rate exceeds 2% (demo)","metric":"error_rate_pct","comparison":"gt","threshold":2,"window_spec":"15m","enabled":true,"cooldown_minutes":15}`
	f := authedFakeAPI(t, respBody, 201)
	if code := cmdAlertPresetEnable([]string{
		"--app", "demo",
		"--webhook-url", "https://hooks.example.com/x",
		"--webhook-secret", "shh",
		"error_rate_2pct",
	}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/apps/demo/alert-presets/error_rate_2pct/enable" {
		t.Errorf("route = %s %s, want POST /v1/apps/demo/alert-presets/error_rate_2pct/enable", f.sawMethod, f.sawPath)
	}
	// Body shape: webhook_url + webhook_secret set, no cooldown_minutes
	// when --cooldown-minutes=0 (the 0 sentinel is "use preset
	// default"), enabled defaults to true.
	var got map[string]any
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatalf("body unmarshal: %v", err)
	}
	if got["webhook_url"] != "https://hooks.example.com/x" {
		t.Errorf("body.webhook_url = %v", got["webhook_url"])
	}
	if got["webhook_secret"] != "shh" {
		t.Errorf("body.webhook_secret = %v", got["webhook_secret"])
	}
	if _, present := got["cooldown_minutes"]; present {
		t.Errorf("body.cooldown_minutes present; want omitted (use preset default)")
	}
	if enabled, _ := got["enabled"].(bool); !enabled {
		t.Errorf("body.enabled = %v, want true", enabled)
	}
}

// TestCmdAlertPresetEnable_CooldownOverride pins the
// --cooldown-minutes override path: a non-zero value lands in
// the body's cooldown_minutes field. The validator's [1, 1440]
// band gate is exercised by TestCmdAlertPresetEnable_BadCooldown.
func TestCmdAlertPresetEnable_CooldownOverride(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"0123456789abcdef0123456789abcdef","name":"r","metric":"error_rate_pct","comparison":"gt","threshold":2,"window_spec":"15m","enabled":true,"cooldown_minutes":30}`, 201)
	if code := cmdAlertPresetEnable([]string{
		"--app", "demo",
		"--webhook-url", "https://hooks.example.com/x",
		"--webhook-secret", "shh",
		"--cooldown-minutes", "30",
		"error_rate_2pct",
	}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var got map[string]any
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatalf("body unmarshal: %v", err)
	}
	if cm, _ := got["cooldown_minutes"].(float64); cm != 30 {
		t.Errorf("body.cooldown_minutes = %v, want 30", got["cooldown_minutes"])
	}
}

// TestCmdAlertsPreset_DispatchTable covers the dispatcher paths
// from a single table so future subcommand additions land on the
// right exit code without re-asserting the helper. Only the
// top-level dispatch (no args, unknown subcommand, "list"
// delegation) is exercised here — the leaves have their own
// targeted tests above.
func TestCmdAlertsPreset_DispatchTable(t *testing.T) {
	resetJSONOut(t)
	// Keep this dispatcher table independent of any real developer
	// Keychain entry. The command reaches an authenticated list route
	// for the final case, so an explicit env token is the hermetic
	// credential source for the fake API.
	t.Setenv("FAAS_TOKEN", "test-token")
	cases := []struct {
		args     []string
		wantCode int
		wantHit  bool
	}{
		{nil, 1, false},                 // no args
		{[]string{"unknown"}, 1, false}, // unknown subcommand
		{[]string{"list"}, 0, true},     // happy list delegates to cmdAlertPresetList
	}
	for _, c := range cases {
		var hits int32
		srv := newFakeAPI(t, `[]`, 200)
		srv.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`[]`))
		})
		t.Setenv("FAAS_API", srv.srv.URL)
		got := cmdAlertsPreset(c.args)
		if got != c.wantCode {
			t.Errorf("args=%v exit = %d, want %d", c.args, got, c.wantCode)
		}
		gotHits := atomic.LoadInt32(&hits) > 0
		if gotHits != c.wantHit {
			t.Errorf("args=%v hit=%v, want %v", c.args, gotHits, c.wantHit)
		}
	}
}
