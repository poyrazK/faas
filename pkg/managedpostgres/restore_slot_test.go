// adr: 20261031123259 — a restore may hold one database over the plan's
// limit for 7 days; the slot is released when the owner has room and ends in a
// scheduled delete when it has none.
package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// slotProvider holds databases for deletion and issues credentials, so a
// copy can be bound before its slot ends.
type slotProvider struct {
	*holdingProvider
}

func (*slotProvider) IssueCredentials(context.Context, CredentialRequest) (CredentialMaterial, error) {
	return CredentialMaterial{ProviderIdentityID: "role-slot", Username: "gregale", Password: "not-persisted", Database: "gregale",
		TLSMode: "require", Endpoints: []Endpoint{{Role: EndpointPooled, Host: "pool.example.test", Port: 5432}}}, nil
}

func (*slotProvider) RevokeCredentials(context.Context, CredentialRequest) error { return nil }

type slotFixture struct {
	service  *Service
	store    *MemoryStore
	provider *holdingProvider
	bindings *BindingService
	sweeper  *RestoreSlotSweeper
	clock    *time.Time
	limit    *int
	expired  *[]Database
	// notified records the notices mailed; notifyErr, when set, fails them.
	notified  *[]RestoreSlotNotice
	notifyErr *error
}

func newSlotFixture(t *testing.T) slotFixture {
	t.Helper()
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	limit := 1
	var expired []Database
	var notified []RestoreSlotNotice
	var notifyErr error
	provider := newHoldingProvider()
	registry, store := testRegistry(t, &slotProvider{provider}, nil), NewMemoryStore()
	sequence := 0
	service, err := NewService(registry, store, ServiceOptions{
		PollInterval:           time.Second,
		ProvisioningEnabled:    func() bool { return true },
		Now:                    func() time.Time { return clock },
		NewID:                  func() string { sequence++; return fmt.Sprintf("database-%d", sequence) },
		NewLeaseToken:          func() string { sequence++; return fmt.Sprintf("lease-%d", sequence) },
		MaxDatabasesPerAccount: func(context.Context, string) (int, error) { return limit, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := NewBindingService(registry, store, store, newBindingCredentialSink(), BindingServiceOptions{ProvisioningEnabled: func() bool { return true }, Now: service.now})
	if err != nil {
		t.Fatal(err)
	}
	sweeper, err := NewRestoreSlotSweeper(service, bindings, RestoreSlotSweeperOptions{
		Expired: func(_ context.Context, database Database) { expired = append(expired, database) },
		Notify: func(_ context.Context, notice RestoreSlotNotice) error {
			if notifyErr != nil {
				return notifyErr
			}
			notified = append(notified, notice)
			store.markRestoreSlotNoticeMailed(notice)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return slotFixture{service: service, store: store, provider: provider, bindings: bindings, sweeper: sweeper, clock: &clock, limit: &limit, expired: &expired,
		notified: &notified, notifyErr: &notifyErr}
}

// sourceAndCopy creates a ready source at the one-database limit and restores
// it into a copy that takes the slot.
func (f slotFixture) sourceAndCopy(t *testing.T) (Database, Database) {
	t.Helper()
	ctx := context.Background()
	source, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "orders", Spec: testSpec()})
	if err != nil || source.State != StateReady {
		t.Fatalf("Create = %+v, %v", source, err)
	}
	*f.clock = f.clock.Add(time.Minute)
	restored, err := f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "orders-copy", PointInTime: source.CreatedAt})
	if err != nil || restored.State != StateReady {
		t.Fatalf("Restore = %+v, %v", restored, err)
	}
	return source, restored
}

func TestRestoreTakesTheSlotPastTheLimitOnce(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	source, restored := f.sourceAndCopy(t)
	if want := restored.CreatedAt.Add(api.ManagedPostgresRestoreSlotWindow); !restored.RestoreSlotUntil.Equal(want) {
		t.Fatalf("restore slot until = %v, want %v", restored.RestoreSlotUntil, want)
	}
	if !source.RestoreSlotUntil.IsZero() {
		t.Fatalf("the source took the slot: %+v", source)
	}
	_, err := f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "orders-second", PointInTime: source.CreatedAt})
	if !errors.Is(err, ErrRestoreSlotInUse) || !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("second restore past the limit = %v, want the slot in use", err)
	}
	if status, code, _ := QuotaProblem(err); status != 403 || code != "managed_postgres_restore_slot_in_use" {
		t.Fatalf("slot problem = %d %s", status, code)
	}
	if _, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "invoices", Spec: testSpec()}); !errors.Is(err, ErrQuotaExceeded) || errors.Is(err, ErrRestoreSlotInUse) {
		t.Fatalf("create past the limit = %v, want the plain quota refusal", err)
	}
}

