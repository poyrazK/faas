// adr: 570
package publicrouting

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeStore struct {
	mu      sync.Mutex
	views   map[Key]state.PublicRoutingViewResult
	err     error
	reads   [][]Key
	block   chan struct{} // when set, a read waits for it
	entered chan struct{}
}

func newFakeStore() *fakeStore {
	return &fakeStore{views: map[Key]state.PublicRoutingViewResult{}}
}

func (s *fakeStore) ReadPublicRoutingViews(ctx context.Context, keys []Key) (map[Key]state.PublicRoutingViewResult, error) {
	s.mu.Lock()
	s.reads = append(s.reads, append([]Key(nil), keys...))
	block, entered := s.block, s.entered
	s.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	results := make(map[Key]state.PublicRoutingViewResult, len(keys))
	for _, key := range keys {
		answer, ok := s.views[key]
		if !ok {
			answer = state.PublicRoutingViewResult{Err: state.ErrNotFound}
		}
		results[key] = answer
	}
	return results, nil
}

func (s *fakeStore) set(key Key, deployment string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.views[key] = state.PublicRoutingViewResult{View: state.PublicRoutingView{
		Weights: []state.ServiceDeploymentWeight{{ID: deployment, TrafficPercent: 100}}, Lane: "production"}}
}

func (s *fakeStore) refuse(key Key, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.views[key] = state.PublicRoutingViewResult{Err: err}
}

func (s *fakeStore) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *fakeStore) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reads)
}

