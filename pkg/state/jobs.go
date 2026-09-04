// jobs.go is the ADR-099 / issue #1184 Workstream A foundation layer
// for run-to-completion jobs. Three tables (jobs / job_runs / job_tasks)
// land across migrations 00255, 00256, 00257, 00571, 00572, 00574, 00575,
// 00576, 00577, 00578 — this file defines the Go-side domain types + the
// JobStore sub-interface that PgStore and MemStore satisfy.
//
// Conventions mirror the cron / trigger sub-surfaces in this package:
//   - raw SQL via the cron-pattern (queries.sql is for sqlc; jobs is a
//     later-cluster add and rides the raw-SQL path until PR-B fences
//     carve sqlc queries out of queries.sql);
//   - quota errors carry the trip-scope + observed/limit pair so apid
//     can render a precise copy;
//   - all nullable columns surface as *string / *int / *time.Time so
//     "unset" stays distinguishable from "zero".
//
// The CRUD surface stays narrow: apid owns POST/PATCH/DELETE on jobs
// (customer intent), schedd owns the dispatch tick (claim + mark + retry),
// and the reaper owns the stuck-task recovery path. Anything that crosses
// the boundary between those three lands here so the API is testable in
// isolation from both PgStore and MemStore.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- Domain types (mirrors schema in migrations/00255, 00256, 00257,
//     00571, 00572, 00574, 00575, 00576, 00577, 00578) --------------

// Job is one row of public.jobs (migrations/00255 + 00572 for command).
// Kind is the closed vocabulary ('app' | 'function') enforced by the
// jobs_kind_check constraint; Status is ('active' | 'paused' | 'deleted')
// enforced by jobs_status_check. EnvOverrides is jsonb so the customer-
// facing knob is open-vocabulary; Command is the OCI entrypoint added
// by 00572 (text[], capped at 64 entries by jobs_command_min_chk).
//
// All UUID columns are exposed as string to match the Cron precedent
// (Cron.ID is string; the pgx conversion lives inside PgStore).
type Job struct {
	ID             string
	AccountID      string
	Kind           string // 'app' | 'function'
	Name           string
	ImageRef       string
	RAMMB          int
	TaskTimeoutS   int
	MaxParallelism int
	RetryMax       int
	EnvOverrides   json.RawMessage
	Status         string // 'active' | 'paused' | 'deleted'
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Command        []string // migrations/00572
}

// JobRun is one row of public.job_runs (migrations/00255 + 00574 for
// dead_letter_count). RetryMax / TaskTimeoutS / StartedAt / FinishedAt
// are nullable (the first two are per-run overrides; the second two
// stay NULL until the run leaves the queued state). AggregateStatus
// is the closed 6-value vocabulary enforced by job_runs_aggregate_status_check.
type JobRun struct {
	ID              string
	JobID           string
	AccountID       string
	TriggerKind     string // 'manual' | 'scheduled' | 'triggered'
	EnvOverrides    json.RawMessage
	Tasks           int
	Parallelism     int
	RetryMax        *int   // nil = inherit from jobs.retry_max
	TaskTimeoutS    *int   // nil = inherit from jobs.task_timeout_s
	AggregateStatus string // 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'dead_letter'
	TasksSucceeded  int
	TasksFailed     int
	TasksCancelled  int
	TasksRunning    int
	DeadLetterCount int // migrations/00574
	StartedAt       *time.Time
	FinishedAt      *time.Time
	CreatedAt       time.Time
}

// JobTask is one row of public.job_tasks (migrations/00255 + 00571 for
// exit_code + next_attempt_at + relaxed CHECK constraints; 00574 for
// lease_token + lease_expires_at + last_lease_node). The PK is the
// composite (run_id, task_index) so the dispatch-tick SELECT FOR UPDATE
// SKIP LOCKED is index-only on the partial-index hot path.
//
// InstanceID + LeaseToken + LeaseExpiresAt + LastLeaseNode + ErrorClass +
// ErrorMessage + ExitCode + NextAttemptAt + StartedAt + FinishedAt are
// all nullable per schema. The CHECK constraints guarantee the
// relationship between instance_id and status (queued ⇒ NULL;
// claimed ⇒ NOT NULL; terminal ⇒ either, see migrations/00571).
type JobTask struct {
	RunID          string
	TaskIndex      int
	Status         string // 'queued' | 'claimed' | 'succeeded' | 'failed' | 'timeout' | 'cancelled' | 'oom'
	Attempt        int
	InstanceID     *string
	ErrorClass     *string
	ErrorMessage   *string
	ExitCode       *int
	StartedAt      *time.Time
	FinishedAt     *time.Time
	CreatedAt      time.Time
	NextAttemptAt  *time.Time // migrations/00571 — retry backoff gate
	LeaseToken     *string    // migrations/00574
	LeaseExpiresAt *time.Time // migrations/00574
	LastLeaseNode  *string    // migrations/00574
}

