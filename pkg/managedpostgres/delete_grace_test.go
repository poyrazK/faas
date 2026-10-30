// adr: 20261029174950 — a customer delete is held for 7 days and can be undone;
// an immediate delete purges and ends the window.
package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type holdingProvider struct {
	*fakeProvider
	held       map[string]bool
	holdErr    error
	releaseErr error
}

func (p *holdingProvider) HoldForDeletion(_ context.Context, id string) error {
	if p.holdErr != nil {
		return p.holdErr
	}
	p.held[id] = true
	return nil
}

func (p *holdingProvider) ReleaseDeletionHold(_ context.Context, id string) error {
	if p.releaseErr != nil {
		return p.releaseErr
	}
	p.held[id] = false
	return nil
}

type graceFixture struct {
	service *Service
	store   *MemoryStore
	clock   *time.Time
}

func newGraceFixture(t *testing.T, provider Provider) graceFixture {
	t.Helper()
	clock := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	sequence := 0
	service, err := NewService(testRegistry(t, provider, nil), store, ServiceOptions{
		PollInterval:        time.Second,
		ProvisioningEnabled: func() bool { return true },
		Now:                 func() time.Time { return clock },
		NewID:               func() string { sequence++; return fmt.Sprintf("database-%d", sequence) },
		NewLeaseToken:       func() string { sequence++; return fmt.Sprintf("lease-%d", sequence) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return graceFixture{service: service, store: store, clock: &clock}
}

func (f graceFixture) readyDatabase(t *testing.T, name string) Database {
	t.Helper()
	database, err := f.service.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: name, Spec: testSpec()})
	if err != nil || database.State != StateReady {
		t.Fatalf("Create = %+v, %v", database, err)
	}
	return database
}

func newHoldingProvider() *holdingProvider {
	return &holdingProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady,
		inspectStatus: ProviderStatusReady, deleteDone: true}, held: map[string]bool{}}
}

func TestScheduleDeleteHoldsTheDatabaseUntilTheWindowEnds(t *testing.T) {
	provider := newHoldingProvider()
	f := newGraceFixture(t, provider)
	ctx := context.Background()
	database := f.readyDatabase(t, "orders")
	scheduled, err := f.service.ScheduleDelete(ctx, "account-a", database.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := f.clock.Add(api.ManagedPostgresDeleteGracePeriod)
	if scheduled.State != StateDeleting || !scheduled.PurgeAfter.Equal(want) || !scheduled.RetryAt.Equal(want) ||
		scheduled.LeaseToken != "" || !provider.held[database.ProviderResourceID] || provider.deleteCalls != 0 {
		t.Fatalf("scheduled %+v held=%v deletes=%d", scheduled, provider.held, provider.deleteCalls)
	}
	if again, err := f.service.ScheduleDelete(ctx, "account-a", database.ID); err != nil || !again.PurgeAfter.Equal(want) {
		t.Fatalf("a second delete restarted the window: %+v, %v", again, err)
	}
	if due, err := f.store.Due(ctx, true, 10, f.clock.Add(api.ManagedPostgresDeleteGracePeriod-time.Second)); err != nil || len(due) != 0 {
		t.Fatalf("due inside the window: %v, %v", due, err)
	}
	if due, err := f.store.Due(ctx, true, 10, want); err != nil || len(due) != 1 {
		t.Fatalf("due when the window ends: %v, %v", due, err)
	}

	*f.clock = f.clock.Add(api.ManagedPostgresDeleteGracePeriod - time.Minute)
	restored, err := f.service.Undelete(ctx, "account-a", database.ID)
	if err != nil || restored.State != StateReady || !restored.PurgeAfter.IsZero() || provider.held[database.ProviderResourceID] || provider.deleteCalls != 0 {
		t.Fatalf("undelete = %+v, %v; held=%v deletes=%d", restored, err, provider.held, provider.deleteCalls)
	}
	if _, err := f.service.Undelete(ctx, "account-a", database.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("undeleting a ready database: %v", err)
	}
}

func TestUndeleteAfterTheWindowOrAPurgeIsGone(t *testing.T) {
	provider := newHoldingProvider()
	f := newGraceFixture(t, provider)
	ctx := context.Background()
	database := f.readyDatabase(t, "orders")
	if _, err := f.service.ScheduleDelete(ctx, "account-a", database.ID); err != nil {
		t.Fatal(err)
	}
	later := *f.clock
	*f.clock = later.Add(api.ManagedPostgresDeleteGracePeriod)
	if _, err := f.service.Undelete(ctx, "account-a", database.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("undelete when the window ended: %v", err)
	}

	*f.clock = later
	// The first database keeps its name while it is scheduled.
	if _, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "orders", Spec: testSpec()}); !errors.Is(err, ErrConflict) {
		t.Fatalf("reusing a scheduled database's name: %v", err)
	}
	second := f.readyDatabase(t, "invoices")
	if _, err := f.service.ScheduleDelete(ctx, "account-a", second.ID); err != nil {
		t.Fatal(err)
	}
	purged, err := f.service.Delete(ctx, "account-a", second.ID)
	if err != nil || purged.State != StateDeleted || !purged.PurgeAfter.IsZero() || provider.deleteCalls != 1 {
		t.Fatalf("purge = %+v, %v; deletes=%d", purged, err, provider.deleteCalls)
	}
	if _, err := f.service.Undelete(ctx, "account-a", second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("undelete after a purge: %v", err)
	}
}

