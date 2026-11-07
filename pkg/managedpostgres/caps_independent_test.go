package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type independentCapsProvider struct {
	*storageCapTestProvider
	observationErr error
}

func (p *independentCapsProvider) ObserveComputeCap(ctx context.Context, id string) (ComputeCapObservation, error) {
	if p.observationErr != nil {
		return ComputeCapObservation{}, p.observationErr
	}
	return p.capTestProvider.ObserveComputeCap(ctx, id)
}

func TestStorageCapsContinueAfterComputeFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode CapEnforcementMode
		read bool
	}{
		{"observation", CapEnforcementOn, true},
		{"write", CapEnforcementOn, false},
		{"shadow observation", CapEnforcementShadow, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
			provider := &independentCapsProvider{storageCapTestProvider: &storageCapTestProvider{
				capTestProvider: &capTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()},
					observed: map[string]ComputeCapObservation{"proj-a": {Scope: "a", PeriodStart: monthStart(now), UsedUnitSeconds: 10}},
					set:      map[string]int64{}, setErr: map[string]error{"proj-a": ErrUnavailable}},
				storage:    map[string]StorageCapObservation{"proj-a": {Scope: "a", SizeBytes: 700 * mib, LimitBytes: 1024 * mib}},
				storageSet: map[string]int64{},
			}}
			if tc.read {
				provider.observationErr = ErrUnavailable
			}
			registry := testRegistry(t, provider, func(config *Config) {
				config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
					StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1 << 40, MaxMonthlyComputeUnitSeconds: 1 << 40,
					MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 40}
			})
			backend, err := registry.Default("us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			spec := testSpec()
			spec.StorageLimitBytes = 1024 * mib
			store := &capTestStore{databases: []Database{{ID: "db-a", AccountID: "acct-a", BackendID: backend.ID,
				BackendFingerprint: backend.Fingerprint, ProviderResourceID: "proj-a", State: StateReady, Spec: spec}}}
			enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
				Mode: tc.mode, StorageMode: CapEnforcementOn, Now: func() time.Time { return now },
				Qualification: testCapQualification(backend), StorageQualification: testStorageCapQualification(backend),
				Ceiling: func(context.Context, string) (CapLimits, error) {
					return CapLimits{Allowance: 10000, Ceiling: 10000, StoragePool: 512 * mib}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			summary, err := enforcer.Enforce(context.Background())
			if !errors.Is(err, ErrUnavailable) || summary.Failed != 1 || summary.StorageFailed != 0 ||
				summary.StorageScopesApplied != 1 || provider.storageSet["proj-a"] != 700*mib {
				t.Fatalf("compute failure stopped storage enforcement: summary=%+v writes=%v err=%v", summary, provider.storageSet, err)
			}
		})
	}
}
