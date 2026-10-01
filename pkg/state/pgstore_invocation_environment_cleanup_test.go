//go:build !no_pg

// adr: 375
package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgInvocationEnvironmentOwner(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testInvocationEnvironmentOwner(t, store)
}

func TestPgInvocationEnvironmentCleanupDrainsIdleWork(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := testInvocationEnvironmentCleanupDrainsIdleWork(t, store)
	var domains, lanes, fairness int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM invocation_work_environment_domains WHERE app_id=$1),
		(SELECT count(*) FROM invocation_work_lanes WHERE app_id=$1),
		(SELECT count(*) FROM invocation_work_fairness_lanes WHERE app_id=$1)`, f.app.ID).Scan(&domains, &lanes, &fairness); err != nil {
		t.Fatal(err)
	}
	// One sibling key owner and two lanes (production/sibling) remain.
	if domains != 1 || lanes != 2 || fairness != 0 {
		t.Fatalf("cleanup left lanes: domains=%d keys=%d fairness=%d", domains, lanes, fairness)
	}
}

func TestPgInvocationEnvironmentCleanupWaitsForRunningWork(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testInvocationEnvironmentCleanupWaitsForRunningWork(t, store)
}

func TestPgInvocationEnvironmentClaimRejectsDamagedPlainWork(t *testing.T) {
	for _, fault := range []struct{ name, sql string }{
		{"missing_pin", `UPDATE invocations SET headers='{}'::jsonb WHERE id=$1`},
		{"missing_owner", `UPDATE invocations SET environment_id=NULL WHERE id=$1`},
		{"production_pin", `UPDATE invocations SET headers=jsonb_build_object('X-Gregale-Revision', $2::text) WHERE id=$1`},
		{"shared_producer", `UPDATE invocations SET source='queue',queue_name='shared' WHERE id=$1`},
	} {
		for _, cap := range []bool{false, true} {
			name := fault.name + "/bare"
			if cap {
				name = fault.name + "/capacity"
			}
			t.Run(name, func(t *testing.T) {
				store, ctx, pool := pgWithPool(t)
				f := seedInvocationWorkEnvironment(t, store)
				row, err := store.EnqueueInvocation(ctx, f.request(t, store, "staging"))
				if err != nil {
					t.Fatal(err)
				}
				if fault.name == "production_pin" {
					_, err = pool.Exec(ctx, fault.sql, row.ID, f.production.ID)
				} else {
					_, err = pool.Exec(ctx, fault.sql, row.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				if cap {
					_, err = store.ClaimInvocationWithCap(ctx, row.ID, "", 30, 10)
				} else {
					_, err = store.ClaimInvocation(ctx, row.ID, "", 30)
				}
				if !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
					t.Fatalf("damaged plain work claimed: %v", err)
				}
				actual, err := store.InvocationByID(ctx, row.ID)
				if err != nil || actual.Attempts != 0 || actual.State != state.InvocationPending || actual.QuotaReserved {
					t.Fatalf("rejected claim mutated work: %+v, %v", actual, err)
				}
				var quotaRows int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_async_quota WHERE account_id=$1`, f.account.ID).Scan(&quotaRows); err != nil || quotaRows != 0 {
					t.Fatalf("rejected claim created capacity state: %d, %v", quotaRows, err)
				}
			})
		}
	}
}

func TestPgInvocationEnvironmentCleanupRejectsBrokenOwnership(t *testing.T) {
	for _, fault := range []struct{ name, sql string }{
		{"missing_admission", `DELETE FROM invocation_work_environment_admissions WHERE invocation_id=$1`},
		{"missing_domain", `DELETE FROM invocation_work_environment_domains WHERE kind='key' AND app_id=(SELECT app_id FROM invocations WHERE id=$1)`},
		{"missing_marker", `UPDATE invocations SET environment_id=NULL WHERE id=$1`},
		{"foreign_marker", `UPDATE invocations SET environment_id=(SELECT id FROM project_environments WHERE slug='empty-stage' AND project_id=(SELECT project_id FROM apps WHERE id=invocations.app_id)) WHERE id=$1`},
	} {
		t.Run(fault.name, func(t *testing.T) {
			store, ctx, pool := pgWithPool(t)
			f := seedInvocationWorkEnvironment(t, store)
			row, err := store.EnqueueKeyedInvocation(ctx, f.request(t, store, "staging"), f.serial, "s:one", "s:customer")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, fault.sql, row.ID); err != nil {
				t.Fatal(err)
			}
			retireInvocationEnvironmentDeployments(t, store, f, "staging")
			if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "staging"); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
				t.Fatalf("broken ownership removed environment: %v", err)
			}
			if _, err := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, "staging"); err != nil {
				t.Fatalf("cleanup left partial environment: %v", err)
			}
			if inv, err := store.InvocationByID(ctx, row.ID); err != nil || inv.State != state.InvocationPending {
				t.Fatalf("cleanup removed unauthenticated work: %+v, %v", inv, err)
			}
		})
	}
}