func TestScheduleDeleteFailuresLeaveTheDatabaseUsable(t *testing.T) {
	t.Run("the provider cannot hold", func(t *testing.T) {
		provider := newHoldingProvider()
		provider.holdErr = ErrUnavailable
		f := newGraceFixture(t, provider)
		database := f.readyDatabase(t, "orders")
		if _, err := f.service.ScheduleDelete(context.Background(), "account-a", database.ID); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("ScheduleDelete = %v", err)
		}
		current, err := f.service.Get(context.Background(), "account-a", database.ID)
		if err != nil || current.State != StateReady || !current.PurgeAfter.IsZero() || current.LeaseToken != "" {
			t.Fatalf("after a failed hold: %+v, %v", current, err)
		}
	})
	t.Run("the provider cannot release", func(t *testing.T) {
		provider := newHoldingProvider()
		f := newGraceFixture(t, provider)
		database := f.readyDatabase(t, "orders")
		scheduled, err := f.service.ScheduleDelete(context.Background(), "account-a", database.ID)
		if err != nil {
			t.Fatal(err)
		}
		provider.releaseErr = ErrUnavailable
		if _, err := f.service.Undelete(context.Background(), "account-a", database.ID); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Undelete = %v", err)
		}
		current, err := f.service.Get(context.Background(), "account-a", database.ID)
		if err != nil || !current.Restorable(*f.clock) || !current.RetryAt.Equal(scheduled.PurgeAfter) || current.LeaseToken != "" {
			t.Fatalf("a failed undo changed the schedule: %+v, %v", current, err)
		}
		provider.releaseErr = nil
		if restored, err := f.service.Undelete(context.Background(), "account-a", database.ID); err != nil || restored.State != StateReady {
			t.Fatalf("retried undo = %+v, %v", restored, err)
		}
	})
	t.Run("a binding still blocks the delete", func(t *testing.T) {
		provider := newHoldingProvider()
		f := newGraceFixture(t, provider)
		database := f.readyDatabase(t, "orders")
		f.store.bindings["binding-1"] = Binding{ID: "binding-1", DatabaseID: database.ID, State: BindingStateReady}
		if _, err := f.service.ScheduleDelete(context.Background(), "account-a", database.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("ScheduleDelete with a binding = %v", err)
		}
		if provider.held[database.ProviderResourceID] {
			t.Fatal("a refused delete held the database")
		}
	})
}

func TestScheduleDeleteWithoutAHoldOrAReadyDatabase(t *testing.T) {
	t.Run("a provider without holds still schedules", func(t *testing.T) {
		f := newGraceFixture(t, &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady, inspectStatus: ProviderStatusReady, deleteDone: true})
		database := f.readyDatabase(t, "orders")
		scheduled, err := f.service.ScheduleDelete(context.Background(), "account-a", database.ID)
		if err != nil || !scheduled.Restorable(*f.clock) {
			t.Fatalf("ScheduleDelete = %+v, %v", scheduled, err)
		}
	})
	t.Run("a database that never became ready deletes at once", func(t *testing.T) {
		provider := newHoldingProvider()
		provider.provisionStatus = ProviderStatusPending
		f := newGraceFixture(t, provider)
		database, err := f.service.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "orders", Spec: testSpec()})
		if err != nil || database.State != StateProvisioning {
			t.Fatalf("Create = %+v, %v", database, err)
		}
		deleted, err := f.service.ScheduleDelete(context.Background(), "account-a", database.ID)
		if err != nil || deleted.State != StateDeleted || !deleted.PurgeAfter.IsZero() || provider.held[database.ProviderResourceID] {
			t.Fatalf("ScheduleDelete of a provisioning database = %+v, %v", deleted, err)
		}
	})
}
