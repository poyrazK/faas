package sched

import (
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261017141024523 — Bounded task evidence, freshness and coordinator progress.
func TestAppTaskObservationPostgresAdmissionAndInvalidTiming(t *testing.T) {
	for _, mode := range []string{"admission_held", "infinite_created", "infinite_lease", "infinite_retry", "future_started", "deleted_active"} {
		t.Run(mode, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			s := state.NewPgStore(pool)
			base := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
			task := observationTask(t, s, "task-corruption", state.AppTaskKindManual, base, nil)
			at := base.Add(time.Minute)
			class := state.AppTaskObservationUnknown
			var boundary time.Time
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(t.Context(), query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "admission_held":
				source, target, cutover := uuid.NewString(), uuid.NewString(), uuid.NewString()
				exec(`INSERT INTO managed_postgres_databases
                 (id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint)
                 VALUES ($1,$3,'source','test',16,'development','single_zone','test',repeat('a',64)),
                        ($2,$3,'target','test',16,'development','single_zone','test',repeat('a',64))`, source, target, task.AccountID)
				exec(`INSERT INTO managed_postgres_cutovers
                 (id,account_id,app_id,scope,source_database_id,target_database_id,
                  source_backend_id,source_backend_fingerprint,source_resource_id,source_generation,
                  target_backend_id,target_backend_fingerprint,target_resource_id,target_generation,
                  state,retry_at,created_at,updated_at,verified_at)
                 VALUES ($1,$2,$3,'default',$4,$5,'test',repeat('a',64),'source',1,
                  'test',repeat('a',64),'target',1,'verified',$6,$6,$6,clock_timestamp())`, cutover, task.AccountID, task.AppID, source, target, base)
				binding := uuid.NewString()
				exec(`INSERT INTO managed_postgres_bindings(id,account_id,database_id,app_id,scope)
                 VALUES ($1,$2,$3,$4,'default')`, binding, task.AccountID, source, task.AppID)
				exec(`INSERT INTO managed_postgres_cutover_credentials
                 (id,cutover_id,source_binding_id,source_credential_generation,environment_key,access,state,
                  provider_identity_id,credential_ref,ciphertext,kid,value_hash,verified_at)
                 VALUES (gen_random_uuid(),$1,$2,1,'DATABASE_URL','read_write','sealed',
                  'fixture-identity','fixture-ref',decode('00','hex'),'fixture-kid','fixture-hash',clock_timestamp())`, cutover, binding)
				exec("UPDATE apps SET managed_postgres_admission_cutover_id=$2,managed_postgres_admission_fenced_at=$3 WHERE id=$1", task.AppID, cutover, base)
				class = state.AppTaskObservationHeld
				if _, err := s.ClaimNextAppTask(t.Context(), "runner", at, time.Minute); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("held task claimable", err)
				}
			case "infinite_created":
				exec("UPDATE app_tasks SET created_at='-infinity' WHERE id=$1", task.ID)
			case "infinite_retry":
				exec("UPDATE app_tasks SET retry_at='infinity' WHERE id=$1", task.ID)
			default:
				claimed, err := s.ClaimNextAppTask(t.Context(), "runner", at, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "infinite_lease" {
					exec("UPDATE app_tasks SET lease_expires_at='infinity' WHERE id=$1", task.ID)
					break
				}
				start := at.Add(time.Second)
				if _, err := s.MarkAppTaskRunning(t.Context(), task.ID, *claimed.LeaseToken, start); err != nil {
					t.Fatal(err)
				}
				if mode == "deleted_active" {
					// Durable active teardown work must not disappear with app intent.
					exec("UPDATE apps SET status='deleted' WHERE id=$1", task.AppID)
					at = start
					class = state.AppTaskObservationRunning
					boundary = start
				}
			}
			expectTaskCohort(t, s, at, 1, class, boundary)
		})
	}
}

func TestAppTaskObservationCronRetryContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := taskObservationStore(t, backend)
			ctx := t.Context()
			account, err := s.CreateAccount(ctx, "task-cron@example.com", api.PlanScale)
			if err != nil {
				t.Fatal(err)
			}
			app, d := primeObservationCandidate(t, s, account.ID, "task-cron", nil)
			cron, err := s.CreateCronWithOptions(ctx, app.ID, "0 0 * * *", "", true, state.CronOptions{Command: []string{"/app/task"}, RetryMax: 1, RetryBackoffSeconds: 60})
			if err != nil {
				t.Fatal(err)
			}
			base := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
			if err := s.UpdateDeploymentStatus(ctx, d.ID, state.DeployLive, ""); err != nil {
				t.Fatal(err)
			}
			task, created, err := s.CreateScheduledCronAppTask(ctx, cron.ID, nil, base)
			if err != nil || !created || task.RetryMax != 1 {
				t.Fatalf("scheduled task admitted=%v retry=%d err=%v", created, task.RetryMax, err)
			}

			claimed, err := s.ClaimNextAppTask(ctx, "runner", base.Add(time.Minute), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, base.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			exit := 42
			code, message := "exit_nonzero", "customer command exited"
			finished := base.Add(70 * time.Second)
			retry, err := s.CompleteAppTask(ctx, state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskFailed, ExitCode: &exit, FailureCode: &code, FailureMessage: &message, FinishedAt: finished})
			if err != nil {
				t.Fatal(err)
			}
			if retry.Status != state.AppTaskQueued || retry.RetryAt == nil {
				t.Fatal("missing retry", retry)
			}
			expectTaskCohort(t, s, finished, 2, state.AppTaskObservationRetryWait, time.Time{})
			expectTaskCohort(t, s, *retry.RetryAt, 2, state.AppTaskObservationQueued, *retry.RetryAt)
		})
	}
}
