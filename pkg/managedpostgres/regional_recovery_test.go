package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type recoverySourceProvider struct {
	historicalSpecProvider
	history []RestoreMajorSpan
}

func (p *recoverySourceProvider) RestoreMajorHistory(context.Context, RestoreSourceDefinition) ([]RestoreMajorSpan, error) {
	return p.history, nil
}

type testRecoveryBackend struct {
	fakeProvider
	proof           *time.Time
	historicalMajor int
	checkedMajor    int
	archiveMajor    int
	checkedAt       time.Time
}

func (p *testRecoveryBackend) CheckRecoveryTarget(_ context.Context, _ string, major int, target time.Time, latest bool) (api.ManagedPostgresRecoveryStatus, error) {
	p.checkedMajor, p.checkedAt = major, target
	if !latest && major == p.historicalMajor {
		return api.ManagedPostgresRecoveryStatus{Region: "us-west-2", Outcome: "covered", Reason: "archive_covered", RecoveryTimeline: 1}, nil
	}
	if !latest || p.proof == nil || (p.archiveMajor != 0 && major != p.archiveMajor) {
		return api.ManagedPostgresRecoveryStatus{Region: "us-west-2", Outcome: "wal_gap", Reason: "missing_wal_segment"}, nil
	}
	return api.ManagedPostgresRecoveryStatus{Region: "us-west-2", Outcome: "covered", Reason: "archive_covered", RecoveryPoint: p.proof, RecoveryTimeline: 2}, nil
}

func TestRegionalRestoreUsesDestinationEvidenceAndFreezesThePoint(t *testing.T) {
	home := &recoverySourceProvider{historicalSpecProvider: historicalSpecProvider{
		fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady},
		resolve:      func(Spec) (Spec, error) { return Spec{}, ErrUnavailable },
	}}
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
		!target.RestorePointInTime.Equal(point) || !destination.lastRestore.FromRecoveryArchive ||
		target.RestoreRecoveryTimeline != 2 || destination.lastRestore.RecoveryTimeline != 2 {
		t.Fatalf("regional restore = %+v %v, dispatch = %+v", target, err, destination.lastRestore)
	}
	destination.proof = nil
	again, err := service.Restore(ctx, request)
	if err != nil || again.ID != target.ID || destination.restoreCalls != 1 || home.resolutions != 0 || home.restoreCalls != 0 {
		t.Fatalf("retry = %+v %v, destination restores=%d, home resolutions=%d, home restores=%d",
			again, err, destination.restoreCalls, home.resolutions, home.restoreCalls)
	}

	t.Run("historical major", func(t *testing.T) {
		home.resolve = func(spec Spec) (Spec, error) {
			spec.PostgresMajor = 16
			return spec, nil
		}
		destination.historicalMajor = 16
		request.Latest, request.PointInTime, request.Name = false, point, "orders-before-upgrade"
		target, err := service.Restore(ctx, request)
		if err != nil || target.Spec.PostgresMajor != 16 || target.Spec.Region != request.RecoveryRegion ||
			!target.RestorePointInTime.Equal(point) || destination.checkedMajor != 16 ||
			destination.lastRestore.Spec.PostgresMajor != 16 || !destination.lastRestore.FromRecoveryArchive ||
			target.RestoreRecoveryTimeline != 1 || destination.lastRestore.RecoveryTimeline != 1 {
			t.Fatalf("historical restore = %+v %v, checked major = %d, dispatch = %+v", target, err, destination.checkedMajor, destination.lastRestore)
		}
		home.resolve = func(Spec) (Spec, error) { return Spec{}, ErrUnavailable }
		again, err := service.Restore(ctx, request)
		if err != nil || again.ID != target.ID || home.resolutions != 1 || destination.restoreCalls != 2 {
			t.Fatalf("retry = %+v %v, home resolutions = %d, restore calls = %d", again, err, home.resolutions, destination.restoreCalls)
		}
	})
	t.Run("latest proof is on the previous major", func(t *testing.T) {
		upgrade := point.Add(time.Second)
		home.history = []RestoreMajorSpan{{PostgresMajor: 16, Until: upgrade}}
		destination.archiveMajor, destination.proof = 16, &point

		current, err := store.Get(ctx, source.AccountID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		status := service.regionalRecoveryStatus(ctx, current)
		if status == nil || status.Outcome != "covered" || status.RecoveryPoint == nil || !status.RecoveryPoint.Equal(point) ||
			destination.checkedMajor != 16 || !destination.checkedAt.Before(upgrade) {
			t.Fatalf("regional status = %+v, checked PostgreSQL %d at %s", status, destination.checkedMajor, destination.checkedAt)
		}

		latest := RestoreDatabaseRequest{
			AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "previous-major-latest",
			RecoveryRegion: request.RecoveryRegion, Latest: true,
		}
		target, err := service.Restore(ctx, latest)
		if err != nil || target.Spec.PostgresMajor != 16 || !target.RestorePointInTime.Equal(point) ||
			destination.lastRestore.Spec.PostgresMajor != 16 || !destination.lastRestore.FromRecoveryArchive ||
			target.RestoreRecoveryTimeline != 2 || destination.lastRestore.RecoveryTimeline != 2 {
			t.Fatalf("regional restore = %+v %v, dispatch = %+v", target, err, destination.lastRestore)
		}

		destination.archiveMajor, destination.proof = 0, nil
		home.history = nil
		again, err := service.Restore(ctx, latest)
		if err != nil || again.ID != target.ID || destination.restoreCalls != 3 {
			t.Fatalf("retry = %+v %v, destination restores = %d", again, err, destination.restoreCalls)
		}
	})
}
