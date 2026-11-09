package objectstorage

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// stallingPublicReadStore stands in for a control-plane database that stalls:
// while stall is set, AppBySlug blocks until the caller's context ends.
type stallingPublicReadStore struct {
	PublicReadStore
	stall     atomic.Bool
	appBySlug atomic.Int32
}

func (s *stallingPublicReadStore) AppBySlug(ctx context.Context, slug string) (state.App, error) {
	s.appBySlug.Add(1)
	if s.stall.Load() {
		<-ctx.Done()
		return state.App{}, ctx.Err()
	}
	return s.PublicReadStore.AppBySlug(ctx, slug)
}

// ageMounts moves every cached answer back by d, as if it had been read that
// long ago.
func ageMounts(f *publicReadForwarder, d time.Duration) {
	c := f.match.mounts
	c.mu.Lock()
	defer c.mu.Unlock()
	for host, entry := range c.hosts {
		entry.expires = entry.expires.Add(-d)
		entry.stored = entry.stored.Add(-d)
		c.hosts[host] = entry
	}
}

// A compute edge's forwarder must not hold a request on the control plane
// once it knows a host: an expired answer routes the request at once while
// the refresh runs in the background, and a refresh that fails during a
// control-plane stall leaves the answer in place (prod 2026-11-06: a stall
// turned every busy host's expiry into 2 s waits and failed requests).
func TestPublicReadForwarderRoutesByAnExpiredAnswerDuringAStall(t *testing.T) {
	store := &stallingPublicReadStore{PublicReadStore: seedForwardBucket(t)}
	h, err := NewPublicReadForwarder(PublicReadForwardConfig{Store: store, AppsDomain: "gregale.dev", Forward: http.NotFoundHandler(), Next: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	f := h.(*publicReadForwarder)
	if got := publicReadRoute(t, h, "assets-app.gregale.dev", "/assets/logo.png"); got != "forward" {
		t.Fatalf("first request went to %q, want forward", got)
	}

	store.stall.Store(true)
	ageMounts(f, publicMountTTL)
	for _, tc := range []struct{ path, want string }{{"/assets/logo.png", "forward"}, {"/api/users", "next"}} {
		start := time.Now()
		if got := publicReadRoute(t, h, "assets-app.gregale.dev", tc.path); got != tc.want {
			t.Fatalf("%s during the stall went to %q, want %q", tc.path, got, tc.want)
		}
		if waited := time.Since(start); waited > api.PublicReadHostLookupTimeout/4 {
			t.Fatalf("%s waited %s for the control plane; the expired answer should route it at once", tc.path, waited)
		}
	}
	// The background refresh fails at the lookup timeout and keeps the answer.
	// Do joins the refresh still in flight and returns when it has failed.
	_, _, _ = f.match.lookups.Do("assets-app.gregale.dev", func() (any, error) { return nil, nil })
	if n := store.appBySlug.Load(); n != 2 {
		t.Fatalf("control-plane reads = %d, want 2 (first lookup, one shared refresh)", n)
	}
	if got := publicReadRoute(t, h, "assets-app.gregale.dev", "/assets/logo.png"); got != "forward" {
		t.Fatalf("after a failed refresh the request went to %q, want forward", got)
	}

	// Past the stale bound the forwarder waits for the control plane again.
	ageMounts(f, api.PublicReadForwardStaleMax)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := f.match.hostMounts(ctx, "assets-app.gregale.dev"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lookup past the stale bound = %v, want it to wait on the stalled store", err)
	}
}

// The control plane's serving handler is the authority on a bucket being
// public: it never answers from an expired entry.
func TestPublicReadHandlerNeverServesAnExpiredAnswer(t *testing.T) {
	h, err := NewPublicReadHandler(PublicReadConfig{Store: seedForwardBucket(t), Registry: &Registry{}, AppsDomain: "gregale.dev", Next: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	if s := h.(*publicReadHandler).staleFor; s != 0 {
		t.Fatalf("serving handler staleFor = %s, want 0", s)
	}
}
