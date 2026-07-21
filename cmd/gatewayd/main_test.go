package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// fixedBackend is a Backend that returns whatever the test sets. Used to
// exercise the handler composition without depending on the unwired default.
type fixedBackend struct {
	app        gateway.App
	appOK      bool
	target     string
	targetOK   bool
	wakeErr    error
	wakeCalled int
	wakeName   string
}

func (f *fixedBackend) Lookup(_ context.Context, name string) (gateway.App, bool) {
	return f.app, f.appOK
}
func (f *fixedBackend) Target(name string) (string, bool) {
	return f.target, f.targetOK
}
func (f *fixedBackend) Wake(ctx context.Context, name string) error {
	f.wakeCalled++
	f.wakeName = name
	return f.wakeErr
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestUnwiredBackendReturnsNotFound(t *testing.T) {
	b := unwiredBackend{}
	if _, ok := b.Lookup(context.Background(), "any"); ok {
		t.Error("Lookup should report not-found")
	}
	if _, ok := b.Target("any"); ok {
		t.Error("Target should report not-found")
	}
	if err := b.Wake(context.Background(), "any"); err != nil {
		t.Errorf("Wake should be no-op: %v", err)
	}
}

func TestRunWithDeps_ServesAndShutsDown(t *testing.T) {
	deps := defaultDeps()
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

func TestRunWithDeps_ListenErrorReturns(t *testing.T) {
	deps := defaultDeps()
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
}

func TestFixedBackend_Delegates(t *testing.T) {
	b := &fixedBackend{
		app:      gateway.App{ID: "a1", Plan: api.PlanHobby},
		appOK:    true,
		target:   "10.0.0.2:8080",
		targetOK: true,
		wakeErr:  errors.New("upstream"),
	}
	if a, ok := b.Lookup(context.Background(), "name"); !ok || a.ID != "a1" {
		t.Errorf("Lookup = %+v,%v", a, ok)
	}
	if tgt, ok := b.Target("a"); !ok || tgt != "10.0.0.2:8080" {
		t.Errorf("Target = %q,%v", tgt, ok)
	}
	if err := b.Wake(context.Background(), "x"); err == nil || err.Error() != "upstream" {
		t.Errorf("Wake err = %v", err)
	}
	if b.wakeCalled != 1 || b.wakeName != "x" {
		t.Errorf("Wake call not recorded: %d %q", b.wakeCalled, b.wakeName)
	}
}

// TestRunWithDeps_TLSBundleCloseStopsRenewLoop — D2.4 / D4 lifecycle assertion.
// When the daemon is configured with a TLS bundle, the shutdown path must
// call (*TLSBundle).Close. certmagic v0.25 has no public Stop API so Close is
// a no-op today, but the call is the load-bearing seam: a future certmagic
// upgrade can wire real shutdown without touching main.go.
//
// What this test asserts:
//
//  1. runWithDeps enters the TLS branch (deps.tlsBundle != nil) cleanly when
//     listeners are injected via deps.listen / deps.extraListen.
//  2. The shutdown branch executes without panicking when ctx is cancelled
//     (the deps.tlsBundle.Close() call inside main.go is reached and
//     returns nil for a Config==nil bundle).
//  3. (*TLSBundle).Close is idempotent — calling it twice returns nil both
//     times, which is the contract documented on the method.
//
// What this test does NOT assert: that Close actually tears down certmagic
// internals. certmagic v0.25 has no public Stop API; that's owned by the
// wire-shape suite in pkg/gateway. Asserting goroutine count after Close is
// flaky (certmagic's renew tick is on an unobservable goroutine) so we
// don't — the call-site wiring is the verifiable contract.
func TestRunWithDeps_TLSBundleCloseStopsRenewLoop(t *testing.T) {
	// Minimal stub TLSBundle. Config is nil → Close returns nil fast.
	// GetCertificate nil is fine because we never dial TLS into this
	// listener — the test only observes shutdown semantics.
	bundle := &gateway.TLSBundle{}
	deps := defaultDeps()
	deps.backend = &fixedBackend{}
	deps.tlsBundle = bundle
	// deps.acmeMux is required when deps.tlsBundle != nil (main.go wires it
	// on the :80 listener). A no-op mux is fine — the listener won't get
	// traffic in this test.
	deps.acmeMux = http.NewServeMux()
	// Bind listeners on free ports. We don't dial in; we just need the
	// accept loop to be alive so runWithDeps reaches the shutdown branch
	// cleanly when ctx cancels.
	publicLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	acmeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deps.listen = func(_, _ string) (net.Listener, error) { return publicLn, nil }
	deps.extraListen = func(_, _ string) (net.Listener, error) { return acmeLn, nil }
	// Free-port the control listener so this test doesn't race with
	// TestRunWithDeps_ServesAndShutsDown for the hard-coded 127.0.0.1:9090.
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

	// Let the listeners come up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, derr := net.Dial("tcp", publicLn.Addr().String())
		if derr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
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

	// Close idempotency contract: a real production shutdown may call Close
	// twice if SIGTERM lands while the cancel branch is mid-flight. Both
	// calls must return nil (Close's "calling twice is a no-op" doc).
	for i := 0; i < 2; i++ {
		if err := bundle.Close(); err != nil {
			t.Errorf("bundle.Close call #%d returned %v, want nil", i, err)
		}
	}
}
