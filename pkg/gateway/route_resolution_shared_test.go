// spec: §4.1 — concurrent route-cache misses for one host share one resolution.
package gateway

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type gatedRouter struct {
	calls   atomic.Int64
	release chan struct{}
}

func (r *gatedRouter) ResolveHost(ctx context.Context, host string) (App, bool, error) {
	r.calls.Add(1)
	select {
	case <-r.release:
	case <-ctx.Done():
		return App{}, false, ctx.Err()
	}
	return App{ID: "app-1", AccountID: "acct-1", Slug: "pulse"}, true, nil
}

// PulseWatch F13: an app change emptied the route cache and every request of
// the burst that followed resolved the host from Postgres at once through
// gatewayd's eight connections (93 canceled acquisitions in a minute on
// production-us). Concurrent misses for one host now share one resolution.
func TestRouteCacheMissesShareOneResolution(t *testing.T) {
	router := &gatedRouter{release: make(chan struct{})}
	b := NewPGBackend(router, nil, nil)
	const burst = 20
	var wg sync.WaitGroup
	found := make(chan bool, burst)
	for range burst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			app, ok := b.Lookup(ctx, "pulse.gregale.app")
			found <- ok && app.ID == "app-1"
		}()
	}
	deadline := time.Now().Add(time.Second)
	for router.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // let the rest of the burst join
	close(router.release)
	wg.Wait()
	close(found)
	for ok := range found {
		if !ok {
			t.Fatal("a request of the burst did not resolve the route")
		}
	}
	if n := router.calls.Load(); n != 1 {
		t.Fatalf("%d concurrent cache misses made %d resolutions, want 1", burst, n)
	}
}

// A caller whose own deadline passes stops waiting; the shared resolution
// still answers the others.
func TestRouteResolutionCallerStopsAtItsOwnDeadline(t *testing.T) {
	router := &gatedRouter{release: make(chan struct{})}
	b := NewPGBackend(router, nil, nil)
	leader := make(chan bool, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, ok := b.Lookup(ctx, "pulse.gregale.app")
		leader <- ok
	}()
	for router.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := b.resolveHostShared(short, "pulse.gregale.app"); err == nil {
		t.Fatal("a caller past its deadline kept waiting for the shared resolution")
	}
	close(router.release)
	if !<-leader {
		t.Fatal("the shared resolution did not answer the first caller")
	}
}
