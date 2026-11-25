package managedpostgres

import (
	"context"
	"sync"
	"testing"
	"time"
)

type wakeStartProvider struct {
	*fakeProvider
	mu      sync.Mutex
	started []string
	done    chan struct{}
}

func (p *wakeStartProvider) StartCompute(_ context.Context, id string) error {
	p.mu.Lock()
	p.started = append(p.started, id)
	p.mu.Unlock()
	p.done <- struct{}{}
	return nil
}

// wakeStartStore claims like the SQL: an opted-in database of the app not
// started within gap.
type wakeStartStore struct {
	*MemoryStore
	mu       sync.Mutex
	enabled  map[string]bool   // database -> opted in
	bindings map[string]string // database -> app
	started  map[string]time.Time
	now      time.Time
	appReads int
	claims   WakeStartClaim
}

func (s *wakeStartStore) SetWakeWithApp(_ context.Context, id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled[id] = enabled
	return nil
}

func (s *wakeStartStore) WakeWithApp(_ context.Context, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, id := range ids {
		if s.enabled[id] {
			out[id] = true
		}
	}
	return out, nil
}

func (s *wakeStartStore) WakeWithAppApps(context.Context) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appReads++
	out := map[string]bool{}
	for id, on := range s.enabled {
		if on {
			out[s.bindings[id]] = true
		}
	}
	return out, nil
}

func (s *wakeStartStore) ClaimWakeStarts(_ context.Context, appID string, gap time.Duration) ([]WakeStartClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []WakeStartClaim
	for id, on := range s.enabled {
		if !on || s.bindings[id] != appID {
			continue
		}
		if last, ok := s.started[id]; ok && s.now.Sub(last) < gap {
			continue
		}
		s.started[id] = s.now
		claim := s.claims
		claim.DatabaseID, claim.ProviderResourceID = id, "provider-"+id
		out = append(out, claim)
	}
	return out, nil
}

func TestWakeStarterStartsOptedInDatabasesOncePerGap(t *testing.T) {
	provider := &wakeStartProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}, done: make(chan struct{}, 8)}
	registry := testRegistry(t, provider, nil)
	backend := registry.backends["primary-a"]
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	store := &wakeStartStore{MemoryStore: NewMemoryStore(), enabled: map[string]bool{"db-1": true, "db-2": false},
		bindings: map[string]string{"db-1": "app-1", "db-2": "app-2"}, started: map[string]time.Time{}, now: now,
		claims: WakeStartClaim{BackendID: backend.ID, BackendFingerprint: backend.Fingerprint}}
	var outcomes []WakeStartOutcome
	var outcomesMu sync.Mutex
	service := &Service{registry: registry, store: store}
	starter := service.NewWakeStarter(nil, func(outcome WakeStartOutcome) {
		outcomesMu.Lock()
		outcomes = append(outcomes, outcome)
		outcomesMu.Unlock()
	})
	starter.now = func() time.Time { return now }
	ctx := context.Background()
	wait := func() {
		t.Helper()
		select {
		case <-provider.done:
		case <-time.After(5 * time.Second):
			t.Fatal("no start")
		}
	}

	// Not a wake, a job, another app, an app without an opted-in database:
	// nothing starts.
	for _, payload := range []string{
		`{"app_id":"app-1","state":"parked"}`,
		`{"instance_id":"i","state":"waking","kind":"job"}`,
		`{"app_id":"app-2","state":"waking"}`,
		`not json`,
	} {
		starter.Handle(ctx, payload)
	}
	starter.Handle(ctx, `{"app_id":"app-1","state":"waking"}`)
	wait()
	// The same wake's later transitions, inside the gap, start nothing more.
	starter.Handle(ctx, `{"app_id":"app-1","state":"running"}`)
	store.now = now.Add(30 * time.Second)
	starter.Handle(ctx, `{"app_id":"app-1","state":"cold_booting"}`)
	// After the gap the next wake starts it again.
	store.now = now.Add(WakeStartGap + time.Second)
	starter.Handle(ctx, `{"app_id":"app-1","state":"waking"}`)
	wait()
	select {
	case <-provider.done:
		t.Fatal("started more than twice")
	case <-time.After(50 * time.Millisecond):
	}
	provider.mu.Lock()
	started := append([]string(nil), provider.started...)
	provider.mu.Unlock()
	if len(started) != 2 || started[0] != "provider-db-1" {
		t.Fatalf("started = %v", started)
	}
	// The app list is read once and reused within its refresh.
	if store.appReads != 1 {
		t.Fatalf("app list reads = %d, want 1", store.appReads)
	}
	outcomesMu.Lock()
	defer outcomesMu.Unlock()
	if len(outcomes) != 2 || outcomes[0] != WakeStartStarted {
		t.Fatalf("outcomes = %v", outcomes)
	}
}

func TestWakeStarterRereadsAppsAfterOptIn(t *testing.T) {
	store := &wakeStartStore{MemoryStore: NewMemoryStore(), enabled: map[string]bool{}, bindings: map[string]string{"db-1": "app-1"}, started: map[string]time.Time{}}
	service := &Service{store: store}
	starter := service.NewWakeStarter(nil, nil)
	ctx := context.Background()
	if starter.appOptedIn(ctx, "app-1") {
		t.Fatal("app opted in before any database")
	}
	store.enabled["db-1"] = true
	if starter.appOptedIn(ctx, "app-1") {
		t.Fatal("list re-read before its refresh")
	}
	starter.invalidate()
	if !starter.appOptedIn(ctx, "app-1") {
		t.Fatal("opt-in not seen after invalidate")
	}
}
