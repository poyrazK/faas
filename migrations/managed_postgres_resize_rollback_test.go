//go:build !no_pg

package migrations_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPostgresResizeMigrationReplayAndRollbackFence(t *testing.T) {
	const version = 20261005174532362
	ctx := t.Context()
	pool := pgtest.Open(t)
	// Later migrations add dependencies on the resize journal. Test the
	// original rollback against the schema that migration owned.
	migrateUpTo(t, ctx, pool, version)
	raw, err := migrations.FS.ReadFile("20261005174532362_managed_postgres_resize.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("resize migration has no rollback")
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal("replay up", err)
	}

	account, database, resize := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, "INSERT INTO accounts (id, email) VALUES ($1, $2)", account, account.String()+"@resize.test"); err != nil {
		t.Fatal("create account", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO managed_postgres_databases (
			id, account_id, name, region, postgres_major, service_class, availability, backend_id, backend_fingerprint,
			provider_resource_id, data_resource_id, state, desired_generation, observed_generation
		) VALUES ($1, $2, 'resize-test', 'us-east-1', 16, 'development', 'single_zone', 'native',
			repeat('a', 64), $1::uuid::text, $1::uuid::text, 'updating', 2, 1)
	`, database, account); err != nil {
		t.Fatal("create updating database", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO managed_postgres_resizes (
			id, account_id, database_id, backend_id, backend_fingerprint, provider_resource_id, data_resource_id,
			source_spec, target_class, generation, created_at
		)
		SELECT $1, account_id, id, backend_id, backend_fingerprint, provider_resource_id, data_resource_id,
			jsonb_build_object('Region', region, 'PostgresMajor', postgres_major, 'Class', service_class,
				'Availability', availability, 'ScaleToZero', scale_to_zero, 'StorageLimitBytes', storage_limit_bytes,
				'RestoreWindowSeconds', restore_window_seconds),
			'burstable', desired_generation, clock_timestamp()
		FROM managed_postgres_databases WHERE id = $2
	`, resize, database); err != nil {
		t.Fatal("create pending resize", err)
	}
	_, err = pool.Exec(ctx, down)
	var rejected *pgconn.PgError
	if !errors.As(err, &rejected) || rejected.Code != "23514" || rejected.ConstraintName != "managed_postgres_resize_conflict" {
		t.Fatalf("pending rollback = %v, want the unresolved resize fence", err)
	}
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "UPDATE managed_postgres_resizes SET state = 'succeeded', completed_at = clock_timestamp() WHERE id = $1", resize); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE managed_postgres_databases
			SET state = 'ready', service_class = 'burstable', observed_generation = desired_generation
			WHERE id = $1`, database)
		return err
	}); err != nil {
		t.Fatal("finish resize", err)
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal("completed rollback", err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal("up after down", err)
	}
}
