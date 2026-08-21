// commands_app_static_egress_ip_test.go — ADR-119 CLI unit tests.
// Mirrors the cmdAppSecurity leaf tests
// (commands_tier_d_test.go::TestTierD_AppSecurity_*) which exercise
// the flag-parsing → authed-client → HTTP path end-to-end through
// a fake-API handler. The static-IP leaf is structurally identical
// (slug + sub-verb + args → http.Client), so the same shape covers
// it: usage gate, happy path, and method/route pinning.
package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestTierD_StaticEgressIP_NoSlugExitsOne — empty slug → usage
// error path. Mirrors TestTierD_AppSecurity's empty-slug gate.
func TestTierD_StaticEgressIP_NoSlugExitsOne(t *testing.T) {
	resetJSONOut(t)
	if code := cmdAppStaticEgressIP("", nil); code != 1 {
		t.Fatalf("empty slug: exit = %d, want 1", code)
	}
}

func TestTierD_StaticEgressIP_NoSubVerbExitsOne(t *testing.T) {
	resetJSONOut(t)
	if code := cmdAppStaticEgressIP("demo", nil); code != 1 {
		t.Fatalf("no sub-verb: exit = %d, want 1", code)
	}
}

func TestTierD_StaticEgressIP_UnknownSubVerbExitsOne(t *testing.T) {
	resetJSONOut(t)
	if code := cmdAppStaticEgressIP("demo", []string{"bogus"}); code != 1 {
		t.Fatalf("unknown sub-verb: exit = %d, want 1", code)
	}
}

// TestTierD_StaticEgressIP_ShowHappyPath pins the GET route.
func TestTierD_StaticEgressIP_ShowHappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"ip":"203.0.113.42","set_at":"2026-08-20T12:00:00Z","plan_cap":1}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdAppStaticEgressIP("demo", []string{"show"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/apps/demo/static-egress-ip" {
		t.Errorf("route = %s %s, want GET /v1/apps/demo/static-egress-ip", f.sawMethod, f.sawPath)
	}
}

// TestTierD_StaticEgressIP_SetHappyPath pins the PUT route +
// body shape. The apid handler rejects non-Scale plans with 402,
// so the happy path expects a 200 with the IP echoed back.
func TestTierD_StaticEgressIP_SetHappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"ip":"203.0.113.42","set_at":"2026-08-20T12:00:00Z","plan_cap":1}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdAppStaticEgressIP("demo", []string{"set", "203.0.113.42"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "PUT" || f.sawPath != "/v1/apps/demo/static-egress-ip" {
		t.Errorf("route = %s %s, want PUT /v1/apps/demo/static-egress-ip", f.sawMethod, f.sawPath)
	}
	var got map[string]any
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, string(f.sawBody))
	}
	if got["ip"] != "203.0.113.42" {
		t.Errorf("body.ip = %v, want 203.0.113.42", got["ip"])
	}
}

// TestTierD_StaticEgressIP_SetMissingIPExitsOne — the leaf
// refuses an empty IP argument list. The deny set (RFC1918 etc.)
// is enforced server-side; the CLI only rejects "no argument
// supplied" so the round-trip isn't wasted.
func TestTierD_StaticEgressIP_SetMissingIPExitsOne(t *testing.T) {
	resetJSONOut(t)
	if code := cmdAppStaticEgressIP("demo", []string{"set"}); code != 1 {
		t.Fatalf("no IP: exit = %d, want 1", code)
	}
}

// TestTierD_StaticEgressIP_ClearHappyPath pins the DELETE route.
func TestTierD_StaticEgressIP_ClearHappyPath(t *testing.T) {
	resetJSONOut(t)
	body := ``
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdAppStaticEgressIP("demo", []string{"clear"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "DELETE" || f.sawPath != "/v1/apps/demo/static-egress-ip" {
		t.Errorf("route = %s %s, want DELETE /v1/apps/demo/static-egress-ip", f.sawMethod, f.sawPath)
	}
}
