// spec: §4.1.2.0 — apps.maintenance_mode answers every request with 503 once a gateway has applied the change.
package gateway

import (
	"context"
	"sync"
	"testing"
	"time"
)

// snapshotRouter answers with the app row as it was when the read began, as
// a Postgres statement does. The first read waits for release after taking
// its snapshot, so an app change can land while it is in flight.
type snapshotRouter struct {
	mu          sync.Mutex
	maintenance bool
	hold        chan struct{}
	held        chan struct{}
}

func (r *snapshotRouter) setMaintenance(on bool) {
	r.mu.Lock()
	r.maintenance = on
	r.mu.Unlock()
}

func (r *snapshotRouter) ResolveHost(ctx context.Context, _ string) (App, bool, error) {
	r.mu.Lock()
	snapshot, hold := r.maintenance, r.hold
	r.hold = nil
	r.mu.Unlock()
	if hold != nil {
		close(r.held)
		select {
		case <-hold:
		case <-ctx.Done():
			return App{}, false, ctx.Err()
		}
	}
	return App{ID: "app-1", AccountID: "acct-1", Slug: "pulse", MaintenanceMode: snapshot}, true, nil
}

// PulseWatch F5: under load, `--maintenance` reached every gateway (traffic
// status reported 2/2 at 1.4 s) yet requests were answered 200 until 2.2 s on
// new connections and 3.4 s on kept-alive ones. A route read that began
// before the change finished after the gateway dropped the app, stored the
// pre-change row in the app cache, which has no TTL, and requests arriving in
// between joined that read. A read that an invalidation of its app overtook
// must not refill the caches, and a request after the invalidation must not
// join it.
func TestRouteReadOvertakenByAppChangeDoesNotRefillCache(t *testing.T) {
	const host = "pulse.gregale.app"
	hold := make(chan struct{})
	router := &snapshotRouter{hold: hold, held: make(chan struct{})}
	b := NewPGBackend(router, nil, nil)
	lookup := func() <-chan App {
		out := make(chan App, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			app, _ := b.Lookup(ctx, host)
			out <- app
		}()
		return out
	}

	before := lookup()
	<-router.held // the read holds the row from before the change

	// apid commits maintenance_mode=true; the app_changed notification
	// invalidates as cmd/gatewayd-internal does.
	router.setMaintenance(true)
	b.ResetApp("app-1")
	b.InvalidateRoutesForApp("app-1")

	during := lookup()
	select {
	case app := <-during:
		if !app.MaintenanceMode {
			t.Fatal("a request after the app change was answered from the read that began before it")
		}
	case <-time.After(time.Second):
		t.Fatal("a request after the app change waited on the read that began before it")
	}

	close(hold)
	if app := <-before; app.MaintenanceMode {
		t.Fatal("the read that began before the change saw the change: the test did not hold it")
	}
	if app := <-lookup(); !app.MaintenanceMode {
		t.Fatal("the next request was answered from the pre-change row the overtaken read cached")
	}
}

// A read no invalidation overtook still fills the cache: the next lookup of
// the host does not go back to the router.
func TestRouteReadFillsCacheWithoutInvalidation(t *testing.T) {
	router := &gatedRouter{release: make(chan struct{})}
	close(router.release)
	b := NewPGBackend(router, nil, nil)
	for range 3 {
		if _, ok := b.Lookup(context.Background(), "pulse.gregale.app"); !ok {
			t.Fatal("route did not resolve")
		}
	}
	if n := router.calls.Load(); n != 1 {
		t.Fatalf("three lookups made %d router reads, want 1", n)
	}
}
