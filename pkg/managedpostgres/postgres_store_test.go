package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func postgresStoreFixture(t *testing.T) (*PostgresStore, *pgxpool.Pool, context.Context, string) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	account, err := state.NewPgStore(pool).CreateAccount(ctx, uuid.NewString()+"@postgres-store.test", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	store, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	return store, pool, ctx, account.ID
}

func postgresTestDatabase(accountID, name string, at time.Time) Database {
	return Database{
		ID:                 uuid.NewString(),
		AccountID:          accountID,
		Name:               name,
		Spec:               testSpec(),
		BackendID:          "primary-a",
		BackendFingerprint: strings.Repeat("a", 64),
		State:              StateProvisioning,
		DesiredGeneration:  1,
		CreatedAt:          at,
		UpdatedAt:          at,
	}
}

func TestPostgresStoreReservationIsIdempotentAndQuotaIsAtomic(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	at := time.Date(2026, 9, 5, 20, 0, 0, 0, time.UTC)
	firstInput := postgresTestDatabase(accountID, "assets", at)
	first, created, err := store.Reserve(ctx, firstInput, 3)
	if err != nil || !created {
		t.Fatalf("first reservation: created=%v database=%+v err=%v", created, first, err)
	}
	retryInput := postgresTestDatabase(accountID, "assets", at.Add(time.Second))
	retry, created, err := store.Reserve(ctx, retryInput, 3)
	if err != nil || created || retry.ID != first.ID {
		t.Fatalf("idempotent reservation: created=%v database=%+v err=%v", created, retry, err)
	}

	var quotaErrors atomic.Int32
	var unexpected atomic.Int32
	var wait sync.WaitGroup
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, _, reserveErr := store.Reserve(ctx, postgresTestDatabase(accountID, fmt.Sprintf("database-%02d", index), at.Add(time.Duration(index+2)*time.Second)), 3)
			switch {
			case reserveErr == nil:
			case errors.Is(reserveErr, ErrQuotaExceeded):
				quotaErrors.Add(1)
			default:
				unexpected.Add(1)
			}
		}(i)
	}
	wait.Wait()
	items, err := store.List(ctx, accountID)
	if err != nil || len(items) != 3 || quotaErrors.Load() != 10 || unexpected.Load() != 0 {
		t.Fatalf("quota race: items=%d quota_errors=%d unexpected=%d err=%v", len(items), quotaErrors.Load(), unexpected.Load(), err)
	}
	if _, err := store.Get(ctx, uuid.NewString(), first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant-scoped get = %v, want ErrNotFound", err)
	}
}

func TestPostgresStoreLeaseRecoveryRetryAndTombstone(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	start := time.Date(2026, 9, 5, 21, 0, 0, 0, time.UTC)
	input := postgresTestDatabase(accountID, "orders", start)
	database, created, err := store.Reserve(ctx, input, 3)
	if err != nil || !created {
		t.Fatalf("reserve: %+v %v %v", database, created, err)
	}
	if rows, err := store.Due(ctx, false, 20, start); err != nil || len(rows) != 0 {
		t.Fatalf("disabled provisioning was due: rows=%d err=%v", len(rows), err)
	}
	if rows, err := store.Due(ctx, true, 20, start); err != nil || len(rows) != 1 {
		t.Fatalf("provisioning not due: rows=%d err=%v", len(rows), err)
	}

	first, err := store.Claim(ctx, accountID, database.ID, "first-worker", StateProvisioning, start, start.Add(2*time.Minute))
	if err != nil || first.AttemptCount != 1 {
		t.Fatalf("first claim: %+v %v", first, err)
	}
	if _, err := store.Claim(ctx, accountID, database.ID, "contender", StateProvisioning, start, start.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent claim = %v, want ErrConflict", err)
	}
	if err := store.RecordProviderResource(ctx, database.ID, first.LeaseToken, "provider-orders", start.Add(time.Second)); err != nil {
		t.Fatalf("record provider resource: %v", err)
	}

	afterCrash := start.Add(3 * time.Minute)
	rows, err := store.Due(ctx, true, 20, afterCrash)
	if err != nil || len(rows) != 1 || rows[0].ProviderResourceID != "provider-orders" {
		t.Fatalf("expired lease discovery: %+v %v", rows, err)
	}
	if _, err := store.FinishProvision(ctx, database.ID, first.LeaseToken, afterCrash); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired lease completion = %v, want ErrConflict", err)
	}
	recovered, err := store.Claim(ctx, accountID, database.ID, "recovered-worker", StateProvisioning, afterCrash, afterCrash.Add(2*time.Minute))
	if err != nil || recovered.AttemptCount != 2 || recovered.ProviderResourceID != "provider-orders" {
		t.Fatalf("recovery claim: %+v %v", recovered, err)
	}
	if _, err := store.FinishProvision(ctx, database.ID, first.LeaseToken, afterCrash); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale completion = %v, want ErrConflict", err)
	}
	if err := store.Release(ctx, database.ID, recovered.LeaseToken, StateProvisioning, "unavailable", afterCrash, afterCrash.Add(time.Minute)); err != nil {
		t.Fatalf("release with cooldown: %v", err)
	}
	if err := store.Release(ctx, database.ID, "wrong-worker", StateProvisioning, "provider password leaked", afterCrash, afterCrash.Add(time.Minute)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsafe error code = %v, want ErrInvalid", err)
	}
	if rows, err := store.Due(ctx, true, 20, afterCrash.Add(30*time.Second)); err != nil || len(rows) != 0 {
		t.Fatalf("cooldown ignored: rows=%d err=%v", len(rows), err)
	}

	thirdAt := afterCrash.Add(time.Minute)
	third, err := store.Claim(ctx, accountID, database.ID, "third-worker", StateProvisioning, thirdAt, thirdAt.Add(2*time.Minute))
	if err != nil || third.AttemptCount != 3 {
		t.Fatalf("third claim: %+v %v", third, err)
	}
	ready, err := store.FinishProvision(ctx, database.ID, third.LeaseToken, thirdAt.Add(time.Second))
	if err != nil || ready.State != StateReady || ready.AttemptCount != 0 || ready.ObservedGeneration != ready.DesiredGeneration {
		t.Fatalf("finish provision: %+v %v", ready, err)
	}

	deletingAt := thirdAt.Add(2 * time.Second)
	deleting, err := store.Claim(ctx, accountID, database.ID, "delete-worker", StateDeleting, deletingAt, deletingAt.Add(2*time.Minute))
	if err != nil || deleting.AttemptCount != 1 {
		t.Fatalf("delete claim: %+v %v", deleting, err)
	}
	deleted, err := store.FinishDelete(ctx, database.ID, deleting.LeaseToken, deletingAt.Add(time.Second))
	if err != nil || deleted.State != StateDeleted || deleted.DeletedAt == nil {
		t.Fatalf("finish delete: %+v %v", deleted, err)
	}
	if listed, err := store.List(ctx, accountID); err != nil || len(listed) != 0 {
		t.Fatalf("tombstone listed: %+v %v", listed, err)
	}
	if tombstone, err := store.Get(ctx, accountID, database.ID); err != nil || tombstone.State != StateDeleted {
		t.Fatalf("tombstone unavailable for idempotency: %+v %v", tombstone, err)
	}
	replacement, created, err := store.Reserve(ctx, postgresTestDatabase(accountID, "orders", deletingAt.Add(2*time.Second)), 3)
	if err != nil || !created || replacement.ID == database.ID {
		t.Fatalf("name reuse: created=%v replacement=%+v err=%v", created, replacement, err)
	}
}

