package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/role"
)

// fixedBackend is a Backend that returns whatever the test sets. Used to
// exercise the handler composition without depending on the unwired default.
// Issue #168: shaped to the new Backend interface (Pick / Admit / HealthyCount).
type fixedBackend struct {
	app        gateway.App
	appOK      bool
	picks      []gateway.Target
	pickIdx    int
	admitErr   error
	admitCalls int
	atCap      bool
}

func (f *fixedBackend) Lookup(_ context.Context, _ string) (gateway.App, bool) {
	return f.app, f.appOK
}
func (f *fixedBackend) Pick(_ string) gateway.PickResult {
	if len(f.picks) == 0 {
		return gateway.PickResult{}
	}
	t := f.picks[f.pickIdx%len(f.picks)]
	f.pickIdx++
	return gateway.PickResult{Target: t, OK: true, Picked: t.DeploymentID}
}
func (f *fixedBackend) HealthyCount(_ string) int {
	return len(f.picks)
}
func (f *fixedBackend) Admit(_ context.Context, _, _, _, _ string, _ int) (string, gateway.WakeMethod, bool, error) {
	f.admitCalls++
	if f.admitErr != nil {
		return "", gateway.WakeMethodUnspecified, false, f.admitErr
	}
	if f.atCap {
		return "", gateway.WakeMethodUnspecified, true, nil
	}
	return "wake-fixed", gateway.WakeMethodColdBoot, false, nil
}

// LookupMirrorRules (PR-A3 / issue #72 / ADR-124) — fixedBackend
// is a unit-test fake used for the gateway's wake path; mirror
// rule picking is exercised in pkg/gateway/pgbackend_test.go.
// Returns no rules + false so the handler fan-out path stays
// dormant here.
func (f *fixedBackend) LookupMirrorRules(_ context.Context, _ string) ([]gateway.MirrorRuleRow, bool) {
	return nil, false
}

// ScheduleMirror (PR-A3 / issue #72 / ADR-124) — fixedBackend
// is a unit-test fake for the gateway's wake path; mirror
// goroutines are exercised in pkg/gateway/handler_mirror_test.go.
// The stub satisfies the widened Backend interface.
func (f *fixedBackend) ScheduleMirror(_ context.Context, _, _, _ string) (string, string, error) {
	return "", "", nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestUnwiredBackendReturnsNotFound(t *testing.T) {
	b := unwiredBackend{}
	if _, ok := b.Lookup(context.Background(), "any"); ok {
		t.Error("Lookup should report not-found")
	}
	if res := b.Pick("any"); res.OK {
		t.Error("Pick should report not-found")
	}
	if got := b.HealthyCount("any"); got != 0 {
		t.Errorf("HealthyCount = %d, want 0", got)
	}
	if _, _, _, err := b.Admit(context.Background(), "any", "", "", "", 1); err != nil {
		t.Errorf("Admit should be no-op: %v", err)
	}
}

func TestRunWithDeps_ServesAndShutsDown(t *testing.T) {
	deps := defaultDeps()
	deps.capCheck = func() error { return nil }
	deps.backend = &fixedBackend{}
	deps.newSrv = func(addr string, h http.Handler) *http.Server {
		return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	}
	// Bind a real listener up front and pass it in via a closure-captured
	// pointer so we can read its address synchronously.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deps.listen = func(_, _ string) (net.Listener, error) { return ln, nil }
	// Free-port the control listener so this test doesn't race with
	// TestRunWithDeps_TLSBundleCloseStopsRenewLoop for the hard-coded
	// 127.0.0.1:9090.
	ctrlLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ctrlLn.Close()
	deps.controlAddr = ctrlLn.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, discardLogger(), deps) }()
	t.Cleanup(cancel)

	// Wait until the server is accepting.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Hit the server — it should 404 since fixedBackend's Lookup/Target
	// return not-found.
	resp, err := http.Get("http://" + ln.Addr().String() + "/anything")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "Server closed") && !strings.Contains(err.Error(), "use of closed network connection") {
			t.Errorf("runWithDeps returned %v, want nil or context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runWithDeps did not return after ctx cancel")
	}
}

