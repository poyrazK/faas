package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type testRecoveryBackend struct {
	fakeProvider
	proof *time.Time
}

func (p *testRecoveryBackend) CheckRecoveryTarget(_ context.Context, _ string, _ int, _ time.Time, latest bool) (api.ManagedPostgresRecoveryStatus, error) {
	if !latest || p.proof == nil {
		return api.ManagedPostgresRecoveryStatus{Region: "us-west-2", Outcome: "wal_gap", Reason: "missing_wal_segment"}, nil
	}
	return api.ManagedPostgresRecoveryStatus{Region: "us-west-2", Outcome: "covered", Reason: "archive_covered", RecoveryPoint: p.proof}, nil
}

func TestRegionalRestoreUsesDestinationEvidenceAndFreezesThePoint(t *testing.T) {
	home := &historicalSpecProvider{
		fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady},
		resolve:      func(Spec) (Spec, error) { return Spec{}, ErrUnavailable },
	}
	destination := &testRecoveryBackend{fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}}
	factory := func(cfg BackendConfig, _ func(string) string) (Provider, error) {
		if cfg.ID == "west" {
			return destination, nil
		}
		return home, nil
	}
	registry, err := NewRegistry(Config{
		DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "east"},
		RecoveryTargets: map[string]string{"us-east-1": "west"},
		Backends: []BackendConfig{
			{ID: "east", Driver: "firecracker", Region: "us-east-1", Namespace: "home"},
			{ID: "west", Driver: "firecracker", Region: "us-west-2", Namespace: "recovery"},
		},
	}, func(string) string { return "" }, map[string]Factory{"firecracker": factory})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	store := NewMemoryStore()
	service := testService(t, registry, store)
	source, err := service.Create(ctx, CreateRequest{AccountID: "account", Name: "orders", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	request := RestoreDatabaseRequest{
		AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "orders-recovery",
		RecoveryRegion: "us-west-2", Latest: true,
	}
	if _, err := service.Restore(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("restore before opt-in: %v", err)
	}
	if err := service.SetRecoveryRegion(ctx, source.AccountID, source.ID, request.RecoveryRegion); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Restore(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("restore past a WAL gap: %v", err)
	}
	if _, err := store.FindByName(ctx, source.AccountID, request.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed evidence reserved a database: %v", err)
	}

	point := source.CreatedAt.Add(time.Second)
	destination.proof = &point
	target, err := service.Restore(ctx, request)
	if err != nil || target.Spec.Region != request.RecoveryRegion || target.BackendID != "west" ||
		!target.RestorePointInTime.Equal(point) || !destination.lastRestore.FromRecoveryArchive {
		t.Fatalf("regional restore = %+v %v, dispatch = %+v", target, err, destination.lastRestore)
	}
	destination.proof = nil
	again, err := service.Restore(ctx, request)
	if err != nil || again.ID != target.ID || destination.restoreCalls != 1 || home.resolutions != 0 || home.restoreCalls != 0 {
		t.Fatalf("retry = %+v %v, destination restores=%d, home resolutions=%d, home restores=%d",
			again, err, destination.restoreCalls, home.resolutions, home.restoreCalls)
	}
}
