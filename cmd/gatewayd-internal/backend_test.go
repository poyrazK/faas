package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
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
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}

	got, ok, err := r.ResolveHost(context.Background(), "blog.apps.gregale.dev")
	if err != nil || !ok {
		t.Fatalf("ResolveHost ok=%v err=%v", ok, err)
	}
	if got.ID != app.ID || got.Plan != api.PlanPro {
		t.Errorf("resolved = %+v, want id=%s plan=pro", got, app.ID)
	}
}

func TestPgRouter_UnknownSlugIsNotFound(t *testing.T) {
	r := pgRouter{store: state.NewMemStore(), appsSuffix: ".apps.gregale.dev"}
	if _, ok, err := r.ResolveHost(context.Background(), "ghost.apps.gregale.dev"); ok || err != nil {
		t.Fatalf("ghost host ok=%v err=%v, want false/nil", ok, err)
	}
}

func TestPgRouter_MultiLabelPrefixRejected(t *testing.T) {
	store := state.NewMemStore()
	seedApp(t, store, "blog", api.PlanFree)
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}
	// "x.blog.apps.gregale.dev" must NOT route to slug "blog" — only a single
	// label under the suffix is a platform subdomain.
	if _, ok, _ := r.ResolveHost(context.Background(), "x.blog.apps.gregale.dev"); ok {
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
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}

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
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}
	if _, ok, _ := r.ResolveHost(context.Background(), "gone.apps.gregale.dev"); ok {
		t.Fatal("deleted app still routed")
	}
}

