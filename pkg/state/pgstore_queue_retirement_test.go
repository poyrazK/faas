//go:build !no_pg

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgQueueBindingRetirementFencesWaitingClaims(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "retirement-fence@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "retirement-fence", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	triggerID := created.Changes[0].TriggerID
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "jobs", DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	receiptID, err := store.InsertTriggerRecord(ctx, triggerID, "receipt", []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	// Hold the same binding row as atomic retirement, then start an UPDATE
	// that has already selected its pending invocation before retirement commits.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `update queue_bindings set retired_at=now(), enabled=false where id=$1`, created.Binding.ID); err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	claimed := make(chan error, 1)
	go func() {
		_, err := conn.Exec(ctx, `update invocations set state='dispatching', attempts=attempts+1,
		lease_expires_at=now()+interval '1 minute' where id=$1 and state='pending'`, inv.ID)
		claimed <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `select coalesce(wait_event_type='Lock',false) from pg_stat_activity where pid=$1`, conn.Conn().PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("claim did not wait on the retiring binding")
		}
		time.Sleep(time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-claimed:
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "queue_binding_retired" {
			t.Fatalf("waiting claim escaped retirement: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waiting claim did not resume")
	}
	if row, err := store.InvocationByID(ctx, inv.ID); err != nil || row.State != state.InvocationPending || row.Attempts != 0 {
		t.Fatalf("waiting claim changed backlog: %+v %v", row, err)
	}
	// This intentionally leaves the cached private trigger enabled. The parent
	// contract must hold its receipt independently of the consumer projection.
	if rows, err := store.ClaimTriggerRecords(ctx, triggerID, 1); err != nil || len(rows) != 0 {
		t.Fatalf("cached consumer claimed retired receipt: %d %v", len(rows), err)
	}
	var receiptState string
	var generation int64
	if err := pool.QueryRow(ctx, `select state, claim_generation from trigger_records where id=$1`, receiptID).Scan(&receiptState, &generation); err != nil || receiptState != "pending" || generation != 0 {
		t.Fatalf("held receipt changed: state=%s generation=%d err=%v", receiptState, generation, err)
	}
	for _, query := range []string{
		`update queue_bindings set retired_at=null where id=$1`,
		`update queue_bindings set queue_name='replacement' where id=$1`,
		`update queue_bindings set max_concurrency=max_concurrency+1 where id=$1`,
	} {
		_, err := pool.Exec(ctx, query, created.Binding.ID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "queue_binding_retirement_identity" {
			t.Fatalf("retirement identity changed: %v", err)
		}
	}
	for _, tc := range []struct{ query, id, constraint string }{
		{`delete from queue_bindings where id=$1`, created.Binding.ID, "queue_binding_retirement_identity"},
		{`delete from triggers where id=$1`, triggerID, "queue_consumer_durable_identity"},
	} {
		_, err := pool.Exec(ctx, tc.query, tc.id)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != tc.constraint {
			t.Fatalf("retained identity deleted: %v", err)
		}
	}
	if id, err := store.TriggerRecordIDByItemIdentifier(ctx, triggerID, "receipt"); err != nil || id != receiptID {
		t.Fatalf("delete guard lost receipt: %q %v", id, err)
	}
	// App cascades are valid once the existing invocation retention contract
	// allows deletion. Only this test's fixture invocation is removed here.
	if _, err := pool.Exec(ctx, `delete from invocations where id=$1`, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `delete from apps where id=$1`, app.ID); err != nil {
		t.Fatalf("retirement obstructed permitted parent cascade: %v", err)
	}

}
