// adr: 389 — read-only database health and durable observation fencing.

package managedpostgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type healthTestProvider struct {
	*fakeProvider
	observe func(context.Context, string) (ObservedDatabase, error)
}

func (p *healthTestProvider) Observe(ctx context.Context, id string) (ObservedDatabase, error) {
	return p.observe(ctx, id)
}

func readyHealthDatabase(t *testing.T, store *MemoryStore, registry *Registry, now time.Time) Database {
	t.Helper()
	backend, err := registry.Default(testSpec().Region)
	if err != nil {
		t.Fatal(err)
	}
	database := Database{ID: uuid.NewString(), AccountID: "account-a", Name: "health-db", Spec: testSpec(), BackendID: backend.ID,
		BackendFingerprint: backend.Fingerprint, State: StateProvisioning, DesiredGeneration: 1, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
	ctx := context.Background()
	if _, _, err := store.Reserve(ctx, database, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(ctx, database.AccountID, database.ID, "provision", StateProvisioning, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, database.ID, "provision", "private-upstream", now); err != nil {
		t.Fatal(err)
	}
	database, err = store.FinishProvision(ctx, database.ID, "provision", now)
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func TestHealthCollectorTracksOutageRecoveryWithoutLifecycleChanges(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var providerErr error
	var calls int
	provider := &healthTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}}
	provider.observe = func(_ context.Context, id string) (ObservedDatabase, error) {
		calls++
		return ObservedDatabase{ProviderResourceID: id, Status: ProviderStatusReady, ComputeState: ComputeStateSuspended, Spec: testSpec()}, providerErr
	}
	registry := testRegistry(t, provider, nil) // Provisioning stays dark.
	store := NewMemoryStore()
	database := readyHealthDatabase(t, store, registry, now)
	collector, err := NewHealthCollector(registry, store, HealthCollectorOptions{Now: func() time.Time { return now }, MinimumSpacing: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(registry, store, ServiceOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	check := func(wantStatus, wantProvider, wantError string) HealthSummary {
		t.Helper()
		if _, err := collector.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		row, err := service.Get(ctx, database.AccountID, database.ID)
		if err != nil {
			t.Fatal(err)
		}
		if row.State != StateReady || row.LastErrorCode != "" || row.Health.Status != wantStatus || row.Health.ProviderStatus != wantProvider || row.Health.LastErrorCode != wantError {
			t.Fatalf("health changed lifecycle or projected an incorrect result: %+v / %+v", row, row.Health)
		}
		return *row.Health
	}
	health := check("healthy", "ready", "")
	if health.ComputeState != ComputeStateSuspended || !health.Fresh {
		t.Fatalf("suspended compute = %+v", health)
	}
	lastSuccess := health.LastSuccessAt
	now = now.Add(time.Minute)
	providerErr = errors.New("connection failed: postgres://user:secret-password@private-host/db")
	health = check("degraded", "unknown", "provider_unavailable")
	if !health.LastSuccessAt.Equal(lastSuccess) || !health.CheckedAt.Equal(now) {
		t.Fatal("failure lost the previous successful observation")
	}
	now = now.Add(time.Minute)
	providerErr = ErrNotFound
	check("degraded", "missing", "resource_missing")
	now = now.Add(time.Minute)
	if summary, err := collector.Sweep(ctx); err != nil || summary.Checked != 0 || calls != 3 {
		t.Fatalf("backoff was bypassed: %+v %v calls=%d", summary, err, calls)
	}
	now = now.Add(time.Minute)
	providerErr = nil
	check("healthy", "ready", "")
	if provider.inspectCalls != 0 || provider.provisionCalls != 0 {
		t.Fatal("health used lifecycle/repair operations")
	}
	rows, err := service.List(ctx, database.AccountID)
	if err != nil || len(rows) != 1 || rows[0].Health.Status != "healthy" {
		t.Fatalf("list health=%+v %v", rows, err)
	}
	if rows, err := store.ReadHealthSnapshots(ctx, "other-account", []string{database.ID}); err != nil || len(rows) != 0 {
		t.Fatal("health crossed account boundaries")
	}
	now = now.Add(6 * time.Minute)
	row, err := service.Get(ctx, database.AccountID, database.ID)
	if err != nil || row.Health.Status != "stale" || row.Health.Fresh || calls != 4 {
		t.Fatalf("GET hid staleness or contacted provider: %+v %v calls=%d", row.Health, err, calls)
	}
}

func TestHealthCollectorReplicasAndCancellationPreserveLeases(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now().UTC()
	started := make(chan struct{})
	provider := &healthTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}, observe: func(ctx context.Context, _ string) (ObservedDatabase, error) {
		close(started)
		<-ctx.Done()
		return ObservedDatabase{}, ctx.Err()
	}}
	registry := testRegistry(t, provider, nil)
	store := NewMemoryStore()
	database := readyHealthDatabase(t, store, registry, now)
	newCollector := func() *HealthCollector {
		c, err := NewHealthCollector(registry, store, HealthCollectorOptions{Now: func() time.Time { return now }, MinimumSpacing: time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	first, second := newCollector(), newCollector()
	done := make(chan error, 1)
	go func() { _, err := first.Sweep(ctx); done <- err }()
	<-started
	if summary, err := second.Sweep(ctx); err != nil || summary.Checked != 0 {
		t.Fatalf("duplicate provider request: %+v %v", summary, err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	rows, err := store.ReadHealthSnapshots(context.Background(), database.AccountID, []string{database.ID})
	if err != nil || !rows[database.ID].CheckedAt.IsZero() {
		t.Fatal("shutdown was recorded as provider failure")
	}
	if _, err := store.ClaimHealthCheck(context.Background(), "recovered", now.Add(31*time.Second), now.Add(time.Minute)); err != nil {
		t.Fatalf("expired lease did not recover: %v", err)
	}
}

func TestHealthPolicyFreshnessAndDisabledCollection(t *testing.T) {
	for _, config := range []HealthConfig{{IntervalSeconds: -1}, {IntervalSeconds: 1}, {IntervalSeconds: 1 << 62}, {StaleAfterSeconds: -1}, {IntervalSeconds: 600, StaleAfterSeconds: 300}} {
		if _, err := config.policy(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid health policy accepted: %+v", config)
		}
	}
	policy, err := (HealthConfig{}).policy()
	if err != nil || !policy.Enabled {
		t.Fatal("default read-only monitoring disabled")
	}
	now := time.Now().UTC()
	for _, tc := range []struct {
		checked time.Time
		status  string
	}{{time.Time{}, "unknown"}, {now, "healthy"}, {now.Add(-6 * time.Minute), "stale"}, {now.Add(time.Minute), "stale"}} {
		if view := policy.Summarize(HealthSnapshot{ProviderStatus: "ready", ComputeState: ComputeStateSuspended, CheckedAt: tc.checked}, now); view.Status != tc.status {
			t.Fatalf("freshness=%+v", view)
		}
	}
	disabled := false
	provider := &fakeProvider{capabilities: testCapabilities()}
	registry := testRegistry(t, provider, func(c *Config) { c.Health.Enabled = &disabled })
	collector, err := NewHealthCollector(registry, NewMemoryStore(), HealthCollectorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if summary, err := collector.Sweep(context.Background()); err != nil || summary.Enabled || summary.Checked != 0 {
		t.Fatalf("disabled=%+v %v", summary, err)
	}
	if summary := registry.HealthPolicy().Summarize(HealthSnapshot{ProviderStatus: "ready", CheckedAt: now, LastErrorCode: strings.Repeat("private", 2)}, now); summary.Status != "disabled" || summary.LastErrorCode != "" {
		t.Fatal("disabled monitoring retained misleading health")
	}
}

func TestHealthObservationRejectsDriftAndInvalidProviderResults(t *testing.T) {
	valid := ObservedDatabase{ProviderResourceID: "private-upstream", Status: ProviderStatusReady, ComputeState: ComputeStateActive, Spec: testSpec()}
	for _, tc := range []struct {
		name      string
		change    func(*ObservedDatabase)
		code      string
		succeeded bool
	}{
		{"wrong resource", func(o *ObservedDatabase) { o.ProviderResourceID = "another-resource" }, "observation_invalid", false},
		{"invalid status", func(o *ObservedDatabase) { o.Status = "secret-provider-error" }, "observation_invalid", false},
		{"invalid compute", func(o *ObservedDatabase) { o.ComputeState = "vendor-internal-state" }, "observation_invalid", false},
		{"unknown compute", func(o *ObservedDatabase) { o.ComputeState = ComputeStateUnknown }, "observation_invalid", true},
		{"spec drift", func(o *ObservedDatabase) { o.Spec.StorageLimitBytes++ }, "spec_mismatch", true},
		{"provider failed", func(o *ObservedDatabase) { o.Status = ProviderStatusFailed }, "provider_failed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := valid
			tc.change(&observed)
			provider := &healthTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}, observe: func(ctx context.Context, _ string) (ObservedDatabase, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 10*time.Second {
					t.Error("metadata request has no bounded deadline")
				}
				return observed, nil
			}}
			registry := testRegistry(t, provider, nil)
			store := NewMemoryStore()
			database := readyHealthDatabase(t, store, registry, time.Now().UTC())
			collector, err := NewHealthCollector(registry, store, HealthCollectorOptions{})
			if err != nil {
				t.Fatal(err)
			}
			result := collector.observe(context.Background(), database)
			if result.LastErrorCode != tc.code || result.Succeeded != tc.succeeded {
				t.Fatalf("observation: %+v", result)
			}
		})
	}
	provider := &fakeProvider{capabilities: testCapabilities()}
	registry := testRegistry(t, provider, nil)
	store := NewMemoryStore()
	database := readyHealthDatabase(t, store, registry, time.Now().UTC())
	collector, err := NewHealthCollector(registry, store, HealthCollectorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result := collector.observe(context.Background(), database); result.LastErrorCode != "observer_unsupported" || provider.inspectCalls != 0 {
		t.Fatal("unsupported observer fell back to mutating lifecycle inspection")
	}
	database.BackendFingerprint = strings.Repeat("0", 64)
	if result := collector.observe(context.Background(), database); result.LastErrorCode != "backend_unavailable" {
		t.Fatalf("placement drift bypassed fingerprint fence: %+v", result)
	}
}
