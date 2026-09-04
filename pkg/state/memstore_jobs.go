// memstore_jobs.go is the in-memory mirror of pgstore_jobs.go for
// handler / dispatch-tick / reaper unit tests. The mutex guards the
// count-then-insert pattern so a MemStore-backed test never sees the
// TOCTOU window the pgstore FOR UPDATE discipline closes — a single
// m.mu lock serialises the per-account / per-run count + insert.
//
// Map shape (see memstore.go MemStore struct):
//   - jobs:     map[id]Job              — keyed by jobs.id
//   - jobRuns:  map[id]JobRun           — keyed by job_runs.id
//   - jobTasks: map[runID]map[idx]JobTask — keyed by (run_id, task_index)
//
// Failure-mode semantics mirror pgstore_jobs.go one-for-one so a test
// can swap store backends without rewriting assertions.
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// --- jobs (template) ------------------------------------------------

// JobCreate inserts a new job row. Mirrors pgstore_jobs.JobCreate
// except no SQL — the row is constructed in memory. EnvOverrides is
// coerced to "{}" when empty so the shape matches the schema's
// NOT NULL DEFAULT.
func (m *MemStore) JobCreate(_ context.Context, accountID, name, kind, imageRef string, command []string, ramMB, taskTimeoutSec, maxParallelism, retryMax int, envOverrides json.RawMessage) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Soft-tombstone invisibility — match pgstore's WHERE status<>'deleted'.
	for _, j := range m.jobs {
		if j.AccountID == accountID && j.Name == name && j.Status != "deleted" {
			return Job{}, fmt.Errorf("%w: jobs_account_name_uniq", ErrConflict)
		}
	}
	if len(envOverrides) == 0 {
		envOverrides = json.RawMessage("{}")
	}
	now := time.Now().UTC()
	j := Job{
		ID:             newUUIDString(),
		AccountID:      accountID,
		Kind:           kind,
		Name:           name,
		ImageRef:       imageRef,
		RAMMB:          ramMB,
		TaskTimeoutS:   taskTimeoutSec,
		MaxParallelism: maxParallelism,
		RetryMax:       retryMax,
		EnvOverrides:   envOverrides,
		Status:         "active",
		CreatedAt:      now,
		UpdatedAt:      now,
		Command:        command,
	}
	m.jobs[j.ID] = j
	return j, nil
}

// JobGetByID returns ErrNotFound when the row is missing OR when it
// is in status='deleted' (soft-tombstone invisibility mirrors pgstore).
func (m *MemStore) JobGetByID(_ context.Context, id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || j.Status == "deleted" {
		return Job{}, ErrNotFound
	}
	return j, nil
}

// JobGetByName mirrors JobGetByID by the customer-facing slug.
func (m *MemStore) JobGetByName(_ context.Context, accountID, name string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.AccountID == accountID && j.Name == name && j.Status != "deleted" {
			return j, nil
		}
	}
	return Job{}, ErrNotFound
}

// JobListByAccount paginates the per-account job list, sorted by
// created_at DESC to mirror the pgstore index order. Soft-deleted
// rows are excluded (same invisibility as JobGetByID).
func (m *MemStore) JobListByAccount(_ context.Context, accountID string, limit, offset int) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []Job
	for _, j := range m.jobs {
		if j.AccountID == accountID && j.Status != "deleted" {
			matched = append(matched, j)
		}
	}
	sort.Slice(matched, func(i, k int) bool {
		return matched[i].CreatedAt.After(matched[k].CreatedAt)
	})
	if offset >= len(matched) {
		return nil, nil
	}
	matched = matched[offset:]
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

