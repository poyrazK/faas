package managedpostgres

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type suspendTimeoutProvider struct {
	*fakeProvider
	mu      sync.Mutex
	seconds int64
	current map[string]int64
	running map[string]bool
	calls   int
}

func (p *suspendTimeoutProvider) SetSuspendTimeout(seconds int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seconds = seconds
}

func (p *suspendTimeoutProvider) ConvergeSuspendTimeout(_ context.Context, id string, seconds int64) (SuspendTimeoutResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	switch {
	case p.current[id] == seconds || p.current[id] == -1:
		return SuspendTimeoutConverged, nil
	case p.running[id]:
		return SuspendTimeoutDeferred, nil
	}
	p.current[id] = seconds
	return SuspendTimeoutChanged, nil
}

type suspendTimeoutStore struct {
	databases []Database
	custom    map[string]int64
	writes    int
}

func (s *suspendTimeoutStore) ListUsageDatabases(_ context.Context, after UsageDatabaseCursor, _ int) ([]Database, error) {
	if !after.isZero() {
		return nil, nil
	}
	return s.databases, nil
}

func (s *suspendTimeoutStore) SetSuspendTimeout(_ context.Context, id string, seconds *int64) error {
	s.writes++
	if seconds == nil {
		delete(s.custom, id)
		return nil
	}
	s.custom[id] = *seconds
	return nil
}

