package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// seedApp creates an account + app in the store and returns the app.
func seedApp(t *testing.T, store state.Store, slug string, plan api.Plan) state.App {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, slug+"@local", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      slug,
		Type:      state.AppTypeApp,
		RAMMB:     128,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return app
}

func TestPgRouter_ResolveSlugHost(t *testing.T) {
	store := state.NewMemStore()
	app := seedApp(t, store, "blog", api.PlanPro)
	r := pgRouter{store: store, appsSuffix: ".apps.example.com"}

	got, ok, err := r.ResolveHost(context.Background(), "blog.apps.example.com")
	if err != nil || !ok {
		t.Fatalf("ResolveHost ok=%v err=%v", ok, err)
	}
	if got.ID != app.ID || got.Plan != api.PlanPro {
		t.Errorf("resolved = %+v, want id=%s plan=pro", got, app.ID)
	}
}

func TestPgRouter_UnknownSlugIsNotFound(t *testing.T) {
	r := pgRouter{store: state.NewMemStore(), appsSuffix: ".apps.example.com"}
	if _, ok, err := r.ResolveHost(context.Background(), "ghost.apps.example.com"); ok || err != nil {
		t.Fatalf("ghost host ok=%v err=%v, want false/nil", ok, err)
	}
}

func TestPgRouter_MultiLabelPrefixRejected(t *testing.T) {
	store := state.NewMemStore()
	seedApp(t, store, "blog", api.PlanFree)
	r := pgRouter{store: store, appsSuffix: ".apps.example.com"}
	// "x.blog.apps.example.com" must NOT route to slug "blog" — only a single
	// label under the suffix is a platform subdomain.
	if _, ok, _ := r.ResolveHost(context.Background(), "x.blog.apps.example.com"); ok {
		t.Fatal("multi-label prefix routed to an app")
	}
}

func TestPgRouter_CustomDomainVerifiedOnly(t *testing.T) {
	store := state.NewMemStore()
	app := seedApp(t, store, "shop", api.PlanScale)
	ctx := context.Background()
	if _, err := store.CreateCustomDomain(ctx, "shop.io", app.ID, "tok"); err != nil {
		t.Fatalf("CreateCustomDomain: %v", err)
	}
	r := pgRouter{store: store, appsSuffix: ".apps.example.com"}

	// Unverified → not routable.
	if _, ok, _ := r.ResolveHost(ctx, "shop.io"); ok {
		t.Fatal("unverified custom domain routed")
	}
	// Verified → routes to the app with the account plan.
	if err := store.MarkDomainVerified(ctx, "shop.io"); err != nil {
		t.Fatalf("MarkDomainVerified: %v", err)
	}
	got, ok, err := r.ResolveHost(ctx, "shop.io")
	if err != nil || !ok {
		t.Fatalf("verified custom domain ok=%v err=%v", ok, err)
	}
	if got.ID != app.ID || got.Plan != api.PlanScale {
		t.Errorf("resolved = %+v", got)
	}
}

