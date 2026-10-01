// adr: 389 — atomic health leases, identity fences, and account isolation.

package managedpostgres

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
)

func TestPostgresHealthLeaseRecoveryAndDeletionFences(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	database := postgresReadyDatabase(t, store, accountID, "health", now.Add(-time.Hour))
	claims := make(chan HealthClaim, 12)
	errorsFound := make(chan error, 12)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			claim, err := store.ClaimHealthCheck(ctx, uuid.NewString(), now, now.Add(30*time.Second))
			if err == nil {
				claims <- claim
			} else if !errors.Is(err, ErrNotFound) {
				errorsFound <- err
			}
		}()
	}
	close(start)
	workers.Wait()
	close(claims)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("concurrent claim: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("claimed %d times, want one provider caller", len(claims))
	}
	old := <-claims
	now = now.Add(31 * time.Second)
	current, err := store.ClaimHealthCheck(ctx, "recovered", now, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("lease recovery: %v", err)
	}
	result := HealthResult{HealthSnapshot: HealthSnapshot{ProviderStatus: "ready", ComputeState: ComputeStateSuspended, CheckedAt: now}, Succeeded: true, NextCheckAt: now.Add(time.Minute)}
	if err := store.FinishHealthCheck(ctx, old, result); !errors.Is(err, ErrConflict) {
		t.Fatalf("old worker overwrote recovery: %v", err)
	}
	if err := store.FinishHealthCheck(ctx, current, result); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ReadHealthSnapshots(ctx, accountID, []string{database.ID})
	if err != nil || rows[database.ID].ComputeState != ComputeStateSuspended || !rows[database.ID].LastSuccessAt.Equal(now) {
		t.Fatalf("snapshot: %+v %v", rows, err)
	}
	if rows, err := store.ReadHealthSnapshots(ctx, uuid.NewString(), []string{database.ID}); err != nil || len(rows) != 0 {
		t.Fatal("health leaked to another account")
	}
	counts, err := store.CountDatabaseHealth(ctx, now.Add(-5*time.Minute), now)
	if err != nil || counts.Healthy != 1 {
		t.Fatalf("counts=%+v %v", counts, err)
	}
	counts, err = store.CountDatabaseHealth(ctx, now.Add(time.Second), now.Add(6*time.Minute))
	if err != nil || counts.Stale != 1 {
		t.Fatalf("stale counts=%+v %v", counts, err)
	}
	// Pin changes hide old observations immediately and schedule a fresh read.
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET provider_resource_id='replacement' WHERE id=$1`, database.ID); err != nil {
		t.Fatal(err)
	}
	if rows, err := store.ReadHealthSnapshots(ctx, accountID, []string{database.ID}); err != nil || len(rows) != 0 {
		t.Fatal("old observation followed a replaced provider identity")
	}
	now = now.Add(time.Second)
	changed, err := store.ClaimHealthCheck(ctx, "replacement", now, now.Add(30*time.Second))
	if err != nil || changed.Database.ProviderResourceID != "replacement" {
		t.Fatalf("new identity not due: %+v %v", changed, err)
	}
	rows, err = store.ReadHealthSnapshots(ctx, accountID, []string{database.ID})
	if err != nil || !rows[database.ID].CheckedAt.IsZero() || rows[database.ID].ProviderStatus != "unknown" {
		t.Fatal("claim relabeled an old observation with the new identity")
	}
	deleted, err := store.ClaimDelete(ctx, accountID, database.ID, "delete", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	result.CheckedAt = now
	result.NextCheckAt = now.Add(time.Minute)
	if err := store.FinishHealthCheck(ctx, changed, result); !errors.Is(err, ErrConflict) {
		t.Fatalf("late result after deletion: %v", err)
	}
	if _, err := store.FinishDelete(ctx, database.ID, deleted.LeaseToken, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM managed_postgres_databases WHERE id=$1`, database.ID); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM managed_postgres_health`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("health retained after catalog deletion: %d %v", remaining, err)
	}
}

func TestPostgresHealthMigrationRoundTrip(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	now := time.Now().UTC()
	database := postgresReadyDatabase(t, store, accountID, "health-rollback", now.Add(-time.Hour))
	claim, err := store.ClaimHealthCheck(ctx, "before-rollback", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	result := HealthResult{HealthSnapshot: HealthSnapshot{ProviderStatus: "ready", ComputeState: ComputeStateActive, CheckedAt: now}, Succeeded: true, NextCheckAt: now.Add(time.Minute)}
	if err := store.FinishHealthCheck(ctx, claim, result); err != nil {
		t.Fatal(err)
	}
	source, err := migrations.FS.ReadFile("20261001134628993_managed_postgres_health.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(source), "-- +goose Down")
	down := strings.Split(strings.Split(parts[1], "-- +goose StatementBegin")[1], "-- +goose StatementEnd")[0]
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatalf("health migration down: %v", err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('managed_postgres_health') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("health table survived rollback: %t %v", exists, err)
	}
	if row, err := store.Get(ctx, accountID, database.ID); err != nil || row.State != StateReady || row.ProviderResourceID != database.ProviderResourceID {
		t.Fatal("health rollback changed database lifecycle or placement")
	}
	up := strings.Split(strings.Split(parts[0], "-- +goose StatementBegin")[1], "-- +goose StatementEnd")[0]
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("health migration up: %v", err)
	}
	if _, err := store.ClaimHealthCheck(ctx, "after-rollback", now, now.Add(time.Minute)); err != nil {
		t.Fatalf("health did not recover after migration reapplication: %v", err)
	}
}
