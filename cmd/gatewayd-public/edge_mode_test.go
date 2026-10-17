// adr: 1343
// edge_mode_test.go pins ADR-1343 §2 for a compute-node edge: it routes to
// its own node's gatewayd-internal and uses the compute pool only while that
// gateway is unhealthy, serves app hosts only, takes the client address from
// the local tunnel connector, and forwards credential-bound requests to the
// control plane.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/state"
)

var loopbackCIDRs = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}

func quietEdgeLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// adr: 1343
func TestParsePublicEdgeMode(t *testing.T) {
	for raw, want := range map[string]publicEdgeMode{"": edgeModeControlPlane, " ": edgeModeControlPlane, "compute": edgeModeCompute, " compute ": edgeModeCompute} {
		got, err := parsePublicEdgeMode(raw)
		if err != nil || got != want {
			t.Errorf("parsePublicEdgeMode(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"Compute", "control-plane", "edge"} {
		if _, err := parsePublicEdgeMode(raw); err == nil {
			t.Errorf("parsePublicEdgeMode(%q) accepted an unknown mode", raw)
		}
	}
}

// adr: 1343
func TestPublicEdgeModeRoleGuard(t *testing.T) {
	for _, tc := range []struct {
		mode        publicEdgeMode
		observed    role.Role
		wantAllowed bool
	}{
		{edgeModeControlPlane, role.RoleSingleBox, true},
		{edgeModeControlPlane, role.RoleControlPlane, true},
		{edgeModeControlPlane, role.RoleComputeOnly, false},
		{edgeModeControlPlane, role.Role("invalid"), false},
		{edgeModeCompute, role.RoleSingleBox, true},
		{edgeModeCompute, role.RoleControlPlane, true},
		{edgeModeCompute, role.RoleComputeOnly, true},
		{edgeModeCompute, role.Role("invalid"), false},
	} {
		modeName := string(tc.mode)
		if modeName == "" {
			modeName = "default"
		}
		t.Run(modeName+"/"+string(tc.observed), func(t *testing.T) {
			err := tc.mode.requireRole(tc.observed)
			if (err == nil) != tc.wantAllowed {
				t.Fatalf("requireRole(%q) = %v, want allowed=%v", tc.observed, err, tc.wantAllowed)
			}
		})
	}
}

// adr: 1343
func TestControlPlaneEdgeRefusesComputeRoleBeforeDatabase(t *testing.T) {
	t.Setenv(publicEdgeModeEnv, "")
	t.Setenv("FAAS_GATEWAYD_PUBLIC_ROLE", string(role.RoleComputeOnly))
	previous := gatewaydPublicCapCheck
	gatewaydPublicCapCheck = func() error { return nil }
	t.Cleanup(func() { gatewaydPublicCapCheck = previous })

	err := run(context.Background(), quietEdgeLog())
	var refused *role.ErrRefused
	if !errors.As(err, &refused) || refused.Daemon != "gatewayd-public" || refused.Observed != role.RoleComputeOnly {
		t.Fatalf("run() = %v, want compute-only role refusal before opening the database", err)
	}
}

// adr: 1343
func TestIsAppHost(t *testing.T) {
	for host, want := range map[string]bool{
		"shop.gregale.dev":       true,
		"SHOP.gregale.dev.":      true,
		"shop.gregale.dev:8090":  true,
		"a.shop.gregale.dev":     true,
		"gregale.dev":            false,
		"api.gregale.dev":        false,
		"operations.gregale.dev": false,
		"s3.gregale.dev":         false,
		"127.0.0.1":              false,
		"[::1]:8090":             false,
		"shop.example.com":       false,
		"evilgregale.dev":        false,
		"":                       false,
	} {
		if got := isAppHost(host, "gregale.dev"); got != want {
			t.Errorf("isAppHost(%q) = %v, want %v", host, got, want)
		}
	}
	if isAppHost("shop.gregale.dev", "") {
		t.Error("an empty apps domain admitted a host")
	}
}

// adr: 1343
func TestAppHostsOnlyAnswersOtherHostsWith421(t *testing.T) {
	m := newEdgeMetrics()
	var served bool
	h := appHostsOnly(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = true }), "gregale.dev", m.misdirected)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://shop.gregale.dev/", nil)
	h.ServeHTTP(rec, req)
	if !served || rec.Code != http.StatusOK {
		t.Fatalf("app host: served=%v code=%d", served, rec.Code)
	}

	served = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "http://api.gregale.dev/v1/apps", nil)
	h.ServeHTTP(rec, req)
	if served || rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("platform host: served=%v code=%d, want 421", served, rec.Code)
	}
	var problem api.Problem
	if err := json.NewDecoder(rec.Body).Decode(&problem); err != nil || problem.Code != "misdirected_request" {
		t.Fatalf("problem = %+v, %v", problem, err)
	}
	if got := testutil.ToFloat64(m.misdirected); got != 1 {
		t.Fatalf("misdirected counter = %v, want 1", got)
	}
}