func TestRestoreInsideTheLimitDoesNotTakeTheSlot(t *testing.T) {
	f := newSlotFixture(t)
	*f.limit = 2
	_, restored := f.sourceAndCopy(t)
	if !restored.RestoreSlotUntil.IsZero() {
		t.Fatalf("a restore inside the limit took the slot: %+v", restored)
	}
}

func TestRestoreSlotIsReleasedWhenTheOwnerHasRoom(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	_, restored := f.sourceAndCopy(t)
	if summary, err := f.sweeper.Sweep(ctx); err != nil || summary.Holders != 1 || summary.Released != 0 {
		t.Fatalf("sweep at the limit = %+v, %v", summary, err)
	}
	*f.limit = 2 // an upgrade
	if summary, err := f.sweeper.Sweep(ctx); err != nil || summary.Released != 1 {
		t.Fatalf("sweep after the upgrade = %+v, %v", summary, err)
	}
	ordinary, err := f.service.Get(ctx, "account-a", restored.ID)
	if err != nil || !ordinary.RestoreSlotUntil.IsZero() || ordinary.State != StateReady {
		t.Fatalf("released copy = %+v, %v", ordinary, err)
	}
	*f.clock = f.clock.Add(api.ManagedPostgresRestoreSlotWindow + time.Hour)
	if summary, err := f.sweeper.Sweep(ctx); err != nil || summary.Holders != 0 || len(*f.expired) != 0 {
		t.Fatalf("an ordinary database reached the slot's end: %+v, %v, expired %d", summary, err, len(*f.expired))
	}
}

func TestRestoreSlotEndsWithAScheduledDeleteWhenThereIsNoRoom(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	source, restored := f.sourceAndCopy(t)
	if _, err := f.bindings.Create(ctx, CreateBindingRequest{AccountID: "account-a", DatabaseID: restored.ID, AppID: "app-a", Scope: "default", EnvironmentKey: "RESTORED_DATABASE_URL", Access: CredentialReadWrite}); err != nil {
		t.Fatalf("bind the copy: %v", err)
	}
	*f.clock = restored.RestoreSlotUntil.Add(-time.Minute)
	if summary, err := f.sweeper.Sweep(ctx); err != nil || summary.Expired != 0 || summary.Waiting != 0 {
		t.Fatalf("sweep inside the window = %+v, %v", summary, err)
	}
	*f.clock = restored.RestoreSlotUntil.Add(time.Minute)
	for range 3 {
		if _, err := f.sweeper.Sweep(ctx); err != nil {
			t.Fatalf("sweep past the window: %v", err)
		}
	}
	if len(*f.expired) != 1 {
		t.Fatalf("expired hook ran %d times, want once", len(*f.expired))
	}
	copyNow, err := f.service.Get(ctx, "account-a", restored.ID)
	if err != nil || copyNow.State != StateDeleting || !copyNow.Restorable(*f.clock) || !f.provider.held[restored.ProviderResourceID] {
		t.Fatalf("expired copy = %+v, held %v, %v", copyNow, f.provider.held[restored.ProviderResourceID], err)
	}
	bindings, err := f.bindings.List(ctx, "account-a", restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if binding.State != BindingStateDeleted {
			t.Fatalf("the copy's binding survived its scheduled delete: %+v", binding)
		}
	}
	sourceNow, err := f.service.Get(ctx, "account-a", source.ID)
	if err != nil || sourceNow.State != StateReady || f.provider.held[source.ProviderResourceID] {
		t.Fatalf("the source was touched: %+v, %v", sourceNow, err)
	}
	if _, err := f.service.Undelete(ctx, "account-a", restored.ID); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("recover an expired copy without room = %v, want the quota refusal", err)
	}
	*f.limit = 2
	recovered, err := f.service.Undelete(ctx, "account-a", restored.ID)
	if err != nil || recovered.State != StateReady {
		t.Fatalf("recover with room = %+v, %v", recovered, err)
	}
	if summary, err := f.sweeper.Sweep(ctx); err != nil || summary.Released != 1 {
		t.Fatalf("sweep after the recover = %+v, %v", summary, err)
	}
}