// JobUpdate mutates the optional fields of a job row. nil pointers
// leave the column untouched; updated_at is stamped to now() on every
// successful update so the audit trail reflects the touch.
//
// Returns ErrNotFound on missing or soft-deleted rows.
func (m *MemStore) JobUpdate(_ context.Context, id string, command []string, imageRef *string, ramMB, taskTimeoutSec, maxParallelism, retryMax *int, envOverrides json.RawMessage, status *string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || j.Status == "deleted" {
		return Job{}, ErrNotFound
	}
	if command != nil {
		j.Command = command
	}
	if imageRef != nil {
		j.ImageRef = *imageRef
	}
	if ramMB != nil {
		j.RAMMB = *ramMB
	}
	if taskTimeoutSec != nil {
		j.TaskTimeoutS = *taskTimeoutSec
	}
	if maxParallelism != nil {
		j.MaxParallelism = *maxParallelism
	}
	if retryMax != nil {
		j.RetryMax = *retryMax
	}
	if len(envOverrides) > 0 {
		j.EnvOverrides = envOverrides
	}
	if status != nil {
		j.Status = *status
	}
	j.UpdatedAt = time.Now().UTC()
	m.jobs[id] = j
	return j, nil
}

// JobSoftDelete mirrors pgstore_jobs.JobSoftDelete. Live-instance
// detection is in-memory (we don't have a memstore instances kind
// index, so we walk m.jobTasks).
//
// Returned tuple semantics match pgstore:
//   - deleted=true,  hasLiveInstances=false: row flipped.
//   - deleted=false, hasLiveInstances=true:  live instance blocked the flip.
//   - deleted=false, hasLiveInstances=false: already deleted (idempotent).
//   - error: ErrNotFound when the id does not resolve.
func (m *MemStore) JobSoftDelete(_ context.Context, id string) (bool, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return false, false, ErrNotFound
	}
	if j.Status == "deleted" {
		return false, false, nil // idempotent re-call
	}
	// Live-instance predicate mirrors pgstore_jobs: kind='job_task'
	// AND status is non-terminal. The memstore doesn't
	// track per-instance status (job_tasks is the dispatch surface,
	// not the live-instance surface); we approximate "live" as "any
	// non-terminal task exists for the job" which matches the pg
	// predicate for the dispatch lifecycle (a parked/destroyed
	// instance implies all its tasks are terminal, which means the
	// walk finds nothing).
	live := false
	for _, tasks := range m.jobTasks {
		for _, t := range tasks {
			if t.Status == "queued" || t.Status == "claimed" {
				// We don't have a backref from task → job without
				// walking job_runs. Look up the parent run first.
				run, ok := m.jobRuns[t.RunID]
				if !ok {
					continue
				}
				if run.JobID == id {
					live = true
					break
				}
			}
		}
		if live {
			break
		}
	}
	if live {
		return false, true, nil
	}
	j.Status = "deleted"
	j.UpdatedAt = time.Now().UTC()
	m.jobs[id] = j
	return true, false, nil
}

// JobCountByAccount counts the non-deleted jobs on the account.
func (m *MemStore) JobCountByAccount(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, j := range m.jobs {
		if j.AccountID == accountID && j.Status != "deleted" {
			count++
		}
	}
	return count, nil
}

// JobConcurrentByAccount counts the live job_task instances on the
// account. The memstore approximation: count claimed/queued tasks
// across all runs on this account. Since the memstore doesn't track
// a parallel `instances` table for job_tasks, this is the closest
// in-memory mirror of the pgstore predicate (kind='job_task' AND
// status is non-terminal).
func (m *MemStore) JobConcurrentByAccount(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, run := range m.jobRuns {
		if run.AccountID != accountID {
			continue
		}
		tasks, ok := m.jobTasks[run.ID]
		if !ok {
			continue
		}
		for _, t := range tasks {
			if t.Status == "queued" || t.Status == "claimed" {
				count++
			}
		}
	}
	return count, nil
}

// --- job_runs --------------------------------------------------------

