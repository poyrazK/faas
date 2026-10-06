package managedpostgres

import (
	"errors"
	"testing"
	"time"
)

func TestPostgresRestorePreflightRejectsBeforeReservationAndReplaysReceipt(t *testing.T) {
	store, _, ctx, account := postgresStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := &recoveryTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}}
	p.read = func(d RestoreSourceDefinition) (RestoreSourceObservation, error) {
		return RestoreSourceObservation{ProviderResourceID: d.ProviderResourceID, DataResourceID: d.DataResourceID,
			Status: ProviderStatusReady, RetentionSeconds: 60, HistoryNotBefore: now.Add(-time.Hour)}, nil
	}
	s, err := NewService(testRegistry(t, p, nil), store, ServiceOptions{ProvisioningEnabled: func() bool { return true }, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.Create(ctx, CreateRequest{AccountID: account, Name: "source", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	request := RestoreDatabaseRequest{AccountID: account, SourceDatabaseID: source.ID, Name: "target", PointInTime: now.Add(-5 * time.Minute)}
	if _, created, err := s.RestoreWithResult(ctx, request); !errors.Is(err, ErrInvalid) || created || p.restoreCalls != 0 {
		t.Fatal(created, err, p.restoreCalls)
	}
	if _, err := store.FindByName(ctx, account, "target"); !errors.Is(err, ErrNotFound) {
		t.Fatal("rejected request reserved target", err)
	}
	request.PointInTime = now.Add(-30 * time.Second)
	target, created, err := s.RestoreWithResult(ctx, request)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	reads := p.reads
	now = now.Add(48 * time.Hour)
	p.read = func(RestoreSourceDefinition) (RestoreSourceObservation, error) {
		t.Error("replay read provider")
		return RestoreSourceObservation{}, ErrUnavailable
	}
	replay, created, err := s.RestoreWithResult(ctx, request)
	if err != nil || created || replay.ID != target.ID || p.reads != reads || p.restoreCalls != 1 {
		t.Fatal(replay, created, err, p.reads, p.restoreCalls)
	}
}