func TestRestoreSlotStaysWithACopyTheCustomerDeleted(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	source, restored := f.sourceAndCopy(t)
	if _, err := f.service.ScheduleDelete(ctx, "account-a", restored.ID); err != nil {
		t.Fatalf("customer delete of the copy: %v", err)
	}
	if _, err := f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "orders-again", PointInTime: source.CreatedAt}); !errors.Is(err, ErrRestoreSlotInUse) {
		t.Fatalf("restore while the deleted copy holds the slot = %v", err)
	}
	*f.clock = restored.RestoreSlotUntil.Add(time.Minute)
	if summary, err := f.sweeper.Sweep(ctx); err != nil || summary.Expired != 0 || len(*f.expired) != 0 {
		t.Fatalf("a copy already deleting was scheduled again: %+v, %v", summary, err)
	}
	if _, err := f.service.Delete(ctx, "account-a", restored.ID); err != nil {
		t.Fatalf("purge the copy: %v", err)
	}
	if _, err := f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "orders-again", PointInTime: f.clock.Add(-time.Minute)}); err != nil {
		t.Fatalf("restore after the purge: %v", err)
	}
}

// TestRestoreRangeMatchesWhatRestoreAccepts pins the shown range to Restore's
// own bounds: a point just inside it restores, one just outside is refused.
func TestRestoreRangeMatchesWhatRestoreAccepts(t *testing.T) {
	f := newSlotFixture(t)
	*f.limit = 10
	ctx := context.Background()
	source, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "orders", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := source.RestoreRange(source.CreatedAt); ok {
		t.Fatal("a database has a restore range at the instant it was created")
	}
	window := time.Duration(source.Spec.RestoreWindowSeconds) * time.Second
	for i, age := range []time.Duration{time.Hour, window + 6*time.Hour} {
		*f.clock = source.CreatedAt.Add(age)
		earliest, latest, ok := source.RestoreRange(*f.clock)
		wantEarliest := source.CreatedAt
		if age > window {
			wantEarliest = f.clock.Add(-window)
		}
		if !ok || !earliest.Equal(wantEarliest) || !latest.Equal(*f.clock) {
			t.Fatalf("age %s: range = %v..%v ok=%v, want %v..%v", age, earliest, latest, ok, wantEarliest, *f.clock)
		}
		if _, err := f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: fmt.Sprintf("before-%d", i), PointInTime: earliest.Add(-time.Second)}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("age %s: a point before the range = %v, want ErrInvalid", age, err)
		}
		if _, err := f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: fmt.Sprintf("inside-%d", i), PointInTime: earliest.Add(time.Second)}); err != nil {
			t.Fatalf("age %s: a point inside the range: %v", age, err)
		}
	}
	deleting := source
	deleting.State = StateDeleting
	if _, _, ok := deleting.RestoreRange(*f.clock); ok {
		t.Fatal("a database being deleted shows a restore range")
	}
}

