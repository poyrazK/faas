package webhookout_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// TestNewDispatcher_ReusesOneConnectionAcrossDispatchers is the regression
// pin for the per-delivery connection leak: schedd builds a Dispatcher per
// delivery and apid one per test send. Each used to build its own
// transport, so every delivery opened a new connection and left it idle in
// a pool nothing could reach. Dispatchers built without a client must now
// share one pool, so sequential deliveries reuse a single connection.
func TestNewDispatcher_ReusesOneConnectionAcrossDispatchers(t *testing.T) {
	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "1")
	var opened atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	const deliveries = 5
	for i := range deliveries {
		d := webhookout.NewDispatcher(webhookout.DispatcherOptions{MaxAttempts: 1, PerAttempt: 5 * time.Second})
		if res := d.Dispatch(context.Background(), testTarget(srv.URL), newTestEvent()); res.Err != nil {
			t.Fatalf("delivery %d: %v", i, res.Err)
		}
	}
	if got := opened.Load(); got != 1 {
		t.Fatalf("connections opened for %d deliveries = %d, want 1 (one shared pool)", deliveries, got)
	}
}

// TestSharedHTTPClient_IsOneBoundedClient pins the shared client's shape:
// one instance per process, an idle-connection timeout so parked
// connections close, no client Timeout of its own (each dispatcher sets
// PerAttempt on a copy), and the loopback escape hatch kept apart from
// the guarded production client.
func TestSharedHTTPClient_IsOneBoundedClient(t *testing.T) {
	guarded := webhookout.SharedHTTPClient()
	if again := webhookout.SharedHTTPClient(); again != guarded {
		t.Fatal("SharedHTTPClient returned a different client on the second call")
	}
	tr, ok := guarded.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", guarded.Transport)
	}
	if tr.IdleConnTimeout <= 0 || tr.MaxIdleConnsPerHost <= 0 {
		t.Fatalf("idle pool unbounded: IdleConnTimeout=%v MaxIdleConnsPerHost=%d", tr.IdleConnTimeout, tr.MaxIdleConnsPerHost)
	}
	if guarded.Timeout != 0 {
		t.Fatalf("shared client Timeout = %v; a dispatcher must set its timeout on a copy", guarded.Timeout)
	}
	// Building a dispatcher must not stamp its PerAttempt onto the shared client.
	webhookout.NewDispatcher(webhookout.DispatcherOptions{PerAttempt: 3 * time.Second})
	if guarded.Timeout != 0 {
		t.Fatalf("NewDispatcher mutated the shared client's Timeout to %v", guarded.Timeout)
	}
	// The guarded client still refuses loopback at dial time.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:1/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := guarded.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("guarded shared client reached loopback")
	} else if !errors.Is(err, oci.ErrImageEgressDenied) {
		t.Fatalf("loopback dial error = %v, want oci.ErrImageEgressDenied", err)
	}

	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "1")
	if loopback := webhookout.SharedHTTPClient(); loopback == guarded {
		t.Fatal("loopback escape hatch returned the guarded production client")
	}
}
