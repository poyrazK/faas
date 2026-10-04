//go:build !no_pg

// adr: 583
package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProductionQueueBoundary(t *testing.T) {
	s, _, _ := pgWithPool(t)
	testProductionQueueBoundary(t, s)
}

func TestPgProductionQueueBoundaryRetainsDamagedAndDeletedStageOwnership(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	f := seedQueueConsumers(t, s)
	if _, err := s.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	prod, err := s.EnqueueInvocation(ctx, state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationQueue, QueueName: "orders", DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	assertHidden := func(id string) {
		t.Helper()
		if _, err := s.ProductionQueueInvocationByID(ctx, id); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("damaged stage reader=%v", err)
		}
		if n, err := s.CountPendingInvocations(ctx, f.app.ID, state.InvocationQueue); err != nil || n != 1 {
			t.Fatalf("damaged stage count=%d %v", n, err)
		}
		if rows, err := s.QueuePeek(ctx, f.app.ID, 1, ""); err != nil || len(rows) != 1 || rows[0].ID != prod.ID {
			t.Fatalf("damaged stage peek=%+v %v", rows, err)
		}
		var rollup int
		if err := pool.QueryRow(ctx, `SELECT coalesce(sum(pending),0)::int FROM invocations_pending_per_app WHERE app_id=$1 AND source='queue'`, f.app.ID).Scan(&rollup); err != nil || rollup != 1 {
			t.Fatalf("production rollup=%d %v", rollup, err)
		}
	}
	proofOnly := enqueueStageQueue(t.Context(), t, s, f)
	if _, err := pool.Exec(ctx, `UPDATE invocations SET environment_id=NULL,headers='{}' WHERE id=$1`, proofOnly.ID); err != nil {
		t.Fatal(err)
	}
	assertHidden(proofOnly.ID)
	release, err := s.PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, "stage", 1800, []state.ProjectReleaseMember{{AppID: f.app.ID, DeploymentID: f.dep.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, pin := range []struct{ key, value string }{
		{"x-gregale-revision", f.dep.ID},
		{"X-GREGALE-REVISION", " \n urn:uuid:{" + strings.ToUpper(f.dep.ID) + "}\t "},
		{"x-gregale-release", "{" + strings.ReplaceAll(release.ID, "-", "") + "}"},
	} {
		inv := enqueueStageQueue(t.Context(), t, s, f)
		headers, _ := json.Marshal(map[string]any{pin.key: pin.value, "unrelated": 42})
		if _, err := pool.Exec(ctx, `DELETE FROM invocation_environment_queue_admissions WHERE invocation_id=$1`, inv.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE invocations SET environment_id=NULL,headers=$2 WHERE id=$1`, inv.ID, headers); err != nil {
			t.Fatal(err)
		}
		assertHidden(inv.ID)
	}
	dead := enqueueStageQueue(t.Context(), t, s, f)
	if _, err := s.ClaimInvocationWithCap(ctx, dead.ID, "", 300, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.FailInvocation(ctx, dead.ID, "private", time.Second, 1); err != nil {
		t.Fatal(err)
	}
	var eventID string
	var owned bool
	if err := pool.QueryRow(ctx, `SELECT id::text,environment_owned FROM dead_letter_events WHERE source='invocation' AND source_id=$1`, dead.ID).Scan(&eventID, &owned); err != nil || !owned {
		t.Fatalf("durable ledger owner=%t %v", owned, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM invocations WHERE id=$1`, dead.ID); err != nil {
		t.Fatal(err)
	}
	// Even source deletion and an attempted marker reset cannot reclassify it.
	if _, err := pool.Exec(ctx, `UPDATE dead_letter_events SET environment_owned=false WHERE id=$1`, eventID); err != nil {
		t.Fatal(err)
	}
	for _, action := range []func() error{
		func() error { _, err := s.DeadLetterEventByID(ctx, f.app.ID, eventID); return err },
		func() error { _, err := s.DeadLetterEventByAccountID(ctx, f.account.ID, eventID); return err },
		func() error { _, err := s.ReplayDeadLetterEvent(ctx, f.account.ID, f.app.ID, eventID); return err },
		func() error { _, err := s.ReplayDeadLetterEventForAccount(ctx, f.account.ID, eventID); return err },
		func() error { return s.DeleteDeadLetterEvent(ctx, f.account.ID, f.app.ID, eventID) },
		func() error { return s.DeleteDeadLetterEventForAccount(ctx, f.account.ID, eventID) },
	} {
		if err := action(); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("orphan stage ledger action=%v", err)
		}
	}
	for _, read := range []func() ([]state.DeadLetterEvent, error){
		func() ([]state.DeadLetterEvent, error) { return s.ListDeadLetterEvents(ctx, f.app.ID, 1, "") },
		func() ([]state.DeadLetterEvent, error) {
			return s.ListDeadLetterEventsForAccount(ctx, f.account.ID, 1, "")
		},
	} {
		if rows, err := read(); err != nil || len(rows) != 0 {
			t.Fatalf("orphan stage ledger read=%+v %v", rows, err)
		}
	}
	if _, err := s.RetryQueueDeadLetter(ctx, f.account.ID, dead.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted stage retry=%v", err)
	}
}

func TestPgProductionQueueBoundaryMigrationBackfillAndRollback(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	f := seedQueueConsumers(t, s)
	if _, err := s.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	pending, dead := enqueueStageQueue(t.Context(), t, s, f), enqueueStageQueue(t.Context(), t, s, f)
	if _, err := s.ClaimInvocationWithCap(ctx, dead.ID, "", 300, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.FailInvocation(ctx, dead.ID, "private", time.Second, 1); err != nil {
		t.Fatal(err)
	}
	prod, err := s.EnqueueInvocation(ctx, state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationQueue, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	source, err := migrations.FS.ReadFile("20261004095153167_production_invocation_work.sql")
	if err != nil {
		t.Fatal(err)
	}
	bodies := strings.Split(string(source), "-- +goose Down")
	if len(bodies) != 2 {
		t.Fatal("invalid migration sections")
	}
	if _, err := pool.Exec(ctx, bodies[1]); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT pending::int FROM invocations_pending_per_app WHERE app_id=$1 AND source='queue'`, f.app.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("rollback rollup=%d %v", count, err)
	}
	// Simulate a retained failure whose source was deleted before upgrade.
	if _, err := pool.Exec(ctx, `DELETE FROM invocations WHERE id=$1`, dead.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, bodies[0]); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	var owned bool
	if err := pool.QueryRow(ctx, `SELECT environment_owned FROM dead_letter_events WHERE source='invocation' AND source_id=$1`, dead.ID).Scan(&owned); err != nil || !owned {
		t.Fatalf("stage ledger backfill=%t %v", owned, err)
	}
	if rows, err := s.QueuePeek(ctx, f.app.ID, 1, ""); err != nil || len(rows) != 1 || rows[0].ID != prod.ID {
		t.Fatalf("reapplied peek=%+v %v", rows, err)
	}
	if _, err := s.ProductionQueueInvocationByID(ctx, pending.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("reapplied reader=%v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM invocations WHERE id=$1`, dead.ID); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.ListDeadLetterEventsForAccount(ctx, f.account.ID, 1, ""); err != nil || len(rows) != 0 {
		t.Fatalf("backfilled orphan became production=%+v %v", rows, err)
	}
}
