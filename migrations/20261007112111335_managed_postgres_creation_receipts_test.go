// adr: 638
package migrations_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCreationCustodyMigrationPreservesReceiptsAcrossReplayAndRollback(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	account, err := state.NewPgStore(pool).CreateAccount(ctx, uuid.NewString()+"@custody-migration.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO managed_postgres_creation_receipts
		(kind,resource_id,account_id,backend_id,backend_fingerprint,generation,point_in_time,source_resource_id,provider_resource_id,provider_created_at,cleanup_started_at)
		VALUES ('snapshot','accepted-snapshot',$1,'primary-a',$2,1,now()-interval '1 minute','project/source','project/snapshots/accepted',now(),now())`, account.ID, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	var before string
	if err := pool.QueryRow(ctx, "SELECT row_to_json(c)::text FROM managed_postgres_creation_receipts c").Scan(&before); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261007112111335_managed_postgres_creation_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	for range 2 {
		if _, err := pool.Exec(ctx, sections[0]); err != nil {
			t.Fatalf("replay: %v", err)
		}
	}
	_, err = pool.Exec(ctx, sections[1])
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "managed_postgres_creation_custody_retained" {
		t.Fatalf("rollback erased retained custody: %v", err)
	}
	var after string
	if err := pool.QueryRow(ctx, "SELECT row_to_json(c)::text FROM managed_postgres_creation_receipts c").Scan(&after); err != nil || before != after {
		t.Fatalf("custody changed during replay/rollback: %s -> %s, %v", before, after, err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM managed_postgres_creation_receipts"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, sections[1]); err != nil {
		t.Fatalf("empty rollback: %v", err)
	}
	if _, err := pool.Exec(ctx, sections[0]); err != nil {
		t.Fatalf("reapply after empty rollback: %v", err)
	}
}