func (s *fakeStore) lastRead() []Key {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads[len(s.reads)-1]
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newKey() Key {
	return Key{AppID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(), Scope: "production"}
}

func newTestRegistry(store *fakeStore) (*Registry, *clock) {
	c := &clock{now: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}
	return New(store).WithClock(c.Now), c
}

func viewDeployment(t *testing.T, view state.PublicRoutingView) string {
	t.Helper()
	if len(view.Weights) != 1 {
		t.Fatalf("view weights = %+v", view.Weights)
	}
	return view.Weights[0].ID
}

func TestViewReadsStoreOnceThenAdmitsFromMemory(t *testing.T) {
	store := newFakeStore()
	registry, _ := newTestRegistry(store)
	key := newKey()
	store.set(key, "d1")
	for range 5 {
		view, err := registry.View(t.Context(), key)
		if err != nil || viewDeployment(t, view) != "d1" {
			t.Fatalf("view = %+v err=%v", view, err)
		}
	}
	if store.readCount() != 1 {
		t.Fatalf("warm requests read the store %d times; want only the first", store.readCount())
	}
}

func TestViewAdmitsThroughOutageUntilGrace(t *testing.T) {
	store := newFakeStore()
	registry, c := newTestRegistry(store)
	key := newKey()
	store.set(key, "d1")
	if _, err := registry.View(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	outage := errors.New("connection refused")
	store.fail(outage)
	c.advance(api.TrafficPublicRoutingOutageGrace - time.Second)
	if err := registry.Refresh(t.Context()); !errors.Is(err, outage) {
		t.Fatalf("refresh during outage = %v", err)
	}
	view, err := registry.View(t.Context(), key)
	if err != nil || viewDeployment(t, view) != "d1" {
		t.Fatalf("inside the grace: view=%+v err=%v; want the last verified view", view, err)
	}
	reads := store.readCount()
	c.advance(2 * time.Second)
	if _, err := registry.View(t.Context(), key); !errors.Is(err, outage) {
		t.Fatalf("past the grace: err=%v; want the store's failure", err)
	}
	if store.readCount() != reads+1 {
		t.Fatal("past the grace the view was served without a store read")
	}
	store.fail(nil)
	store.set(key, "d2")
	view, err = registry.View(t.Context(), key)
	if err != nil || viewDeployment(t, view) != "d2" {
		t.Fatalf("after recovery: view=%+v err=%v", view, err)
	}
}

// A refusal is never answered from memory, and a refresh that refuses a key
// drops its view at once, with no outage grace.
func TestViewRefusalIsConfirmedByTheStore(t *testing.T) {
	store := newFakeStore()
	registry, _ := newTestRegistry(store)
	key := newKey()
	for range 2 {
		if _, err := registry.View(t.Context(), key); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("unverified owner: %v", err)
		}
	}
	if store.readCount() != 2 {
		t.Fatalf("refusals read the store %d times; want every time", store.readCount())
	}
	store.set(key, "d1")
	if _, err := registry.View(t.Context(), key); err != nil {
		t.Fatalf("owner verified after a refusal: %v", err)
	}
	bound := errors.New("public routing deployment limit exceeded")
	store.refuse(key, bound)
	if err := registry.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.View(t.Context(), key); !errors.Is(err, bound) {
		t.Fatalf("refused refresh kept serving the view: %v", err)
	}
}

func TestRefreshUpdatesViewsAndForgetsIdleApps(t *testing.T) {
	store := newFakeStore()
	registry, c := newTestRegistry(store)
	busy, idle := newKey(), newKey()
	store.set(busy, "d1")
	store.set(idle, "d1")
	for _, key := range []Key{busy, idle} {
		if _, err := registry.View(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
	store.set(busy, "d2")
	if err := registry.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	reads := store.readCount()
	view, err := registry.View(t.Context(), busy)
	if err != nil || viewDeployment(t, view) != "d2" || store.readCount() != reads {
		t.Fatalf("refreshed view=%+v err=%v reads=%d->%d", view, err, reads, store.readCount())
	}
	c.advance(api.TrafficPublicRoutingWarmTTL - time.Second)
	if _, err := registry.View(t.Context(), busy); err != nil {
		t.Fatal(err)
	}
	c.advance(2 * time.Second)
	if err := registry.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if registry.Tracked() != 1 || len(store.lastRead()) != 1 || store.lastRead()[0] != busy {
		t.Fatalf("tracked=%d last refresh=%+v; want only the app used within the warm TTL", registry.Tracked(), store.lastRead())
	}
}

func TestRefreshAppReadsOnlyThatApp(t *testing.T) {
	store := newFakeStore()
	registry, _ := newTestRegistry(store)
	first, second := newKey(), newKey()
	staging := first
	staging.Scope = "staging"
	for _, key := range []Key{first, second, staging} {
		store.set(key, "d1")
		if _, err := registry.View(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.RefreshApp(t.Context(), first.AppID); err != nil {
		t.Fatal(err)
	}
	read := store.lastRead()
	if len(read) != 2 || read[0].AppID != first.AppID || read[1].AppID != first.AppID {
		t.Fatalf("route refresh read %+v; want both of the app's scopes only", read)
	}
}

func TestRunRefreshesNotifiedAppPromptly(t *testing.T) {
	store := newFakeStore()
	registry, _ := newTestRegistry(store)
	notified, other := newKey(), newKey()
	for _, key := range []Key{notified, other} {
		store.set(key, "d1")
		if _, err := registry.View(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
	store.set(notified, "d2")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go registry.Run(ctx)
	registry.RequestRefresh(notified.AppID)
	deadline := time.Now().Add(api.TrafficPublicRoutingRefreshInterval / 2)
	for {
		view, err := registry.View(t.Context(), notified)
		if err == nil && viewDeployment(t, view) == "d2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("notified app was not refreshed before the periodic refresh")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if read := store.lastRead(); len(read) != 1 || read[0] != notified {
		t.Fatalf("notification refresh read %+v; want only the notified app", read)
	}
}

// Concurrent requests for one cold app share one store read.
func TestViewSharesConcurrentReads(t *testing.T) {
	store := newFakeStore()
	registry, _ := newTestRegistry(store)
	key := newKey()
	store.set(key, "d1")
	store.block, store.entered = make(chan struct{}), make(chan struct{}, 1)
	var wg sync.WaitGroup
	var failures atomic.Int32
	for range 8 {
		wg.Go(func() {
			if _, err := registry.View(t.Context(), key); err != nil {
				failures.Add(1)
			}
		})
	}
	<-store.entered
	time.Sleep(20 * time.Millisecond) // let the other callers join the read
	close(store.block)
	wg.Wait()
	if failures.Load() != 0 || store.readCount() != 1 {
		t.Fatalf("failures=%d reads=%d; want one shared read", failures.Load(), store.readCount())
	}
}

// A refresh that began before a newer read must not bring back its older view.
func TestOlderReadDoesNotReplaceNewerView(t *testing.T) {
	store := newFakeStore()
	registry, c := newTestRegistry(store)
	key := newKey()
	store.set(key, "d1")
	if _, err := registry.View(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	store.block, store.entered = release, make(chan struct{}, 2)
	refreshed := make(chan error, 1)
	go func() { refreshed <- registry.Refresh(t.Context()) }()
	<-store.entered // the refresh read began with d1 in the store
	c.advance(api.TrafficPublicRoutingOutageGrace + time.Second)
	store.mu.Lock()
	store.views[key] = state.PublicRoutingViewResult{View: state.PublicRoutingView{Weights: []state.ServiceDeploymentWeight{{ID: "d2", TrafficPercent: 100}}}}
	store.block = nil
	store.mu.Unlock()
	view, err := registry.View(t.Context(), key)
	if err != nil || viewDeployment(t, view) != "d2" {
		t.Fatalf("newer read: view=%+v err=%v", view, err)
	}
	store.mu.Lock()
	store.views[key] = state.PublicRoutingViewResult{View: state.PublicRoutingView{Weights: []state.ServiceDeploymentWeight{{ID: "d1", TrafficPercent: 100}}}}
	store.mu.Unlock()
	close(release)
	if err := <-refreshed; err != nil {
		t.Fatal(err)
	}
	view, err = registry.View(t.Context(), key)
	if err != nil || viewDeployment(t, view) != "d2" {
		t.Fatalf("an older refresh replaced the newer view: %+v err=%v", view, err)
	}
}

func TestViewBoundsTrackedApps(t *testing.T) {
	store := newFakeStore()
	registry, c := newTestRegistry(store)
	first := newKey()
	store.set(first, "d1")
	if _, err := registry.View(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	for range api.TrafficPublicRoutingMaxViews {
		c.advance(time.Millisecond)
		key := newKey()
		store.set(key, "d1")
		if _, err := registry.View(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
	if registry.Tracked() != api.TrafficPublicRoutingMaxViews {
		t.Fatalf("tracked=%d; want the bound %d", registry.Tracked(), api.TrafficPublicRoutingMaxViews)
	}
	reads := store.readCount()
	if _, err := registry.View(t.Context(), first); err != nil || store.readCount() != reads+1 {
		t.Fatal("the least recently used view was not the one dropped")
	}
}

func TestViewRefusesMalformedOwnerWithoutRead(t *testing.T) {
	store := newFakeStore()
	registry, _ := newTestRegistry(store)
	valid := newKey()
	for _, key := range []Key{
		{AppID: "not-a-uuid", AccountID: valid.AccountID},
		{AppID: valid.AppID, AccountID: ""},
		{AppID: valid.AppID, AccountID: valid.AccountID, ProjectID: "x"},
		{AppID: uuid.Nil.String(), AccountID: valid.AccountID},
		{AppID: "{" + valid.AppID + "}", AccountID: valid.AccountID},
	} {
		if _, err := registry.View(t.Context(), key); err == nil {
			t.Fatalf("malformed key %+v admitted", key)
		}
	}
	if store.readCount() != 0 {
		t.Fatal("a malformed owner reached the store")
	}
}
