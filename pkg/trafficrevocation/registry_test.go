// adr: 570
package trafficrevocation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type testStore struct {
	mu     sync.Mutex
	states map[Scope]State
	err    error
	wait   bool
}

func (s *testStore) Read(ctx context.Context, scopes []Scope) (map[Scope]State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	result := make(map[Scope]State)
	for _, scope := range scopes {
		result[scope] = s.states[scope]
	}
	return result, s.err
}

func (s *testStore) set(scope Scope, state State, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[scope], s.err = state, err
}

func (s *testStore) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

type countingStore struct {
	*testStore
	reads atomic.Int64
}

func (s *countingStore) Read(ctx context.Context, scopes []Scope) (map[Scope]State, error) {
	s.reads.Add(1)
	return s.testStore.Read(ctx, scopes)
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func admitted(t *testing.T, r *Registry, scopes ...Scope) (context.Context, func()) {
	t.Helper()
	ctx, cancel := context.WithCancelCause(t.Context())
	t.Cleanup(func() { cancel(nil) })
	release, err := r.Admit(ctx, scopes, cancel)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, release
}

func TestRegistryRetainsPinnedGenerationAndUnregistersAfterCleanup(t *testing.T) {
	scope := Scope{"app", "app-a"}
	store := &testStore{states: map[Scope]State{scope: {Revision: 1}}}
	registry := New(store)
	defer registry.Close()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	release, err := registry.Admit(ctx, []Scope{scope, scope}, cancel)
	if err != nil {
		t.Fatal(err)
	}
	if requests, scopes := registry.Tracked(); requests != 1 || scopes != 1 {
		t.Fatalf("tracked=%d/%d, want one deduplicated exchange/scope", requests, scopes)
	}
	if err := registry.Refresh(t.Context()); err != nil || ctx.Err() != nil {
		t.Fatalf("unchanged generation canceled a pinned request: %v / %v", err, ctx.Err())
	}
	store.set(scope, State{Revision: 2, Revoked: true}, nil)
	if err := registry.Refresh(t.Context()); err != nil || !errors.Is(context.Cause(ctx), ErrRevoked) {
		t.Fatalf("revocation did not reach the owner: %v / %v", err, context.Cause(ctx))
	}
	if requests, _ := registry.Tracked(); requests != 1 {
		t.Fatal("cancellation released ownership before cleanup")
	}
	release()
	release()
	if requests, scopes := registry.Tracked(); requests != 0 || scopes != 0 {
		t.Fatalf("cleanup leaked registration: %d/%d", requests, scopes)
	}
}

func TestRegistryChangedAndUnverifiableSnapshotsCancel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		after    State
		storeErr error
		cause    error
	}{
		{"revoked", State{Revision: 2, Revoked: true}, nil, ErrRevoked},
		{"missed-revoke-and-release", State{Revision: 3}, nil, ErrRevoked},
		{"regression", State{Revision: 0}, nil, ErrUnavailable},
		{"conflicting-equal-generation", State{Revision: 1, Revoked: true}, nil, ErrUnavailable},
		{"store-outage", State{}, errors.New("store disconnected"), ErrUnavailable},
		{"invalid-snapshot", State{Revision: -1}, nil, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := Scope{"account", "owner"}
			other := Scope{"account", "unrelated"}
			store := &testStore{states: map[Scope]State{scope: {Revision: 1}, other: {Revision: 1}}}
			clock := newFakeClock()
			registry := New(store).WithClock(clock.Now)
			defer registry.Close()
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			release, err := registry.Admit(ctx, []Scope{scope}, cancel)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			otherCtx, otherCancel := context.WithCancelCause(t.Context())
			defer otherCancel(nil)
			otherRelease, err := registry.Admit(otherCtx, []Scope{other}, otherCancel)
			if err != nil {
				t.Fatal(err)
			}
			defer otherRelease()
			store.set(scope, tc.after, tc.storeErr)
			_ = registry.Refresh(t.Context())
			if tc.storeErr != nil {
				// ADR-20261026160000: an outage keeps verified traffic running
				// until the outage grace runs out.
				if ctx.Err() != nil || otherCtx.Err() != nil {
					t.Fatal("a store outage canceled traffic inside the outage grace")
				}
				clock.Advance(api.TrafficSecurityOutageGrace + time.Second)
				_ = registry.Refresh(t.Context())
			}
			if !errors.Is(context.Cause(ctx), tc.cause) {
				t.Fatalf("cause=%v, want %v", context.Cause(ctx), tc.cause)
			}
			if (tc.storeErr == nil && tc.after.Revision >= 0) && otherCtx.Err() != nil {
				t.Fatal("another account's generation canceled unrelated traffic")
			}
		})
	}
}

