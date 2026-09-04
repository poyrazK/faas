// pgstore_jobs.go is the ADR-099 / issue #1184 Workstream A Postgres
// adapter for the JobStore sub-interface (defined in jobs.go). Mirrors
// the cron pgstore style: raw SQL via s.pool.QueryRow / Query /
// BeginTx, mapErr at the boundary, errors wrapped with %w + operation
// context per the package-wide CLAUDE.md convention.
//
// Each method's docstring names the failure modes (ErrNotFound, ErrConflict,
// *JobQuotaError, mapErr-wrapped SQL errors) and the lock semantics (the
// few methods that hold a transaction-scoped row lock call it out
// explicitly).
//
// Column-order contract: scanJobTaskCols / scanJobRunCols / scanJobCols
// are the single source of column order for SELECTs against
// job_tasks / job_runs / jobs respectively. Every SELECT lists the
// columns in the order the helper expects; if a future column lands,
// update both the helper and every SELECT in the same commit so a
// SELECT-write drift cannot silently swallow a column.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// --- column-order contracts ----------------------------------------

// jobSelectCols is the canonical column order for jobs. Keep in lock-
// step with scanJobCols and the migrations 00255 + 00572 DDL. The
// command column (00572) is the last entry so the SELECT list reads
// in schema-add order.
const jobSelectCols = `id, account_id, kind, name, image_ref, ram_mb, task_timeout_s,
       max_parallelism, retry_max, env_overrides, status, created_at,
       updated_at, command`

// jobRunSelectCols is the canonical column order for job_runs.
// Includes dead_letter_count (00574). ORDER BY id keeps the contract
// stable when the dashboard adds new columns.
const jobRunSelectCols = `id, job_id, account_id, trigger_kind, env_overrides, tasks,
       parallelism, retry_max, task_timeout_s, aggregate_status,
       tasks_succeeded, tasks_failed, tasks_cancelled, tasks_running,
       dead_letter_count, started_at, finished_at, created_at`

// jobTaskSelectCols is the canonical column order for job_tasks.
// Includes exit_code + next_attempt_at (00571), lease_token +
// lease_expires_at + last_lease_node (00574). The dispatch-tick
// SELECT FOR UPDATE SKIP LOCKED in JobTaskClaimBatch uses this list
// too — same row surface, same scan helper.
const jobTaskSelectCols = `run_id, task_index, status, attempt, instance_id, error_class,
       error_message, exit_code, started_at, finished_at, created_at,
       next_attempt_at, lease_token, lease_expires_at, last_lease_node`

// scanJobCols reads the jobSelectCols row into a Job. Nullable columns
// don't apply (every column on jobs is NOT NULL), but env_overrides
// is jsonb — pgx decodes it into json.RawMessage directly via Scan.
func scanJobCols(scan func(...any) error) (Job, error) {
	var j Job
	var envOverrides []byte
	if err := scan(&j.ID, &j.AccountID, &j.Kind, &j.Name, &j.ImageRef, &j.RAMMB,
		&j.TaskTimeoutS, &j.MaxParallelism, &j.RetryMax, &envOverrides, &j.Status,
		&j.CreatedAt, &j.UpdatedAt, &j.Command); err != nil {
		return Job{}, err
	}
	if len(envOverrides) > 0 {
		j.EnvOverrides = json.RawMessage(envOverrides)
	}
	return j, nil
}

// scanJobRunCols reads the jobRunSelectCols row into a JobRun.
// Nullable columns (retry_max, task_timeout_s, started_at, finished_at)
// scan into *int / *time.Time so the distinction between "unset" and
// "zero" is preserved.
func scanJobRunCols(scan func(...any) error) (JobRun, error) {
	var r JobRun
	var envOverrides []byte
	if err := scan(&r.ID, &r.JobID, &r.AccountID, &r.TriggerKind, &envOverrides,
		&r.Tasks, &r.Parallelism, &r.RetryMax, &r.TaskTimeoutS, &r.AggregateStatus,
		&r.TasksSucceeded, &r.TasksFailed, &r.TasksCancelled, &r.TasksRunning,
		&r.DeadLetterCount, &r.StartedAt, &r.FinishedAt, &r.CreatedAt); err != nil {
		return JobRun{}, err
	}
	if len(envOverrides) > 0 {
		r.EnvOverrides = json.RawMessage(envOverrides)
	}
	return r, nil
}

// scanJobTaskCols reads the jobTaskSelectCols row into a JobTask.
// Every nullable column scans into its *T type so the JobTask surface
// is pointer-clean — caller code never has to deal with sql.Null*.
func scanJobTaskCols(scan func(...any) error) (JobTask, error) {
	var t JobTask
	if err := scan(&t.RunID, &t.TaskIndex, &t.Status, &t.Attempt, &t.InstanceID,
		&t.ErrorClass, &t.ErrorMessage, &t.ExitCode, &t.StartedAt, &t.FinishedAt,
		&t.CreatedAt, &t.NextAttemptAt, &t.LeaseToken, &t.LeaseExpiresAt,
		&t.LastLeaseNode); err != nil {
		return JobTask{}, err
	}
	return t, nil
}

