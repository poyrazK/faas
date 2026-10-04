//go:build !no_pg

// adr: 566
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresDataResourceProvisionCommitsIdentityWithReadiness(t *testing.T) {
	store, _, ctx, account := postgresStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	database, _, err := store.Reserve(ctx, postgresTestDatabase(account, "pinned", now), 2)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(ctx, account, database.ID, uuid.NewString(), StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, database.ID, claimed.LeaseToken, "project", now); err != nil {
		t.Fatal(err)
	}
	claimed.ProviderResourceID = "project"
	observed := ObservedDatabase{ProviderResourceID: "project", DataResourceID: "project/original-branch", Status: ProviderStatusReady, Spec: database.Spec}
	ready, err := store.FinishProvisionWithDataResource(ctx, claimed, observed, now)
	if err != nil || ready.State != StateReady || ready.ProviderResourceID != "project" || ready.DataResourceID != observed.DataResourceID || ready.LeaseToken != "" {
		t.Fatalf("atomic ready identity = %+v, %v", ready, err)
	}
	// A restarted process reads the pin through lifecycle, customer, list and
	// usage catalog paths. No mutable provider selector is resolved on recovery.
	getters := []func() (Database, error){
		func() (Database, error) { return store.Get(ctx, account, database.ID) },
		func() (Database, error) { return store.GetCustomerDatabase(ctx, account, database.ID) },
		func() (Database, error) { return store.FindByName(ctx, account, database.Name) },
	}
	for _, get := range getters {
		actual, err := get()
		if err != nil || actual.DataResourceID != ready.DataResourceID {
			t.Fatalf("catalog dropped identity: %+v, %v", actual, err)
		}
	}
	for _, list := range []func() ([]Database, error){
		func() ([]Database, error) { return store.List(ctx, account) },
		func() ([]Database, error) { return store.ListCustomerDatabases(ctx, account) },
		func() ([]Database, error) { return store.ListUsageDatabases(ctx, UsageDatabaseCursor{}, 2) },
	} {
		rows, err := list()
		if err != nil || len(rows) != 1 || rows[0].DataResourceID != ready.DataResourceID {
			t.Fatalf("list dropped identity: %+v, %v", rows, err)
		}
	}
	observed.DataResourceID = "project/replacement-branch"
	if _, err := store.FinishProvisionWithDataResource(ctx, claimed, observed, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("ready dataset was replaced: %v", err)
	}
}

func TestPostgresDataResourceProvisionFencesExpiredLeaseAfterLockWait(t *testing.T) {
	store, _, ctx, account := postgresStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	database, _, err := store.Reserve(ctx, postgresTestDatabase(account, "pinned", now), 2)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(ctx, account, database.ID, uuid.NewString(), StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, database.ID, claimed.LeaseToken, "project", now); err != nil {
		t.Fatal(err)
	}
	claimed.ProviderResourceID = "project"
	if _, err := store.pool.Exec(ctx, `update managed_postgres_databases set lease_until=clock_timestamp()+interval '500 milliseconds' where id=$1`, database.ID); err != nil {
		t.Fatal(err)
	}
	locker, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := locker.Exec(ctx, `select id from managed_postgres_databases where id=$1 for update`, database.ID); err != nil {
		t.Fatal(err)
	}
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := store.FinishProvisionWithDataResource(callCtx, claimed, ObservedDatabase{ProviderResourceID: "project", DataResourceID: "project/branch", Status: ProviderStatusReady, Spec: database.Spec}, now)
		result <- err
	}()
	blocked := false
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if err := store.pool.QueryRow(callCtx, `select exists(select 1 from pg_stat_activity where wait_event_type='Lock' and position('LockManagedPostgresLifecycleDatabase' in query)>0)`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("pin did not wait for the catalog row lock")
	}
	time.Sleep(600 * time.Millisecond)
	if err := locker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrConflict) {
		t.Fatalf("expired lease pinned data: %v", err)
	}
	actual, err := store.Get(ctx, account, database.ID)
	if err != nil || actual.State != StateProvisioning || actual.DataResourceID != "" || actual.LeaseToken != claimed.LeaseToken {
		t.Fatalf("rejected pin changed readiness: %+v, %v", actual, err)
	}
}