func TestRegistryRefusesRevokedAdmissionAndCancelsEarlierRequest(t *testing.T) {
	scope := Scope{"app", "app-a"}
	store := &testStore{states: make(map[Scope]State)}
	registry := New(store)
	defer registry.Close()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	release, err := registry.Admit(ctx, []Scope{scope}, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	store.set(scope, State{Revision: 1, Revoked: true}, nil)
	// A tracked scope admits from the refreshed view; the refresher (every
	// second, or on a notification) is what discovers the revoke.
	if err := registry.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Admit(t.Context(), []Scope{scope}, func(error) {}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked admission=%v", err)
	}
	if !errors.Is(context.Cause(ctx), ErrRevoked) {
		t.Fatal("new admission discovered a revoke without canceling older traffic")
	}
	store.set(scope, State{Revision: 2}, nil)
	if err := registry.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	newCtx, newCancel := context.WithCancelCause(t.Context())
	defer newCancel(nil)
	newRelease, err := registry.Admit(newCtx, []Scope{scope}, newCancel)
	if err != nil {
		t.Fatal(err)
	}
	defer newRelease()
	if newCtx.Err() != nil || !errors.Is(context.Cause(ctx), ErrRevoked) {
		t.Fatal("release revived the old exchange or blocked a newly verified one")
	}
}

func TestRegistryNormalReleaseDoesNotCancelResponseBeforeFinalFlush(t *testing.T) {
	registry := New(&testStore{states: make(map[Scope]State)})
	defer registry.Close()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	release, err := registry.Admit(ctx, []Scope{{"app", "app-a"}}, cancel)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if ctx.Err() != nil {
		t.Fatal("unregister canceled the response before its final buffered flush")
	}
}

func TestRegistryHandoffVerifiesExactAdmittedBaseline(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after State
		want  error
	}{
		{"unchanged", State{Revision: 1}, nil},
		{"revoked", State{Revision: 2, Revoked: true}, ErrRevoked},
		{"missed-revoke-release", State{Revision: 3}, ErrRevoked},
		{"regressed", State{}, ErrUnavailable},
		{"conflicting", State{Revision: 1, Revoked: true}, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := Scope{"app", "app-a"}
			store := &testStore{states: map[Scope]State{scope: {Revision: 1}}}
			compute, public := New(store), New(store)
			defer compute.Close()
			defer public.Close()
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			baseline, release, err := compute.AdmitSnapshot(ctx, []Scope{scope}, cancel)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			store.set(scope, tc.after, nil)
			publicCtx, publicCancel := context.WithCancelCause(t.Context())
			defer publicCancel(nil)
			publicRelease, err := public.AdmitAt(publicCtx, baseline, publicCancel)
			if !errors.Is(err, tc.want) {
				t.Fatalf("handoff=%v, want %v", err, tc.want)
			}
			if err != nil {
				if n, scopes := public.Tracked(); n != 0 || scopes != 0 {
					t.Fatal("refused handoff retained ownership")
				}
				return
			}
			defer publicRelease()
			baseline[scope] = State{Revision: 99} // Neither hop may retain the caller's map.
			if err := compute.Refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := public.Refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			if ctx.Err() != nil || publicCtx.Err() != nil {
				t.Fatal("mutating transferred map changed private baseline")
			}
			store.set(scope, State{Revision: 3}, nil)
			_ = compute.Refresh(t.Context())
			_ = public.Refresh(t.Context())
			if !errors.Is(context.Cause(ctx), ErrRevoked) || !errors.Is(context.Cause(publicCtx), ErrRevoked) {
				t.Fatal("handoff did not retain both admitted baselines")
			}
		})
	}
}