func TestPgInvocationEnvironmentCleanupRejectsBrokerReferences(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedInvocationWorkEnvironment(t, store)
	row, err := store.EnqueueKeyedInvocation(ctx, f.request(t, store, "staging"), f.serial, "s:one", "s:customer")
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, f.app.ID, "kafka", "orders", true,
		[]byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	recordID, err := store.InsertTriggerRecord(ctx, trigger.ID.String(), "partition-0-offset-1", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Broker work has no stage ownership yet. A reference to an owned stage
	// lane must stop cleanup rather than delete a lock beneath that record.
	if _, err := pool.Exec(ctx, `UPDATE trigger_records r SET
		work_policy_name=i.work_policy_name,work_key_digest=i.work_key_digest,
		work_sequence=i.work_sequence,work_policy_revision=i.work_policy_revision,
		work_fairness_digest=i.work_fairness_digest,work_fairness_limit=i.work_fairness_limit,
		work_expires_at=i.work_expires_at FROM invocations i WHERE i.id=$1 AND r.id=$2`, row.ID, recordID); err != nil {
		t.Fatal(err)
	}
	retireInvocationEnvironmentDeployments(t, store, f, "staging")
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "staging"); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
		t.Fatalf("unsupported broker reference was removed: %v", err)
	}
	if _, err := store.InvocationByID(ctx, row.ID); err != nil {
		t.Fatalf("refused cleanup removed invocation: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invocation_work_lanes WHERE app_id=$1 AND policy_name=$2 AND key_digest=$3`, f.app.ID, f.serial.Name, row.WorkKeyDigest).Scan(&count); err != nil || count != 1 {
		t.Fatalf("refused cleanup removed broker lane: %d, %v", count, err)
	}
}

func TestPgInvocationEnvironmentDeletionFencesAdmission(t *testing.T) {
	for _, kind := range []string{"plain", "keyed", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			store, ctx, pool := pgWithPool(t)
			f := seedInvocationWorkEnvironment(t, store)
			request := f.request(t, store, "staging")
			env, err := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, "staging")
			if err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			var id string
			if err := tx.QueryRow(ctx, `SELECT id FROM project_environments WHERE id=$1 FOR UPDATE`, env.ID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			var pid int
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			operationCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				switch kind {
				case "plain":
					_, err = store.EnqueueInvocation(operationCtx, request)
				case "keyed":
					_, err = store.EnqueueKeyedInvocation(operationCtx, request, f.latest, "s:one")
				case "cancel":
					_, err = store.CancelPendingEnvironmentKeyedInvocations(operationCtx, f.account.ID, f.app.ID, "staging", "latest", "s:one", uuid.NewString(), env.ID)
				}
				done <- err
			}()
			waitForEnvironmentLock(t, operationCtx, pool, pid)
			// The environment lock used by deletion wins before admission. Its
			// removal is committed while the writer waits on that exact lock.
			if _, err := tx.Exec(ctx, `DELETE FROM project_environments WHERE id=$1`, env.ID); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("late scoped writer survived deletion: %v", err)
			}
			var invocations, lanes, receipts int
			if err := pool.QueryRow(ctx, `SELECT
				(SELECT count(*) FROM invocations WHERE app_id=$1),
				(SELECT count(*) FROM invocation_work_lanes WHERE app_id=$1),
				(SELECT count(*) FROM invocation_work_cancellations WHERE app_id=$1)`, f.app.ID).Scan(&invocations, &lanes, &receipts); err != nil {
				t.Fatal(err)
			}
			if invocations != 0 || lanes != 0 || receipts != 0 {
				t.Fatalf("late writer partially committed: invocations=%d lanes=%d receipts=%d", invocations, lanes, receipts)
			}
		})
	}
}

func waitForEnvironmentLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool, pid int) {
	t.Helper()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("writer did not take the environment lock")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestPgInvocationEnvironmentOwnerMigrationBackfillsPins(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedInvocationWorkEnvironment(t, store)
	owners := make(map[string]string)
	for _, scope := range []string{"production", "staging", "empty-stage"} {
		request := f.request(t, store, scope)
		row, err := store.EnqueueInvocation(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		owners[row.ID] = row.EnvironmentID
	}
	request := f.request(t, store, "staging")
	request.Headers = []byte(`{"X-Gregale-Revision":"` + f.stage.ID + `"}`)
	row, err := store.EnqueueInvocation(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	owners[row.ID] = row.EnvironmentID
	row, err = store.EnqueueKeyedInvocation(ctx, f.request(t, store, "staging"), f.serial, "s:proof", "s:customer")
	if err != nil {
		t.Fatal(err)
	}
	owners[row.ID] = row.EnvironmentID
	// The durable keyed proof is authoritative even when its pin is missing.
	if _, err := pool.Exec(ctx, `UPDATE invocations SET headers='{}'::jsonb WHERE id=$1`, row.ID); err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "unbound-app", WorkloadName: "unbound", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	foreignRow, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: foreign.ID, AccountID: f.account.ID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invocations SET headers=jsonb_build_object('X-Gregale-Release',$2::text) WHERE id=$1`, foreignRow.ID, f.stageRelease.ID); err != nil {
		t.Fatal(err)
	}
	owners[foreignRow.ID] = ""
	ambiguous, err := store.EnqueueInvocation(ctx, f.request(t, store, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invocations SET headers=jsonb_build_object('X-Gregale-Release',$2::text,'X-Gregale-Revision',$3::text) WHERE id=$1`, ambiguous.ID, f.stageRelease.ID, f.stage.ID); err != nil {
		t.Fatal(err)
	}
	owners[ambiguous.ID] = ""
	raw, err := migrations.FS.ReadFile("20261001030000000_invocation_environment_owner.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing migration down section")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	for id, expected := range owners {
		actual, err := store.InvocationEnvironmentID(ctx, id)
		if err != nil || actual != expected {
			t.Fatalf("backfilled owner: invocation=%s owner=%s want=%s, %v", id, actual, expected, err)
		}
	}
}