func TestAppsSuffix(t *testing.T) {
	cases := map[string]string{
		"":                   "",
		"apps.gregale.dev":   ".apps.gregale.dev",
		".apps.gregale.dev":  ".apps.gregale.dev",
		" apps.Example.COM ": ".apps.example.com",
	}
	for in, want := range cases {
		if got := appsSuffix(in); got != want {
			t.Errorf("appsSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeInvalidator records EvictInstance / FlushRoutes /
// InvalidatePublicAuth calls (issue #477 / ADR-079) and
// RefreshDeploymentWeights calls (issue #556 / PR-B).
// ResetEdgeRules (ADR-089 PR 3) is a no-op here — the test
// is about the switch-arm dispatch, not the matcher itself.
// ResetApp (ADR-091 amendment) is a no-op here for the same
// reason — the test asserts the dispatch arm is taken, not
// the per-app cache delete semantics (those live in
// pkg/gateway's pgbackend_test.go).
type fakeInvalidator struct {
	mu            sync.Mutex
	evicted       map[string]string // instance_id -> app_id
	flushCnt      int
	publicAuthCnt int
	refreshed     []string // app_ids that received RefreshDeploymentWeights
	resetCnt      int      // ResetEdgeRules call count (ADR-089 PR 3)
	resetApps     []string // app_ids that received ResetApp (ADR-091 amendment)
	// responseCacheByApp (ADR-122 §Decision) records app_ids
	// that received InvalidateResponseCacheByApp — paired
	// 1:1 with resetApps in the NotifyAppChanged handler arm
	// so tests can assert both fire on the same notification.
	responseCacheByApp  []string
	responseCacheByPath []struct {
		appID    string
		pathGlob string
	}
	// responseCacheAll counts InvalidateResponseCacheAll calls
	// (the NotifyEdgeRuleChanged handler arm fires wholesale).
	responseCacheAll int
	// remintSurfaces records the surface_ids that received
	// RequestCertForSurface (ADR-100 / issue #879). remintErr
	// makes the call return that error so the test can assert
	// the log-and-swallow path.
	remintSurfaces []string
	remintErr      error
	// resetCorsPresetsAccounts records the account_ids that
	// received ResetCorsPresets (issue #975 #4 PR-B /
	// ADR-129 D4). One entry per NotifyCorsPresetChanged
	// notification — the trigger at migrations/00428 fires
	// pg_notify('cors_preset_changed', account_id) on every
	// cors_presets INSERT / UPDATE / DELETE.
	resetCorsPresetsAccounts []string
	// mirrorRefreshed (issue #72 / ADR-125 PR-A3) records
	// app_ids that received RefreshMirrorRules via a
	// kind="mirror" deployment_changed notify. Paired with
	// `refreshed` for the kind=traffic / kind="" discriminator.
	mirrorRefreshed []string
	// liveRefreshed records running-instance refreshes. Service
	// replicas are admitted out-of-band by schedd and must be merged
	// into an already-warm picker.
	liveRefreshed []string
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
func (f *fakeInvalidator) InvalidatePublicAuth() {
	f.mu.Lock()
	f.publicAuthCnt++
	f.mu.Unlock()
}
func (f *fakeInvalidator) ResetEdgeRules() {
	f.mu.Lock()
	f.resetCnt++
	f.mu.Unlock()
}
func (f *fakeInvalidator) ResetApp(appID string) {
	f.mu.Lock()
	f.resetApps = append(f.resetApps, appID)
	f.mu.Unlock()
}
func (f *fakeInvalidator) InvalidateResponseCacheByApp(appID string) {
	f.mu.Lock()
	f.responseCacheByApp = append(f.responseCacheByApp, appID)
	f.mu.Unlock()
}
func (f *fakeInvalidator) InvalidateResponseCacheByPath(appID, pathGlob string) error {
	f.mu.Lock()
	f.responseCacheByPath = append(f.responseCacheByPath, struct {
		appID    string
		pathGlob string
	}{appID: appID, pathGlob: pathGlob})
	f.mu.Unlock()
	return nil
}
func (f *fakeInvalidator) InvalidateResponseCacheAll() {
	f.mu.Lock()
	f.responseCacheAll++
	f.mu.Unlock()
}
func (f *fakeInvalidator) RefreshDeploymentWeights(_ context.Context, appID string) error {
	f.mu.Lock()
	f.refreshed = append(f.refreshed, appID)
	f.mu.Unlock()
	return nil
}
func (f *fakeInvalidator) RefreshMirrorRules(_ context.Context, appID string) error {
	f.mu.Lock()
	f.mirrorRefreshed = append(f.mirrorRefreshed, appID)
	f.mu.Unlock()
	return nil
}
func (f *fakeInvalidator) RefreshLiveTargets(_ context.Context, appID string) error {
	f.mu.Lock()
	f.liveRefreshed = append(f.liveRefreshed, appID)
	f.mu.Unlock()
	return nil
}
func (f *fakeInvalidator) RequestCertForSurface(_ context.Context, surfaceID string) error {
	f.mu.Lock()
	f.remintSurfaces = append(f.remintSurfaces, surfaceID)
	err := f.remintErr
	f.mu.Unlock()
	return err
}
func (f *fakeInvalidator) ResetCorsPresets(accountID string) {
	f.mu.Lock()
	f.resetCorsPresetsAccounts = append(f.resetCorsPresetsAccounts, accountID)
	f.mu.Unlock()
}

func TestHandleInvalidation(t *testing.T) {
	f := &fakeInvalidator{}
	log := testLogger()

	handleInvalidation(context.Background(), f, db.Notification{Channel: db.NotifyInstanceChanged, Payload: `{"instance_id":"i-1","app_id":"app-7","state":"parked"}`}, log)
	// ADR-091 amendment: NotifyAppChanged payload is the app_id verbatim
	// (apps_maintenance_mode_notify emits NEW.id::text — see
	// migrations/00221_apps_maintenance_mode.sql). The handler drops
	// only that app from the apps LRU (ResetApp), not wholesale
	// FlushRoutes. Old {"app_id":...} payload also still works
	// through the same arm (see TestHandleInvalidation_LegacyAppChangedPayload
	// below for the wholesale fallback).
	handleInvalidation(context.Background(), f, db.Notification{Channel: db.NotifyAppChanged, Payload: "app-7"}, log)
	handleInvalidation(context.Background(), f, db.Notification{Channel: db.NotifyDomainChanged, Payload: `{"domain":"x.io"}`}, log)
	// Malformed instance payload → no evict, no panic.
	handleInvalidation(context.Background(), f, db.Notification{Channel: db.NotifyInstanceChanged, Payload: `not json`}, log)
	// instance payload missing instance_id → no evict.
	handleInvalidation(context.Background(), f, db.Notification{Channel: db.NotifyInstanceChanged, Payload: `{"app_id":"app-7"}`}, log)
	// Unknown channel → ignored.
	handleInvalidation(context.Background(), f, db.Notification{Channel: "other", Payload: `{}`}, log)

	f.mu.Lock()
	defer f.mu.Unlock()
	if got, want := f.evicted["i-1"], "app-7"; got != want {
		t.Errorf("evicted[i-1] = %q, want %q", got, want)
	}
	if len(f.evicted) != 1 {
		t.Errorf("evicted map = %v, want 1 entry", f.evicted)
	}
	// FlushRoutes fires only for NotifyDomainChanged (1x) — the
	// ADR-091 amendment moved the NotifyAppChanged arm off the
	// wholesale path so a maintenance_mode flip on a single app
	// doesn't evict every other app's cache entry.
	if f.flushCnt != 1 {
		t.Errorf("flush count = %d, want 1 (domain only; NotifyAppChanged uses ResetApp)", f.flushCnt)
	}
	if len(f.resetApps) != 1 || f.resetApps[0] != "app-7" {
		t.Errorf("resetApps = %v, want [app-7]", f.resetApps)
	}
}

// TestHandleInvalidation_DeploymentChangedRefreshesWeights (issue #556 /
// PR-B) — a db.NotifyDeploymentChanged event must trigger
// RefreshDeploymentWeights on the picker so a `faas traffic set`
// takes effect within ~1s. Malformed / empty payloads are
// logged-and-dropped rather than crashing the edge loop.
func TestHandleInvalidation_DeploymentChangedRefreshesWeights(t *testing.T) {
	f := &fakeInvalidator{}
	log := testLogger()

	// Happy path: valid payload → refresh.
	handleInvalidation(context.Background(), f, db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"app-7","deployment_id":"dep-3"}`,
	}, log)
	// Malformed payload → no refresh, no panic.
	handleInvalidation(context.Background(), f, db.Notification{Channel: db.NotifyDeploymentChanged, Payload: `not json`}, log)
	// Empty app_id → no refresh.
	handleInvalidation(context.Background(), f, db.Notification{Channel: db.NotifyDeploymentChanged, Payload: `{"app_id":"","deployment_id":"d-1"}`}, log)

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.refreshed) != 1 {
		t.Fatalf("refreshed = %v, want 1 entry (app-7)", f.refreshed)
	}
	if f.refreshed[0] != "app-7" {
		t.Errorf("refreshed[0] = %q, want app-7", f.refreshed[0])
	}
	if len(f.responseCacheByApp) != 1 || f.responseCacheByApp[0] != "app-7" {
		t.Errorf("responseCacheByApp = %v, want [app-7]", f.responseCacheByApp)
	}
}

func TestHandleInvalidation_CachePurge(t *testing.T) {
	f := &fakeInvalidator{}
	log := testLogger()
	handleInvalidation(context.Background(), f, db.Notification{
		Channel: db.NotifyCachePurge,
		Payload: `{"app_id":"app-7","path_glob":"/products/*"}`,
	}, log)
	handleInvalidation(context.Background(), f, db.Notification{
		Channel: db.NotifyCachePurge,
		Payload: `not json`,
	}, log)

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.responseCacheByPath) != 1 {
		t.Fatalf("responseCacheByPath = %v, want one entry", f.responseCacheByPath)
	}
	if got := f.responseCacheByPath[0]; got.appID != "app-7" || got.pathGlob != "/products/*" {
		t.Errorf("cache purge = %+v, want app-7 /products/*", got)
	}
}

// TestHandleInvalidation_CorsPresetChanged (issue #975 #4 PR-B /
// ADR-129 D4) — a db.NotifyCorsPresetChanged event must trigger
// ResetCorsPresets on the invalidator so the per-host edge-rule
// LRU drops the account's compiled CORS slices and the next
// request recompiles against the up-to-date preset row. The
// trigger at migrations/00428 fires on every cors_presets
// INSERT / UPDATE / DELETE; the payload is the bare account_id
// verbatim (NEW.account_id::text on INSERT/UPDATE, OLD on
// DELETE). Empty payload is logged-and-dropped: a future trigger
// without a payload falls back to no-op rather than crash.
func TestHandleInvalidation_CorsPresetChanged(t *testing.T) {
	f := &fakeInvalidator{}
	log := testLogger()

	// Happy path: valid account_id payload → reset recorded.
	handleInvalidation(context.Background(), f, db.Notification{
		Channel: db.NotifyCorsPresetChanged,
		Payload: "acc-42",
	}, log)
	// Empty payload → no reset, no panic. (The trigger always
	// emits NEW/OLD.account_id; this is the defensive path for
	// any future trigger that forgets the payload.)
	handleInvalidation(context.Background(), f, db.Notification{Channel: db.NotifyCorsPresetChanged, Payload: ""}, log)

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.resetCorsPresetsAccounts) != 1 {
		t.Fatalf("resetCorsPresetsAccounts = %v, want 1 entry (acc-42)", f.resetCorsPresetsAccounts)
	}
	if f.resetCorsPresetsAccounts[0] != "acc-42" {
		t.Errorf("resetCorsPresetsAccounts[0] = %q, want acc-42", f.resetCorsPresetsAccounts[0])
	}
}

// TestHandleInvalidation_EdgeRuleChanged (ADR-089 / issue #561 PR 3)
// pins the new arm: any db.NotifyEdgeRuleChanged event must trigger
// ResetEdgeRules on the invalidator so the per-host edge-rule LRU
// is dropped wholesale. Payload contents are intentionally dropped —
// the cache is per-host and the matcher can't surgical-evict without
// the rule's match_host. Malformed / unknown channels are no-ops.
func TestHandleInvalidation_EdgeRuleChanged(t *testing.T) {
	f := &fakeInvalidator{}
	log := testLogger()

	handleInvalidation(context.Background(), f, db.Notification{
		Channel: db.NotifyEdgeRuleChanged,
		Payload: `{"app_id":"app-7","rule_id":"r-1"}`,
	}, log)
	// Malformed payload → still reset (wholesale, payload-agnostic).
	handleInvalidation(context.Background(), f, db.Notification{
		Channel: db.NotifyEdgeRuleChanged,
		Payload: `not json`,
	}, log)

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.resetCnt != 2 {
		t.Errorf("resetCnt = %d, want 2", f.resetCnt)
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
		handleInvalidation(context.Background(), f, db.Notification{Channel: db.NotifyInstanceChanged, Payload: payload}, log)

		f.mu.Lock()
		evicted := len(f.evicted)
		f.mu.Unlock()
		if evicted != 0 {
			t.Errorf("state=%q: evicted %d entries, want 0 (cache-self-destruct guard)", state, evicted)
		}
	}
}

func TestHandleInvalidation_RunningRefreshesLiveTargets(t *testing.T) {
	f := &fakeInvalidator{}
	handleInvalidation(context.Background(), f, db.Notification{
		Channel: db.NotifyInstanceChanged,
		Payload: `{"instance_id":"service-2","app_id":"app-9","state":"running"}`,
	}, testLogger())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.liveRefreshed) != 1 || f.liveRefreshed[0] != "app-9" {
		t.Fatalf("liveRefreshed = %v, want [app-9]", f.liveRefreshed)
	}
	if len(f.evicted) != 0 {
		t.Fatalf("running refresh evicted targets: %v", f.evicted)
	}
}

// TestHandleInvalidation_TenantSurfaceChanged (ADR-100 / issue
// #879) pins the cert-remint dispatch: a db.NotifyTenantSurfaceChanged
// event must trigger RequestCertForSurface with the bare surface
// uuid as the argument. Errors are logged-and-swallowed so a
// transient CA failure can't block the notify loop. The test
// exercises both the success path and a remintErr return.
func TestHandleInvalidation_TenantSurfaceChanged(t *testing.T) {
	log := testLogger()

	// Success: the bare surface uuid is forwarded verbatim.
	f := &fakeInvalidator{}
	handleInvalidation(context.Background(), f, db.Notification{
		Channel: db.NotifyTenantSurfaceChanged,
		Payload: "srf-abc-123",
	}, log)
	f.mu.Lock()
	if len(f.remintSurfaces) != 1 || f.remintSurfaces[0] != "srf-abc-123" {
		t.Errorf("remintSurfaces = %v, want [srf-abc-123]", f.remintSurfaces)
	}
	f.mu.Unlock()

	// remintErr is non-nil → handler logs and swallows; the next
	// event in the queue still dispatches.
	f = &fakeInvalidator{remintErr: errors.New("ca outage")}
	handleInvalidation(context.Background(), f, db.Notification{
		Channel: db.NotifyTenantSurfaceChanged,
		Payload: "srf-second",
	}, log)
	f.mu.Lock()
	if len(f.remintSurfaces) != 1 || f.remintSurfaces[0] != "srf-second" {
		t.Errorf("remintSurfaces (err path) = %v, want [srf-second]", f.remintSurfaces)
	}
	f.mu.Unlock()
}

// TestHandleInvalidation_TerminalStatesEvict (issue #168 + Tier A5 /
// ADR-066) verifies the companion to the lifecycle test: terminal-ish
// states (stopped, failed, parked, snapshotting, migrating) DO evict so
// the next request re-admits on a different node / wakes fresh.
//
// Tier A5: state='migrating' is the cross-node live-instance handoff
// state (Phase 2 of the four-phase commit at pkg/sched/migration_handoff.go).
// Evicting on this state prevents the picker from routing traffic to a
// node whose VM is mid-Park-then-destroy — the next request must
// re-admit which lands on the destination's wake path.
func TestHandleInvalidation_TerminalStatesEvict(t *testing.T) {
	for _, state := range []string{"stopped", "failed", "parked", "snapshotting", "migrating"} {
		f := &fakeInvalidator{}
		log := testLogger()
		payload := `{"instance_id":"i-term","app_id":"app-9","state":"` + state + `"}`
		handleInvalidation(context.Background(), f, db.Notification{Channel: db.NotifyInstanceChanged, Payload: payload}, log)

		f.mu.Lock()
		got := f.evicted["i-term"]
		f.mu.Unlock()
		if got != "app-9" {
			t.Errorf("state=%q: evicted[i-term] = %q, want app-9", state, got)
		}
	}
}

// TestAccountRateLimit_TenOhOneReturns429 — ADR-040 / issue #292
// acceptance: 1001 requests from the same account in 60s, the 1001st
// returns 429. Wires the production gateway handler against a real
// pgRouter + MemStore + PGBackend so the AccountID plumbing is exercised
// end-to-end (issue #292 threat model: botnet rotating across one
// customer's apps). The per-app limiter is bypassed so the test isolates
// the per-account scope; the per-account limiter uses a frozen clock so
// 1001 sequential requests don't refill mid-loop (Free plan: 50/min RPM
// burst = 50 tokens).
func TestAccountRateLimit_TenOhOneReturns429(t *testing.T) {
	store := state.NewMemStore()
	app := seedApp(t, store, "ratelimited", api.PlanFree) // Free: per-account burst 50
	ctx := context.Background()

	router := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}
	// Pre-resolve so the apps cache is warm — the test focuses on the
	// rate-limit path, not on cold-start latency.
	if _, ok, err := router.ResolveHost(ctx, "ratelimited.apps.gregale.dev"); !ok || err != nil {
		t.Fatalf("pre-resolve: ok=%v err=%v", ok, err)
	}

	// Real upstream the legacy proxy can talk to (no real Firecracker
	// here — the test only needs the proxy path to return 200 so the
	// 50 burst requests succeed and the 951 429s fire on the per-account
	// scope).
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(upstream.Close)

	// FakeScheduler's nodeID is what the legacy proxy dials. Point it
	// at the upstream listener's address so the 50 successful requests
	// actually proxy and return 200.
	sched := gateway.NewFakeScheduler(upstream.Listener.Addr().String()).
		WithInstanceID("i-rl").
		WithWakeID("w-rl")
	backend := gateway.NewPGBackend(router, sched, testLogger())

	// Frozen clock so the per-account bucket doesn't refill during 1001
	// sequential requests. Without this the Free plan would refill
	// 50/60 ≈ 0.83 tokens/sec and the test would race.
	frozen := time.Now()
	acctLim := gateway.NewLimiterWithClock(func() time.Time { return frozen })

	h := gateway.NewHandlerWith(backend, gateway.NewMetrics(), testLogger())
	h.SetWakeGateHook()
	h.WithLimiter(unlimitedLimiterForTest()) // bypass per-app scope
	h.WithAccountLimiter(acctLim)            // frozen-clock per-account

	// Drive 1001 requests sequentially. The first 50 succeed (Free burst),
	// 51..1001 all 429 with x-faas-rate-limit-scope: account.
	var (
		ok200   int
		ok429   int
		last429 *httptest.ResponseRecorder
	)
	for i := 0; i < 1001; i++ {
		req := httptest.NewRequest("GET", "http://ratelimited.apps.gregale.dev/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		switch rec.Code {
		case http.StatusOK:
			ok200++
		case http.StatusTooManyRequests:
			ok429++
			last429 = rec
		default:
			t.Fatalf("request %d: status = %d, want 200 or 429", i, rec.Code)
		}
	}
	// 50 successful (Free burst), 951 429s.
	if ok200 != 50 {
		t.Errorf("ok200 = %d, want 50 (Free per-account burst)", ok200)
	}
	if ok429 != 951 {
		t.Errorf("ok429 = %d, want 951", ok429)
	}
	if last429 == nil {
		t.Fatal("last429 not captured")
	}
	if last429.Header().Get("x-faas-rate-limit-scope") != "account" {
		t.Errorf("1001st request should carry x-faas-rate-limit-scope: account; got %q",
			last429.Header().Get("x-faas-rate-limit-scope"))
	}
	if last429.Header().Get("Retry-After") == "" {
		t.Error("429 should carry Retry-After")
	}

	// Scrape the metrics registry and confirm the per-account counter
	// incremented for this account (at least 951 — could be more if a
	// follow-up scrape happens).
	mrec := httptest.NewRecorder()
	mreq := httptest.NewRequest("GET", "/metrics", nil)
	h.Metrics().Handler().ServeHTTP(mrec, mreq)
	body := mrec.Body.String()
	want := `gateway_per_account_rate_limited_total{account_id="` + app.AccountID + `",plan="free"} `
	if !strings.Contains(body, want) {
		t.Errorf("missing exposition line %q in body:\n%s", want, body)
	}
}

// unlimitedLimiterForTest is the per-app noop limiter used by the 1001-request
// acceptance test so the per-app bucket can't 429 the test before the
// per-account scope is exercised. Mirrors pkg/gateway/limiters_test.go's
// unlimitedLimiter but lives here because cmd/gatewayd-internal tests are
// `package main` and can't import test-only helpers from pkg/gateway's
// _test.go files.
func unlimitedLimiterForTest() *gateway.Limiter {
	return gateway.NewLimiter().WithNoop()
}