func TestPgRouter_DeletedAppNotRouted(t *testing.T) {
	store := state.NewMemStore()
	app := seedApp(t, store, "gone", api.PlanFree)
	if err := store.DeleteApp(context.Background(), app.ID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	r := pgRouter{store: store, appsSuffix: ".apps.example.com"}
	if _, ok, _ := r.ResolveHost(context.Background(), "gone.apps.example.com"); ok {
		t.Fatal("deleted app still routed")
	}
}

func TestAppsSuffix(t *testing.T) {
	cases := map[string]string{
		"":                   "",
		"apps.example.com":   ".apps.example.com",
		".apps.example.com":  ".apps.example.com",
		" apps.Example.COM ": ".apps.example.com",
	}
	for in, want := range cases {
		if got := appsSuffix(in); got != want {
			t.Errorf("appsSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeInvalidator records EvictInstance / FlushRoutes calls.
type fakeInvalidator struct {
	mu       sync.Mutex
	evicted  map[string]string // instance_id -> app_id
	flushCnt int
}

func (f *fakeInvalidator) EvictInstance(appID, instanceID string) {
	f.mu.Lock()
	if f.evicted == nil {
		f.evicted = map[string]string{}
	}
	f.evicted[instanceID] = appID
	f.mu.Unlock()
}
func (f *fakeInvalidator) FlushRoutes() {
	f.mu.Lock()
	f.flushCnt++
	f.mu.Unlock()
}

func TestHandleInvalidation(t *testing.T) {
	f := &fakeInvalidator{}
	log := testLogger()

	handleInvalidation(f, db.Notification{Channel: db.NotifyInstanceChanged, Payload: `{"instance_id":"i-1","app_id":"app-7","state":"parked"}`}, log)
	handleInvalidation(f, db.Notification{Channel: db.NotifyAppChanged, Payload: `{"app_id":"app-7"}`}, log)
	handleInvalidation(f, db.Notification{Channel: db.NotifyDomainChanged, Payload: `{"domain":"x.io"}`}, log)
	// Malformed instance payload → no evict, no panic.
	handleInvalidation(f, db.Notification{Channel: db.NotifyInstanceChanged, Payload: `not json`}, log)
	// instance payload missing instance_id → no evict.
	handleInvalidation(f, db.Notification{Channel: db.NotifyInstanceChanged, Payload: `{"app_id":"app-7"}`}, log)
	// Unknown channel → ignored.
	handleInvalidation(f, db.Notification{Channel: "other", Payload: `{}`}, log)

	f.mu.Lock()
	defer f.mu.Unlock()
	if got, want := f.evicted["i-1"], "app-7"; got != want {
		t.Errorf("evicted[i-1] = %q, want %q", got, want)
	}
	if len(f.evicted) != 1 {
		t.Errorf("evicted map = %v, want 1 entry", f.evicted)
	}
	if f.flushCnt != 2 {
		t.Errorf("flush count = %d, want 2 (app + domain)", f.flushCnt)
	}
}

// TestHandleInvalidation_LifecycleStatesDoNotEvict (issue #168) pins the
// cache-self-destruct guard: lifecycle states (waking/cold_booting/running)
// must NOT evict. The wake flow emits two notifications per successful
// wake — WAKING right after CreateInstance, RUNNING after vmmd boot —
// and the gateway adds the Target to its cache on the Admit RPC return
// between those two emissions. Evicting on either notification drops
// the Target we just added, defeating the cache.
func TestHandleInvalidation_LifecycleStatesDoNotEvict(t *testing.T) {
	for _, state := range []string{"waking", "cold_booting", "running"} {
		f := &fakeInvalidator{}
		log := testLogger()
		payload := `{"instance_id":"i-lifecycle","app_id":"app-9","state":"` + state + `"}`
		handleInvalidation(f, db.Notification{Channel: db.NotifyInstanceChanged, Payload: payload}, log)

		f.mu.Lock()
		evicted := len(f.evicted)
		f.mu.Unlock()
		if evicted != 0 {
			t.Errorf("state=%q: evicted %d entries, want 0 (cache-self-destruct guard)", state, evicted)
		}
	}
}

// TestHandleInvalidation_TerminalStatesEvict (issue #168) verifies the
// companion to the lifecycle test: terminal-ish states (stopped, failed,
// parked, snapshotting) DO evict so the next request re-admits on a
// different node / wakes fresh.
func TestHandleInvalidation_TerminalStatesEvict(t *testing.T) {
	for _, state := range []string{"stopped", "failed", "parked", "snapshotting"} {
		f := &fakeInvalidator{}
		log := testLogger()
		payload := `{"instance_id":"i-term","app_id":"app-9","state":"` + state + `"}`
		handleInvalidation(f, db.Notification{Channel: db.NotifyInstanceChanged, Payload: payload}, log)

		f.mu.Lock()
		got := f.evicted["i-term"]
		f.mu.Unlock()
		if got != "app-9" {
			t.Errorf("state=%q: evicted[i-term] = %q, want app-9", state, got)
		}
	}
}
