//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgQueueBindingAdmissionRetainsObservedOwnerWhileWaiting(t *testing.T) {
	for _, tc := range []struct {
		name, queue string
		disable     bool
	}{
		{"named rename", "orders", false}, {"unnamed disable", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, pool, ctx := pgStoreWithPool(t)
			account, err := store.CreateAccount(ctx, "admission-owner@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "admission-owner", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
			if err != nil {
				t.Fatal(err)
			}
			original, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AppID: app.ID, AccountID: account.ID, Name: "original", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if tc.disable {
				if _, err := tx.Exec(ctx, `update queue_bindings set enabled=false,updated_at=now() where id=$1`, original.Binding.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `update triggers set enabled=false where id=$1`, original.Changes[0].TriggerID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := tx.Exec(ctx, `update queue_bindings set queue_name='payments',updated_at=now() where id=$1`, original.Binding.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `update triggers set slug='payments' where id=$1`, original.Changes[0].TriggerID); err != nil {
					t.Fatal(err)
				}
			}
			conn, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			var id string
			admitted := make(chan error, 1)
			go func() {
				err := conn.QueryRow(ctx, `insert into invocations(account_id,app_id,source,queue_name,payload,headers,due_at)
      values($1,$2,'queue',$3,'{}','{}',now()) returning id`, account.ID, app.ID, tc.queue).Scan(&id)
				admitted <- err
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
					t.Fatal("admission did not lock its observed owner")
				}
				time.Sleep(time.Millisecond)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-admitted:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("admission did not resume")
			}
			row, err := store.InvocationByID(ctx, id)
			if err != nil || row.QueueBindingID != original.Binding.ID || row.QueueName != tc.queue || row.State != state.InvocationPending {
				t.Fatalf("waiting producer lost observed owner=%+v %v", row, err)
			}
			if rows, err := store.ListDueInvocations(ctx, time.Now(), 100); err != nil || len(rows) != 0 {
				t.Fatalf("owned work fell into generic drain=%d %v", len(rows), err)
			}
		})
	}
}