// JobRunCreate inserts a job_runs row + fans out `tasks` rows in
// jobTasks. Mirrors pgstore_jobs.JobRunCreate; the fan-out is a
// simple loop since memstore batches aren't bound by round-trips.
//
// Returns the run row + the fanned-out task slice (task_index 0..N-1,
// all status='queued').
func (m *MemStore) JobRunCreate(_ context.Context, jobID, accountID, triggerKind string, parallelism, retryMaxOverride, taskTimeoutOverride *int, envOverrides json.RawMessage, tasks int) (JobRun, []JobTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok || job.Status == "deleted" {
		return JobRun{}, nil, ErrNotFound
	}
	if len(envOverrides) == 0 {
		envOverrides = json.RawMessage("{}")
	}
	now := time.Now().UTC()
	run := JobRun{
		ID:              newUUIDString(),
		JobID:           jobID,
		AccountID:       accountID,
		TriggerKind:     triggerKind,
		EnvOverrides:    envOverrides,
		Tasks:           tasks,
		Parallelism:     derefInt(parallelism, job.MaxParallelism),
		RetryMax:        retryMaxOverride,
		TaskTimeoutS:    taskTimeoutOverride,
		AggregateStatus: "queued",
		CreatedAt:       now,
	}
	m.jobRuns[run.ID] = run

	// Fan out the task rows.
	fanned := make([]JobTask, 0, tasks)
	taskMap := make(map[int]JobTask, tasks)
	for i := 0; i < tasks; i++ {
		t := JobTask{
			RunID:     run.ID,
			TaskIndex: i,
			Status:    "queued",
			Attempt:   1,
			CreatedAt: now,
		}
		fanned = append(fanned, t)
		taskMap[i] = t
	}
	m.jobTasks[run.ID] = taskMap

	return run, fanned, nil
}

// JobRunGetByID returns ErrNotFound when the row is missing.
func (m *MemStore) JobRunGetByID(_ context.Context, id string) (JobRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.jobRuns[id]
	if !ok {
		return JobRun{}, ErrNotFound
	}
	return r, nil
}