func (s *suspendTimeoutStore) SuspendTimeouts(_ context.Context, ids []string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, id := range ids {
		if v, ok := s.custom[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

func TestSuspendTimeoutPolicyIsPushedToProvidersAndBounded(t *testing.T) {
	provider := &suspendTimeoutProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}}
	registry := testRegistry(t, provider, nil)
	if provider.seconds != DefaultSuspendTimeoutSeconds || registry.SuspendTimeoutSeconds() != 120 {
		t.Fatalf("startup timeout = %d / %d, want the 120 s default", provider.seconds, registry.SuspendTimeoutSeconds())
	}
	if err := registry.ApplyLivePolicyJSON(json.RawMessage(`{"suspend_timeout_seconds": 300}`)); err != nil {
		t.Fatal(err)
	}
	if provider.seconds != 300 || registry.SuspendTimeoutSeconds() != 300 {
		t.Fatalf("live timeout = %d / %d, want 300", provider.seconds, registry.SuspendTimeoutSeconds())
	}
	for _, bad := range []string{`{"suspend_timeout_seconds": 59}`, `{"suspend_timeout_seconds": 604801}`, `{"suspend_timeout_seconds": -1}`} {
		if err := registry.CheckLivePolicyJSON(json.RawMessage(bad)); err == nil || !strings.Contains(err.Error(), "suspend_timeout_seconds") {
			t.Fatalf("%s accepted: %v", bad, err)
		}
	}
	if err := registry.ApplyLivePolicyJSON(json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if provider.seconds != 120 {
		t.Fatalf("cleared policy timeout = %d, want the default back", provider.seconds)
	}
}

func TestSuspendTimeoutSweepConvergesAndRechecksOnlyChangedDatabases(t *testing.T) {
	provider := &suspendTimeoutProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()},
		current: map[string]int64{"a": 300, "b": 300, "c": -1, "d": 300, "e": 300}, running: map[string]bool{"b": true}}
	registry := testRegistry(t, provider, nil)
	backend := registry.backends["primary-a"]
	db := func(id string, state State, scaleToZero bool) Database {
		return Database{ID: id, State: state, ProviderResourceID: id, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
			Spec: Spec{ScaleToZero: scaleToZero}}
	}
	store := &suspendTimeoutStore{custom: map[string]int64{"e": 600}, databases: []Database{
		db("a", StateReady, true), db("b", StateReady, true), db("c", StateReady, false), db("d", StateUpdating, true), db("e", StateReady, true),
	}}
	sweeper, err := NewSuspendTimeoutSweeper(registry, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	summary, err := sweeper.Sweep(ctx)
	if err != nil || summary.Changed != 2 || summary.Deferred != 1 || provider.current["a"] != 120 || provider.current["e"] != 600 || provider.current["d"] != 300 {
		t.Fatalf("first sweep = %+v, %v; current = %v", summary, err, provider.current)
	}
	// Only the deferred database is read again.
	calls := provider.calls
	if summary, err = sweeper.Sweep(ctx); err != nil || summary.Unchanged != 2 || summary.Deferred != 1 || provider.calls != calls+1 {
		t.Fatalf("second sweep = %+v, %v; calls %d -> %d", summary, err, calls, provider.calls)
	}
	delete(provider.running, "b")
	if summary, err = sweeper.Sweep(ctx); err != nil || summary.Changed != 1 || provider.current["b"] != 120 {
		t.Fatalf("third sweep = %+v, %v", summary, err)
	}
	calls = provider.calls
	if summary, _ = sweeper.Sweep(ctx); summary.Unchanged != 3 || provider.calls != calls {
		t.Fatalf("converged fleet was read again: %+v", summary)
	}
	// A customer's own value sends that database through again.
	seconds := int64(900)
	if err := store.SetSuspendTimeout(ctx, "a", &seconds); err != nil {
		t.Fatal(err)
	}
	if summary, err = sweeper.Sweep(ctx); err != nil || summary.Changed != 1 || provider.current["a"] != 900 {
		t.Fatalf("sweep after customer change = %+v, %v", summary, err)
	}
	// A compute-policy change resets the endpoint to the default and moves
	// the row: the sweep puts the database's own value back.
	provider.current["a"] = 120
	store.databases[0].UpdatedAt = store.databases[0].UpdatedAt.Add(time.Minute)
	if summary, err = sweeper.Sweep(ctx); err != nil || summary.Changed != 1 || provider.current["a"] != 900 {
		t.Fatalf("sweep after row change = %+v, %v; current = %v", summary, err, provider.current)
	}
	// So does the platform default; databases with their own keep it.
	if err := registry.ApplyLivePolicyJSON(json.RawMessage(`{"suspend_timeout_seconds": 90}`)); err != nil {
		t.Fatal(err)
	}
	if summary, err = sweeper.Sweep(ctx); err != nil || summary.Changed != 1 || provider.current["b"] != 90 || provider.current["e"] != 600 || provider.current["a"] != 900 {
		t.Fatalf("sweep after default change = %+v, %v; current = %v", summary, err, provider.current)
	}
}

// A customer's value written after the sweep's batch read wins.
func TestSuspendTimeoutSweepRereadsTheValueBeforeWriting(t *testing.T) {
	provider := &suspendTimeoutProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}, current: map[string]int64{"a": 300}}
	registry := testRegistry(t, provider, nil)
	backend := registry.backends["primary-a"]
	store := &racingSuspendTimeoutStore{suspendTimeoutStore: suspendTimeoutStore{custom: map[string]int64{}, databases: []Database{{ID: "a", State: StateReady,
		ProviderResourceID: "a", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Spec: Spec{ScaleToZero: true}}}}}
	sweeper, err := NewSuspendTimeoutSweeper(registry, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sweeper.Sweep(context.Background()); err != nil || provider.current["a"] != 1800 {
		t.Fatalf("sweep wrote %d over the customer's 1800: %v", provider.current["a"], err)
	}
}

// racingSuspendTimeoutStore answers the batch read before a customer's write
// and every later read after it.
type racingSuspendTimeoutStore struct {
	suspendTimeoutStore
	reads int
}

func (s *racingSuspendTimeoutStore) SuspendTimeouts(ctx context.Context, ids []string) (map[string]int64, error) {
	s.reads++
	if s.reads == 2 {
		s.custom["a"] = 1800
	}
	return s.suspendTimeoutStore.SuspendTimeouts(ctx, ids)
}
func TestCustomerSuspendTimeoutBounds(t *testing.T) {
	for seconds, want := range map[int64]bool{59: false, 60: true, 120: true, 3600: true, 3601: false} {
		if got := ValidCustomerSuspendTimeout(seconds); got != want {
			t.Errorf("ValidCustomerSuspendTimeout(%d) = %v, want %v", seconds, got, want)
		}
	}
}