// --- Quota error ----------------------------------------------------

// JobQuotaScope names the cap that the JobCreate family tripped on.
// Each value corresponds to a JobMaxPerAccount / JobConcurrentPerAccount
// / JobRAMMB / JobTaskTimeoutSec / JobMaxParallelismPerRun /
// JobMaxTasksPerRun / JobMaxRetries slot in pkg/api/limits.go. The
// handler renders scope-specific copy so the customer knows whether
// to delete one of their own jobs (Scope="per_account") or back off
// on a single field (the value-bound scopes).
type JobQuotaScope string

const (
	// JobQuotaScopePerAccount trips when limits.JobMaxPerAccount was
	// reached. Customer-side copy: "delete a job to add another".
	JobQuotaScopePerAccount JobQuotaScope = "per_account"
	// JobQuotaScopeConcurrent trips when limits.JobConcurrentPerAccount
	// was reached — live job_task instances are still running. Copy:
	// "wait for live tasks to finish, or cancel one".
	JobQuotaScopeConcurrent JobQuotaScope = "concurrent"
	// JobQuotaScopeRAMMB trips when limits.JobRAMMB[plan] was exceeded
	// by the requested RAM. Copy: "reduce RAM or upgrade plan".
	JobQuotaScopeRAMMB JobQuotaScope = "ram_mb"
	// JobQuotaScopeTaskTimeout trips when limits.JobTaskTimeoutSec[plan]
	// was exceeded. Copy: "lower the timeout or upgrade plan".
	JobQuotaScopeTaskTimeout JobQuotaScope = "task_timeout"
	// JobQuotaScopeParallelism trips when limits.JobMaxParallelismPerRun
	// was exceeded. Copy: "lower parallelism or upgrade plan".
	JobQuotaScopeParallelism JobQuotaScope = "parallelism_per_run"
	// JobQuotaScopeTasksPerRun trips when limits.JobMaxTasksPerRun was
	// exceeded. Copy: "lower the per-run fan-out or upgrade plan".
	JobQuotaScopeTasksPerRun JobQuotaScope = "tasks_per_run"
	// JobQuotaScopeRetries trips when limits.JobMaxRetries was exceeded.
	// Copy: "lower retry_max or upgrade plan".
	JobQuotaScopeRetries JobQuotaScope = "retries"
)

// JobQuotaError is returned by the JobCreate / JobRunCreate paths when
// a per-plan cap is reached. Distinct from CronQuotaError so errors.As
// recovers Scope / Limit / Observed for the apid handler's render path,
// and so errors.Is(err, ErrJobQuotaExceeded) doesn't collide with the
// cron chain.
type JobQuotaError struct {
	Scope    JobQuotaScope
	Limit    int
	Observed int
}

func (e *JobQuotaError) Error() string {
	return fmt.Sprintf("state: job quota exceeded (scope=%s, limit=%d, observed=%d)", e.Scope, e.Limit, e.Observed)
}

// Is allows errors.Is(err, ErrJobQuotaExceeded) to match any *JobQuotaError.
func (e *JobQuotaError) Is(target error) bool {
	return target == ErrJobQuotaExceeded
}

// ErrJobQuotaExceeded is the sentinel callers compare against via errors.Is.
// apid's createJob + createJobRun handlers branch on errors.As to read
// the typed fields; the per-scope render copy in pkg/api/errors.go
// (ErrJobQuota) consumes Scope / Limit / Observed.
var ErrJobQuotaExceeded = errors.New("state: job quota exceeded")

// newUUIDString is a thin shim over uuid.NewString so the memstore
// can mint row ids without pulling in a context argument. The pgstore
// path uses gen_random_uuid() at the SQL boundary, so this helper is
// memstore-only.
func newUUIDString() string {
	return uuid.NewString()
}

// --- JobStore sub-interface -----------------------------------------

