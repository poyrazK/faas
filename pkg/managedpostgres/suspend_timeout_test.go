package managedpostgres

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
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

func (p *suspendTimeoutProvider) ConvergeSuspendTimeout(_ context.Context, id string) (SuspendTimeoutResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	switch {
	case p.current[id] == p.seconds || p.current[id] == -1:
		return SuspendTimeoutConverged, nil
	case p.running[id]:
		return SuspendTimeoutDeferred, nil
	}
	p.current[id] = p.seconds
	return SuspendTimeoutChanged, nil
}

type suspendTimeoutStore struct{ databases []Database }

func (s suspendTimeoutStore) ListUsageDatabases(_ context.Context, after UsageDatabaseCursor, _ int) ([]Database, error) {
	if !after.isZero() {
		return nil, nil
	}
	return s.databases, nil
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

func TestSuspendTimeoutSweepConvergesThenRestsUntilTheSettingChanges(t *testing.T) {
	provider := &suspendTimeoutProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()},
		current: map[string]int64{"a": 300, "b": 300, "c": -1, "d": 300}, running: map[string]bool{"b": true}}
	registry := testRegistry(t, provider, nil)
	backend := registry.backends["primary-a"]
	db := func(id string, state State, scaleToZero bool) Database {
		return Database{ID: id, State: state, ProviderResourceID: id, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
			Spec: Spec{ScaleToZero: scaleToZero}}
	}
	sweeper, err := NewSuspendTimeoutSweeper(registry, suspendTimeoutStore{databases: []Database{
		db("a", StateReady, true), db("b", StateReady, true), db("c", StateReady, false), db("d", StateUpdating, true),
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	summary, err := sweeper.Sweep(ctx)
	if err != nil || summary.Changed != 1 || summary.Deferred != 1 || provider.current["a"] != 120 || provider.current["d"] != 300 {
		t.Fatalf("first sweep = %+v, %v; current = %v", summary, err, provider.current)
	}
	delete(provider.running, "b")
	if summary, err = sweeper.Sweep(ctx); err != nil || summary.Changed != 1 || summary.Converged != 1 {
		t.Fatalf("second sweep = %+v, %v", summary, err)
	}
	if summary, err = sweeper.Sweep(ctx); err != nil || summary.Skipped || summary.Converged != 2 {
		t.Fatalf("confirming sweep = %+v, %v", summary, err)
	}
	calls := provider.calls
	if summary, _ = sweeper.Sweep(ctx); !summary.Skipped || provider.calls != calls {
		t.Fatalf("converged fleet was read again: %+v", summary)
	}
	if err := registry.ApplyLivePolicyJSON(json.RawMessage(`{"suspend_timeout_seconds": 90}`)); err != nil {
		t.Fatal(err)
	}
	if summary, err = sweeper.Sweep(ctx); err != nil || summary.Skipped || summary.Changed != 2 || provider.current["a"] != 90 {
		t.Fatalf("sweep after change = %+v, %v", summary, err)
	}
}