// adr: 1343
func TestCloudflareClientIPFromLocalConnector(t *testing.T) {
	for _, tc := range []struct {
		name, remote, cf, xff, want string
	}{
		{"connector with spoofed list", "127.0.0.1:41000", "203.0.113.7", "198.51.100.9, 203.0.113.7", "203.0.113.7"},
		{"connector, ipv6 client", "127.0.0.1:41000", "2001:db8::7", "", "2001:db8::7"},
		{"untrusted peer keeps its address", "192.0.2.10:41000", "203.0.113.7", "203.0.113.7", "192.0.2.10"},
		{"invalid header ignored", "127.0.0.1:41000", "not-an-ip", "198.51.100.9", "198.51.100.9"},
		{"no header: single forwarded value", "127.0.0.1:41000", "", "198.51.100.9", "198.51.100.9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			h := cloudflareClientIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got, _ = gateway.CanonicalForwardingContext(r, loopbackCIDRs)
			}), loopbackCIDRs)
			req := httptest.NewRequest(http.MethodGet, "http://shop.gregale.dev/", nil)
			req.RemoteAddr = tc.remote
			if tc.cf != "" {
				req.Header.Set("CF-Connecting-IP", tc.cf)
			}
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Fatalf("client IP = %q, want %q", got, tc.want)
			}
		})
	}
}

type dialerFunc func(context.Context, string) (net.Conn, error)

func (f dialerFunc) DialContext(ctx context.Context, target string) (net.Conn, error) {
	return f(ctx, target)
}

// healthServer answers gatewayd-internal's /healthz with status.
func healthServer(t *testing.T, status int) gateway.InternalDialer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != internalGatewayHealthHost || r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return gateway.NewTCPDialer(u.Host)
}

// adr: 1343
func TestLocalFirstDialerUsesThePoolOnlyWhileLocalIsUnhealthy(t *testing.T) {
	target := &url.URL{Scheme: "http", Host: "gatewayd-internal"}
	var poolDials int
	pool := dialerFunc(func(context.Context, string) (net.Conn, error) {
		poolDials++
		c, _ := net.Pipe()
		return c, nil
	})

	m := newEdgeMetrics()
	d := newLocalFirstDialer(healthServer(t, http.StatusServiceUnavailable), pool, target, m, quietEdgeLog())
	d.checkLocal(context.Background())
	if d.healthy.Load() {
		t.Fatal("an unhealthy local gateway was marked healthy")
	}
	if c, err := d.DialContext(context.Background(), target.String()); err != nil || poolDials != 1 {
		t.Fatalf("unhealthy local: err=%v poolDials=%d, want the pool", err, poolDials)
	} else {
		_ = c.Close()
	}

	m = newEdgeMetrics()
	d = newLocalFirstDialer(healthServer(t, http.StatusOK), pool, target, m, quietEdgeLog())
	d.checkLocal(context.Background())
	if !d.healthy.Load() || testutil.ToFloat64(m.localReady) != 1 {
		t.Fatal("a healthy local gateway was not marked healthy")
	}
	poolDials = 0
	c, err := d.DialContext(context.Background(), target.String())
	if err != nil || poolDials != 0 {
		t.Fatalf("healthy local: err=%v poolDials=%d, want the local socket", err, poolDials)
	}
	_ = c.Close()
	if got := testutil.ToFloat64(m.fallbacks); got != 0 {
		t.Fatalf("fallbacks = %v, want 0", got)
	}
}

