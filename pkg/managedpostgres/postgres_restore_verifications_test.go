// adr: 1259
package managedpostgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
)

func postgresVerificationFixture(t *testing.T) (*PostgresStore, Database, RestoreVerification) {
	t.Helper()
	store, _, ctx, account := postgresStoreFixture(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	source := postgresReadyDatabase(t, store, account, "verification-source", at)
	input := postgresTestDatabase(account, "verification-target", at)
	input.RestoreSourceDatabaseID = source.ID
	input.RestoreSourceResourceID = source.ProviderResourceID
	input.RestorePointInTime = at.Add(-time.Minute)
	target, _, err := store.Reserve(ctx, input, 100)
	if err != nil {
		t.Fatal(err)
	}
	target, err = store.Claim(ctx, account, target.ID, "verification-worker", StateProvisioning, at, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, target.ID, target.LeaseToken, "project/br-target", at); err != nil {
		t.Fatal(err)
	}
	target.ProviderResourceID = "project/br-target"
	v := RestoreVerification{ProviderResourceID: target.ProviderResourceID, SourceResourceID: source.ProviderResourceID,
		PointInTime: target.RestorePointInTime, Evidence: "neon-branch-v1|project|br-target|br-source|0/1E25788|2026-10-14T12:20:59Z|parent-data"}
	return store, target, v
}

func TestPostgresRestoreVerificationIsDurableAndAppendOnce(t *testing.T) {
	store, target, v := postgresVerificationFixture(t)
	ctx := t.Context()
	if _, err := store.GetRestoreVerification(ctx, target.AccountID, target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("verification before any was recorded: %v", err)
	}
	if err := store.RecordRestoreVerification(ctx, target, v); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPostgresStore(store.pool)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restarted.GetRestoreVerification(ctx, target.AccountID, target.ID)
	if err != nil || !sameRestoreVerification(got, v) {
		t.Fatalf("restart verification: %+v %v", got, err)
	}
	if err := restarted.RecordRestoreVerification(ctx, target, v); err != nil {
		t.Fatalf("lost write acknowledgement replay: %v", err)
	}
	current, err := restarted.Get(ctx, target.AccountID, target.ID)
	if err != nil || current.State != StateProvisioning {
		t.Fatalf("verification changed the lifecycle: %+v %v", current, err)
	}
	for _, fault := range []string{"evidence", "point", "target", "source", "lease"} {
		t.Run(fault, func(t *testing.T) {
			changed, holder := v, target
			switch fault {
			case "evidence":
				changed.Evidence = strings.Replace(v.Evidence, "0/1E25788", "0/1E257A0", 1)
			case "point":
				changed.PointInTime = v.PointInTime.Add(time.Second)
			case "target":
				changed.ProviderResourceID = "project/br-substitute"
			case "source":
				changed.SourceResourceID = "project/br-foreign"
			case "lease":
				holder.LeaseToken = "stale-worker"
			}
			if err := restarted.RecordRestoreVerification(ctx, holder, changed); err == nil {
				t.Fatal("changed verification accepted")
			}
			got, err := restarted.GetRestoreVerification(ctx, target.AccountID, target.ID)
			if err != nil || !sameRestoreVerification(got, v) {
				t.Fatalf("verification replaced: %+v %v", got, err)
			}
		})
	}
}

func TestPostgresRestoreVerificationMigrationRetainsRecords(t *testing.T) {
	store, target, v := postgresVerificationFixture(t)
	ctx := t.Context()
	if err := store.RecordRestoreVerification(ctx, target, v); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261014173402804_managed_postgres_restore_verifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	for range 2 {
		if _, err := store.pool.Exec(ctx, sections[0]); err != nil {
			t.Fatalf("replay: %v", err)
		}
	}
	_, err = store.pool.Exec(ctx, sections[1])
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "managed_postgres_restore_verifications_retained" {
		t.Fatalf("rollback erased retained verifications: %v", err)
	}
	if got, err := store.GetRestoreVerification(ctx, target.AccountID, target.ID); err != nil || !sameRestoreVerification(got, v) {
		t.Fatalf("verification changed during replay/rollback: %+v %v", got, err)
	}
	// Final account erasure hard-deletes deleted database tombstones; a
	// verification must go with its row, never block GDPR deletion.
	var onDelete string
	if err := store.pool.QueryRow(ctx, `SELECT confdeltype::text FROM pg_constraint
		WHERE conrelid='managed_postgres_restore_verifications'::regclass AND confrelid='managed_postgres_databases'::regclass`).Scan(&onDelete); err != nil || onDelete != "c" {
		t.Fatalf("verification foreign key on delete = %q, want cascade: %v", onDelete, err)
	}
}
