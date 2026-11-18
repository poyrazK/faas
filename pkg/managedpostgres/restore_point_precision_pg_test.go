package managedpostgres

import (
	"context"
	"testing"
	"time"
)

// production journey 2026-11-18: a restore to 13:52:38.3528715Z reserved its
// copy, read the point back from the catalog at microsecond precision, and
// then refused its own reservation as a conflict. The point is normalised to
// the catalog's precision first, so the restore succeeds and a retry with the
// same request adopts the copy.
func TestPostgresRestoreAcceptsAPointFinerThanAMicrosecond(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	created := time.Date(2026, 11, 18, 13, 42, 0, 0, time.UTC)
	provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
	registry := testRegistry(t, provider, nil)
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	input := postgresTestDatabase(accountID, "precision-source", created)
	input.BackendID, input.BackendFingerprint = backend.ID, backend.Fingerprint
	source := retirementReadyDatabase(t, store, input)
	now := created.Add(15 * time.Minute)
	service, err := NewService(registry, store, ServiceOptions{
		PollInterval:        time.Second,
		ProvisioningEnabled: func() bool { return true },
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	point := created.Add(10*time.Minute + 352871500*time.Nanosecond) // .3528715
	request := RestoreDatabaseRequest{AccountID: accountID, SourceDatabaseID: source.ID, Name: "precision-copy", PointInTime: point}
	copied, err := service.Restore(ctx, request)
	if err != nil {
		t.Fatalf("restore to a sub-microsecond point: %v", err)
	}
	if want := point.Truncate(time.Microsecond); !copied.RestorePointInTime.Equal(want) {
		t.Fatalf("restore point = %s, want %s", copied.RestorePointInTime, want)
	}
	again, err := service.Restore(context.Background(), request)
	if err != nil || again.ID != copied.ID {
		t.Fatalf("retry = %+v, %v; want the same copy", again, err)
	}
}