func TestRegistryStoreOperationIsBoundedAndFailureRefusesAdmission(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(fmt.Sprint(refresh), func(t *testing.T) {
			store := &testStore{states: make(map[Scope]State)}
			clock := newFakeClock()
			registry := New(store).WithClock(clock.Now)
			defer registry.Close()
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			scopes := []Scope{{"app", "app-a"}}
			if refresh {
				release, err := registry.Admit(ctx, scopes, cancel)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			store.wait = true
			start := time.Now()
			var err error
			if refresh {
				err = registry.Refresh(t.Context())
			} else {
				_, err = registry.Admit(ctx, scopes, cancel)
			}
			if !errors.Is(err, ErrUnavailable) || time.Since(start) > time.Second {
				t.Fatalf("unbounded/unverified operation: elapsed=%s error=%v", time.Since(start), err)
			}
			if refresh {
				if ctx.Err() != nil {
					t.Fatal("a store timeout inside the outage grace canceled verified traffic")
				}
				clock.Advance(api.TrafficSecurityOutageGrace + time.Second)
				if err := registry.Refresh(t.Context()); !errors.Is(err, ErrUnavailable) || !errors.Is(context.Cause(ctx), ErrUnavailable) {
					t.Fatalf("unverified active traffic survived the outage grace: %v / %v", err, context.Cause(ctx))
				}
			}
		})
	}
}

func TestRegistryBoundsDistinctScopesAndReleasesCapacity(t *testing.T) {
	registry := New(&testStore{states: make(map[Scope]State)})
	defer registry.Close()
	releases := make([]func(), 0, api.TrafficSecurityMaxScopes)
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for i := range api.TrafficSecurityMaxScopes {
		release, err := registry.Admit(t.Context(), []Scope{{"app", fmt.Sprint(i)}}, func(error) {})
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, err := registry.Admit(t.Context(), []Scope{{"app", "extra"}}, func(error) {}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("scope overflow=%v, want bounded refusal", err)
	}
	releases[0]()
	extra, err := registry.Admit(t.Context(), []Scope{{"app", "extra"}}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	extra()
}

func TestRegistryBoundsExchangesAndReportsObservedLimit(t *testing.T) {
	registry := New(&testStore{states: make(map[Scope]State)})
	defer registry.Close()
	releases := make([]func(), 0, api.TrafficSecurityMaxExchanges)
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	scopes := []Scope{{"app", "app-a"}}
	for range api.TrafficSecurityMaxExchanges {
		release, err := registry.Admit(t.Context(), scopes, func(error) {})
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	_, err := registry.Admit(t.Context(), scopes, func(error) {})
	var limit *CapacityError
	if !errors.As(err, &limit) || limit.Limit != api.TrafficSecurityMaxExchanges || limit.Observed != limit.Limit+1 {
		t.Fatalf("exchange overflow lacks actual limit/observed evidence: %v", err)
	}
	releases[0]()
	final, err := registry.Admit(t.Context(), scopes, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	final()
}

func TestRegistryRepairAndShutdown(t *testing.T) {
	scope := Scope{"app", "app-a"}
	store := &testStore{states: make(map[Scope]State)}
	registry := New(store)
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	release, err := registry.Admit(ctx, []Scope{scope}, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	runCtx, stop := context.WithCancel(t.Context())
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); registry.Run(runCtx) }()
	store.set(scope, State{Revision: 1, Revoked: true}, nil)
	registry.RequestRefresh()
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), ErrRevoked) {
			t.Fatal(context.Cause(ctx))
		}
	case <-time.After(time.Second):
		t.Fatal("notification did not request an authoritative repair read")
	}
	stop()
	<-done
	if _, err := registry.Admit(t.Context(), []Scope{{"app", "other"}}, func(error) {}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed registry admitted traffic: %v", err)
	}
}

