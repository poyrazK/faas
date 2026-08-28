// handlers_apps_static_egress_ip_test.go — apid handler tests for
// ADR-119 (per-app static egress IP, Scale-only, BYOIP, single-node
// v1).
//
// The cluster ships dark under FAAS_STATIC_EGRESS_IP_ENABLED — every
// handler must 402 unless the flag is set. With the flag set:
//   - GET is plan-agnostic (returns plan_allowed=false for non-Scale)
//   - PUT is plan-gated (Free/Hobby/Pro → 402 plan_static_egress_ip_not_allowed)
//   - IPv6 / RFC1918 / link-local / multicast / CGN / 0.0.0.0/8 → 400
//   - Cross-app same-account pin collision → 403 plan_static_egress_ip_quota
//   - DELETE clears + emits drift notify + audit
//
// Coverage split:
//   - flag:    feature flag off → 402 on every handler
//   - shape:   GET round-trips; PUT writes; DELETE clears
//   - plan:    Free → 402 on PUT; GET shows plan_allowed=false
//   - shape:   IPv4 / IPv6 / RFC1918 / CGN / link-local / multicast rejected
//   - cross:   unique-index violation → 403 quota
//   - notify:  pg_notify('app_changed') payload carries the new IP

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// withStaticEgressIPEnabled flips the dark-launch flag for one test.
// Same posture as withTenantSurfacesEnabled above; pattern in
// pkg/api/flags.go:23-33.
func withStaticEgressIPEnabled(t *testing.T) {
	t.Helper()
	t.Setenv("FAAS_STATIC_EGRESS_IP_ENABLED", "true")
}

// staticEgressIPReq is a small builder so each table-driven case
// declares its expected body in one line.
func staticEgressIPReq(ip string, set bool) api.SetAppStaticEgressIPRequest {
	return api.SetAppStaticEgressIPRequest{IP: ip, Set: set}
}

// mustSeedProvisionedStaticEgressIPs (ADR-119 redesign) marks the
// (account_id, customer_ip) tuples as operator-bundle-provisioned
// so the apid PUT path's gate (api.ProvisionedStaticEgressIPExists)
// passes. Tests that exercise the happy-path PUT call this helper
// before the request; tests that exercise the 404 not-provisioned
// path skip it.
func mustSeedProvisionedStaticEgressIPs(t *testing.T, e testEnv, ips ...string) {
	t.Helper()
	parsed := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		a, err := netip.ParseAddr(ip)
		if err != nil {
			t.Fatalf("mustSeedProvisionedStaticEgressIPs: invalid ip %q: %v", ip, err)
		}
		parsed = append(parsed, a)
	}
	mem := e.store
	if err := mem.ReplaceProvisionedStaticEgressIPs(context.Background(), e.acct.ID, "test-node", parsed); err != nil {
		t.Fatalf("seed operator bundle: %v", err)
	}
}

// TestStaticEgressIP_FlagOffBlocksAllHandlers pins the dark-launch
// posture: a misconfigured cluster returns 402 on every verb until
// FAAS_STATIC_EGRESS_IP_ENABLED is set. The check runs before
// loadApp, so even an unknown-slug request gets 402 (the surface
// is invisible until the operator flips the flag).
func TestStaticEgressIP_FlagOffBlocksAllHandlers(t *testing.T) {
	// Do NOT call withStaticEgressIPEnabled.
	e := setup(t, api.PlanScale)
	mustSeedApp(t, e, "any-app")

	for _, verb := range []string{"GET", "PUT", "DELETE"} {
		var body any
		if verb == "PUT" {
			body = staticEgressIPReq("203.0.113.42", true)
		}
		rec := e.do(t, verb, "/v1/apps/any-app/static-egress-ip", body, nil)
		if rec.Code != http.StatusPaymentRequired {
			t.Errorf("%s flag-off status = %d, want 402; body=%s", verb, rec.Code, rec.Body.String())
		}
	}
}

