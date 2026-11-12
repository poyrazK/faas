//go:build !no_pg

package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestObjectAccountLockAdmitsForeignKeyInserts: object jobs serialize on the
// account's namespace and account rows. Taking those rows FOR UPDATE also
// blocked the KEY SHARE lock of every foreign-key insert naming the account,
// so one slow object job stalled the account's request journal and filled
// apid's pool (CP stalls 2026-11-05). A job holding the object locks must not
// hold up those inserts.
func TestObjectAccountLockAdmitsForeignKeyInserts(t *testing.T) {
	st, pool, _ := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	// Park a versioning request after its account locks: it takes the account
	// and namespace rows before the bucket this transaction holds.
	fixture, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	var pid int32
	if err = fixture.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.Exec(ctx, "SELECT id FROM object_buckets WHERE id=$1 FOR UPDATE", b.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, e := st.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Enabled")
		result <- e
	}()
	if err = waitObjectJobOnBucket(ctx, pool, pid); err != nil {
		t.Fatal("versioning never held the account while waiting on the bucket:", err)
	}

	insertCtx, insertCancel := context.WithTimeout(ctx, 3*time.Second)
	defer insertCancel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	recorded, err := st.RecordRequestIDJournalBatch(insertCtx, []state.RequestIDJournalEntry{{
		ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, RequestID: "object-lock-fk",
		ReceivedAt: now, ExpiresAt: now.Add(time.Hour),
	}})
	if err != nil || len(recorded) != 1 {
		t.Fatalf("request journal waited on the object job's namespace lock: %v %v", recorded, err)
	}
	if _, err = pool.Exec(insertCtx, "SELECT 1 FROM accounts WHERE id=$1 FOR KEY SHARE", b.AccountID); err != nil {
		t.Fatalf("a foreign-key check on the account waited on the object job: %v", err)
	}

	if err = fixture.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
}

// waitObjectJobOnBucket returns once the versioning request waits on the
// bucket pid holds. It reaches that lock only after the account locks.
func waitObjectJobOnBucket(ctx context.Context, pool *pgxpool.Pool, pid int32) error {
	for {
		var parked bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
 WHERE datname=current_database() AND wait_event_type='Lock'
 AND $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%name: ObjectCapacityLockBucket %')`, pid).Scan(&parked)
		if err != nil || parked {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestObjectFencesLockNamespaceWithoutKeyLock keeps the object fences and
// receipt guards on FOR NO KEY UPDATE: a later redefinition copied from an
// older body would bring back the FOR UPDATE that stalled the account's
// foreign-key inserts.
func TestObjectFencesLockNamespaceWithoutKeyLock(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	rows, err := pool.Query(ctx, `SELECT p.proname FROM pg_proc p JOIN pg_namespace s ON s.oid=p.pronamespace
 WHERE s.nspname=current_schema()
 AND p.proname ~ '^((fence|guard|protect)_(untracked_)?object_|guard_bound_(multipart|upload)_mutation$)'
 AND p.prosrc ~* '(resource_namespaces|from accounts)[^;]*for update' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		t.Errorf("%s locks the account or its namespace FOR UPDATE; use FOR NO KEY UPDATE", name)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// reapplyObjectAccountNoKeyLocks repeats migration 20261105193328820, which
// later relaxes these functions' account and namespace row locks to FOR NO
// KEY UPDATE, so a round trip of an older migration compares only its own
// changes.
func reapplyObjectAccountNoKeyLocks(t *testing.T, ctx context.Context, tx pgx.Tx, functions ...string) {
	t.Helper()
	for _, name := range functions {
		var definition string
		if err := tx.QueryRow(ctx, `SELECT pg_get_functiondef(to_regprocedure($1))`, name+"()").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(definition, "\n")
		relaxed := false
		for i, line := range lines {
			if (strings.Contains(line, "FROM accounts WHERE id=") || strings.Contains(line, "FROM resource_namespaces WHERE id=")) &&
				strings.HasSuffix(line, " FOR UPDATE;") {
				lines[i] = strings.TrimSuffix(line, " FOR UPDATE;") + " FOR NO KEY UPDATE;"
				relaxed = true
			}
		}
		if !relaxed {
			t.Fatalf("%s takes no account lock to relax", name)
		}
		if _, err := tx.Exec(ctx, strings.Join(lines, "\n")); err != nil {
			t.Fatal(err)
		}
	}
}