type delayedSnapshotStore struct {
	*testStore
	holdNext atomic.Bool
	started  chan struct{}
	resume   chan struct{}
}

func (s *delayedSnapshotStore) Read(ctx context.Context, scopes []Scope) (map[Scope]State, error) {
	states, err := s.testStore.Read(ctx, scopes)
	if s.holdNext.Swap(false) {
		close(s.started)
		select {
		case <-s.resume:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return states, err
}

func TestRegistryLateAdmissionCannotRepublishOlderGeneration(t *testing.T) {
	scope := Scope{"app", "app-a"}
	store := &delayedSnapshotStore{testStore: &testStore{states: map[Scope]State{scope: {Revision: 1}}},
		started: make(chan struct{}), resume: make(chan struct{})}
	clock := newFakeClock()
	registry := New(store).WithClock(clock.Now)
	defer registry.Close()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	release, err := registry.Admit(ctx, []Scope{scope}, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// Past the outage grace the cached generation no longer admits, so the
	// late admission reads the store and races the refresh below.
	clock.Advance(api.TrafficSecurityOutageGrace + time.Second)
	store.holdNext.Store(true)
	late := make(chan error, 1)
	go func() {
		lateRelease, err := registry.Admit(t.Context(), []Scope{scope}, func(error) {})
		if lateRelease != nil {
			lateRelease()
		}
		late <- err
	}()
	select {
	case <-store.started:
	case <-time.After(5 * time.Second):
		t.Fatal("a stale cached generation admitted without reading the store")
	}
	store.set(scope, State{Revision: 3}, nil) // a revoke/release pair completed
	if err := registry.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	close(store.resume)
	if err := <-late; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("late old-generation admission=%v, want refusal", err)
	}
	if !errors.Is(context.Cause(ctx), ErrRevoked) {
		t.Fatal("late admission revived the superseded exchange")
	}
}

func TestRegistryWarmScopeAdmitsWithoutStoreRead(t *testing.T) {
	scope := Scope{"app", "app-a"}
	store := &countingStore{testStore: &testStore{states: map[Scope]State{scope: {Revision: 1}}}}
	clock := newFakeClock()
	registry := New(store).WithClock(clock.Now)
	defer registry.Close()
	_, release := admitted(t, registry, scope)
	release()
	if got := store.reads.Load(); got != 1 {
		t.Fatalf("first admission reads=%d, want 1", got)
	}
	_, release = admitted(t, registry, scope)
	release()
	if got := store.reads.Load(); got != 1 {
		t.Fatalf("warm admission read the store: reads=%d", got)
	}
	if err := registry.Refresh(t.Context()); err != nil || store.reads.Load() != 2 {
		t.Fatalf("refresh did not keep the warm scope verified: %v reads=%d", err, store.reads.Load())
	}
	clock.Advance(api.TrafficSecurityWarmScopeTTL + time.Second)
	if err := registry.Refresh(t.Context()); err != nil || store.reads.Load() != 2 {
		t.Fatalf("idle warm scope still refreshed: %v reads=%d", err, store.reads.Load())
	}
	_, release = admitted(t, registry, scope)
	release()
	if got := store.reads.Load(); got != 3 {
		t.Fatalf("a forgotten scope admitted without a read: reads=%d", got)
	}
}

func TestRegistryStoreOutageGrace(t *testing.T) {
	active, warm, cold := Scope{"app", "active"}, Scope{"app", "warm"}, Scope{"app", "cold"}
	store := &testStore{states: map[Scope]State{active: {Revision: 1}, warm: {Revision: 1}, cold: {Revision: 1}}}
	clock := newFakeClock()
	registry := New(store).WithClock(clock.Now)
	defer registry.Close()
	ctx, release := admitted(t, registry, active)
	defer release()
	_, warmRelease := admitted(t, registry, warm)
	warmRelease()

	store.fail(errors.New("control-plane Postgres unreachable"))
	clock.Advance(api.TrafficSecurityOutageGrace - time.Second)
	if err := registry.Refresh(t.Context()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("refresh during outage=%v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("admitted traffic canceled inside the outage grace")
	}
	_, warmRelease = admitted(t, registry, warm)
	warmRelease()
	if _, err := registry.Admit(t.Context(), []Scope{cold}, func(error) {}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("untracked scope admitted without verification: %v", err)
	}

	clock.Advance(2 * time.Second)
	_ = registry.Refresh(t.Context())
	if !errors.Is(context.Cause(ctx), ErrUnavailable) {
		t.Fatalf("admitted traffic outlived the outage grace: %v", context.Cause(ctx))
	}
	if _, err := registry.Admit(t.Context(), []Scope{warm}, func(error) {}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("warm scope admitted past the outage grace: %v", err)
	}

	store.fail(nil)
	if err := registry.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, release2 := admitted(t, registry, warm)
	release2()
}

func TestRegistryHandoffReadsWhenBaselineIsNewerThanCache(t *testing.T) {
	scope := Scope{"app", "app-a"}
	store := &countingStore{testStore: &testStore{states: map[Scope]State{scope: {Revision: 1}}}}
	compute, public := New(store), New(store)
	defer compute.Close()
	defer public.Close()
	_, publicRelease := admitted(t, public, scope) // the public hop caches revision 1
	publicRelease()
	store.set(scope, State{Revision: 2}, nil) // revoke and release land before the next request
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	baseline, release, err := compute.AdmitSnapshot(ctx, []Scope{scope}, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	reads := store.reads.Load()
	handoff, err := public.AdmitAt(t.Context(), baseline, func(error) {})
	if err != nil {
		t.Fatalf("baseline newer than the public cache was refused: %v", err)
	}
	handoff()
	if store.reads.Load() != reads+1 {
		t.Fatal("public hop admitted a newer baseline without reading the store")
	}
}

func TestRegistryInconsistentRefreshUnverifiesCache(t *testing.T) {
	scope := Scope{"app", "app-a"}
	store := &testStore{states: map[Scope]State{scope: {Revision: 2}}}
	registry := New(store)
	defer registry.Close()
	_, release := admitted(t, registry, scope)
	release()
	store.set(scope, State{Revision: 1}, nil) // a regressed store result
	if err := registry.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Admit(t.Context(), []Scope{scope}, func(error) {}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("admission after a regressed refresh=%v, want refusal", err)
	}
}

func TestRegistryWarmScopesGiveWayToActiveOnes(t *testing.T) {
	clock := newFakeClock()
	registry := New(&testStore{states: make(map[Scope]State)}).WithClock(clock.Now)
	defer registry.Close()
	for i := range api.TrafficSecurityMaxScopes {
		clock.Advance(time.Millisecond)
		_, release := admitted(t, registry, Scope{"app", fmt.Sprint(i)})
		release()
	}
	clock.Advance(time.Millisecond)
	_, release := admitted(t, registry, Scope{"app", "new"})
	defer release()
	registry.mu.Lock()
	tracked := len(registry.latest)
	_, oldestKept := registry.latest[Scope{"app", "0"}]
	registry.mu.Unlock()
	if tracked > api.TrafficSecurityMaxScopes || oldestKept {
		t.Fatalf("tracked=%d oldest warm kept=%v; a refresh would exceed the store bound", tracked, oldestKept)
	}
}

func TestRegistryRevokedCacheConfirmsWithStore(t *testing.T) {
	scope := Scope{"app", "app-a"}
	store := &countingStore{testStore: &testStore{states: map[Scope]State{scope: {Revision: 1}}}}
	registry := New(store)
	defer registry.Close()
	_, release := admitted(t, registry, scope)
	release()
	store.set(scope, State{Revision: 2, Revoked: true}, nil)
	if err := registry.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	reads := store.reads.Load()
	if _, err := registry.Admit(t.Context(), []Scope{scope}, func(error) {}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked admission=%v", err)
	}
	if store.reads.Load() != reads+1 {
		t.Fatal("a cached revoke refused without confirming with the store")
	}
	store.set(scope, State{Revision: 3}, nil) // released
	_, release = admitted(t, registry, scope)
	release()
}