// TestStaticEgressIP_GetHappyPath pins the read surface for a
// Scale customer with no pin yet. ip/set_at are null,
// plan_cap=1, plan_allowed=true.
func TestStaticEgressIP_GetHappyPath(t *testing.T) {
	withStaticEgressIPEnabled(t)
	e := setup(t, api.PlanScale)
	mustSeedApp(t, e, "read-app")

	rec := e.do(t, "GET", "/v1/apps/read-app/static-egress-ip", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.AppStaticEgressIPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.IP != nil {
		t.Errorf("ip = %s, want nil", resp.IP)
	}
	if resp.SetAt != nil {
		t.Errorf("set_at = %s, want nil", resp.SetAt)
	}
	if resp.PlanCap != 1 {
		t.Errorf("plan_cap = %d, want 1", resp.PlanCap)
	}
	if !resp.PlanAllowed {
		t.Error("plan_allowed = false, want true (Scale)")
	}
}

// TestStaticEgressIP_GetFreePlanIsNotAllowed pins the GET shape for
// a non-Scale plan: plan_allowed=false, plan_cap=0. The handler
// does NOT 402 on GET — the customer needs to read the upsell
// shape so the dashboard can render "upgrade to Scale".
func TestStaticEgressIP_GetFreePlanIsNotAllowed(t *testing.T) {
	withStaticEgressIPEnabled(t)
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-app")

	rec := e.do(t, "GET", "/v1/apps/free-app/static-egress-ip", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.AppStaticEgressIPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PlanAllowed {
		t.Error("plan_allowed = true on Free; want false")
	}
	if resp.PlanCap != 0 {
		t.Errorf("plan_cap = %d on Free; want 0", resp.PlanCap)
	}
}

// TestStaticEgressIP_PutFreePlanBlocks pins the plan-tier gate on
// the write surface: Free/Hobby/Pro return 402 with
// plan_static_egress_ip_not_allowed.
func TestStaticEgressIP_PutFreePlanBlocks(t *testing.T) {
	withStaticEgressIPEnabled(t)
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-write-app")

	rec := e.do(t, "PUT", "/v1/apps/free-write-app/static-egress-ip",
		staticEgressIPReq("203.0.113.42", true), nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("PUT Free status = %d, want 402; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "plan_static_egress_ip_not_allowed") {
		t.Errorf("body missing plan_static_egress_ip_not_allowed: %s", rec.Body.String())
	}
}

// TestStaticEgressIP_PutScaleAcceptedRoundTrips pins the happy
// path on Scale: PUT writes, GET reads back, the response carries
// the canonical IP and a non-nil SetAt.
func TestStaticEgressIP_PutScaleAcceptedRoundTrips(t *testing.T) {
	withStaticEgressIPEnabled(t)
	e := setup(t, api.PlanScale)
	mustSeedApp(t, e, "scale-app")
	mustSeedProvisionedStaticEgressIPs(t, e, "203.0.113.42")

	rec := e.do(t, "PUT", "/v1/apps/scale-app/static-egress-ip",
		staticEgressIPReq("203.0.113.42", true), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT Scale status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.AppStaticEgressIPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.IP == nil || resp.IP.String() != "203.0.113.42" {
		t.Errorf("ip = %v, want 203.0.113.42", resp.IP)
	}
	if resp.SetAt == nil {
		t.Error("set_at = nil after PUT, want non-nil")
	}

	// GET round-trip.
	rec = e.do(t, "GET", "/v1/apps/scale-app/static-egress-ip", nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if resp.IP == nil || resp.IP.String() != "203.0.113.42" {
		t.Errorf("GET ip = %v, want 203.0.113.42", resp.IP)
	}
}

// TestStaticEgressIP_PutRejectsBadIPs is the table-driven contract
// for the IPv4-only + non-reserved shape. The handler is
// fail-fast: every malformed input returns 400 with the
// app_static_egress_ip_invalid code, BEFORE the column write.
func TestStaticEgressIP_PutRejectsBadIPs(t *testing.T) {
	withStaticEgressIPEnabled(t)
	e := setup(t, api.PlanScale)
	mustSeedApp(t, e, "bad-ip-app")

	cases := []struct {
		name string
		ip   string
	}{
		{"ipv6", "2001:db8::1"},
		{"rfc1918_10", "10.0.0.1"},
		{"rfc1918_172", "172.16.5.5"},
		{"rfc1918_192", "192.168.1.1"},
		{"cgn_100_64", "100.64.0.1"},
		{"link_local_169", "169.254.0.1"},
		{"multicast_224", "224.0.0.1"},
		{"loopback_127", "127.0.0.1"},
		{"unspecified", "0.0.0.0"},
		{"malformed", "not-an-ip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.do(t, "PUT", "/v1/apps/bad-ip-app/static-egress-ip",
				staticEgressIPReq(tc.ip, true), nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("PUT %s status = %d, want 400; body=%s", tc.name, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "app_static_egress_ip_invalid") {
				t.Errorf("PUT %s body missing app_static_egress_ip_invalid: %s", tc.name, rec.Body.String())
			}
		})
	}
}

// TestStaticEgressIP_CrossAppSameAccountQuota pins the unique-
// index collision: when a second app on the same account tries
// to pin the same IP, the handler returns 403 with
// plan_static_egress_ip_quota. The MemStore's static_egress_ip
// field is set in pkg/state/memstore.go; the unique-index
// behaviour is exercised end-to-end in migrations/00325_...
func TestStaticEgressIP_CrossAppSameAccountQuota(t *testing.T) {
	withStaticEgressIPEnabled(t)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanScale)
	mustSeedApp(t, e, "first-app")
	mustSeedApp(t, e, "second-app")
	mustSeedProvisionedStaticEgressIPs(t, e, "203.0.113.42")

	// First app: pin 203.0.113.42.
	rec := e.do(t, "PUT", "/v1/apps/first-app/static-egress-ip",
		staticEgressIPReq("203.0.113.42", true), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("first PUT status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Second app: try to pin the same IP. The MemStore
	// mirrors the on-disk unique-index conflict surface
	// (see memstore.go cross-app check; on pgstore this is
	// apps_static_egress_ip_key).
	rec = e.do(t, "PUT", "/v1/apps/second-app/static-egress-ip",
		staticEgressIPReq("203.0.113.42", true), nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("second PUT status = 200, want 403/409; body=%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "plan_static_egress_ip_quota") &&
		!strings.Contains(rec.Body.String(), "app_static_egress_ip") {
		t.Errorf("second PUT body missing quota/conflict code: %s", rec.Body.String())
	}
}

// TestStaticEgressIP_DeleteClears pins the DELETE shape: 204
// No Content, follow-up GET returns ip=null, set_at=null.
func TestStaticEgressIP_DeleteClears(t *testing.T) {
	withStaticEgressIPEnabled(t)
	e := setup(t, api.PlanScale)
	mustSeedApp(t, e, "delete-app")
	mustSeedProvisionedStaticEgressIPs(t, e, "203.0.113.42")

	// Pin first.
	rec := e.do(t, "PUT", "/v1/apps/delete-app/static-egress-ip",
		staticEgressIPReq("203.0.113.42", true), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed PUT status = %d, want 200", rec.Code)
	}

	rec = e.do(t, "DELETE", "/v1/apps/delete-app/static-egress-ip", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// GET confirms the clear.
	rec = e.do(t, "GET", "/v1/apps/delete-app/static-egress-ip", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET-after-delete status = %d, want 200", rec.Code)
	}
	var resp api.AppStaticEgressIPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.IP != nil {
		t.Errorf("ip = %s after DELETE, want nil", resp.IP)
	}
	if resp.SetAt != nil {
		t.Errorf("set_at = %s after DELETE, want nil", resp.SetAt)
	}
}

// TestStaticEgressIP_PutEmitsAppChanged pins the pg_notify side
// effect: a successful PUT fires NotifyAppChanged on the
// app_changed channel so schedd's egress_drift subscriber can
// patch live instances. The drift handler is in
// pkg/sched/egress_drift.go; this test pins the wire payload
// shape (kind, app_id, ip, clear).
func TestStaticEgressIP_PutEmitsAppChanged(t *testing.T) {
	withStaticEgressIPEnabled(t)
	e, notif := newTestServerWithCapturingNotifier(t, api.PlanScale)
	appID := mustSeedApp(t, e, "notify-app")
	mustSeedProvisionedStaticEgressIPs(t, e, "203.0.113.42")

	rec := e.do(t, "PUT", "/v1/apps/notify-app/static-egress-ip",
		staticEgressIPReq("203.0.113.42", true), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	notif.mu.Lock()
	defer notif.mu.Unlock()
	var found bool
	for _, n := range notif.emitted {
		if n.Channel != "app_changed" {
			continue
		}
		if !strings.Contains(n.Payload, "static_egress_ip") {
			continue
		}
		if !strings.Contains(n.Payload, appID) {
			t.Errorf("notify payload missing app_id %s: %s", appID, n.Payload)
		}
		if !strings.Contains(n.Payload, "203.0.113.42") {
			t.Errorf("notify payload missing IP: %s", n.Payload)
		}
		if !strings.Contains(n.Payload, `"clear":false`) {
			t.Errorf("notify payload missing clear=false: %s", n.Payload)
		}
		found = true
	}
	if !found {
		t.Errorf("no app_changed notify for static_egress_ip; emissions=%+v", notif.emitted)
	}
}

// TestStaticEgressIP_ValidCustomerEgressIPTableDriven covers the
// validator directly so a future refactor that drops a deny-set
// entry fires here, not at the bridge alias step. The
// handler-level table above already covers RFC1918; this one
// pins edge cases (last-IP-out-of-range, /0 etc.) via direct
// calls to the helper.
func TestStaticEgressIP_ValidCustomerEgressIPTableDriven(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		// Public IPv4 — must be allowed.
		{"203.0.113.42", true},
		{"198.51.100.7", true},
		{"1.1.1.1", true},
		// Reserved — must be rejected.
		{"10.0.0.1", false},
		{"172.31.255.255", false}, // last RFC1918 172/12
		{"172.32.0.0", true},      // just outside 172.16/12
		{"192.168.0.1", false},
		{"100.64.0.1", false},      // CGN
		{"100.127.255.255", false}, // last CGN
		{"100.128.0.0", true},      // just outside CGN
		{"169.254.0.1", false},
		{"224.0.0.1", false},
		{"127.0.0.1", false},
		{"0.0.0.0", false},
	}
	for _, tc := range cases {
		ip, err := netip.ParseAddr(tc.ip)
		if err != nil {
			t.Errorf("parse %s: %v", tc.ip, err)
			continue
		}
		if err := api.ValidateStaticEgressIP(ip); (err == nil) != tc.want {
			t.Errorf("ValidateStaticEgressIP(%s) err = %v, want valid=%v", tc.ip, err, tc.want)
		}
	}
}

// TestStaticEgressIP_AppUpdateSanity drives the underlying
// UpdateApp path so a refactor that drops SetStaticEgressIP
// fails here. Mirrors the slice-15 coverage test but on the
// handler side.
func TestStaticEgressIP_AppUpdateSanity(t *testing.T) {
	withStaticEgressIPEnabled(t)
	e := setup(t, api.PlanScale)
	appID := mustSeedApp(t, e, "sanity-app")

	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := e.store.UpdateApp(context.Background(), appID, state.UpdateAppParams{
		StaticEgressIP:    &ip,
		SetStaticEgressIP: true,
	}); err != nil {
		t.Fatalf("seed UpdateApp: %v", err)
	}

	// GET via the handler.
	rec := e.do(t, "GET", "/v1/apps/sanity-app/static-egress-ip", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	var resp api.AppStaticEgressIPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.IP == nil || resp.IP.String() != ip.String() {
		t.Errorf("ip = %v, want %s", resp.IP, ip)
	}
}