// adr: 1343
func TestLocalFirstDialerFallsBackWhenLocalRefusesTheDial(t *testing.T) {
	var poolDials int
	pool := dialerFunc(func(context.Context, string) (net.Conn, error) {
		poolDials++
		c, _ := net.Pipe()
		return c, nil
	})
	local := dialerFunc(func(context.Context, string) (net.Conn, error) { return nil, errors.New("connection refused") })
	m := newEdgeMetrics()
	d := newLocalFirstDialer(local, pool, &url.URL{Scheme: "http", Host: "gatewayd-internal"}, m, quietEdgeLog())
	d.healthy.Store(true)
	c, err := d.DialContext(context.Background(), "http://gatewayd-internal")
	if err != nil || poolDials != 1 || testutil.ToFloat64(m.fallbacks) != 1 {
		t.Fatalf("err=%v poolDials=%d fallbacks=%v", err, poolDials, testutil.ToFloat64(m.fallbacks))
	}
	_ = c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.DialContext(ctx, "http://gatewayd-internal"); err == nil || poolDials != 1 {
		t.Fatalf("a cancelled request fell back to the pool: err=%v poolDials=%d", err, poolDials)
	}
}

// adr: 1343
func TestControlPlaneForwarderKeepsHostRequestIDAndClientIP(t *testing.T) {
	type seen struct{ host, xff, proto, requestID, path string }
	got := make(chan seen, 1)
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- seen{r.Host, r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Forwarded-Proto"), r.Header.Get(api.RequestIDHeader), r.URL.Path}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer cp.Close()
	fwd, err := newControlPlaneForwarder(cp.URL, loopbackCIDRs, quietEdgeLog())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://shop.gregale.dev/uploads/avatars", strings.NewReader("x"))
	req.RemoteAddr = "127.0.0.1:41000"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set(api.RequestIDHeader, "req-123")
	rec := httptest.NewRecorder()
	fwd.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}
	s := <-got
	if s.host != "shop.gregale.dev" || s.xff != "203.0.113.7" || s.proto != "https" || s.requestID != "req-123" || s.path != "/uploads/avatars" {
		t.Fatalf("control plane saw %+v", s)
	}
}

// adr: 1343
func TestControlPlaneForwarderFailsClosedWithoutTarget(t *testing.T) {
	fwd, err := newControlPlaneForwarder("", loopbackCIDRs, quietEdgeLog())
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	fwd.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://shop.gregale.dev/v1/otel/v1/traces", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "control_plane_unavailable") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, bad := range []string{"10.0.0.1:8090", "ftp://10.0.0.1", "http://10.0.0.1:8090/api"} {
		if _, err := newControlPlaneForwarder(bad, loopbackCIDRs, quietEdgeLog()); err == nil {
			t.Errorf("target %q accepted", bad)
		}
	}
}

// emptyPublicReadStore has no apps, so no request matches a public bucket.
type emptyPublicReadStore struct{}

func (emptyPublicReadStore) AppBySlug(context.Context, string) (state.App, error) {
	return state.App{}, state.ErrNotFound
}

func (emptyPublicReadStore) AppByID(context.Context, string) (state.App, error) {
	return state.App{}, state.ErrNotFound
}

func (emptyPublicReadStore) DomainByName(context.Context, string) (state.CustomDomain, error) {
	return state.CustomDomain{}, state.ErrNotFound
}

func (emptyPublicReadStore) ListObjectBuckets(context.Context, string, string) ([]state.ObjectBucket, error) {
	return nil, nil
}

// adr: 1343
func TestComputeEdgeRootForwardsUploads(t *testing.T) {
	var hit string
	forward := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = "forward" })
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = "next" })
	root, err := computeEdgeRoot(next, forward, emptyPublicReadStore{}, "gregale.dev", quietEdgeLog())
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"/uploads/avatars":            "forward",
		"/uploads/avatars/receipts/1": "forward",
		"/api/users":                  "next",
	} {
		hit = ""
		root.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "http://shop.gregale.dev"+path, nil))
		if hit != want {
			t.Errorf("%s went to %q, want %q", path, hit, want)
		}
	}
}

func TestRequireComputeEdgeConfig(t *testing.T) {
	if err := requireComputeEdgeConfig(func(string) string { return "" }); err == nil {
		t.Fatal("compute edge started without FAAS_APPS_DOMAIN")
	}
	if err := requireComputeEdgeConfig(func(string) string { return "gregale.dev" }); err != nil {
		t.Fatal(err)
	}
}
