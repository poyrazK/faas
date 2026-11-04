package gateway

// adr: 20261027085556 — amendment 3: on the cached host policy path a failed
// route read with no last-known-good route is retried and then refused; it is
// never an unknown host.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type flakyHostRouter struct {
	calls atomic.Int64
	fail  func(call int64) error
}

func (r *flakyHostRouter) ResolveHost(_ context.Context, _ string) (App, bool, error) {
	if err := r.fail(r.calls.Add(1)); err != nil {
		return App{}, false, err
	}
	return App{ID: "app-1", AccountID: "acct-1", Slug: "pulse"}, true, nil
}

func cachedHostPolicyFixture(t *testing.T, router Router) (*Handler, *PGBackend, *http.Request) {
	t.Helper()
	h, base, _ := newTestHandler(t)
	backend := NewPGBackend(router, nil, nil)
	h.backend = backend
	return h, backend, httptest.NewRequest(http.MethodGet, "http://"+base.host+"/", nil)
}

// n2, 2026-11-04: a GitOps app's first requests met a slow organization read.
// The cached path called the host unknown and answered 404 "No such app".
func TestCachedHostPolicyRetriesAFailedRouteRead(t *testing.T) {
	router := &flakyHostRouter{fail: func(call int64) error {
		if call == 1 {
			return errors.New("state: scan org: context deadline exceeded")
		}
		return nil
	}}
	h, _, r := cachedHostPolicyFixture(t, router)
	app, found, err := h.lookupAppPolicy(r, "pulse.gregale.app")
	if err != nil || !found || app.ID != "app-1" || router.calls.Load() != 2 {
		t.Fatalf("lookup = %+v found=%t err=%v reads=%d; want the retry's app", app, found, err, router.calls.Load())
	}
}

func TestCachedHostPolicyRefusesAnUnreadableHost(t *testing.T) {
	router := &flakyHostRouter{fail: func(int64) error { return errors.New("acquire connection: pool exhausted") }}
	h, backend, r := cachedHostPolicyFixture(t, router)
	if _, found, err := h.lookupAppPolicy(r, "pulse.gregale.app"); err == nil || found {
		t.Fatalf("lookup found=%t err=%v; want a refusal the handler answers with 503", found, err)
	}
	// Callers without an error path keep the miss.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, found := backend.Lookup(ctx, "pulse.gregale.app"); found {
		t.Fatal("Lookup found an unreadable host")
	}
}

func TestCachedHostPolicyKeepsUnknownAndLastKnownGoodHosts(t *testing.T) {
	var failing, unknown atomic.Bool
	router := &unknownOrFlakyRouter{flakyHostRouter{fail: func(int64) error {
		if failing.Load() {
			return errors.New("acquire connection: pool exhausted")
		}
		return nil
	}}, &unknown}
	h, backend, r := cachedHostPolicyFixture(t, router)
	unknown.Store(true)
	if _, found, err := h.lookupAppPolicy(r, "gone.gregale.app"); err != nil || found || router.calls.Load() != 1 {
		t.Fatalf("unknown host found=%t err=%v reads=%d; want one read and a miss", found, err, router.calls.Load())
	}
	unknown.Store(false)
	if _, found, err := h.lookupAppPolicy(r, "pulse.gregale.app"); err != nil || !found {
		t.Fatalf("first read found=%t err=%v", found, err)
	}
	backend.routes.Invalidate("pulse.gregale.app")
	failing.Store(true)
	if app, found, err := h.lookupAppPolicy(r, "pulse.gregale.app"); err != nil || !found || app.ID != "app-1" {
		t.Fatalf("stale lookup = %+v found=%t err=%v; want the last-known-good route", app, found, err)
	}
}

type unknownOrFlakyRouter struct {
	flakyHostRouter
	unknown *atomic.Bool
}

func (r *unknownOrFlakyRouter) ResolveHost(ctx context.Context, host string) (App, bool, error) {
	if r.unknown.Load() {
		r.calls.Add(1)
		return App{}, false, nil
	}
	return r.flakyHostRouter.ResolveHost(ctx, host)
}
