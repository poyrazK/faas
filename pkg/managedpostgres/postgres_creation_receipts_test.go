// adr: 638
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

func postgresCreationFixture(t *testing.T) (*PostgresStore, Database, CreationReceipt) {
	t.Helper()
	store, _, ctx, account := postgresStoreFixture(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	source := postgresReadyDatabase(t, store, account, "receipt-source", at)
	input := postgresTestDatabase(account, "receipt-target", at)
	input.RestoreSourceDatabaseID = source.ID
	input.RestoreSourceResourceID = source.ProviderResourceID
	input.RestorePointInTime = at.Add(-time.Minute)
	target, _, err := store.Reserve(ctx, input, 100)
	if err != nil {
		t.Fatal(err)
	}
	target, err = store.Claim(ctx, account, target.ID, "receipt-worker", StateProvisioning, at, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginAccounting(ctx, target.ID, target.LeaseToken, at); err != nil {
		t.Fatal(err)
	}
	a := CreationAcknowledgement{ProviderResourceID: "accepted-target", SourceResourceID: source.ProviderResourceID, CreatedAt: at}
	return store, target, restoreCreationReceipt(target, a)
}

func TestPostgresCreationReceiptIsDurableImmutableAndSeparateFromReadiness(t *testing.T) {
	store, target, r := postgresCreationFixture(t)
	ctx := t.Context()
	if err := store.RecordCreationReceipt(ctx, r, target.LeaseToken); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPostgresStore(store.pool)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restarted.GetCreationReceipt(ctx, r.Kind, r.BackendID, r.ResourceID)
	if err != nil || !sameCreationReceipt(got, r) {
		t.Fatalf("restart receipt: %+v %v", got, err)
	}
	if err := restarted.RecordCreationReceipt(ctx, r, target.LeaseToken); err != nil {
		t.Fatalf("lost write acknowledgement replay: %v", err)
	}
	current, err := restarted.Get(ctx, target.AccountID, target.ID)
	if err != nil || current.ProviderResourceID != "" || current.State != StateProvisioning || !current.AccountingRequired {
		t.Fatalf("custody promoted unverified data: %+v %v", current, err)
	}
	for _, fault := range []string{"target", "source", "generation", "backend", "account", "time", "lease"} {
		t.Run(fault, func(t *testing.T) {
			changed := r
			token := target.LeaseToken
			switch fault {
			case "target":
				changed.Acknowledgement.ProviderResourceID = "foreign-target"
			case "source":
				changed.Acknowledgement.SourceResourceID = "foreign-source"
			case "generation":
				changed.Generation++
			case "backend":
				changed.BackendID = "foreign-backend"
			case "account":
				changed.AccountID = uuid.NewString()
			case "time":
				changed.Acknowledgement.CreatedAt = changed.Acknowledgement.CreatedAt.Add(-time.Second)
			case "lease":
				token = "stale-worker"
			}
			if err := restarted.RecordCreationReceipt(ctx, changed, token); err == nil {
				t.Fatal("changed receipt accepted")
			}
			got, err := restarted.GetCreationReceipt(ctx, r.Kind, r.BackendID, r.ResourceID)
			if err != nil || !sameCreationReceipt(got, r) {
				t.Fatalf("receipt replaced: %+v %v", got, err)
			}
		})
	}
	snapshot := r
	snapshot.Kind = "snapshot"
	snapshot.DatabaseID = ""
	snapshot.ResourceID = "undispatched-snapshot"
	snapshot.Acknowledgement.ProviderResourceID = "snapshot"
	if err := store.RecordCreationReceipt(ctx, snapshot, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("snapshot without dispatched intent: %v", err)
	}
}

func TestPostgresCreationReceiptRechecksLeaseAfterRowLock(t *testing.T) {
	store, target, r := postgresCreationFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "update managed_postgres_databases set lease_until=clock_timestamp()+interval '120 milliseconds' where id=$1", target.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- store.RecordCreationReceipt(ctx, r, target.LeaseToken) }()
	time.Sleep(180 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrConflict) {
		t.Fatalf("queued stale acknowledgement: %v", err)
	}
	if _, err := store.GetCreationReceipt(ctx, r.Kind, r.BackendID, r.ResourceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale receipt persisted: %v", err)
	}
}

func TestPostgresCreationCleanupCheckpointIsFencedAndDurable(t *testing.T) {
	store, target, receipt := postgresCreationFixture(t)
	ctx := t.Context()
	if err := store.RecordCreationReceipt(ctx, receipt, target.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCreationCleanup(ctx, receipt, target.LeaseToken); !errors.Is(err, ErrConflict) {
		t.Fatalf("provisioning lease authorized cleanup: %v", err)
	}
	if err := store.Release(ctx, target.ID, target.LeaseToken, StateProvisioning, "", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	deleting, err := store.ClaimDelete(ctx, target.AccountID, target.ID, "cleanup-worker", time.Now(), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCreationCleanup(ctx, receipt, target.LeaseToken); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale worker cleanup: %v", err)
	}
	// A cleanup transaction must not acquire a second pool connection while
	// holding the database row lock: small production pools must still progress.
	config := store.pool.Config()
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	single, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := single.RecordCreationCleanup(writeCtx, receipt, deleting.LeaseToken); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPostgresStore(store.pool)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := restarted.GetCreationReceipt(ctx, receipt.Kind, receipt.BackendID, receipt.ResourceID)
	if err != nil || observed.CleanupStartedAt.IsZero() || !sameCreationReceipt(observed, receipt) {
		t.Fatalf("cleanup checkpoint: %+v %v", observed, err)
	}
	if err := restarted.RecordCreationCleanup(ctx, receipt, deleting.LeaseToken); err != nil {
		t.Fatalf("lost checkpoint reply replay: %v", err)
	}
	again, err := restarted.GetCreationReceipt(ctx, receipt.Kind, receipt.BackendID, receipt.ResourceID)
	if err != nil || !again.CleanupStartedAt.Equal(observed.CleanupStartedAt) {
		t.Fatalf("checkpoint replaced: %+v %v", again, err)
	}
}

func TestPostgresCreationCustodyAllowsFinalAccountErasureAfterCleanup(t *testing.T) {
	store, target, receipt := postgresCreationFixture(t)
	ctx := t.Context()
	if err := store.RecordCreationReceipt(ctx, receipt, target.LeaseToken); err != nil {
		t.Fatal(err)
	}
	accounts := state.NewPgStore(store.pool)
	if err := accounts.DeleteAccount(ctx, target.AccountID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("active account erased: %v", err)
	}
	if _, err := store.GetCreationReceipt(ctx, receipt.Kind, receipt.BackendID, receipt.ResourceID); err != nil {
		t.Fatalf("active custody erased: %v", err)
	}
	if err := accounts.MarkAccountDeletionPending(ctx, target.AccountID); err == nil {
		t.Fatal("live resource allowed account deletion")
	}
	if err := store.Release(ctx, target.ID, target.LeaseToken, StateProvisioning, "", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	deleting := retirementDelete(t, store, target, time.Now(), false)
	if err := store.RecordCreationCleanup(ctx, receipt, deleting.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, target.ID, deleting.LeaseToken, receipt.Acknowledgement.ProviderResourceID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishDelete(ctx, target.ID, deleting.LeaseToken, time.Now()); err != nil {
		t.Fatal(err)
	}
	source, err := store.Get(ctx, target.AccountID, target.RestoreSourceDatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	retirementDelete(t, store, source, time.Now(), true)
	if err := accounts.MarkAccountDeletionPending(ctx, target.AccountID); err != nil {
		t.Fatal(err)
	}
	if err := accounts.DeleteAccount(ctx, target.AccountID); err != nil {
		t.Fatalf("confirmed cleanup prevented final erasure: %v", err)
	}
	if _, err := store.GetCreationReceipt(ctx, receipt.Kind, receipt.BackendID, receipt.ResourceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("receipt retained after erasure: %v", err)
	}
}