// JobStore is the persistence surface for the jobs / job_runs / job_tasks
// tables. It is a sub-interface embedded into Store so the JobStore-
// specific test doubles don't have to satisfy the whole Store surface,
// matching the ComputeNodeUsageBatcher precedent (store.go:582). The
// full ownership rationale:
//
//   - apid owns JobCreate / JobGetByID / JobGetByName / JobListByAccount /
//     JobUpdate / JobSoftDelete / JobRunListByJob / JobRunListByAccount /
//     JobTaskList / JobTaskGet (customer CRUD + read-back).
//   - schedd owns JobRunCreate / JobRunGetByID / JobRunRecompute /
//     JobRunCancel / JobRunIncrementDeadLetter / JobTaskClaimBatch /
//     JobTaskMarkClaimed / JobTaskMarkTerminal / JobTaskRetry /
//     JobTaskCancel (dispatch tick + lifecycle transitions).
//   - the reaper goroutine inside schedd owns JobTaskFindStuck +
//     JobTaskRetry (reclaim expired leases).
//   - apid's admission-control gate (and meterd's billing sweep) own
//     JobCountByAccount + JobConcurrentByAccount.
//
// Failure modes are noted per method. The lock semantics follow the
// cron pgstore precedent: SELECT FOR UPDATE on the parent row to
// serialise count-then-insert, transaction-scoped so the count read
// and the insert are atomic.
type JobStore interface {
	// --- jobs (template) ---

	// JobCreate inserts a new job row. accountID is the owning tenant.
	// name is the customer-facing slug (CHECK constraints on length +
	// charset, see migrations/00255 jobs_name_check). command is the
	// OCI entrypoint (migrations/00572, capped at 64 entries).
	// ramMB / taskTimeoutSec / maxParallelism / retryMax are clamped
	// against the per-plan caps by the caller (apid) before this is
	// invoked — this method does NOT enforce plan caps. envOverrides
	// is the open-vocabulary jsonb map.
	//
	// Failure modes:
	//   - ErrConflict on duplicate (account_id, name) while the row
	//     is non-deleted (jobs_account_name_uniq partial index).
	//   - mapErr-wrapped CHECK violations if the caller violated
	//     the schema constraints (handler validation prevents this).
	//   - mapErr-wrapped FK violations if accountID does not resolve.
	JobCreate(ctx context.Context, accountID, name, kind, imageRef string, command []string, ramMB, taskTimeoutSec, maxParallelism, retryMax int, envOverrides json.RawMessage) (Job, error)
	// JobGetByID returns ErrNotFound when the row is missing OR when
	// it is in status='deleted' (soft-tombstoned jobs are invisible
	// to customer CRUD — they show up only in admin audit paths).
	JobGetByID(ctx context.Context, id string) (Job, error)
	// JobGetByName mirrors JobGetByID by the customer-facing slug.
	// Same ErrNotFound semantics; the partial unique index
	// jobs_account_name_uniq (WHERE status<>'deleted') means the
	// soft-tombstone is invisible to this lookup.
	JobGetByName(ctx context.Context, accountID, name string) (Job, error)
	// JobListByAccount paginates the dashboard's primary index
	// (jobs_account_idx: (account_id, created_at DESC)). limit /
	// offset are passed verbatim to LIMIT/OFFSET — handler validates
	// the bounds (limit > 0, limit <= 200, offset >= 0).
	JobListByAccount(ctx context.Context, accountID string, limit, offset int) ([]Job, error)
	// JobUpdate mutates the optional fields of a job row. nil
	// pointers leave the column untouched. status='deleted' is the
	// soft-delete path — JobSoftDelete is the customer-facing
	// variant that enforces the no-live-instances guard via the
	// soft_delete_job_if_no_live_instances() helper.
	//
	// Failure modes:
	//   - ErrNotFound on missing row.
	//   - mapErr-wrapped CHECK violations if any of the values
	//     violate the schema constraint.
	JobUpdate(ctx context.Context, id string, command []string, imageRef *string, ramMB, taskTimeoutSec, maxParallelism, retryMax *int, envOverrides json.RawMessage, status *string) (Job, error)
	// JobSoftDelete flips status='active'|'paused' to status='deleted'
	// iff no live (waking, cold_booting, or running) job_task instance exists
	// for the job. Implemented via the soft_delete_job_if_no_live_instances()
	// PL/pgSQL helper (migrations/00576) on PgStore; memstore mirrors
	// the predicate directly.
	//
	// Returned tuple:
	//   - deleted=true,  hasLiveInstances=false: row flipped.
	//   - deleted=false, hasLiveInstances=true:  row stayed active;
	//                                            caller maps to
	//                                            CodeJobHasLiveInstances.
	//   - deleted=false, hasLiveInstances=false: row already deleted
	//                                            (idempotent — soft-
	//                                            delete of an already-
	//                                            soft-deleted job is
	//                                            a no-op success).
	//   - error: ErrNotFound when the id does not resolve.
	JobSoftDelete(ctx context.Context, id string) (deleted bool, hasLiveInstances bool, err error)
	// JobCountByAccount counts the non-deleted jobs on the account
	// (the jobs_account_idx covers this read path). Used by the
	// apid admission-control gate to enforce JobMaxPerAccount.
	JobCountByAccount(ctx context.Context, accountID string) (int, error)
	// JobConcurrentByAccount counts the live job_task instances on
	// the account (instances.kind='job_task' AND state IN
	// ('waking','cold_booting','running')). Used by the apid admission-control
	// gate to enforce JobConcurrentPerAccount before accepting a
	// new run + by meterd's billing sweep for the live-pool bill.
	JobConcurrentByAccount(ctx context.Context, accountID string) (int, error)

	// --- job_runs ---

	// JobRunCreate inserts a job_runs row + fans out `tasks` rows
	// in job_tasks, all inside one transaction. The fan-out uses
	// generate_series so a 5000-task run is one INSERT, not 5000.
	// parallelism / retryMaxOverride / taskTimeoutOverride are the
	// per-run fields; nil pointers inherit from the parent job (the
	// fan-out reaper reads these as COALESCE in the dispatch path).
	//
	// Returns the run row + the fanned-out task slice (task_index 0..N-1,
	// all status='queued') so the caller can echo them back without
	// a second round-trip.
	//
	// Failure modes:
	//   - ErrNotFound when the parent job_id is gone (FK violation).
	//   - mapErr-wrapped CHECK violations on bad tasks / parallelism.
	JobRunCreate(ctx context.Context, jobID, accountID, triggerKind string, parallelism, retryMaxOverride, taskTimeoutOverride *int, envOverrides json.RawMessage, tasks int) (JobRun, []JobTask, error)
	// JobRunGetByID returns ErrNotFound when the row is missing.
	// Does NOT cascade through tasks — callers that need the task
	// slice call JobTaskList separately so the read paths stay
	// independently cacheable.
	JobRunGetByID(ctx context.Context, id string) (JobRun, error)
	// JobRunListByJob paginates the per-job run list
	// (job_runs_job_idx: (job_id, created_at DESC)). Used by the
	// job-detail page on the dashboard.
	JobRunListByJob(ctx context.Context, jobID string, limit, offset int) ([]JobRun, error)
	// JobRunListByAccount paginates the per-account run list
	// (job_runs_account_idx). Used by the dashboard's runs tab.
	JobRunListByAccount(ctx context.Context, accountID string, limit, offset int) ([]JobRun, error)
	// JobRunRecompute recomputes the denormalised counter columns
	// (tasks_succeeded / tasks_failed / tasks_cancelled / tasks_running
	// / dead_letter_count) + the aggregate_status in a single SQL.
	// Called by schedd after every task lifecycle transition.
	//
	// Returns the refreshed run row so the caller can echo it back
	// to the API client without a follow-up JobRunGetByID.
	//
	// Failure modes:
	//   - ErrNotFound when the run id is missing (e.g. the run was
	//     hard-deleted between ClaimBatch and recompute).
	JobRunRecompute(ctx context.Context, runID string) (JobRun, error)
	// JobRunCancel transitions every non-terminal task of the run
	// to status='cancelled' and flips the run's aggregate_status to
	// 'cancelled' (or stays 'cancelled' if it was already terminal-
	// cancelled). Idempotent: a re-call on a cancelled run is a
	// no-op success.
	//
	// Returns the refreshed run row.
	JobRunCancel(ctx context.Context, runID string) (JobRun, error)
	// JobRunIncrementDeadLetter bumps dead_letter_count by 1. Called
	// by schedd when a task exhausts retry_max and the dispatcher
	// writes a dead-letter envelope (the run-level dead_letter
	// counter rolls up the per-task dead-letter decisions).
	//
	// Failure modes:
	//   - ErrNotFound on missing run id.
	JobRunIncrementDeadLetter(ctx context.Context, runID string) error

	// --- job_tasks ---

	// JobTaskClaimBatch returns up to `limit` queued tasks ordered
	// by created_at ASC, holding a SELECT FOR UPDATE SKIP LOCKED
	// lock for the duration of the transaction. Concurrent schedd
	// replicas (multi-host scale-out) each claim disjoint row
	// sets without retry-on-collision. The caller is responsible
	// for transitioning queued→claimed via JobTaskMarkClaimed in
	// a follow-up call (the lock is released at tx commit).
	//
	// Failure modes:
	//   - mapErr-wrapped SQL errors on tx begin / commit.
	JobTaskClaimBatch(ctx context.Context, limit int) ([]JobTask, error)
	// JobTaskMarkClaimed transitions a single task from queued to
	// claimed AND stamps the lease columns (lease_token, lease_expires_at,
	// last_lease_node). Called by schedd after WakeJob mints the
	// microVM instance.
	//
	// Returns ErrNotFound when (run_id, task_index) does not resolve
	// OR when the task is no longer status='queued' (e.g. a parallel
	// dispatcher claimed it first; lost the race).
	JobTaskMarkClaimed(ctx context.Context, runID string, taskIndex int, instanceID, leaseToken string, leaseExpiresAt time.Time, nodeID string) error
	// JobTaskMarkTerminal transitions a single task to a terminal
	// status ('succeeded' | 'failed' | 'timeout' | 'cancelled' |
	// 'oom') AND stamps exit_code + error_class + error_message +
	// finished_at. Called by schedd when the guest's job_exit DGRAM
	// arrives, or by the reaper when a lease expires.
	//
	// Returns ErrNotFound when (run_id, task_index) does not resolve
	// OR when the task is already terminal (the WHERE clause gates
	// on status IN ('queued','claimed')).
	JobTaskMarkTerminal(ctx context.Context, runID string, taskIndex int, status string, exitCode int, errorClass, errorMessage string, finishedAt time.Time) error
	// JobTaskRetry reverses a failed/timeout/oom transition back to
	// queued and stamps next_attempt_at with the per-attempt backoff
	// (JobBackoffBaseSeconds * 2^(attempt-1), capped at
	// JobBackoffMaxSeconds — caller computes the timestamp). The
	// task's attempt counter is incremented and the prior instance_id
	// is cleared so the next dispatch mints a fresh microVM.
	//
	// Returns ErrNotFound when (run_id, task_index) does not resolve.
	JobTaskRetry(ctx context.Context, runID string, taskIndex int, nextAttemptAt time.Time) error
	// JobTaskRequeue reverses a CLAIMED-but-not-yet-executed task
	// back to 'queued' WITHOUT incrementing the attempt counter.
	// Used by the dispatch tick when admission / quota / vmmd
	// bootstrapping fails before the customer's code runs (CR-7 /
	// code-review #7 — the previous code path called JobTaskRetry
	// for transient rejections, which silently consumed the
	// customer's retry budget for failures that were never the
	// customer's fault). Stamps next_attempt_at so the next
	// dispatch tick picks the task up; clears instance_id +
	// lease_token + lease_expires_at + last_lease_node + started_at
	// so the next claim mints a fresh microVM.
	//
	// Distinct from JobTaskRetry's contract in two ways: attempt is
	// NOT incremented, and the WHERE clause accepts both 'queued'
	// and 'claimed' (JobTaskRetry only matches rows that previously
	// reached a terminal state).
	//
	// Returns ErrNotFound when (run_id, task_index) does not resolve.
	JobTaskRequeue(ctx context.Context, runID string, taskIndex int, nextAttemptAt time.Time) error
	// JobTaskCancel transitions a single task to status='cancelled'
	// (called when the parent run is cancelled mid-flight, or when
	// the job is paused). Idempotent on tasks already terminal.
	//
	// Returns ErrNotFound when (run_id, task_index) does not resolve.
	JobTaskCancel(ctx context.Context, runID string, taskIndex int) error
	// JobTaskFindStuck returns claimed tasks whose lease_expires_at
	// is older than now()-ttl (lease expired; the owning schedd
	// crashed / lost connectivity). The reaper loops over this set
	// and calls JobTaskRetry (or JobTaskMarkTerminal if attempt has
	// exhausted retry_max) per row.
	JobTaskFindStuck(ctx context.Context, ttl time.Duration) ([]JobTask, error)
	// JobTaskGet returns ErrNotFound when (run_id, task_index) does
	// not resolve.
	JobTaskGet(ctx context.Context, runID string, taskIndex int) (JobTask, error)
	// JobTaskList paginates the per-run task slice
	// (job_tasks_run_idx: (run_id, task_index)). Used by the
	// run-detail page on the dashboard.
	JobTaskList(ctx context.Context, runID string, limit, offset int) ([]JobTask, error)
	// ListJobInstances (issue #1184 Workstream A / ADR-099) returns
	// every live kind='job_task' instance for the meterd
	// sampler. Mirrors ListAllApps for the job workload class:
	// only rows with state IN ('waking','cold_booting','running') are
	// included; terminal rows remain available through the normal
	// instance retention path.
	ListJobInstances(ctx context.Context) ([]Instance, error)
}

// compile-time check: api.Limits is referenced (the JobQuotaError
// payload uses its shape indirectly) so an accidental deletion of the
// Limits type would surface here.
var _ = api.Limits{}