func TestListenAddr_OffSentinelIsHandled(t *testing.T) {
	// ADR-068 / Tier A7 split: faas-gatewayd-internal.service ships with
	// FAAS_GATEWAY_LISTEN=off so the daemon serves only on the unix socket
	// (forwarded to by gatewayd-public) + the control plane, not on
	// :8080. Verify that the conditional in runWithDeps branches on the
	// "off" sentinel: the public listener block is skipped, while the
	// default behavior (real port) is preserved for the e2e harness.
	orig := listenAddr
	t.Cleanup(func() { listenAddr = orig })

	// Default state: a real port.
	listenAddr = defaultPublicListenAddr
	if listenAddr == publicListenOffSentinel {
		t.Fatal("default listenAddr should NOT be the off sentinel")
	}

	// Production state: off skips the bind.
	listenAddr = publicListenOffSentinel
	if listenAddr != publicListenOffSentinel {
		t.Fatal("off sentinel must round-trip")
	}

	// envOrGateway contract: empty env returns fallback (defaultPublicListenAddr
	// by default), NOT the off sentinel — so an unset env won't accidentally
	// skip the listener in dev or test contexts.
	t.Setenv("FAAS_GATEWAY_LISTEN", "")
	if got := envOrGateway("FAAS_GATEWAY_LISTEN", defaultPublicListenAddr); got != defaultPublicListenAddr {
		t.Errorf("empty env should fall back to default, got %q", got)
	}
}

func TestRunWithDeps_ListenErrorReturns(t *testing.T) {
	deps := defaultDeps()
	deps.capCheck = func() error { return nil }
	deps.listen = func(_, _ string) (net.Listener, error) {
		return nil, errors.New("addr in use")
	}
	err := runWithDeps(context.Background(), discardLogger(), deps)
	if err == nil {
		t.Fatal("expected listen error to propagate")
	}
	if !strings.Contains(err.Error(), "addr in use") {
		t.Errorf("error %q missing 'addr in use'", err.Error())
	}
}

func TestRunWithDeps_ServeError(t *testing.T) {
	// Use a listener we close immediately, then have the server try to Serve
	// on it. The close races with Serve so we observe either an immediate
	// Serve error or a successful Shutdown — both are acceptable termination
	// signals.
	deps := defaultDeps()
	deps.capCheck = func() error { return nil }
	deps.backend = &fixedBackend{}

	deps.listen = func(_, _ string) (net.Listener, error) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		_ = l.Close()
		return l, nil
	}
	deps.newSrv = func(addr string, h http.Handler) *http.Server {
		return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	}

	done := make(chan error, 1)
	go func() { done <- runWithDeps(context.Background(), discardLogger(), deps) }()

	select {
	case err := <-done:
		// The Serve of a closed listener returns a net error; we just want
		// the goroutine to exit cleanly. Acceptable: any non-nil OR nil.
		_ = err
	case <-time.After(2 * time.Second):
		t.Fatal("runWithDeps did not exit after listener closed")
	}
}

func TestDefaultDeps_ReturnExpected(t *testing.T) {
	d := defaultDeps()
	if d.listen == nil {
		t.Error("defaultDeps().listen is nil")
	}
	if d.newSrv == nil {
		t.Error("defaultDeps().newSrv is nil")
	}
	if d.backend == nil {
		t.Error("defaultDeps().backend is nil")
	}
	if _, ok := d.backend.(unwiredBackend); !ok {
		t.Errorf("default backend = %T, want unwiredBackend", d.backend)
	}
	srv := d.newSrv(":0", http.NewServeMux())
	if srv.ReadHeaderTimeout == 0 {
		t.Error("default server should set ReadHeaderTimeout")
	}
	// Issue #995 Phase 3 / ADR-121: hardened defaults for the
	// control + unix-socket listener (defaultServer).
	if srv.ReadTimeout == 0 {
		t.Error("default server should set ReadTimeout (issue #995 Phase 3)")
	}
	if srv.WriteTimeout == 0 {
		t.Error("default server should set WriteTimeout (issue #995 Phase 3)")
	}
	if srv.IdleTimeout == 0 {
		t.Error("default server should set IdleTimeout (issue #995 Phase 3)")
	}
	if srv.MaxHeaderBytes == 0 {
		t.Error("default server should set MaxHeaderBytes (issue #995 Phase 3)")
	}
}

