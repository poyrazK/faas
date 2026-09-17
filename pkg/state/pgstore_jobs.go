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
// in schema-add order; image materialization columns follow it.
const jobSelectCols = `id, account_id, kind, name, image_ref, ram_mb, task_timeout_s,
       max_parallelism, retry_max, env_overrides, status, created_at,
       updated_at, command, coalesce(image_resolved_digest, ''),
       coalesce(image_storage_key, ''), image_materialization_status,
       coalesce(image_materialization_error, ''),
       image_materialized_at`

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
       next_attempt_at, lease_token, lease_expires_at, last_lease_node,
       log_content, log_truncated`

// jobTaskSelectColsQualified is the same column order as jobTaskSelectCols,
// with an explicit table qualifier for joins that also expose a status column.
const jobTaskSelectColsQualified = `job_tasks.run_id, job_tasks.task_index,
       job_tasks.status, job_tasks.attempt, job_tasks.instance_id,
       job_tasks.error_class, job_tasks.error_message, job_tasks.exit_code,
       job_tasks.started_at, job_tasks.finished_at, job_tasks.created_at,
       job_tasks.next_attempt_at, job_tasks.lease_token,
       job_tasks.lease_expires_at, job_tasks.last_lease_node,
       job_tasks.log_content, job_tasks.log_truncated`

// scanJobCols reads the jobSelectCols row into a Job. Nullable columns
// don't apply (every column on jobs is NOT NULL), but env_overrides
// is jsonb — pgx decodes it into json.RawMessage directly via Scan.
func scanJobCols(scan func(...any) error) (Job, error) {
	var j Job
	var envOverrides []byte
	if err := scan(&j.ID, &j.AccountID, &j.Kind, &j.Name, &j.ImageRef, &j.RAMMB,
		&j.TaskTimeoutS, &j.MaxParallelism, &j.RetryMax, &envOverrides, &j.Status,
		&j.CreatedAt, &j.UpdatedAt, &j.Command, &j.ImageResolvedDigest,
		&j.ImageStorageKey, &j.ImageMaterializationStatus,
		&j.ImageMaterializationError, &j.ImageMaterializedAt); err != nil {
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
		&t.LastLeaseNode, &t.LogContent, &t.LogTruncated); err != nil {
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

type pgxQueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
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
		   image_resolved_digest = case when $3 is null then image_resolved_digest else null end,
		   image_storage_key = case when $3 is null then image_storage_key else null end,
		   image_materialization_status = case when $3 is null then image_materialization_status else 'pending' end,
		   image_materialization_error = case when $3 is null then image_materialization_error else null end,
		   image_materialized_at = case when $3 is null then image_materialized_at else null end,
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

// JobListPendingImageMaterialization returns active jobs whose source image
// has not yet produced a canonical ext4 artifact. The order is stable so a
// restart drains the oldest pending work first.
func (s *PgStore) JobListPendingImageMaterialization(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 64
	}
	rows, err := s.pool.Query(ctx,
		`select `+jobSelectCols+` from jobs
		  where status <> 'deleted'
		    and image_materialization_status = 'pending'
		  order by updated_at asc, id asc
		  limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list pending job image materializations: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

