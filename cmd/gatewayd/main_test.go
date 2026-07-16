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