func TestRestoreSlotNoticesAreMailedOnceEach(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	_, restored := f.sourceAndCopy(t)
	sweep := func() RestoreSlotSummary {
		t.Helper()
		summary, err := f.sweeper.Sweep(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return summary
	}

	*f.clock = restored.RestoreSlotUntil.Add(-api.ManagedPostgresRestoreSlotNoticeLead - time.Minute)
	if summary := sweep(); summary.Noticed != 0 || len(*f.notified) != 0 {
		t.Fatalf("a sweep before the lead = %+v, notified %+v", summary, *f.notified)
	}

	// Inside the lead the notice is recorded once; a failed email is retried.
	*f.clock = restored.RestoreSlotUntil.Add(-api.ManagedPostgresRestoreSlotNoticeLead + time.Minute)
	*f.notifyErr = errors.New("outbox unavailable")
	if summary := sweep(); summary.Noticed != 1 || summary.NotifyFailed != 1 || len(*f.notified) != 0 {
		t.Fatalf("the first sweep in the lead = %+v", summary)
	}
	*f.notifyErr = nil
	*f.clock = f.clock.Add(time.Hour)
	if summary := sweep(); summary.Noticed != 0 || summary.NotifyFailed != 0 || len(*f.notified) != 1 {
		t.Fatalf("the retry = %+v, notified %+v", summary, *f.notified)
	}
	ending := (*f.notified)[0]
	if ending.Kind != RestoreSlotNoticeEnding || ending.DatabaseID != restored.ID || ending.AccountID != "account-a" ||
		ending.DatabaseName != "orders-copy" || !ending.SlotUntil.Equal(restored.RestoreSlotUntil) || !ending.RestorableUntil.IsZero() {
		t.Fatalf("ending notice = %+v", ending)
	}
	*f.clock = f.clock.Add(time.Hour)
	if sweep(); len(*f.notified) != 1 {
		t.Fatalf("a mailed notice was mailed again: %+v", *f.notified)
	}

	// The slot ends without room: the delete is scheduled and mailed once.
	*f.clock = restored.RestoreSlotUntil.Add(time.Minute)
	if summary := sweep(); summary.Expired != 1 || summary.Noticed != 1 || len(*f.notified) != 2 {
		t.Fatalf("the expiry sweep = %+v, notified %+v", summary, *f.notified)
	}
	expired := (*f.notified)[1]
	if expired.Kind != RestoreSlotNoticeExpired || expired.DatabaseID != restored.ID || len(*f.expired) != 1 ||
		!expired.RestorableUntil.Equal((*f.expired)[0].PurgeAfter) || expired.RestorableUntil.IsZero() {
		t.Fatalf("expired notice = %+v, scheduled %+v", expired, *f.expired)
	}
	*f.clock = f.clock.Add(time.Hour)
	if sweep(); len(*f.notified) != 2 {
		t.Fatalf("the expiry was mailed again: %+v", *f.notified)
	}
}

func TestRestoreSlotEndingNoticeIsNotMailedOnceTheSlotIsReleased(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	_, restored := f.sourceAndCopy(t)
	*f.clock = restored.RestoreSlotUntil.Add(-time.Hour)
	*f.notifyErr = errors.New("outbox unavailable")
	if summary, err := f.sweeper.Sweep(ctx); err != nil || summary.Noticed != 1 {
		t.Fatalf("sweep = %+v, %v", summary, err)
	}
	// A plan upgrade makes room: the copy keeps its data and the warning no
	// longer applies.
	*f.limit = 2
	*f.notifyErr = nil
	if summary, err := f.sweeper.Sweep(ctx); err != nil || summary.Released != 1 || len(*f.notified) != 0 {
		t.Fatalf("release sweep = %+v, %v, notified %+v", summary, err, *f.notified)
	}
	// A notice older than a day is never mailed either.
	notices, err := f.store.UnmailedRestoreSlotNotices(ctx, f.clock.Add(-api.InternalMailManagedPostgresRestoreSlotTTL), *f.clock, 10)
	if err != nil || len(notices) != 0 {
		t.Fatalf("unmailed = %+v, %v", notices, err)
	}
}