// JobSetImageMaterialization atomically publishes the resolved digest and
// storage key, or records a failed attempt. A ready row must carry both
// immutable identifiers; failed/pending rows clear the materialized timestamp.
func (s *PgStore) JobSetImageMaterialization(ctx context.Context, id, sourceRef, status, resolvedDigest, storageKey, failure string) (Job, error) {
	if status != "pending" && status != "ready" && status != "failed" {
		return Job{}, fmt.Errorf("state: invalid job image materialization status %q", status)
	}
	if status == "ready" && (resolvedDigest == "" || storageKey == "") {
		return Job{}, fmt.Errorf("state: ready job image materialization requires digest and storage key")
	}
	row := s.pool.QueryRow(ctx,
		`update jobs set
		   image_materialization_status = $3,
		   image_resolved_digest = nullif($4, ''),
		   image_storage_key = nullif($5, ''),
		   image_materialization_error = nullif($6, ''),
		   image_materialized_at = case when $3 = 'ready' then now() else null end,
		   updated_at = now()
		 where id = $1::uuid and image_ref = $2 and status <> 'deleted'
		 returning `+jobSelectCols,
		id, sourceRef, status, resolvedDigest, storageKey, failure)
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
//   - ErrNotFound when the parent job_id is gone or belongs to another account.
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

	// 1. Insert the run row. Parallelism is NOT NULL on job_runs, so a
	// nil per-run override must be materialized from jobs.max_parallelism.
	// retry_max and task_timeout_s stay nullable: nil is their durable
	// "inherit from the parent job at dispatch time" representation.
	// INSERT ... SELECT keeps the default lookup and insert atomic, and the
	// account predicate prevents a caller from pairing another tenant's job
	// with its own account row.
	row := tx.QueryRow(ctx,
		`insert into job_runs (job_id, account_id, trigger_kind, env_overrides,
		                       tasks, parallelism, retry_max, task_timeout_s)
		 select j.id, $2::uuid, $3, $4::jsonb, $5,
		        coalesce($6, j.max_parallelism), $7, $8
		   from jobs j
		  where j.id = $1::uuid
		    and j.account_id = $2::uuid
		    and j.status <> 'deleted'
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

// JobRunListActive keeps the operator incident query bounded to work that can
// still hold scheduler capacity. The optional account filter uses the existing
// partial active index; the fleet view is intentionally capped by the caller.
func (s *PgStore) JobRunListActive(ctx context.Context, accountID string, limit, offset int) ([]JobRun, error) {
	query := `select ` + jobRunSelectCols + ` from job_runs
		  where aggregate_status in ('queued', 'running')
		  order by created_at desc
		  limit $1 offset $2`
	args := []any{limit, offset}
	if accountID != "" {
		query = `select ` + jobRunSelectCols + ` from job_runs
		  where account_id = $1::uuid
		    and aggregate_status in ('queued', 'running')
		  order by created_at desc
		  limit $2 offset $3`
		args = []any{accountID, limit, offset}
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("state: list active runs: %w", err)
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
	return queryJobRunRecompute(ctx, s.pool, runID, false)
}

// queryJobRunRecompute updates counters from the task rows visible to q.
// preserveCancel keeps JobRunCancel's explicit terminal-status semantics
// while still using the same counter query as ordinary recomputation.
func queryJobRunRecompute(ctx context.Context, q pgxQueryRower, runID string, preserveCancel bool) (JobRun, error) {
	// CTE-first form: PG15 UPDATE...FROM with a subquery that
	// references the UPDATE target (r.id) inside the subquery's
	// WHERE clause errors with "invalid reference to FROM-clause
	// entry" — the FROM alias isn't in scope inside the inner
	// query. Materialising the task counts via a WITH keeps the
	// same logic and reads identically.
	row := q.QueryRow(ctx,
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
		       when $2::boolean and r.aggregate_status in ('queued', 'running') then 'cancelled'
		       when $2::boolean then r.aggregate_status
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
		       when r.started_at is null and ($2::boolean or (
		            coalesce((select running from counts), 0) +
		            coalesce((select queued_or_claimed from counts), 0) > 0
		            or r.aggregate_status in ('queued', 'running'))) then now()
		       else r.started_at
		   end,
		   finished_at = case
		       when $2::boolean and r.aggregate_status in ('queued', 'running') then now()
		       when not $2::boolean and coalesce((select queued_or_claimed from counts), 0) = 0
		            and coalesce((select running from counts), 0) = 0
		            and r.finished_at is null then now()
		       else r.finished_at
		   end
		 where r.id = $1::uuid
		 returning `+jobRunSelectCols,
		runID, preserveCancel)
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

	// 2. Recompute the denormalised counters and aggregate status from
	//    the just-cancelled task rows in the same transaction. Keeping
	//    this query on tx makes the cancellation response agree with the
	//    task rows before the commit becomes visible to later reads.
	run, err := queryJobRunRecompute(ctx, tx, runID, true)
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

func (s *PgStore) JobRunReopenDeadLetter(ctx context.Context, runID string) error {
	tag, err := s.pool.Exec(ctx,
		`update job_runs set
		   dead_letter_count = greatest(dead_letter_count - 1, 0),
		   aggregate_status = 'running',
		   finished_at = null
		 where id = $1::uuid and dead_letter_count > 0`, runID)
	if err != nil {
		return fmt.Errorf("state: reopen dead-letter run %s: %w", runID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- job_tasks -------------------------------------------------------

// JobTaskClaimBatch returns up to `limit` queued candidates ordered by
// created_at ASC. SELECT FOR UPDATE SKIP LOCKED prevents overlap while this
// short transaction is open. After commit, CreateAndClaimJobInstance is the
// authoritative ownership race; it atomically attaches the winner and rolls
// back the losing instance insert without holding a DB lock across cold boot.
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
		`select `+jobTaskSelectColsQualified+` from job_tasks
		  join job_runs r on r.id = job_tasks.run_id
		  join jobs j on j.id = r.job_id
		  where job_tasks.status = 'queued'
		    and (job_tasks.next_attempt_at is null or job_tasks.next_attempt_at <= now())
		    and j.image_materialization_status = 'ready'
		    and j.image_storage_key is not null
		  order by job_tasks.created_at asc
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

// CreateAndClaimJobInstance makes instance creation and queued-task ownership
// one PostgreSQL transaction. In particular, the instance FK is satisfied
// before job_tasks is updated, while a lost queued->claimed race rolls the
// insert back instead of leaving an unbound billable row.
func (s *PgStore) CreateAndClaimJobInstance(ctx context.Context, instanceID, jobID, runID string, taskIndex int, instanceState string, ramMB int, computeNodeID, wakeID, leaseToken string, leaseExpiresAt time.Time, leaseOwnerNodeID string) (Instance, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Instance{}, fmt.Errorf("state: begin create-and-claim job instance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit

	row := tx.QueryRow(ctx,
		`insert into instances (id, app_id, deployment_id, job_id, kind, state, ram_mb, node_id, wake_id, started_at, mode)
		 values ($1::uuid, null, null, $2::uuid, 'job_task', $3, $4, $5::uuid,
		         case when $6::text = '' then gen_random_uuid() else ($6::text)::uuid end, now(), 'job')
		 returning id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		           coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode, request_count`,
		instanceID, jobID, instanceState, ramMB, computeNodeID, wakeID)
	inst, err := scanInstanceCols(row.Scan)
	if err != nil {
		return Instance{}, fmt.Errorf("state: create job instance for atomic claim (instance=%s run=%s task=%d): %w", instanceID, runID, taskIndex, err)
	}

	tag, err := tx.Exec(ctx,
		`update job_tasks set
		   status = 'claimed', instance_id = $3::uuid,
		   lease_token = $4, lease_expires_at = $5,
		   last_lease_node = $6::uuid,
		   started_at = coalesce(started_at, now())
		 where run_id = $1::uuid and task_index = $2 and status = 'queued'`,
		runID, taskIndex, instanceID, leaseToken, leaseExpiresAt.UTC(), leaseOwnerNodeID)
	if err != nil {
		return Instance{}, fmt.Errorf("state: claim task for job instance (%s, %d): %w", runID, taskIndex, err)
	}
	if tag.RowsAffected() == 0 {
		return Instance{}, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return Instance{}, fmt.Errorf("state: commit create-and-claim job instance: %w", err)
	}
	inst.Kind = "job_task"
	inst.JobID = jobID
	inst.JobRunID = runID
	inst.JobTaskIndex = taskIndex
	return inst, nil
}

// JobTaskMarkTerminal transitions a single task to a terminal status
// AND stamps exit_code + error_class + error_message + finished_at.
//
// Returns ErrNotFound when (run_id, task_index) does not resolve OR
// when the task is already terminal (the WHERE clause gates on
// status IN ('queued','claimed')).
func (s *PgStore) JobTaskMarkTerminal(ctx context.Context, runID string, taskIndex int, status string, exitCode int, errorClass, errorMessage string, finishedAt time.Time) error {
	return s.jobTaskMarkTerminal(ctx, runID, taskIndex, status, exitCode, errorClass, errorMessage, "", false, false, finishedAt)
}

// JobTaskMarkTerminalWithLogs settles a task and persists its retained output
// in the same UPDATE. The log write is deliberately limited to the guest exit
// path; reapers continue using JobTaskMarkTerminal and preserve empty output.
func (s *PgStore) JobTaskMarkTerminalWithLogs(ctx context.Context, runID string, taskIndex int, status string, exitCode int, errorClass, errorMessage, logContent string, logTruncated bool, finishedAt time.Time) error {
	return s.jobTaskMarkTerminal(ctx, runID, taskIndex, status, exitCode, errorClass, errorMessage, logContent, logTruncated, true, finishedAt)
}

func (s *PgStore) jobTaskMarkTerminal(ctx context.Context, runID string, taskIndex int, status string, exitCode int, errorClass, errorMessage, logContent string, logTruncated, persistLogs bool, finishedAt time.Time) error {
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
		   log_content   = case when $8 then $9 else log_content end,
		   log_truncated = case when $8 then $10 else log_truncated end,
		   lease_token   = null,
		   lease_expires_at = null
		 where run_id = $1::uuid and task_index = $7
		   and status in ('queued', 'claimed')`,
		runID, status, exitCode, errorClassArg, errorMessageArg, finishedAt.UTC(), taskIndex,
		persistLogs, logContent, logTruncated)
	if err != nil {
		return fmt.Errorf("state: mark task (%s, %d) terminal: %w", runID, taskIndex, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// JobTaskRetry reverses a failed/timeout/oom/cancelled transition back to
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
		   log_content      = '',
		   log_truncated    = false,
		   lease_token       = null,
		   lease_expires_at  = null,
		   last_lease_node   = null
		 where run_id = $1::uuid and task_index = $2
		   and status in ('failed', 'timeout', 'oom', 'cancelled')`,
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

func (s *PgStore) ListOrphanedJobInstances(ctx context.Context, limit int) ([]Instance, error) {
	if limit <= 0 {
		limit = 64
	}
	rows, err := s.pool.Query(ctx,
		`select i.id, i.state, i.ram_mb, coalesce(i.node_id::text, ''),
		        coalesce(i.job_id::text, ''), i.started_at
		   from instances i
		  where i.kind = 'job_task'
		    and i.state in ('waking', 'cold_booting', 'running')
		    and not exists (
		      select 1 from job_tasks t
		       where t.instance_id = i.id and t.status = 'claimed'
		    )
		  order by i.started_at nulls first, i.id
		  limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list orphaned job instances: %w", err)
	}
	defer rows.Close()
	out := make([]Instance, 0)
	for rows.Next() {
		var ins Instance
		if err := rows.Scan(&ins.ID, &ins.State, &ins.RAMMB, &ins.NodeID, &ins.JobID, &ins.StartedAt); err != nil {
			return nil, fmt.Errorf("state: scan orphaned job instance: %w", err)
		}
		ins.Kind = "job_task"
		ins.Mode = string(InstanceModeJob)
		out = append(out, ins)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate orphaned job instances: %w", err)
	}
	return out, nil
}