type concurrentReadyProvider struct {
	provisionCalls atomic.Int32
}

func (*concurrentReadyProvider) Capabilities() Capabilities { return testCapabilities() }

func (p *concurrentReadyProvider) Provision(_ context.Context, request ProvisionRequest) (ObservedDatabase, error) {
	p.provisionCalls.Add(1)
	return ObservedDatabase{
		ProviderResourceID: "upstream-" + request.ResourceID,
		Status:             ProviderStatusReady,
		Spec:               request.Spec,
	}, nil
}

func (*concurrentReadyProvider) Inspect(_ context.Context, providerResourceID string) (ObservedDatabase, error) {
	return ObservedDatabase{ProviderResourceID: providerResourceID, Status: ProviderStatusReady, Spec: testSpec()}, nil
}

func (*concurrentReadyProvider) Update(context.Context, UpdateRequest) (ObservedDatabase, error) {
	return ObservedDatabase{}, ErrUnsupported
}

func (*concurrentReadyProvider) Delete(context.Context, DeleteRequest) (DeleteResult, error) {
	return DeleteResult{Done: true}, nil
}

func (*concurrentReadyProvider) IssueCredentials(context.Context, CredentialRequest) (CredentialMaterial, error) {
	return CredentialMaterial{}, ErrUnsupported
}

func (*concurrentReadyProvider) Usage(_ context.Context, _ string, window UsageWindow) (Usage, error) {
	return Usage{Window: window}, nil
}

func TestPostgresStoreConcurrentReconcilersContactProviderOnce(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	provider := &concurrentReadyProvider{}
	registry := testRegistry(t, provider, nil)
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 23, 0, 0, 0, time.UTC)
	input := postgresTestDatabase(accountID, "concurrent", now)
	input.BackendFingerprint = backend.Fingerprint
	database, created, err := store.Reserve(ctx, input, 3)
	if err != nil || !created {
		t.Fatalf("reserve: %+v %v %v", database, created, err)
	}

	service, err := NewService(registry, store, ServiceOptions{
		LeaseDuration:   2 * time.Minute,
		ProviderTimeout: time.Second,
		PollInterval:    time.Second,
		Now:             func() time.Time { return now },
		NewID:           uuid.NewString,
		NewLeaseToken:   uuid.NewString,
	})
	if err != nil {
		t.Fatal(err)
	}
	newReconciler := func() *Reconciler {
		reconciler, reconcileErr := NewReconciler(service, ReconcilerOptions{
			Interval:            time.Second,
			Now:                 func() time.Time { return now },
			IncludeProvisioning: func() bool { return true },
		})
		if reconcileErr != nil {
			t.Fatal(reconcileErr)
		}
		return reconciler
	}

	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, reconciler := range []*Reconciler{newReconciler(), newReconciler()} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, sweepErr := reconciler.Sweep(ctx)
			if sweepErr != nil && !errors.Is(sweepErr, ErrConflict) {
				results <- sweepErr
			}
		}()
	}
	wait.Wait()
	close(results)
	for sweepErr := range results {
		t.Fatalf("sweep: %v", sweepErr)
	}
	if got := provider.provisionCalls.Load(); got != 1 {
		t.Fatalf("provider provision calls = %d, want 1", got)
	}
	ready, err := store.Get(ctx, accountID, database.ID)
	if err != nil || ready.State != StateReady {
		t.Fatalf("ready database: %+v %v", ready, err)
	}
}