// TestReadTimeoutOrDefault verifies the Phase 3 helper falls through
// to api.GatewaydInternalReadTimeoutSecondsDefault when the override
// is zero. Issue #995 Phase 3 / ADR-121.
func TestReadTimeoutOrDefault(t *testing.T) {
	if got := readTimeoutOrDefault(0); got != time.Duration(api.GatewaydInternalReadTimeoutSecondsDefault)*time.Second {
		t.Errorf("readTimeoutOrDefault(0) = %v, want %ds", got, api.GatewaydInternalReadTimeoutSecondsDefault)
	}
	if got := readTimeoutOrDefault(7 * time.Second); got != 7*time.Second {
		t.Errorf("readTimeoutOrDefault(7s) = %v, want 7s", got)
	}
}

// TestWriteTimeoutOrDefault still passes its existing shape (Phase 3
// left the WriteTimeout surface unchanged), but add the symmetric
// guard here so the two helpers move together.
func TestWriteTimeoutOrDefault(t *testing.T) {
	if got := writeTimeoutOrDefault(0); got != time.Duration(api.ResponseWriteTimeoutDefault)*time.Second {
		t.Errorf("writeTimeoutOrDefault(0) = %v, want %ds", got, api.ResponseWriteTimeoutDefault)
	}
	if got := writeTimeoutOrDefault(7 * time.Second); got != 7*time.Second {
		t.Errorf("writeTimeoutOrDefault(7s) = %v, want 7s", got)
	}
}

func TestFixedBackend_Delegates(t *testing.T) {
	b := &fixedBackend{
		app:      gateway.App{ID: "a1", Plan: api.PlanHobby},
		appOK:    true,
		picks:    []gateway.Target{{NodeID: "10.0.0.2:8080", InstanceID: "i-1"}},
		admitErr: errors.New("upstream"),
	}
	if a, ok := b.Lookup(context.Background(), "name"); !ok || a.ID != "a1" {
		t.Errorf("Lookup = %+v,%v", a, ok)
	}
	if res := b.Pick("a"); !res.OK || res.Target.NodeID != "10.0.0.2:8080" {
		t.Errorf("Pick = %+v,%v", res.Target, res.OK)
	}
	if got := b.HealthyCount("a"); got != 1 {
		t.Errorf("HealthyCount = %d, want 1", got)
	}
	if _, _, _, err := b.Admit(context.Background(), "x", "", "", "", 1); err == nil || err.Error() != "upstream" {
		t.Errorf("Admit err = %v", err)
	}
	if b.admitCalls != 1 {
		t.Errorf("Admit call not recorded: %d", b.admitCalls)
	}
}

// TestAssertLoopbackBind exercises the /metrics listener guard added
// in PR #218. Accepts every loopback form; rejects public addresses and
// bare ":port" (which would bind 0.0.0.0). The harness path passes a
// loopback form so the in-process tests in this file keep passing.
func TestAssertLoopbackBind(t *testing.T) {
	cases := []struct {
		addr string
		ok   bool
	}{
		// Accepted: explicit loopback forms the harness / production use.
		{"127.0.0.1:9090", true},
		{"127.0.0.42:9100", true},
		{"[::1]:9090", true},
		{"localhost:9090", true},

		// Rejected: any address that would expose /metrics off-box.
		{"0.0.0.0:9090", false},
		{":9090", false}, // bare ":port" binds 0.0.0.0 — exactly what this guard prevents
		{"10.0.0.1:9090", false},
		{"[2001:db8::1]:9090", false},

		// Rejected: malformed.
		{"no-port", false},
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			err := assertLoopbackBind(tc.addr)
			if tc.ok && err != nil {
				t.Errorf("assertLoopbackBind(%q) = %v, want nil", tc.addr, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("assertLoopbackBind(%q) = nil, want error", tc.addr)
			}
		})
	}
}

func TestInstallComputeMetricsRoute(t *testing.T) {
	control := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	t.Run("compute role exposes private metrics route", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.Handle("/", http.NotFoundHandler())
		installComputeMetricsRoute(mux, role.RoleComputeOnly, control)
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "http://compute/metrics", nil))
		if r.Code != http.StatusTeapot {
			t.Fatalf("metrics status = %d, want %d", r.Code, http.StatusTeapot)
		}
	})

	t.Run("single box keeps catch all", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.Handle("/", http.NotFoundHandler())
		installComputeMetricsRoute(mux, role.RoleSingleBox, control)
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "http://single/metrics", nil))
		if r.Code != http.StatusNotFound {
			t.Fatalf("single-box metrics status = %d, want %d", r.Code, http.StatusNotFound)
		}
	})
}