// JobRunListByJob paginates the per-job run list, sorted by
// created_at DESC to mirror the pgstore index.
func (m *MemStore) JobRunListByJob(_ context.Context, jobID string, limit, offset int) ([]JobRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []JobRun
	for _, r := range m.jobRuns {
		if r.JobID == jobID {
			matched = append(matched, r)
		}
	}
	sort.Slice(matched, func(i, k int) bool {
		return matched[i].CreatedAt.After(matched[k].CreatedAt)
	})
	if offset >= len(matched) {
		return nil, nil
	}
	matched = matched[offset:]
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

// JobRunListByAccount paginates the per-account run list, sorted by
// created_at DESC.
func (m *MemStore) JobRunListByAccount(_ context.Context, accountID string, limit, offset int) ([]JobRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []JobRun
	for _, r := range m.jobRuns {
		if r.AccountID == accountID {
			matched = append(matched, r)
		}
	}
	sort.Slice(matched, func(i, k int) bool {
		return matched[i].CreatedAt.After(matched[k].CreatedAt)
	})
	if offset >= len(matched) {
		return nil, nil
	}
	matched = matched[offset:]
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

// JobRunRecompute walks the per-run task slice, counts each status,
// and applies the same aggregate_status precedence as
// pgstore_jobs.JobRunRecompute. started_at / finished_at are stamped
// alongside so the terminal-pair invariant stays satisfied in tests
// that later write to a live pgstore (the schema CHECK is the same).
func (m *MemStore) JobRunRecompute(_ context.Context, runID string) (JobRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.jobRuns[runID]
	if !ok {
		return JobRun{}, ErrNotFound
	}
	tasks, ok := m.jobTasks[runID]
	if !ok {
		// No tasks — degenerate case (shouldn't happen post-create).
		return run, nil
	}
	var succ, fail, canc, running, queuedOrClaimed int
	for _, t := range tasks {
		switch t.Status {
		case "succeeded":
			succ++
		case "failed":
			fail++
		case "cancelled", "timeout", "oom":
			// 00571 broadened the terminal vocabulary; memstore
			// folds timeout/oom into "cancelled" for the aggregate
			// status so a 00571 test asserts the same outcome as
			// the pgstore SQL.
			canc++
		case "claimed":
			running++
		case "queued":
			queuedOrClaimed++
		}
	}
	now := time.Now().UTC()
	run.TasksSucceeded = succ
	run.TasksFailed = fail
	run.TasksCancelled = canc
	run.TasksRunning = running

	// Aggregate-status precedence (mirrors pgstore SQL).
	switch {
	case running > 0, queuedOrClaimed > 0:
		run.AggregateStatus = "running"
	case canc > 0 && fail == 0 && run.DeadLetterCount == 0:
		run.AggregateStatus = "cancelled"
	case fail > 0 && run.DeadLetterCount == 0:
		run.AggregateStatus = "failed"
	case run.DeadLetterCount > 0:
		run.AggregateStatus = "dead_letter"
	default:
		run.AggregateStatus = "succeeded"
	}
	if run.StartedAt == nil && (running+queuedOrClaimed) > 0 {
		run.StartedAt = &now
	}
	if queuedOrClaimed == 0 && running == 0 && run.FinishedAt == nil {
		run.FinishedAt = &now
	}
	m.jobRuns[runID] = run
	return run, nil
}

// JobRunCancel transitions every non-terminal task of the run to
// status='cancelled' and flips the run's aggregate_status to
// 'cancelled'. Idempotent: re-calling on an already-cancelled run
// is a no-op success.
func (m *MemStore) JobRunCancel(_ context.Context, runID string) (JobRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.jobRuns[runID]
	if !ok {
		return JobRun{}, ErrNotFound
	}
	tasks, ok := m.jobTasks[runID]
	if !ok {
		return run, nil
	}
	now := time.Now().UTC()
	for i, t := range tasks {
		if t.Status == "queued" || t.Status == "claimed" {
			t.Status = "cancelled"
			if t.FinishedAt == nil {
				t.FinishedAt = &now
			}
			tasks[i] = t
		}
	}
	m.jobTasks[runID] = tasks

	if run.AggregateStatus == "queued" || run.AggregateStatus == "running" {
		run.AggregateStatus = "cancelled"
		if run.StartedAt == nil {
			run.StartedAt = &now
		}
		run.FinishedAt = &now
	}
	m.jobRuns[runID] = run
	return run, nil
}

// JobRunIncrementDeadLetter bumps dead_letter_count by 1.
func (m *MemStore) JobRunIncrementDeadLetter(_ context.Context, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.jobRuns[runID]
	if !ok {
		return ErrNotFound
	}
	r.DeadLetterCount++
	m.jobRuns[runID] = r
	return nil
}

// --- job_tasks -------------------------------------------------------

// JobTaskClaimBatch returns up to `limit` queued tasks ordered by
// created_at ASC. The memstore doesn't need SELECT FOR UPDATE SKIP
// LOCKED — the mutex is held for the duration of the claim so no
// concurrent goroutine can race the read.
//
// Returns the row surface read under the lock; the caller is
// responsible for calling JobTaskMarkClaimed to flip queued→claimed.
func (m *MemStore) JobTaskClaimBatch(_ context.Context, limit int) ([]JobTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []JobTask
	now := time.Now().UTC()
	for _, tasks := range m.jobTasks {
		for _, t := range tasks {
			if t.Status != "queued" {
				continue
			}
			// Backoff gate: skip tasks whose next_attempt_at is in the future.
			if t.NextAttemptAt != nil && t.NextAttemptAt.After(now) {
				continue
			}
			matched = append(matched, t)
		}
	}
	sort.Slice(matched, func(i, k int) bool {
		if matched[i].CreatedAt.Equal(matched[k].CreatedAt) {
			return matched[i].TaskIndex < matched[k].TaskIndex
		}
		return matched[i].CreatedAt.Before(matched[k].CreatedAt)
	})
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

// JobTaskMarkClaimed transitions a single task from queued to
// claimed AND stamps the lease columns.
//
// Returns ErrNotFound when (run_id, task_index) does not resolve OR
// when the task is no longer status='queued'.
func (m *MemStore) JobTaskMarkClaimed(_ context.Context, runID string, taskIndex int, instanceID, leaseToken string, leaseExpiresAt time.Time, nodeID string) (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks, ok := m.jobTasks[runID]
	if !ok {
		return ErrNotFound
	}
	t, ok := tasks[taskIndex]
	if !ok || t.Status != "queued" {
		return ErrNotFound
	}
	t.Status = "claimed"
	t.InstanceID = &instanceID
	t.LeaseToken = &leaseToken
	exp := leaseExpiresAt.UTC()
	t.LeaseExpiresAt = &exp
	t.LastLeaseNode = &nodeID
	if t.StartedAt == nil {
		now := time.Now().UTC()
		t.StartedAt = &now
	}
	tasks[taskIndex] = t
	m.jobTasks[runID] = tasks
	return nil
}

// JobTaskMarkTerminal transitions a single task to a terminal status
// AND stamps exit_code + error_class + error_message + finished_at.
//
// Returns ErrNotFound when (run_id, task_index) does not resolve OR
// when the task is already terminal.
func (m *MemStore) JobTaskMarkTerminal(_ context.Context, runID string, taskIndex int, status string, exitCode int, errorClass, errorMessage string, finishedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks, ok := m.jobTasks[runID]
	if !ok {
		return ErrNotFound
	}
	t, ok := tasks[taskIndex]
	if !ok {
		return ErrNotFound
	}
	if t.Status != "queued" && t.Status != "claimed" {
		return ErrNotFound
	}
	t.Status = status
	t.ExitCode = &exitCode
	if errorClass != "" {
		t.ErrorClass = &errorClass
	}
	if errorMessage != "" {
		t.ErrorMessage = &errorMessage
	}
	fin := finishedAt.UTC()
	t.FinishedAt = &fin
	t.LeaseToken = nil
	t.LeaseExpiresAt = nil
	tasks[taskIndex] = t
	m.jobTasks[runID] = tasks
	return nil
}

// JobTaskRetry reverses a failed/timeout/oom transition back to
// queued. The attempt counter is incremented and the prior instance_id
// + lease columns are cleared.
func (m *MemStore) JobTaskRetry(_ context.Context, runID string, taskIndex int, nextAttemptAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks, ok := m.jobTasks[runID]
	if !ok {
		return ErrNotFound
	}
	t, ok := tasks[taskIndex]
	if !ok {
		return ErrNotFound
	}
	t.Status = "queued"
	t.Attempt++
	t.InstanceID = nil
	next := nextAttemptAt.UTC()
	t.NextAttemptAt = &next
	t.StartedAt = nil
	t.FinishedAt = nil
	t.ErrorClass = nil
	t.ErrorMessage = nil
	t.ExitCode = nil
	t.LeaseToken = nil
	t.LeaseExpiresAt = nil
	t.LastLeaseNode = nil
	tasks[taskIndex] = t
	m.jobTasks[runID] = tasks
	return nil
}

// JobTaskRequeue reverses a CLAIMED-but-not-executed task back to
// queued WITHOUT incrementing attempt. Mirrors JobTaskRetry's
// column-reset contract (clears instance_id + lease columns +
// started_at) but preserves the attempt counter — the customer's
// retry budget is not consumed by transient dispatch-side failures
// (admission denied, vmmd unreachable, run-lookup race).
func (m *MemStore) JobTaskRequeue(_ context.Context, runID string, taskIndex int, nextAttemptAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks, ok := m.jobTasks[runID]
	if !ok {
		return ErrNotFound
	}
	t, ok := tasks[taskIndex]
	if !ok {
		return ErrNotFound
	}
	if t.Status != "queued" && t.Status != "claimed" {
		return ErrNotFound
	}
	t.Status = "queued"
	// Attempt is preserved — see CR-7.
	next := nextAttemptAt.UTC()
	t.NextAttemptAt = &next
	t.StartedAt = nil
	t.InstanceID = nil
	t.LeaseToken = nil
	t.LeaseExpiresAt = nil
	t.LastLeaseNode = nil
	tasks[taskIndex] = t
	m.jobTasks[runID] = tasks
	return nil
}

// JobTaskCancel transitions a single task to status='cancelled'.
// Idempotent on tasks already terminal.
func (m *MemStore) JobTaskCancel(_ context.Context, runID string, taskIndex int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks, ok := m.jobTasks[runID]
	if !ok {
		return ErrNotFound
	}
	t, ok := tasks[taskIndex]
	if !ok {
		return ErrNotFound
	}
	if t.Status != "queued" && t.Status != "claimed" {
		return ErrNotFound
	}
	t.Status = "cancelled"
	if t.FinishedAt == nil {
		now := time.Now().UTC()
		t.FinishedAt = &now
	}
	tasks[taskIndex] = t
	m.jobTasks[runID] = tasks
	return nil
}

// JobTaskFindStuck returns claimed tasks whose lease_expires_at is
// older than now()-ttl. The reaper loops over this set and reclaims
// each row via JobTaskRetry.
func (m *MemStore) JobTaskFindStuck(_ context.Context, ttl time.Duration) ([]JobTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().UTC().Add(-ttl)
	var out []JobTask
	for _, tasks := range m.jobTasks {
		for _, t := range tasks {
			if t.Status != "claimed" {
				continue
			}
			if t.LeaseExpiresAt == nil {
				continue
			}
			if t.LeaseExpiresAt.Before(cutoff) {
				out = append(out, t)
			}
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].LeaseExpiresAt == nil {
			return false
		}
		if out[k].LeaseExpiresAt == nil {
			return true
		}
		return out[i].LeaseExpiresAt.Before(*out[k].LeaseExpiresAt)
	})
	return out, nil
}

// JobTaskGet returns ErrNotFound when (run_id, task_index) does not
// resolve.
func (m *MemStore) JobTaskGet(_ context.Context, runID string, taskIndex int) (JobTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks, ok := m.jobTasks[runID]
	if !ok {
		return JobTask{}, ErrNotFound
	}
	t, ok := tasks[taskIndex]
	if !ok {
		return JobTask{}, ErrNotFound
	}
	return t, nil
}

// JobTaskList paginates the per-run task slice, sorted by task_index.
func (m *MemStore) JobTaskList(_ context.Context, runID string, limit, offset int) ([]JobTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks, ok := m.jobTasks[runID]
	if !ok {
		return nil, nil
	}
	out := make([]JobTask, 0, len(tasks))
	for i := 0; ; i++ {
		t, ok := tasks[i]
		if !ok {
			break
		}
		out = append(out, t)
	}
	if offset >= len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// derefInt returns *deref when non-nil, else the fallback. Used by
// JobRunCreate to apply per-run overrides on top of the parent job's
// defaults (parallelism falls back to job.MaxParallelism when nil).
func derefInt(p *int, fallback int) int {
	if p != nil {
		return *p
	}
	return fallback
}

// ListJobInstances (issue #1184 Workstream A / ADR-099) returns
// every kind='job_task' instance for the meterd sampler. Walks
// m.instances (memstore has no secondary index on kind; the
// meter sampler is called once per minute, so an O(N) scan is
// fine for the test + e2e harness surface).
func (m *MemStore) ListJobInstances(_ context.Context) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Instance, 0, len(m.instances)/4)
	for _, ins := range m.instances {
		if ins.Kind != "job_task" {
			continue
		}
		if ins.State != "waking" && ins.State != "cold_booting" && ins.State != "running" {
			continue
		}
		out = append(out, ins)
	}
	return out, nil
}
