// adr: 638
package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestCreationCustodyRollbackSerializesConcurrentAcknowledgements(t *testing.T) {
	pool := pgtest.Open(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	account, err := state.NewPgStore(pool).CreateAccount(ctx, uuid.NewString()+"@custody-rollback.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261007112111335_managed_postgres_creation_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	downSQL := strings.SplitN(string(raw), "-- +goose Down", 2)[1]
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(context.Background())
	_, err = writer.Exec(ctx, `INSERT INTO managed_postgres_creation_receipts
		(kind,resource_id,account_id,backend_id,backend_fingerprint,generation,point_in_time,source_resource_id,provider_resource_id,provider_created_at)
		VALUES ('snapshot','concurrent-snapshot',$1,'primary-a',$2,1,now()-interval '1 minute','project/source','project/snapshots/concurrent',now())`, account.ID, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	down, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pid := down.Conn().PgConn().PID()
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		_, err := down.Exec(ctx, downSQL)
		result <- err
		close(finished)
	}()
	defer func() {
		cancel()
		_ = writer.Rollback(context.Background())
		<-finished
		down.Release()
	}()
	// Wait for the rollback's exclusive table lock, not a timing assumption.
	// The old guard inspected an empty snapshot before waiting here and then
	// dropped the newly committed receipt. The guard must read after the lock.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks
			WHERE pid=$1 AND relation='managed_postgres_creation_receipts'::regclass
			AND mode='AccessExclusiveLock' AND NOT granted)`, pid).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("rollback did not wait for the acknowledgement: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "managed_postgres_creation_custody_retained" {
			t.Fatalf("rollback erased concurrently committed custody: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var target string
	if err := pool.QueryRow(ctx, "SELECT provider_resource_id FROM managed_postgres_creation_receipts WHERE resource_id='concurrent-snapshot'").Scan(&target); err != nil || target != "project/snapshots/concurrent" {
		t.Fatalf("concurrent custody was not preserved: %q, %v", target, err)
	}
}