// scanJob wraps scanJobCols and maps pgx.ErrNoRows to ErrNotFound.
func scanJob(row pgx.Row) (Job, error) {
	j, err := scanJobCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Job{}, ErrNotFound
		}
		return Job{}, err
	}
	return j, nil
}

// scanJobRun wraps scanJobRunCols and maps pgx.ErrNoRows to ErrNotFound.
func scanJobRun(row pgx.Row) (JobRun, error) {
	r, err := scanJobRunCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return JobRun{}, ErrNotFound
		}
		return JobRun{}, err
	}
	return r, nil
}

// scanJobTask wraps scanJobTaskCols and maps pgx.ErrNoRows to ErrNotFound.
func scanJobTask(row pgx.Row) (JobTask, error) {
	t, err := scanJobTaskCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return JobTask{}, ErrNotFound
		}
		return JobTask{}, err
	}
	return t, nil
}

func scanJobs(rows pgx.Rows) ([]Job, error) {
	var out []Job
	for rows.Next() {
		j, err := scanJobCols(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func scanJobRuns(rows pgx.Rows) ([]JobRun, error) {
	var out []JobRun
	for rows.Next() {
		r, err := scanJobRunCols(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanJobTasks(rows pgx.Rows) ([]JobTask, error) {
	var out []JobTask
	for rows.Next() {
		t, err := scanJobTaskCols(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- jobs (template) ------------------------------------------------

// JobCreate inserts a new job row. The id defaults to gen_random_uuid()
// at the SQL boundary, so the caller doesn't have to mint one. The
// env_overrides parameter is json.RawMessage — pgx writes it verbatim
// as jsonb, so a nil payload serialises as NULL (the column is NOT
// NULL with DEFAULT '{}'::jsonb, so we coerce nil to '{}' to keep the
// CHECK happy).
//
// Returns the inserted row via RETURNING *. Failure modes:
//   - ErrConflict on duplicate (account_id, name) while the row is
//     non-deleted (jobs_account_name_uniq partial index).
//   - mapErr-wrapped FK violations if accountID does not resolve.
//   - mapErr-wrapped CHECK violations if the caller violated schema
//     constraints (handler validation prevents this in production).
func (s *PgStore) JobCreate(ctx context.Context, accountID, name, kind, imageRef string, command []string, ramMB, taskTimeoutSec, maxParallelism, retryMax int, envOverrides json.RawMessage) (Job, error) {
	if len(envOverrides) == 0 {
		envOverrides = json.RawMessage("{}")
	}
	row := s.pool.QueryRow(ctx,
		`insert into jobs (account_id, kind, name, image_ref, ram_mb,
		                  task_timeout_s, max_parallelism, retry_max,
		                  env_overrides, command)
		 values ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10)
		 returning `+jobSelectCols,
		accountID, kind, name, imageRef, ramMB, taskTimeoutSec, maxParallelism, retryMax, []byte(envOverrides), command)
	return scanJob(row)
}

// JobGetByID returns ErrNotFound when the row is missing OR when it
// is in status='deleted' (soft-tombstoned jobs are invisible to
// customer CRUD — the dashboard's admin audit path is a separate
// surface that bypasses this gate).
func (s *PgStore) JobGetByID(ctx context.Context, id string) (Job, error) {
	row := s.pool.QueryRow(ctx,
		`select `+jobSelectCols+` from jobs where id = $1::uuid and status <> 'deleted'`,
		id)
	return scanJob(row)
}

// JobGetByName mirrors JobGetByID by the customer-facing slug. The
// partial unique index jobs_account_name_uniq (WHERE status<>'deleted')
// means the soft-tombstone is invisible to this lookup.
func (s *PgStore) JobGetByName(ctx context.Context, accountID, name string) (Job, error) {
	row := s.pool.QueryRow(ctx,
		`select `+jobSelectCols+` from jobs where account_id = $1::uuid and name = $2 and status <> 'deleted'`,
		accountID, name)
	return scanJob(row)
}

// JobListByAccount paginates the dashboard's primary index
// (jobs_account_idx: (account_id, created_at DESC)). limit / offset
// are passed verbatim to LIMIT/OFFSET — handler validates the bounds
// (limit > 0, limit <= 200, offset >= 0).
func (s *PgStore) JobListByAccount(ctx context.Context, accountID string, limit, offset int) ([]Job, error) {
	rows, err := s.pool.Query(ctx,
		`select `+jobSelectCols+` from jobs
		 where account_id = $1::uuid and status <> 'deleted'
		 order by created_at desc
		 limit $2 offset $3`,
		accountID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("state: list jobs for account %s: %w", accountID, err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

// JobUpdate mutates the optional fields of a job row. nil pointers
// leave the column untouched (COALESCE guard). updated_at is stamped
// to now() unconditionally so the audit trail reflects the touch.
//
// Failure modes:
//   - ErrNotFound when the row is missing or soft-deleted.
//   - mapErr-wrapped CHECK violations on bad values.
func (s *PgStore) JobUpdate(ctx context.Context, id string, command []string, imageRef *string, ramMB, taskTimeoutSec, maxParallelism, retryMax *int, envOverrides json.RawMessage, status *string) (Job, error) {
	// jsonb needs a non-nil byte slice for the COALESCE branch to
	// type-match (passing nil to a $N::jsonb column throws a
	// type-mismatch error). Empty json.RawMessage → nil-byte-slice
	// → maps to NULL → COALESCE keeps the existing value.
	var envOverridesArg any
	if len(envOverrides) > 0 {
		envOverridesArg = []byte(envOverrides)
	}
	row := s.pool.QueryRow(ctx,
		`update jobs set
		   command         = coalesce($2::text[],  command),
		   image_ref       = coalesce($3,          image_ref),
		   ram_mb          = coalesce($4,          ram_mb),
		   task_timeout_s  = coalesce($5,          task_timeout_s),
		   max_parallelism = coalesce($6,          max_parallelism),
		   retry_max       = coalesce($7,          retry_max),
		   env_overrides   = coalesce($8::jsonb,   env_overrides),
		   status          = coalesce($9,          status),
		   updated_at      = now()
		 where id = $1::uuid and status <> 'deleted'
		 returning `+jobSelectCols,
		id, command, imageRef, ramMB, taskTimeoutSec, maxParallelism, retryMax,
		envOverridesArg, status)
	return scanJob(row)
}

// JobSoftDelete flips status='active'|'paused' to status='deleted'
// iff no live job_task instance exists for the job. Implemented as:
//  1. SELECT count(*) of live (waking/cold_booting/running) instances
//     with kind='job_task' for this job (defensive — the helper
//     ALSO checks this predicate, but the explicit count lets us
//     distinguish "already deleted" from "live instances exist"
//     without a second SELECT after the helper).
//  2. SELECT soft_delete_job_if_no_live_instances($1) — returns
//     TRUE if it flipped a row, FALSE if missing or live.
//  3. If the helper returned TRUE → (deleted=true, hasLiveInstances=false).
//     If FALSE but the row exists with status<>'deleted' →
//     (deleted=false, hasLiveInstances=true) — caller maps to
//     CodeJobHasLiveInstances.
//     If FALSE and the row doesn't exist → ErrNotFound.
//     If FALSE and the row exists with status='deleted' →
//     (deleted=false, hasLiveInstances=false) — idempotent re-call.
//
// Two queries instead of one because the helper's single boolean
// return conflates "missing" with "live" with "no-op success" — the
// explicit count + status check disambiguates without modifying the
// helper signature.
func (s *PgStore) JobSoftDelete(ctx context.Context, id string) (deleted bool, hasLiveInstances bool, err error) {
	// 1. Live-instance count. The helper's predicate mirrors this
	//    exactly (kind='job_task' AND state IN ('waking','cold_booting','running'))
	//    so a non-zero count means the helper will refuse the flip.
	var liveCount int
	if err := s.pool.QueryRow(ctx,
		`select count(*) from instances
		  where job_id = $1::uuid
		    and kind   = 'job_task'
		    and state in ('waking', 'cold_booting', 'running')`,
		id,
	).Scan(&liveCount); err != nil {
		return false, false, fmt.Errorf("state: count live instances for job %s: %w", id, err)
	}

	// 2. Invoke the helper.
	var flipped bool
	if err := s.pool.QueryRow(ctx,
		`select soft_delete_job_if_no_live_instances($1::uuid)`,
		id,
	).Scan(&flipped); err != nil {
		return false, false, fmt.Errorf("state: soft delete job %s: %w", id, err)
	}
	if flipped {
		return true, false, nil
	}

	// 3. Helper refused the flip — distinguish the three reasons.
	//    status<>'deleted'  + liveCount==0  → row is missing (deleted
	//                                         before we got here).
	//    status<>'deleted'  + liveCount>0   → live instances exist;
	//                                         the cap was tripped
	//                                         between our count and
	//                                         the helper call (rare
	//                                         race; surface as
	//                                         hasLiveInstances=true).
	//    status='deleted'                   → idempotent re-call.
	var currentStatus string
	err = s.pool.QueryRow(ctx,
		`select status from jobs where id = $1::uuid`, id,
	).Scan(&currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, ErrNotFound
	}
	if err != nil {
		return false, false, fmt.Errorf("state: read job status after soft delete: %w", err)
	}
	if currentStatus == "deleted" {
		return false, false, nil // idempotent re-call
	}
	// status<>'deleted' + flipped=false → live instances blocked the flip.
	return false, liveCount > 0, nil
}

// JobCountByAccount counts the non-deleted jobs on the account.
// Used by apid's admission-control gate (JobMaxPerAccount enforcement
// happens in apid, not here — this is the read primitive).
func (s *PgStore) JobCountByAccount(ctx context.Context, accountID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`select count(*) from jobs where account_id = $1::uuid and status <> 'deleted'`,
		accountID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("state: count jobs for account %s: %w", accountID, err)
	}
	return count, nil
}

// JobConcurrentByAccount counts the live job_task instances on the
// account (instances.kind='job_task' AND state IN ('waking',
// 'cold_booting','running')).
// Used by apid's admission-control gate to enforce JobConcurrentPerAccount
// before accepting a new run + by meterd's billing sweep for the live-pool
// bill.
func (s *PgStore) JobConcurrentByAccount(ctx context.Context, accountID string) (int, error) {
	// job_task instances carry job_id (no app_id); we resolve the
	// owning account via the FK to jobs. The state predicate
	// matches the soft-delete helper (00576) so terminal instances
	// never count against the cap.
	var count int
	err := s.pool.QueryRow(ctx,
		`select count(*) from instances i
		   join jobs     j on j.id = i.job_id
		  where j.account_id = $1::uuid
		    and i.kind       = 'job_task'
		    and i.state      in ('waking', 'cold_booting', 'running')`,
		accountID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("state: count concurrent jobs for account %s: %w", accountID, err)
	}
	return count, nil
}

// --- job_runs --------------------------------------------------------

// JobRunCreate inserts a job_runs row + fans out `tasks` rows in
// job_tasks, all inside one transaction. The fan-out uses
// generate_series so a 5000-task run is one INSERT, not 5000.
//
// Returned slice: task_index 0..N-1, all status='queued'. The caller
// uses this to echo the slice back without a second round-trip.
//
// Failure modes:
//   - ErrNotFound when the parent job_id is gone (FK violation).
//   - mapErr-wrapped CHECK violations on bad tasks / parallelism.
//   - mapErr-wrapped FK violations on accountID.
func (s *PgStore) JobRunCreate(ctx context.Context, jobID, accountID, triggerKind string, parallelism, retryMaxOverride, taskTimeoutOverride *int, envOverrides json.RawMessage, tasks int) (JobRun, []JobTask, error) {
	if len(envOverrides) == 0 {
		envOverrides = json.RawMessage("{}")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return JobRun{}, nil, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	// 1. Insert the run row. Returning * spreads into scanJobRunCols.
	row := tx.QueryRow(ctx,
		`insert into job_runs (job_id, account_id, trigger_kind, env_overrides,
		                       tasks, parallelism, retry_max, task_timeout_s)
		 values ($1::uuid, $2::uuid, $3, $4::jsonb, $5, $6, $7, $8)
		 returning `+jobRunSelectCols,
		jobID, accountID, triggerKind, []byte(envOverrides),
		tasks, parallelism, retryMaxOverride, taskTimeoutOverride)
	run, err := scanJobRun(row)
	if err != nil {
		// mapErr unwraps FK violations + ErrNoRows to ErrNotFound.
		// 23503 (foreign_key_violation) on a missing jobs.id or
		// accounts.id surfaces as ErrNotFound via the standard
		// mapping in pgstore (look up the pgerrcode branch).
		return JobRun{}, nil, mapErr(err)
	}

	// 2. Fan out the task rows. generate_series is one INSERT, not
	//    N — a 5000-task run stays at one round-trip.
	if _, err := tx.Exec(ctx,
		`insert into job_tasks (run_id, task_index, status)
		 select $1::uuid, g, 'queued' from generate_series(0, $2 - 1) g`,
		run.ID, tasks,
	); err != nil {
		return JobRun{}, nil, fmt.Errorf("state: fan out tasks for run %s: %w", run.ID, err)
	}

	// 3. Read the fanned-out tasks back so the caller can echo them.
	//    Sorted by task_index so the slice is deterministic.
	rows, err := tx.Query(ctx,
		`select `+jobTaskSelectCols+` from job_tasks
		  where run_id = $1::uuid order by task_index`,
		run.ID,
	)
	if err != nil {
		return JobRun{}, nil, fmt.Errorf("state: read fanned tasks for run %s: %w", run.ID, err)
	}
	fanned, err := scanJobTasks(rows)
	rows.Close()
	if err != nil {
		return JobRun{}, nil, fmt.Errorf("state: scan fanned tasks: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return JobRun{}, nil, fmt.Errorf("state: commit job run create: %w", err)
	}
	return run, fanned, nil
}

// JobRunGetByID returns ErrNotFound when the row is missing.
func (s *PgStore) JobRunGetByID(ctx context.Context, id string) (JobRun, error) {
	row := s.pool.QueryRow(ctx,
		`select `+jobRunSelectCols+` from job_runs where id = $1::uuid`, id)
	return scanJobRun(row)
}

// JobRunListByJob paginates the per-job run list (job_runs_job_idx).
func (s *PgStore) JobRunListByJob(ctx context.Context, jobID string, limit, offset int) ([]JobRun, error) {
	rows, err := s.pool.Query(ctx,
		`select `+jobRunSelectCols+` from job_runs
		  where job_id = $1::uuid
		  order by created_at desc
		  limit $2 offset $3`,
		jobID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("state: list runs for job %s: %w", jobID, err)
	}
	defer rows.Close()
	return scanJobRuns(rows)
}

// JobRunListByAccount paginates the per-account run list
// (job_runs_account_idx).
func (s *PgStore) JobRunListByAccount(ctx context.Context, accountID string, limit, offset int) ([]JobRun, error) {
	rows, err := s.pool.Query(ctx,
		`select `+jobRunSelectCols+` from job_runs
		  where account_id = $1::uuid
		  order by created_at desc
		  limit $2 offset $3`,
		accountID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("state: list runs for account %s: %w", accountID, err)
	}
	defer rows.Close()
	return scanJobRuns(rows)
}

// JobRunRecompute recomputes the denormalised counter columns + the
// aggregate_status in a single SQL. The CASE chain reads every
// transition once per recompute; for a 5000-task run that's one
// table scan (job_tasks_run_idx covers it).
//
// The aggregate_status precedence (highest to lowest) is:
//   - any 'running' task                      → 'running'
//   - else any non-terminal ('queued'|'claimed') → 'running'
//   - else tasks_cancelled > 0 (and no failed) → 'cancelled'
//   - else tasks_failed > 0 (and no dead-letter) → 'failed'
//   - else dead_letter_count > 0              → 'dead_letter'
//   - else all succeeded                      → 'succeeded'
//
// The started_at / finished_at columns are stamped alongside so the
// terminal-pair CHECK constraint (job_runs_terminal_pair_chk) stays
// satisfied. finished_at is NULL while the run is non-terminal — it
// is stamped to now() on the first terminal recompute.
func (s *PgStore) JobRunRecompute(ctx context.Context, runID string) (JobRun, error) {
	// CTE-first form: PG15 UPDATE...FROM with a subquery that
	// references the UPDATE target (r.id) inside the subquery's
	// WHERE clause errors with "invalid reference to FROM-clause
	// entry" — the FROM alias isn't in scope inside the inner
	// query. Materialising the task counts via a WITH keeps the
	// same logic and reads identically.
	row := s.pool.QueryRow(ctx,
		`with counts as (
		   select
		     -- 00571 broadened the terminal vocabulary to
		     -- succeeded/failed/timeout/cancelled/oom. CR-E /
		     -- code-review #2 round-5: the previous SUM arms did
		     -- NOT include 'timeout' or 'oom', so reaped tasks
		     -- (reaper_jobs.go::ReapStuckJobTasks flips status to
		     -- 'timeout') contributed 0 to every bucket and the
		     -- aggregate_status fell into the ELSE 'succeeded'
		     -- arm, masking every timeout in the dashboard as a
		     -- green run. Memstore mirror folds timeout/oom into
		     -- canc (memstore_jobs.go:385); align the pgstore
		     -- SQL to match. The retry-eligible statuses
		     -- (failed/timeout/oom) are NOT folded into canc —
		     -- only the dead-letter-tally statuses are.
		     sum(case when status = 'succeeded' then 1 else 0 end) as succ,
		     sum(case when status = 'failed' then 1 else 0 end) as fail,
		     sum(case when status in ('cancelled','timeout','oom') then 1 else 0 end) as canc,
		     sum(case when status = 'claimed' then 1 else 0 end) as running,
		     sum(case when status = 'queued' then 1 else 0 end) as queued_or_claimed
		   from job_tasks where run_id = $1::uuid
		 )
		 update job_runs r set
		   tasks_succeeded = coalesce((select succ from counts), 0),
		   tasks_failed    = coalesce((select fail from counts), 0),
		   tasks_cancelled = coalesce((select canc from counts), 0),
		   tasks_running   = coalesce((select running from counts), 0),
		   aggregate_status = case
		       when coalesce((select running from counts), 0) > 0 then 'running'
		       when coalesce((select queued_or_claimed from counts), 0) > 0 then 'running'
		       when coalesce((select canc from counts), 0) > 0
		            and coalesce((select fail from counts), 0) = 0
		            and r.dead_letter_count = 0 then 'cancelled'
		       when coalesce((select fail from counts), 0) > 0
		            and r.dead_letter_count = 0 then 'failed'
		       when r.dead_letter_count > 0 then 'dead_letter'
		       else 'succeeded'
		   end,
		   started_at = case
		       when r.started_at is null and coalesce((select running from counts), 0) +
		            coalesce((select queued_or_claimed from counts), 0) > 0 then now()
		       else r.started_at
		   end,
		   finished_at = case
		       when coalesce((select queued_or_claimed from counts), 0) = 0
		            and coalesce((select running from counts), 0) = 0
		            and r.finished_at is null then now()
		       else r.finished_at
		   end
		 where r.id = $1::uuid
		 returning `+jobRunSelectCols,
		runID)
	return scanJobRun(row)
}

// JobRunCancel transitions every non-terminal task of the run to
// status='cancelled' and flips the run's aggregate_status to
// 'cancelled' (or stays 'cancelled' if it was already terminal).
//
// Idempotent: a re-call on an already-cancelled run is a no-op success.
// The UPDATE...RETURNING pattern makes this one round-trip on the
// run row; the task UPDATE is a second query but doesn't need a
// RETURNING because the caller doesn't need the task slice back.
func (s *PgStore) JobRunCancel(ctx context.Context, runID string) (JobRun, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return JobRun{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	// 1. Cancel every non-terminal task. WHERE guards on
	//    status NOT IN (terminal set) make this idempotent.
	//
	//    CR-F / code-review #2 round-6: the previous shape flipped
	//    status='cancelled' but did NOT clear lease_token /
	//    lease_expires_at / last_lease_node. For the 20 (out of
	//    200) tasks that were already claimed+leased, the lease
	//    columns survived the cancel — the partial unique index
	//    job_tasks_lease_uniq held 20 stale entries, blocking
	//    lease-key reuse, and JobTaskFindStuck's status='claimed'
	//    gate never reaped them (status is now 'cancelled'). Fix:
	//    clear all three lease columns alongside the status flip so
	//    the row is fully released and the lease-key namespace is
	//    free for reuse.
	if _, err := tx.Exec(ctx,
		`update job_tasks
		    set status            = 'cancelled',
		        finished_at       = coalesce(finished_at, now()),
		        lease_token       = null,
		        lease_expires_at  = null,
		        last_lease_node   = null
		  where run_id = $1::uuid
		    and status in ('queued', 'claimed')`,
		runID,
	); err != nil {
		return JobRun{}, fmt.Errorf("state: cancel tasks for run %s: %w", runID, err)
	}

	// 2. Flip the run's aggregate_status. The CASE keeps an already-
	//    terminal-cancelled/dead_letter run intact; a 'queued' or
	//    'running' run flips to 'cancelled'. started_at is stamped
	//    if it was still NULL (first-touch invariant).
	row := tx.QueryRow(ctx,
		`update job_runs set
		   aggregate_status = case
		       when aggregate_status in ('queued', 'running') then 'cancelled'
		       else aggregate_status
		   end,
		   started_at  = coalesce(started_at, now()),
		   finished_at = case
		       when aggregate_status in ('queued', 'running') then now()
		       else finished_at
		   end
		 where id = $1::uuid
		 returning `+jobRunSelectCols,
		runID)
	run, err := scanJobRun(row)
	if err != nil {
		return JobRun{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return JobRun{}, fmt.Errorf("state: commit job run cancel: %w", err)
	}
	return run, nil
}

// JobRunIncrementDeadLetter bumps dead_letter_count by 1 AND the
// paired tasks_failed by 1. The CHECK constraint
// `dead_letter_count <= tasks_failed` (00574) requires the two
// counters move together — a dead-lettered task is, by definition,
// a failed task that exhausted retries. Bumping both keeps the
// cross-field invariant intact; the recompute that follows reads
// the new totals.
func (s *PgStore) JobRunIncrementDeadLetter(ctx context.Context, runID string) error {
	tag, err := s.pool.Exec(ctx,
		`update job_runs set
		   dead_letter_count = dead_letter_count + 1,
		   tasks_failed      = tasks_failed + 1
		 where id = $1::uuid`,
		runID)
	if err != nil {
		return fmt.Errorf("state: increment dead letter for run %s: %w", runID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- job_tasks -------------------------------------------------------

// JobTaskClaimBatch returns up to `limit` queued tasks ordered by
// created_at ASC, holding a SELECT FOR UPDATE SKIP LOCKED lock for
// the duration of the transaction. Concurrent schedd replicas each
// claim disjoint row sets without retry-on-collision.
//
// Returns the row surface (jobTaskSelectCols) read under the lock.
// The caller is responsible for transitioning queued→claimed via
// JobTaskMarkClaimed in a follow-up call; the lock releases at tx
// commit so two schedulers calling ClaimBatch in lockstep could in
// principle see the same row, but the MarkClaimed WHERE guard
// (status='queued') makes the second attempt a no-op success.
//
// Failure modes:
//   - mapErr-wrapped SQL errors on tx begin / commit.
func (s *PgStore) JobTaskClaimBatch(ctx context.Context, limit int) ([]JobTask, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	rows, err := tx.Query(ctx,
		`select `+jobTaskSelectCols+` from job_tasks
		  where status = 'queued'
		    and (next_attempt_at is null or next_attempt_at <= now())
		  order by created_at asc
		  limit $1
		  for update skip locked`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("state: claim tasks: %w", err)
	}
	tasks, err := scanJobTasks(rows)
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("state: scan claimed tasks: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: commit claim tasks: %w", err)
	}
	return tasks, nil
}

// JobTaskMarkClaimed transitions a single task from queued to
// claimed AND stamps the lease columns. Called by schedd after
// WakeJob mints the microVM instance.
//
// Returns ErrNotFound when (run_id, task_index) does not resolve OR
// when the task is no longer status='queued' (parallel dispatcher
// claimed it first; lost the race).
func (s *PgStore) JobTaskMarkClaimed(ctx context.Context, runID string, taskIndex int, instanceID, leaseToken string, leaseExpiresAt time.Time, nodeID string) error {
	tag, err := s.pool.Exec(ctx,
		`update job_tasks set
		   status            = 'claimed',
		   instance_id       = $2::uuid,
		   lease_token       = $3::uuid,
		   lease_expires_at  = $4,
		   last_lease_node   = $5,
		   started_at        = coalesce(started_at, now())
		 where run_id = $1::uuid and task_index = $6 and status = 'queued'`,
		runID, instanceID, leaseToken, leaseExpiresAt.UTC(), nodeID, taskIndex)
	if err != nil {
		return fmt.Errorf("state: mark task (%s, %d) claimed: %w", runID, taskIndex, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// JobTaskMarkTerminal transitions a single task to a terminal status
// AND stamps exit_code + error_class + error_message + finished_at.
//
// Returns ErrNotFound when (run_id, task_index) does not resolve OR
// when the task is already terminal (the WHERE clause gates on
// status IN ('queued','claimed')).
func (s *PgStore) JobTaskMarkTerminal(ctx context.Context, runID string, taskIndex int, status string, exitCode int, errorClass, errorMessage string, finishedAt time.Time) error {
	// nullify error_class / error_message when the caller passes
	// the empty string — the CHECK constraint on error_class has a
	// closed vocabulary and "" isn't in it.
	var errorClassArg any
	if errorClass != "" {
		errorClassArg = errorClass
	}
	var errorMessageArg any
	if errorMessage != "" {
		errorMessageArg = errorMessage
	}
	tag, err := s.pool.Exec(ctx,
		`update job_tasks set
		   status        = $2,
		   exit_code     = $3,
		   error_class   = $4,
		   error_message = $5,
		   finished_at   = $6,
		   lease_token   = null,
		   lease_expires_at = null
		 where run_id = $1::uuid and task_index = $7
		   and status in ('queued', 'claimed')`,
		runID, status, exitCode, errorClassArg, errorMessageArg, finishedAt.UTC(), taskIndex)
	if err != nil {
		return fmt.Errorf("state: mark task (%s, %d) terminal: %w", runID, taskIndex, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// JobTaskRetry reverses a failed/timeout/oom transition back to
// queued and stamps next_attempt_at with the per-attempt backoff.
// The task's attempt counter is incremented and the prior instance_id
// + lease columns are cleared so the next dispatch mints a fresh
// microVM.
//
// Returns ErrNotFound when (run_id, task_index) does not resolve.
func (s *PgStore) JobTaskRetry(ctx context.Context, runID string, taskIndex int, nextAttemptAt time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`update job_tasks set
		   status            = 'queued',
		   attempt           = attempt + 1,
		   instance_id       = null,
		   next_attempt_at   = $3,
		   started_at        = null,
		   finished_at       = null,
		   error_class       = null,
		   error_message     = null,
		   exit_code         = null,
		   lease_token       = null,
		   lease_expires_at  = null,
		   last_lease_node   = null
		 where run_id = $1::uuid and task_index = $2`,
		runID, taskIndex, nextAttemptAt.UTC())
	if err != nil {
		return fmt.Errorf("state: retry task (%s, %d): %w", runID, taskIndex, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// JobTaskRequeue reverses a CLAIMED-but-not-executed task back to
// queued WITHOUT incrementing attempt. Mirrors JobTaskRetry's
// column-reset contract (clears instance_id + lease columns +
// started_at) but preserves the attempt counter — the customer's
// retry budget is not consumed by transient dispatch-side failures
// (admission denied, vmmd unreachable, run-lookup race, per-account
// quota at cap). See CR-7 / code-review #7.
func (s *PgStore) JobTaskRequeue(ctx context.Context, runID string, taskIndex int, nextAttemptAt time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`update job_tasks set
		   status            = 'queued',
		   instance_id       = null,
		   next_attempt_at   = $3,
		   started_at        = null,
		   lease_token       = null,
		   lease_expires_at  = null,
		   last_lease_node   = null
		 where run_id = $1::uuid and task_index = $2
		   and status in ('queued','claimed')`,
		runID, taskIndex, nextAttemptAt.UTC())
	if err != nil {
		return fmt.Errorf("state: requeue task (%s, %d): %w", runID, taskIndex, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// JobTaskCancel transitions a single task to status='cancelled'.
// Idempotent on tasks already terminal.
//
// Returns ErrNotFound when (run_id, task_index) does not resolve.
func (s *PgStore) JobTaskCancel(ctx context.Context, runID string, taskIndex int) error {
	tag, err := s.pool.Exec(ctx,
		`update job_tasks set
		   status = 'cancelled',
		   finished_at = coalesce(finished_at, now())
		 where run_id = $1::uuid and task_index = $2
		   and status in ('queued', 'claimed')`,
		runID, taskIndex)
	if err != nil {
		return fmt.Errorf("state: cancel task (%s, %d): %w", runID, taskIndex, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// JobTaskFindStuck returns claimed tasks whose lease_expires_at is
// older than now()-ttl. The interval is computed in Go (passed as
// $1 seconds) because pgx can't bind a time.Duration directly.
func (s *PgStore) JobTaskFindStuck(ctx context.Context, ttl time.Duration) ([]JobTask, error) {
	ttlSecs := int64(ttl / time.Second)
	if ttlSecs < 1 {
		ttlSecs = 1
	}
	rows, err := s.pool.Query(ctx,
		`select `+jobTaskSelectCols+` from job_tasks
		  where status = 'claimed'
		    and lease_expires_at is not null
		    and lease_expires_at < now() - make_interval(secs => $1)
		  order by lease_expires_at asc`,
		ttlSecs)
	if err != nil {
		return nil, fmt.Errorf("state: find stuck tasks: %w", err)
	}
	defer rows.Close()
	return scanJobTasks(rows)
}

// JobTaskGet returns ErrNotFound when (run_id, task_index) does not
// resolve.
func (s *PgStore) JobTaskGet(ctx context.Context, runID string, taskIndex int) (JobTask, error) {
	row := s.pool.QueryRow(ctx,
		`select `+jobTaskSelectCols+` from job_tasks
		  where run_id = $1::uuid and task_index = $2`,
		runID, taskIndex)
	return scanJobTask(row)
}

// JobTaskList paginates the per-run task slice (job_tasks_run_idx).
func (s *PgStore) JobTaskList(ctx context.Context, runID string, limit, offset int) ([]JobTask, error) {
	rows, err := s.pool.Query(ctx,
		`select `+jobTaskSelectCols+` from job_tasks
		  where run_id = $1::uuid
		  order by task_index
		  limit $2 offset $3`,
		runID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("state: list tasks for run %s: %w", runID, err)
	}
	defer rows.Close()
	return scanJobTasks(rows)
}

// ListJobInstances returns every kind='job_task' instance for the
// meterd sampler. Uses instances_job_active_idx
// (migrations/00540_job_id_on_delete_restrict.sql) — a partial
// index on (job_id) WHERE kind='job_task' AND state IN
// ('waking','cold_booting','running') — so the sampler is O(active job
// instances), not O(total instances).
//
// Called once per minute from cmd/meterd/main.go's SampleJobsAndRoll
// goroutine; bounded by the partial index on the small active
// subset (terminal job rows are excluded by the index
// predicate). No transaction needed — a single SELECT on a
// hot-index-friendly predicate.
//
// State filter mirrors the memstore path's live-state guard; terminal
// rows are retained for audit but must not be billed or sampled.
//
// Scans only the four columns the sampler reads (id, state,
// ram_mb, job_id) directly into a partial Instance rather than
// running the full scanInstanceCols helper. The sampler uses
// just these four plus a separate JobGetByID lookup for
// account_id; the additional Instance columns are wasted
// bandwidth on a hot 1m ticker.
func (s *PgStore) ListJobInstances(ctx context.Context) ([]Instance, error) {
	rows, err := s.pool.Query(ctx,
		`select id, state, ram_mb, job_id from instances
		  where kind = 'job_task'::text
		    and state in ('waking', 'cold_booting', 'running')
		  order by started_at nulls last, id`)
	if err != nil {
		return nil, fmt.Errorf("state: list job_task instances: %w", err)
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var ins Instance
		var jobID *string
		if err := rows.Scan(&ins.ID, &ins.State, &ins.RAMMB, &jobID); err != nil {
			return nil, fmt.Errorf("state: scan job_task instance: %w", err)
		}
		if jobID != nil {
			ins.JobID = *jobID
			ins.Kind = "job_task"
		}
		out = append(out, ins)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate job_task instances: %w", err)
	}
	return out, nil
}
