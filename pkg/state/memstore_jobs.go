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

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/jobresult"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// recordJobTaskAttemptLocked keeps the same first-terminal-result semantics
// as the PostgreSQL journal trigger. The caller holds m.mu.
func (m *MemStore) recordJobTaskAttemptLocked(task JobTask) {
	if task.FinishedAt == nil || task.Attempt < 1 {
		return
	}
	switch task.Status {
	case "succeeded", "failed", "timeout", "cancelled", "oom":
	default:
		return
	}
	if m.jobTaskAttempts[task.RunID] == nil {
		m.jobTaskAttempts[task.RunID] = make(map[int]map[int]JobTaskAttempt)
	}
	if m.jobTaskAttempts[task.RunID][task.TaskIndex] == nil {
		m.jobTaskAttempts[task.RunID][task.TaskIndex] = make(map[int]JobTaskAttempt)
	}
	if _, exists := m.jobTaskAttempts[task.RunID][task.TaskIndex][task.Attempt]; exists {
		return
	}
	m.jobTaskAttempts[task.RunID][task.TaskIndex][task.Attempt] = JobTaskAttempt{
		RunID: task.RunID, TaskIndex: task.TaskIndex, Attempt: task.Attempt,
		InputID: task.InputID, InputRef: task.InputRef, Status: task.Status,
		InstanceID: task.InstanceID, ErrorClass: task.ErrorClass,
		ErrorMessage: task.ErrorMessage, ExitCode: task.ExitCode,
		StartedAt: task.StartedAt, FinishedAt: *task.FinishedAt,
		LogContent: task.LogContent, LogTruncated: task.LogTruncated,
		OutputManifest: append(json.RawMessage(nil), task.OutputManifest...),
		WorkDecision:   workpolicy.Clone(task.WorkDecision), OutcomeCode: task.OutcomeCode,
	}
}

// Callers hold m.mu, so the JobRun and operation generation are checked
// atomically with the task transition.
func (m *MemStore) exclusiveJobRunCurrentLocked(run JobRun) bool {
	if run.ExclusiveOperationID == "" {
		return true
	}
	operation, ok := m.exclusiveOperations[run.ExclusiveOperationID]
	if !ok || operation.AccountID != run.AccountID || operation.JobID != run.JobID ||
		operation.State != "running" || operation.Generation != run.ExclusiveGeneration ||
		operation.LeaseExpiresAt == nil || operation.AttemptDeadline == nil {
		return false
	}
	now := m.exclusiveTimeLocked()
	return operation.LeaseExpiresAt.After(now) && operation.AttemptDeadline.After(now)
}

func (m *MemStore) JobTaskAttemptList(_ context.Context, runID string, taskIndex, limit, offset int) ([]JobTaskAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var attempts []JobTaskAttempt
	for _, attempt := range m.jobTaskAttempts[runID][taskIndex] {
		attempts = append(attempts, attempt)
	}
	sort.Slice(attempts, func(i, j int) bool { return attempts[i].Attempt < attempts[j].Attempt })
	if offset >= len(attempts) || limit <= 0 || offset < 0 {
		return nil, nil
	}
	attempts = attempts[offset:]
	if limit < len(attempts) {
		attempts = attempts[:limit]
	}
	return attempts, nil
}

// --- jobs (template) ------------------------------------------------

// JobCreate inserts a new job row. Mirrors pgstore_jobs.JobCreate
// except no SQL — the row is constructed in memory. EnvOverrides is
// coerced to "{}" when empty so the shape matches the schema's
// NOT NULL DEFAULT.
func (m *MemStore) JobCreate(_ context.Context, accountID, name, kind, imageRef string, command []string, ramMB, taskTimeoutSec, maxParallelism, retryMax int, envOverrides json.RawMessage) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobCreateLocked(accountID, name, kind, imageRef, command, ramMB, taskTimeoutSec, maxParallelism, retryMax, envOverrides)
}

// JobCreateIfUnderQuota combines the per-account count and insert under the
// MemStore mutex, mirroring PgStore's account-row lock transaction.
func (m *MemStore) JobCreateIfUnderQuota(_ context.Context, accountID, name, kind, imageRef string, command []string, ramMB, taskTimeoutSec, maxParallelism, retryMax int, envOverrides json.RawMessage, limit int) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, j := range m.jobs {
		if j.AccountID == accountID && j.Status != "deleted" {
			count++
		}
	}
	if count >= limit {
		return Job{}, &JobQuotaError{Scope: JobQuotaScopePerAccount, Limit: limit, Observed: count}
	}
	return m.jobCreateLocked(accountID, name, kind, imageRef, command, ramMB, taskTimeoutSec, maxParallelism, retryMax, envOverrides)
}

// JobCreateScheduledIfUnderQuota combines account quota admission, job
// creation, and recurring schedule persistence under the MemStore mutex.
func (m *MemStore) JobCreateScheduledIfUnderQuota(_ context.Context, job Job, limit int) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, existing := range m.jobs {
		if existing.AccountID == job.AccountID && existing.Status != "deleted" {
			count++
		}
	}
	if count >= limit {
		return Job{}, &JobQuotaError{Scope: JobQuotaScopePerAccount, Limit: limit, Observed: count}
	}
	created, err := m.jobCreateLocked(job.AccountID, job.Name, job.Kind, job.ImageRef, job.Command,
		job.RAMMB, job.TaskTimeoutS, job.MaxParallelism, job.RetryMax, job.EnvOverrides)
	if err != nil {
		return Job{}, err
	}
	created.SchedulePolicy = workpolicy.Clone(job.SchedulePolicy)
	created.FailureRules = workpolicy.Clone(job.FailureRules)
	created.ScheduleRevision = 1
	created.CronSchedule = job.CronSchedule
	created.CronTimezone = job.CronTimezone
	if created.CronTimezone == "" {
		created.CronTimezone = "UTC"
	}
	m.jobs[created.ID] = created
	return created, nil
}

func (m *MemStore) jobCreateLocked(accountID, name, kind, imageRef string, command []string, ramMB, taskTimeoutSec, maxParallelism, retryMax int, envOverrides json.RawMessage) (Job, error) {
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
		ID:                         newUUIDString(),
		AccountID:                  accountID,
		Kind:                       kind,
		Name:                       name,
		ImageRef:                   imageRef,
		RAMMB:                      ramMB,
		TaskTimeoutS:               taskTimeoutSec,
		MaxParallelism:             maxParallelism,
		RetryMax:                   retryMax,
		EnvOverrides:               envOverrides,
		Status:                     "active",
		CronTimezone:               "UTC",
		ScheduleRevision:           1,
		CreatedAt:                  now,
		UpdatedAt:                  now,
		Command:                    command,
		ImageMaterializationStatus: "pending",
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

// JobListScheduled returns a stable snapshot of active recurring jobs. The
// scheduler still claims each candidate transactionally through
// JobRunCreateScheduled before it can produce side effects.
func (m *MemStore) JobListScheduled(_ context.Context) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []Job
	for _, job := range m.jobs {
		if job.Status == "active" && job.Kind == "recurring" && job.CronSchedule != "" {
			matched = append(matched, job)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].CreatedAt.Before(matched[j].CreatedAt)
	})
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
	if m.environmentGitOpsManagedJobLocked(id) {
		return Job{}, ErrEnvironmentGitManaged
	}
	return m.jobUpdateLocked(id, command, imageRef, ramMB, taskTimeoutSec, maxParallelism, retryMax, envOverrides, status, nil, nil)
}

// JobUpdateWithSchedule applies ordinary job edits and schedule changes in a
// single critical section, mirroring the PostgreSQL row update transaction.
func (m *MemStore) JobUpdateWithSchedule(_ context.Context, id string, command []string, imageRef *string, ramMB, taskTimeoutSec, maxParallelism, retryMax *int, envOverrides json.RawMessage, status, schedule, timezone *string, policyOptions ...JobPolicyOptions) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.environmentGitOpsManagedJobLocked(id) {
		return Job{}, ErrEnvironmentGitManaged
	}
	updated, err := m.jobUpdateLocked(id, command, imageRef, ramMB, taskTimeoutSec, maxParallelism, retryMax, envOverrides, status, schedule, timezone)

	if err != nil {
		return Job{}, err
	}
	if len(policyOptions) > 0 {
		if policyOptions[0].SchedulePolicy != nil {
			updated.SchedulePolicy = workpolicy.Clone(policyOptions[0].SchedulePolicy)
			updated.ScheduleRevision++
		}
		if policyOptions[0].FailureRules != nil {
			updated.FailureRules = workpolicy.Clone(policyOptions[0].FailureRules)
		}
		m.jobs[id] = updated
	}
	return updated, nil
}

func (m *MemStore) jobUpdateLocked(id string, command []string, imageRef *string, ramMB, taskTimeoutSec, maxParallelism, retryMax *int, envOverrides json.RawMessage, status, schedule, timezone *string) (Job, error) {
	j, ok := m.jobs[id]
	if !ok || j.Status == "deleted" {
		return Job{}, ErrNotFound
	}
	for runID, run := range m.jobRuns {
		if run.JobID != id {
			continue
		}
		for _, task := range m.jobTasks[runID] {
			if task.Status == "queued" || task.Status == "claimed" {
				return Job{}, ErrConflict
			}
		}
	}
	if command != nil {
		j.Command = command
	}
	if imageRef != nil {
		j.ImageRef = *imageRef
		j.ImageResolvedDigest = ""
		j.ImageStorageKey = ""
		j.ImageMaterializationStatus = "pending"
		j.ImageMaterializationError = ""
		j.ImageMaterializedAt = nil
		j.ImageMaterializationAttempts = 0
		j.ImageMaterializationNextAttemptAt = nil
		delete(m.jobMaterializationClaims, id)
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
	now := time.Now().UTC()
	if schedule != nil {
		j.CronSchedule = *schedule
		if *schedule == "" {
			j.Kind = "batch"
		} else {
			j.Kind = "recurring"
		}
		j.LastScheduledAt = &now
	}
	if timezone != nil {
		j.CronTimezone = *timezone
		if schedule == nil && j.CronSchedule != "" {
			j.LastScheduledAt = &now
		}
	}
	j.UpdatedAt = now
	m.jobs[id] = j
	return j, nil
}

// JobListPendingImageMaterialization mirrors the PostgreSQL worker queue.
func (m *MemStore) JobListPendingImageMaterialization(_ context.Context, limit int) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Job
	for _, j := range m.jobs {
		if j.Status != "deleted" && j.ImageMaterializationStatus == "pending" &&
			(j.ImageMaterializationNextAttemptAt == nil || !j.ImageMaterializationNextAttemptAt.After(time.Now())) {
			if claim, ok := m.jobMaterializationClaims[j.ID]; ok && claim.leaseUntil.After(time.Now()) {
				continue
			}
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].UpdatedAt.Equal(out[k].UpdatedAt) {
			return out[i].ID < out[k].ID
		}
		return out[i].UpdatedAt.Before(out[k].UpdatedAt)
	})
	if limit <= 0 {
		limit = 64
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// JobClaimImageMaterialization claims one requested job without accidentally
// substituting a different pending row when a direct notification arrives.
func (m *MemStore) JobClaimImageMaterialization(_ context.Context, id, owner string, lease time.Duration) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if owner == "" {
		owner = "imaged"
	}
	if lease <= 0 {
		lease = 15 * time.Minute
	}
	now := time.Now().UTC()
	j, ok := m.jobs[id]
	if !ok || j.Status == "deleted" || j.ImageMaterializationStatus != "pending" {
		return Job{}, ErrNotFound
	}
	if j.ImageMaterializationNextAttemptAt != nil && j.ImageMaterializationNextAttemptAt.After(now) {
		return Job{}, ErrNotFound
	}
	if claim, ok := m.jobMaterializationClaims[id]; ok && claim.leaseUntil.After(now) {
		return Job{}, ErrNotFound
	}
	j.ImageMaterializationAttempts++
	j.UpdatedAt = now
	m.jobs[id] = j
	m.jobMaterializationClaims[id] = jobMaterializationClaim{owner: owner, leaseUntil: now.Add(lease)}
	return j, nil
}

// JobClaimPendingImageMaterialization mirrors the PostgreSQL SKIP LOCKED
// claim in memory. Expired claims are reclaimable, while a live claim keeps a
// second imaged worker from duplicating the pull/build work.
func (m *MemStore) JobClaimPendingImageMaterialization(_ context.Context, limit int, owner string, lease time.Duration) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 64
	}
	if owner == "" {
		owner = "imaged"
	}
	if lease <= 0 {
		lease = 15 * time.Minute
	}
	now := time.Now().UTC()
	var out []Job
	for _, j := range m.jobs {
		if j.Status == "deleted" || j.ImageMaterializationStatus != "pending" {
			continue
		}
		if j.ImageMaterializationNextAttemptAt != nil && j.ImageMaterializationNextAttemptAt.After(now) {
			continue
		}
		if claim, ok := m.jobMaterializationClaims[j.ID]; ok && claim.leaseUntil.After(now) {
			continue
		}
		j.ImageMaterializationAttempts++
		j.UpdatedAt = now
		m.jobs[j.ID] = j
		m.jobMaterializationClaims[j.ID] = jobMaterializationClaim{owner: owner, leaseUntil: now.Add(lease)}
		out = append(out, j)
		if len(out) == limit {
			break
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].UpdatedAt.Equal(out[k].UpdatedAt) {
			return out[i].ID < out[k].ID
		}
		return out[i].UpdatedAt.Before(out[k].UpdatedAt)
	})
	return out, nil
}

// JobRenewImageMaterializationLease extends only the current live claim. An
// expired lease cannot be revived after another worker becomes eligible.
func (m *MemStore) JobRenewImageMaterializationLease(_ context.Context, id, sourceRef, owner string, attempt int, lease time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lease <= 0 {
		lease = 15 * time.Minute
	}
	j, ok := m.jobs[id]
	claim, claimed := m.jobMaterializationClaims[id]
	if !ok || j.Status == "deleted" || j.ImageRef != sourceRef || j.ImageMaterializationStatus != "pending" ||
		j.ImageMaterializationAttempts != attempt || !claimed || claim.owner != owner || !claim.leaseUntil.After(time.Now()) {
		return ErrConflict
	}
	now := time.Now().UTC()
	claim.leaseUntil = now.Add(lease)
	j.UpdatedAt = now
	m.jobMaterializationClaims[id] = claim
	m.jobs[id] = j
	return nil
}

// JobSetImageMaterialization is the general state setter used by setup and
// non-claiming compatibility callers. Claimed workers publish through the
// claim-fenced JobPublishImageMaterialization method below.
func (m *MemStore) JobSetImageMaterialization(_ context.Context, id, sourceRef, status, resolvedDigest, storageKey, failure string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if status != "pending" && status != "ready" && status != "failed" {
		return Job{}, fmt.Errorf("state: invalid job image materialization status %q", status)
	}
	if status == "ready" && (resolvedDigest == "" || storageKey == "") {
		return Job{}, fmt.Errorf("state: ready job image materialization requires digest and storage key")
	}
	j, ok := m.jobs[id]
	if !ok || j.Status == "deleted" {
		return Job{}, ErrNotFound
	}
	if j.ImageRef != sourceRef {
		return Job{}, ErrConflict
	}
	if status == "ready" {
		if _, claimed := m.jobMaterializationClaims[id]; claimed {
			return Job{}, ErrConflict
		}
	}
	j.ImageMaterializationStatus = status
	j.ImageResolvedDigest = resolvedDigest
	j.ImageStorageKey = storageKey
	j.ImageMaterializationError = failure
	j.ImageMaterializedAt = nil
	j.ImageMaterializationNextAttemptAt = nil
	delete(m.jobMaterializationClaims, id)
	if status == "ready" {
		now := time.Now().UTC()
		j.ImageMaterializedAt = &now
	}
	j.UpdatedAt = time.Now().UTC()
	m.jobs[id] = j
	if status == "ready" {
		m.bindJobRunImageLocked(j)
	}
	if status == "failed" {
		m.settleJobImageFailureLocked(id, failure)
	}
	return j, nil
}

// JobPublishImageMaterialization only lets the current live claim publish its
// unique artifact. ImageMaterializationAttempts is the generation and owner is
// unique per claim, so ref changes and lease takeovers both fence stale workers.
func (m *MemStore) JobPublishImageMaterialization(_ context.Context, id, sourceRef, owner string, attempt int, resolvedDigest, storageKey string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if resolvedDigest == "" || storageKey == "" {
		return Job{}, fmt.Errorf("state: ready job image materialization requires digest and storage key")
	}
	j, ok := m.jobs[id]
	if !ok || j.Status == "deleted" {
		return Job{}, ErrNotFound
	}
	if j.ImageRef != sourceRef || j.ImageMaterializationStatus != "pending" || j.ImageMaterializationAttempts != attempt {
		return Job{}, ErrConflict
	}
	claim, ok := m.jobMaterializationClaims[id]
	if !ok || claim.owner != owner || !claim.leaseUntil.After(time.Now()) {
		return Job{}, ErrConflict
	}
	now := time.Now().UTC()
	j.ImageMaterializationStatus = "ready"
	j.ImageResolvedDigest = resolvedDigest
	j.ImageStorageKey = storageKey
	j.ImageMaterializationError = ""
	j.ImageMaterializedAt = &now
	j.ImageMaterializationNextAttemptAt = nil
	j.UpdatedAt = now
	delete(m.jobMaterializationClaims, id)
	m.jobs[id] = j
	m.bindJobRunImageLocked(j)
	return j, nil
}

func (m *MemStore) bindJobRunImageLocked(job Job) {
	for id, run := range m.jobRuns {
		if run.JobID != job.ID || run.ImageRefSnapshot != job.ImageRef || run.ImageStorageKeySnapshot != "" {
			continue
		}
		run.ImageResolvedDigestSnapshot = job.ImageResolvedDigest
		run.ImageStorageKeySnapshot = job.ImageStorageKey
		m.jobRuns[id] = run
	}
}

// JobRecordImageMaterializationFailure mirrors the durable retry transition.
// Attempts are incremented by JobClaimPendingImageMaterialization; once the
// bounded budget is exhausted the row becomes terminally failed.
func (m *MemStore) JobRecordImageMaterializationFailure(_ context.Context, id, sourceRef, owner, reason string, retryAt time.Time, maxAttempts int) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	j, ok := m.jobs[id]
	if !ok || j.Status == "deleted" {
		return Job{}, ErrNotFound
	}
	if j.ImageRef != sourceRef {
		return Job{}, ErrConflict
	}
	claim, ok := m.jobMaterializationClaims[id]
	if !ok || claim.owner != owner || !claim.leaseUntil.After(time.Now()) {
		return Job{}, ErrConflict
	}
	terminal := j.ImageMaterializationAttempts >= maxAttempts
	if terminal {
		j.ImageMaterializationStatus = "failed"
		j.ImageMaterializationNextAttemptAt = nil
	} else {
		j.ImageMaterializationStatus = "pending"
		next := retryAt.UTC()
		j.ImageMaterializationNextAttemptAt = &next
	}
	j.ImageMaterializationError = reason
	j.ImageMaterializedAt = nil
	j.UpdatedAt = time.Now().UTC()
	delete(m.jobMaterializationClaims, id)
	m.jobs[id] = j
	if terminal {
		m.settleJobImageFailureLocked(id, reason)
	}
	return j, nil
}

// settleJobImageFailureLocked closes queued tasks when the image can never
// become dispatchable. The caller holds m.mu.
func (m *MemStore) settleJobImageFailureLocked(jobID, reason string) {
	now := time.Now().UTC()
	for runID, run := range m.jobRuns {
		if run.JobID != jobID {
			continue
		}
		tasks := m.jobTasks[runID]
		changed := false
		for index, task := range tasks {
			if task.Status != "queued" {
				continue
			}
			task.Status = "failed"
			exitCode := 1
			task.ExitCode = &exitCode
			class := "infra"
			task.ErrorClass = &class
			task.ErrorMessage = &reason
			task.FinishedAt = &now
			task.NextAttemptAt = nil
			m.recordJobTaskAttemptLocked(task)
			tasks[index] = task
			changed = true
		}
		if changed {
			m.jobRuns[runID] = recomputeJobRun(run, tasks, now)
		}
	}
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
	if m.environmentGitOpsManagedJobLocked(id) {
		return false, false, ErrEnvironmentGitManaged
	}
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

// JobConcurrentByAccount mirrors the PostgreSQL live-instance predicate.
// Queued tasks consume no VM slot and must not count against the account cap.
func (m *MemStore) JobConcurrentByAccount(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobConcurrentByAccountLocked(accountID), nil
}

func (m *MemStore) jobConcurrentByAccountLocked(accountID string) int {
	count := 0
	for _, ins := range m.instances {
		if ins.Kind != "job_task" || (ins.State != string(StateWaking) && ins.State != string(StateColdBooting) && ins.State != string(StateRunning)) {
			continue
		}
		if job, ok := m.jobs[ins.JobID]; ok && job.AccountID == accountID {
			count++
		}
	}
	return count
}

func runHasPermanentFailure(run JobRun, tasks map[int]JobTask, job Job) bool {
	if run.FailurePolicy != "fail_fast" {
		return false
	}
	retryMax := job.RetryMax
	if run.RetryMax != nil {
		retryMax = *run.RetryMax
	}
	for _, task := range tasks {
		if (task.Status == "failed" || task.Status == "timeout" || task.Status == "oom") && (task.WorkDecision != nil && task.WorkDecision.Action == "fail_partition" || (task.WorkDecision == nil || task.WorkDecision.Action == "retry") && task.Attempt > retryMax) {
			return true
		}
	}
	return false
}

// --- job_runs --------------------------------------------------------

// JobRunCreate inserts a job_runs row + fans out `tasks` rows in
// jobTasks. Mirrors pgstore_jobs.JobRunCreate; the fan-out is a
// simple loop since memstore batches aren't bound by round-trips.
//
// Returns the run row + the fanned-out task slice (task_index 0..N-1,
// all status='queued').
func (m *MemStore) JobRunCreate(_ context.Context, jobID, accountID, triggerKind string, parallelism, retryMaxOverride, taskTimeoutOverride *int, envOverrides json.RawMessage, tasks int, options ...JobRunOptions) (JobRun, []JobTask, error) {
	var opts JobRunOptions
	if len(options) > 0 {
		opts = options[0]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok || job.AccountID != accountID || job.Status == "deleted" {
		return JobRun{}, nil, ErrNotFound
	}
	if job.ImageMaterializationStatus == "failed" {
		return JobRun{}, nil, ErrConflict
	}
	if len(envOverrides) == 0 {
		envOverrides = json.RawMessage("{}")
	}
	now := time.Now().UTC()
	var inputs []JobInput
	runID, exclusiveOperationID := "", ""
	var exclusiveGeneration int64
	if len(options) > 0 {
		inputs = options[0].Inputs
		runID = options[0].ID
		exclusiveOperationID = options[0].ExclusiveOperationID
		exclusiveGeneration = options[0].ExclusiveGeneration
	}
	if (exclusiveOperationID == "") != (exclusiveGeneration == 0) {
		return JobRun{}, nil, ErrInvalidArgument
	}
	if runID != "" {
		if _, err := uuid.Parse(runID); err != nil {
			return JobRun{}, nil, ErrInvalidArgument
		}
	}
	if exclusiveOperationID != "" {
		operation, exists := m.exclusiveOperations[exclusiveOperationID]
		if !exists || operation.AccountID != accountID || operation.JobID != jobID || operation.State != "running" ||
			operation.Generation != exclusiveGeneration || operation.LeaseExpiresAt == nil || !operation.LeaseExpiresAt.After(m.exclusiveTimeLocked()) ||
			operation.AttemptDeadline == nil || !operation.AttemptDeadline.After(m.exclusiveTimeLocked()) {
			return JobRun{}, nil, exclusivework.ErrStaleOwner
		}
	}
	if len(inputs) > 0 && len(inputs) != tasks {
		return JobRun{}, nil, fmt.Errorf("state: input count %d must equal tasks %d", len(inputs), tasks)
	}
	manifestVersion, inputDigest := jobInputDigest(inputs)
	inputManifestURI, inputManifestSHA256 := "", ""
	if len(options) > 0 {
		inputManifestURI, inputManifestSHA256 = options[0].InputManifestURI, options[0].InputManifestSHA256
	}
	executionClass := "standard"
	failurePolicy := "continue"
	var eligibleAt, latestStartAt *time.Time
	if len(options) > 0 {
		if options[0].ExecutionClass != "" {
			executionClass = options[0].ExecutionClass
		}
		if options[0].FailurePolicy != "" {
			failurePolicy = options[0].FailurePolicy
		}
		eligibleAt, latestStartAt = options[0].EligibleAt, options[0].LatestStartAt
	}
	if (executionClass == "standard" && (eligibleAt != nil || latestStartAt != nil)) ||
		(executionClass == "flexible" && (eligibleAt == nil || latestStartAt == nil || !latestStartAt.After(*eligibleAt))) ||
		(executionClass != "standard" && executionClass != "flexible") {
		return JobRun{}, nil, fmt.Errorf("state: invalid job execution window")
	}
	if failurePolicy != "continue" && failurePolicy != "fail_fast" {
		return JobRun{}, nil, fmt.Errorf("state: invalid job failure policy")
	}
	if eligibleAt != nil {
		value := eligibleAt.UTC()
		eligibleAt = &value
	}
	if latestStartAt != nil {
		value := latestStartAt.UTC()
		latestStartAt = &value
	}
	command := append([]string{}, job.Command...)
	if len(options) > 0 && options[0].CommandArgs != nil {
		command = append(append([]string{}, job.Command[:min(1, len(job.Command))]...), (*options[0].CommandArgs)...)
	}
	retryMax := derefInt(retryMaxOverride, job.RetryMax)
	timeout := derefInt(taskTimeoutOverride, job.TaskTimeoutS)
	effectiveEnv, err := jobEffectiveEnv(job.EnvOverrides, envOverrides)
	if err != nil {
		return JobRun{}, nil, err
	}
	ramMB := job.RAMMB
	if runID == "" {
		runID = newUUIDString()
	}
	if _, exists := m.jobRuns[runID]; exists {
		return JobRun{}, nil, ErrConflict
	}
	run := JobRun{
		FailureRules:                workpolicy.Clone(job.FailureRules),
		ID:                          runID,
		JobID:                       jobID,
		AccountID:                   accountID,
		ExclusiveOperationID:        exclusiveOperationID,
		ExclusiveGeneration:         exclusiveGeneration,
		TriggerKind:                 triggerKind,
		EnvOverrides:                envOverrides,
		Tasks:                       tasks,
		InputManifestVersion:        manifestVersion,
		InputDigest:                 inputDigest,
		InputManifestURI:            inputManifestURI,
		InputManifestSHA256:         inputManifestSHA256,
		Parallelism:                 derefInt(parallelism, job.MaxParallelism),
		ExecutionClass:              executionClass,
		FailurePolicy:               failurePolicy,
		EligibleAt:                  eligibleAt,
		LatestStartAt:               latestStartAt,
		RetryMax:                    &retryMax,
		TaskTimeoutS:                &timeout,
		Command:                     command,
		ImageRefSnapshot:            job.ImageRef,
		ImageResolvedDigestSnapshot: job.ImageResolvedDigest,
		ImageStorageKeySnapshot:     job.ImageStorageKey,
		RAMMBSnapshot:               &ramMB,
		EffectiveEnvSnapshot:        effectiveEnv,
		AggregateStatus:             "queued",
		CreatedAt:                   now,
	}
	if opts.FailureRules != nil {
		run.FailureRules = workpolicy.Clone(opts.FailureRules)
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
		if len(inputs) > 0 {
			t.InputID = inputs[i].ID
			t.InputRef = inputs[i].Ref
		}
		fanned = append(fanned, t)
		taskMap[i] = t
	}
	m.jobTasks[run.ID] = taskMap

	return run, fanned, nil
}

func (m *MemStore) JobRunReplayFailed(_ context.Context, sourceRunID, accountID string) (JobRun, []JobTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	source, ok := m.jobRuns[sourceRunID]
	if !ok || source.AccountID != accountID {
		return JobRun{}, nil, ErrNotFound
	}
	if source.FinishedAt == nil {
		return JobRun{}, nil, ErrConflict
	}
	job, ok := m.jobs[source.JobID]
	if !ok || job.Status != "active" {
		return JobRun{}, nil, ErrNotFound
	}
	if job.ImageMaterializationStatus != "ready" || job.ImageStorageKey == "" ||
		source.ImageRefSnapshot == "" || source.ImageResolvedDigestSnapshot == "" ||
		source.ImageRefSnapshot != job.ImageRef ||
		source.ImageResolvedDigestSnapshot != job.ImageResolvedDigest {
		return JobRun{}, nil, ErrConflict
	}
	var failed []JobTask
	for _, task := range m.jobTasks[sourceRunID] {
		switch task.Status {
		case "failed", "timeout", "oom", "cancelled":
			failed = append(failed, task)
		case "queued", "claimed":
			return JobRun{}, nil, ErrConflict
		}
	}
	if len(failed) == 0 {
		return JobRun{}, nil, ErrConflict
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].TaskIndex < failed[j].TaskIndex })
	now := time.Now().UTC()
	linked := sourceRunID
	run := JobRun{
		FailureRules: workpolicy.Clone(source.FailureRules),
		ID:           newUUIDString(), JobID: source.JobID, AccountID: accountID,
		TriggerKind: "manual", EnvOverrides: append(json.RawMessage(nil), source.EnvOverrides...),
		Tasks: len(failed), Parallelism: source.Parallelism,
		ExecutionClass: "standard", FailurePolicy: source.FailurePolicy,
		RetryMax: source.RetryMax, TaskTimeoutS: source.TaskTimeoutS,
		Command:          append([]string(nil), source.Command...),
		ImageRefSnapshot: job.ImageRef, ImageResolvedDigestSnapshot: job.ImageResolvedDigest,
		ImageStorageKeySnapshot: job.ImageStorageKey, RAMMBSnapshot: source.RAMMBSnapshot,
		EffectiveEnvSnapshot: append(json.RawMessage(nil), source.EffectiveEnvSnapshot...),
		SourceRunID:          &linked, AggregateStatus: "queued", CreatedAt: now,
	}
	if run.Command == nil {
		run.Command = append([]string{}, job.Command...)
	}
	if run.RAMMBSnapshot == nil {
		value := job.RAMMB
		run.RAMMBSnapshot = &value
	}
	if len(run.EffectiveEnvSnapshot) == 0 {
		var err error
		run.EffectiveEnvSnapshot, err = jobEffectiveEnv(job.EnvOverrides, run.EnvOverrides)
		if err != nil {
			return JobRun{}, nil, err
		}
	}
	inputs := make([]JobInput, len(failed))
	for i, task := range failed {
		inputs[i] = JobInput{ID: task.InputID, Ref: task.InputRef}
	}
	if source.InputManifestVersion > 0 {
		run.InputManifestVersion, run.InputDigest = jobInputDigest(inputs)
	}
	fanned := make([]JobTask, 0, len(failed))
	tasks := make(map[int]JobTask, len(failed))
	for i, task := range failed {
		origin := task.TaskIndex
		created := JobTask{RunID: run.ID, TaskIndex: i, SourceTaskIndex: &origin,
			InputID: task.InputID, InputRef: task.InputRef, Status: "queued", Attempt: 1, CreatedAt: now}
		fanned = append(fanned, created)
		tasks[i] = created
	}
	m.jobRuns[run.ID] = run
	m.jobTasks[run.ID] = tasks
	return run, fanned, nil
}

// JobRunCreateScheduled atomically advances the occurrence cursor and creates
// a one-task scheduled run. The expected schedule/cursor fence protects
// against duplicate fires and stale candidates after a customer update.
func (m *MemStore) JobRunCreateScheduled(ctx context.Context, jobID, schedule, timezone string, expectedLastScheduledAt *time.Time, firedAt time.Time) (JobRun, bool, error) {
	return m.JobRunCreateScheduledOccurrence(ctx, jobID, schedule, timezone, expectedLastScheduledAt, firedAt, JobScheduledOccurrenceOptions{ScheduledFor: firedAt})
}

func (m *MemStore) JobRunCreateScheduledOccurrence(_ context.Context, jobID, schedule, timezone string, expectedLastScheduledAt *time.Time, firedAt time.Time, options JobScheduledOccurrenceOptions) (JobRun, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, managed := m.exclusiveTriggerBindings["job_schedule\x00"+jobID]; managed && options.Disposition == "" {
		return JobRun{}, false, nil
	}
	job, ok := m.jobs[jobID]
	if !ok || job.Status != "active" || job.Kind != "recurring" ||
		job.CronSchedule != schedule || job.CronTimezone != timezone ||
		!sameTimePointer(job.LastScheduledAt, expectedLastScheduledAt) ||
		(options.ScheduleRevision > 0 && job.ScheduleRevision != options.ScheduleRevision) {
		return JobRun{}, false, nil
	}
	if expectedLastScheduledAt != nil && !firedAt.After(*expectedLastScheduledAt) {
		return JobRun{}, false, nil
	}
	firedAt = firedAt.UTC()
	scheduledFor := options.ScheduledFor.UTC()
	if options.ScheduledFor.IsZero() {
		scheduledFor = firedAt
	}
	if expectedLastScheduledAt != nil && !scheduledFor.After(*expectedLastScheduledAt) {
		return JobRun{}, false, nil
	}
	policy := job.SchedulePolicy
	if policy == nil {
		policy = &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "allow", MissedRuns: "skip"}
	}
	deadline := policy.Deadline(scheduledFor)
	status, reason, blocker := "queued", "", ""
	if options.Disposition != "" {
		if options.Disposition != "coalesced" && options.Disposition != "missed_deadline" {
			return JobRun{}, false, ErrInvalidArgument
		}
		status, reason = options.Disposition, options.Reason
	} else if workpolicy.DeadlineMissed(deadline, firedAt) {
		status, reason = "missed_deadline", "start deadline expired before the scheduler could dispatch the occurrence"
	}
	if status == "queued" && policy.Overlap != "allow" {
		for _, active := range m.jobRuns {
			if active.JobID == jobID && active.TriggerKind == "scheduled" && (active.AggregateStatus == "queued" || active.AggregateStatus == "running") {
				if active.OccurrenceID != "" {
					blocker = active.OccurrenceID
				}
				if policy.Overlap == "skip" {
					status, reason = "skipped_overlap", "an earlier scheduled run for this job is still active"
				} else {
					status, reason = "waiting_replacement", "replacement is waiting for the earlier scheduled run to stop"
				}
				break
			}
		}
	}
	job.LastScheduledAt = &scheduledFor
	m.jobs[jobID] = job
	now := firedAt
	occurrence := ScheduleOccurrence{ID: newUUIDString(), AccountID: job.AccountID, JobID: jobID,
		ScheduleRevision: job.ScheduleRevision, ScheduledFor: scheduledFor, StartDeadlineAt: deadline,
		SchedulePolicy: *workpolicy.Clone(policy), Status: status, Reason: reason,
		BlockingOccurrenceID: blocker, CreatedAt: now, UpdatedAt: now}
	m.scheduleOccurrences[occurrence.ID] = occurrence
	if status != "queued" {
		return JobRun{}, false, nil
	}

	envOverrides := append(json.RawMessage(nil), job.EnvOverrides...)
	if len(envOverrides) == 0 {
		envOverrides = json.RawMessage("{}")
	}
	effectiveEnv, err := jobEffectiveEnv(job.EnvOverrides, envOverrides)
	if err != nil {
		return JobRun{}, false, err
	}
	ramMB := job.RAMMB
	run := JobRun{
		FailureRules:                workpolicy.Clone(job.FailureRules),
		OccurrenceID:                occurrence.ID,
		StartDeadlineAt:             deadline,
		ID:                          newUUIDString(),
		JobID:                       jobID,
		AccountID:                   job.AccountID,
		TriggerKind:                 "scheduled",
		EnvOverrides:                envOverrides,
		Tasks:                       1,
		Parallelism:                 job.MaxParallelism,
		ExecutionClass:              "standard",
		FailurePolicy:               "continue",
		RetryMax:                    &job.RetryMax,
		TaskTimeoutS:                &job.TaskTimeoutS,
		Command:                     append([]string{}, job.Command...),
		ImageRefSnapshot:            job.ImageRef,
		ImageResolvedDigestSnapshot: job.ImageResolvedDigest,
		ImageStorageKeySnapshot:     job.ImageStorageKey,
		RAMMBSnapshot:               &ramMB,
		EffectiveEnvSnapshot:        effectiveEnv,
		AggregateStatus:             "queued",
		CreatedAt:                   firedAt,
	}
	occurrence.JobRunID = run.ID
	occurrence.Status = "queued"
	occurrence.UpdatedAt = now
	m.scheduleOccurrences[occurrence.ID] = occurrence
	m.jobRuns[run.ID] = run
	task := JobTask{RunID: run.ID, TaskIndex: 0, Status: "queued", Attempt: 1, CreatedAt: firedAt}
	m.jobTasks[run.ID] = map[int]JobTask{0: task}
	return run, true, nil
}

// JobScheduleAdvanceOccurrence advances a managed schedule cursor after its
// occurrence has been durably admitted as an exclusive operation. The
// idempotency key for admission is derived from the same expected cursor, so a
// retry after a crash reuses the receipt before advancing it again.
func (m *MemStore) JobScheduleAdvanceOccurrence(_ context.Context, jobID, schedule, timezone string, expectedLastScheduledAt *time.Time, firedAt time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok || job.Status != "active" || job.Kind != "recurring" || job.CronSchedule != schedule || job.CronTimezone != timezone ||
		!sameTimePointer(job.LastScheduledAt, expectedLastScheduledAt) || (expectedLastScheduledAt != nil && !firedAt.After(*expectedLastScheduledAt)) {
		return false, nil
	}
	firedAt = firedAt.UTC()
	job.LastScheduledAt = &firedAt
	m.jobs[jobID] = job
	return true, nil
}

func sameTimePointer(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
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

func (m *MemStore) JobRunListByExclusiveOperation(_ context.Context, accountID, operationID string) ([]JobRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var runs []JobRun
	for _, run := range m.jobRuns {
		if run.AccountID == accountID && run.ExclusiveOperationID == operationID {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ExclusiveGeneration < runs[j].ExclusiveGeneration })
	return runs, nil
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

// JobRunListActive mirrors the PostgreSQL operator incident query. Empty
// accountID selects the bounded fleet view.
func (m *MemStore) JobRunListActive(_ context.Context, accountID string, limit, offset int) ([]JobRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []JobRun
	for _, r := range m.jobRuns {
		if (accountID == "" || r.AccountID == accountID) && (r.AggregateStatus == "queued" || r.AggregateStatus == "running") {
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

// recomputeJobRun applies the same aggregate-status precedence as
// pgstore_jobs.JobRunRecompute and updates all denormalised counters.
// Callers hold m.mu when invoking this helper.
func recomputeJobRun(run JobRun, tasks map[int]JobTask, now time.Time) JobRun {
	var succ, fail, canc, running, queuedOrClaimed int
	for _, t := range tasks {
		switch t.Status {
		case "succeeded":
			succ++
		case "failed", "timeout", "oom":
			fail++
		case "cancelled":
			canc++
		case "claimed":
			running++
		case "queued":
			queuedOrClaimed++
		}
	}
	run.TasksSucceeded = succ
	run.TasksFailed = fail
	run.TasksCancelled = canc
	run.TasksRunning = running
	wasActive := run.AggregateStatus == "queued" || run.AggregateStatus == "running"

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
	if run.StartedAt == nil && ((running+queuedOrClaimed) > 0 || wasActive) {
		run.StartedAt = &now
	}
	if queuedOrClaimed == 0 && running == 0 && run.FinishedAt == nil {
		run.FinishedAt = &now
	}
	return run
}

func (m *MemStore) syncJobOccurrenceLocked(run JobRun, now time.Time) {
	if run.OccurrenceID == "" {
		return
	}
	occurrence, ok := m.scheduleOccurrences[run.OccurrenceID]
	if !ok {
		return
	}
	switch run.AggregateStatus {
	case "queued":
		occurrence.Status = "queued"
	case "running":
		occurrence.Status = "running"
	case "succeeded":
		occurrence.Status = "succeeded"
	case "cancelled":
		occurrence.Status = "cancelled"
	case "failed", "dead_letter":
		occurrence.Status = "failed"
	default:
		return
	}
	startedAt := firstJobTaskStartedAt(m.jobTasks[run.ID])
	if (run.AggregateStatus == "failed" || run.AggregateStatus == "cancelled" || run.AggregateStatus == "succeeded" || run.AggregateStatus == "dead_letter") &&
		startedAt == nil && workpolicy.DeadlineMissed(occurrence.StartDeadlineAt, now) {
		occurrence.Status = "missed_deadline"
		occurrence.Reason = "no task started before the occurrence start deadline"
	}
	if startedAt != nil {
		occurrence.StartedAt = cloneTimePtr(startedAt)
	}
	if occurrence.Status == "succeeded" || occurrence.Status == "failed" || occurrence.Status == "cancelled" || occurrence.Status == "missed_deadline" {
		finished := now.UTC()
		occurrence.FinishedAt = &finished
	}
	occurrence.UpdatedAt = now.UTC()
	m.scheduleOccurrences[occurrence.ID] = occurrence
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
	if job, ok := m.jobs[run.JobID]; ok && runHasPermanentFailure(run, tasks, job) {
		for index, task := range tasks {
			if task.Status != "queued" {
				continue
			}
			task.Status = "cancelled"
			class, message := "cancelled", "fail_fast after permanent task failure"
			task.ErrorClass, task.ErrorMessage = &class, &message
			finished := time.Now().UTC()
			task.FinishedAt = &finished
			m.recordJobTaskAttemptLocked(task)
			tasks[index] = task
		}
		m.jobTasks[runID] = tasks
	}
	run = recomputeJobRun(run, tasks, time.Now().UTC())
	m.jobRuns[runID] = run
	m.syncJobOccurrenceLocked(run, time.Now().UTC())
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
			t.LeaseToken = nil
			t.LeaseExpiresAt = nil
			t.LastLeaseNode = nil
			if t.FinishedAt == nil {
				t.FinishedAt = &now
			}
			m.recordJobTaskAttemptLocked(t)
			tasks[i] = t
		}
	}
	m.jobTasks[runID] = tasks

	originalStatus := run.AggregateStatus
	wasActive := originalStatus == "queued" || originalStatus == "running"
	run = recomputeJobRun(run, tasks, now)
	if wasActive {
		// Cancellation is an explicit terminal transition even when a
		// task had already failed; preserve the existing cancel contract
		// while taking the counters from the task rows.
		run.AggregateStatus = "cancelled"
		if run.StartedAt == nil {
			run.StartedAt = &now
		}
		run.FinishedAt = &now
	} else {
		run.AggregateStatus = originalStatus
	}
	m.jobRuns[runID] = run
	m.syncJobOccurrenceLocked(run, now)
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

func (m *MemStore) JobRunReopenDeadLetter(_ context.Context, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.jobRuns[runID]
	if !ok || r.DeadLetterCount <= 0 {
		return ErrNotFound
	}
	r.DeadLetterCount--
	r.AggregateStatus = "running"
	r.FinishedAt = nil
	m.jobRuns[runID] = r
	m.syncJobOccurrenceLocked(r, time.Now().UTC())
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
			run, runOK := m.jobRuns[t.RunID]
			job, jobOK := m.jobs[run.JobID]
			if !runOK || !jobOK || job.ImageMaterializationStatus != "ready" || job.ImageStorageKey == "" || !m.exclusiveJobRunCurrentLocked(run) {
				continue
			}
			if runHasPermanentFailure(run, tasks, job) {
				continue
			}
			if t.Status != "queued" {
				continue
			}
			if run.ExecutionClass == "flexible" && (run.EligibleAt == nil || run.LatestStartAt == nil || now.Before(*run.EligibleAt) || !now.Before(*run.LatestStartAt)) {
				continue
			}
			if run.StartDeadlineAt != nil && workpolicy.DeadlineMissed(run.StartDeadlineAt, now) && !jobRunHasStartedTask(m.jobTasks[t.RunID]) {
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
		left := m.jobRuns[matched[i].RunID]
		right := m.jobRuns[matched[k].RunID]
		if left.ExecutionClass != right.ExecutionClass {
			return left.ExecutionClass == "standard"
		}
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

func (m *MemStore) JobTaskExpireUnstarted(_ context.Context, now time.Time) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var runIDs []string
	expired := 0
	for runID, tasks := range m.jobTasks {
		if expired >= 1000 {
			break
		}
		run, ok := m.jobRuns[runID]
		if !ok {
			continue
		}
		flexibleExpired := run.ExecutionClass == "flexible" && run.LatestStartAt != nil && !now.Before(*run.LatestStartAt)
		scheduledExpired := run.StartDeadlineAt != nil && workpolicy.DeadlineMissed(run.StartDeadlineAt, now) && !jobRunHasStartedTask(tasks)
		if !flexibleExpired && !scheduledExpired {
			continue
		}
		changed := false
		for index, task := range tasks {
			if expired >= 1000 {
				break
			}
			if task.Status != "queued" {
				continue
			}
			task.Status = "cancelled"
			class, message := "cancelled", "flexible start window expired before admission"
			if scheduledExpired {
				message = "scheduled start deadline expired before first task start"
			}
			task.ErrorClass, task.ErrorMessage = &class, &message
			finished := now.UTC()
			task.FinishedAt = &finished
			m.recordJobTaskAttemptLocked(task)
			tasks[index] = task
			expired++
			changed = true
		}
		if changed {
			m.jobTasks[runID] = tasks
			runIDs = append(runIDs, runID)
		}
	}
	return runIDs, nil
}

func jobRunHasStartedTask(tasks map[int]JobTask) bool {
	return firstJobTaskStartedAt(tasks) != nil
}

func firstJobTaskStartedAt(tasks map[int]JobTask) *time.Time {
	var first *time.Time
	for _, task := range tasks {
		if task.StartedAt != nil && (first == nil || task.StartedAt.Before(*first)) {
			first = task.StartedAt
		}
	}
	return cloneTimePtr(first)
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

func (m *MemStore) CreateAndClaimJobInstance(_ context.Context, instanceID, jobID, runID string, taskIndex int, instanceState string, ramMB int, computeNodeID, wakeID, leaseToken string, leaseExpiresAt time.Time, leaseOwnerNodeID string) (Instance, error) {
	if err := validateMemStoreCreateInstanceState(instanceState); err != nil {
		return Instance{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return Instance{}, ErrNotFound
	}
	run, ok := m.jobRuns[runID]
	if !ok || run.JobID != jobID || run.AccountID != job.AccountID {
		return Instance{}, ErrNotFound
	}
	if !m.exclusiveJobRunCurrentLocked(run) {
		return Instance{}, exclusivework.ErrStaleOwner
	}
	now := time.Now().UTC()
	if run.ExecutionClass == "flexible" && (run.EligibleAt == nil || run.LatestStartAt == nil || now.Before(*run.EligibleAt) || !now.Before(*run.LatestStartAt)) {
		return Instance{}, ErrNotFound
	}
	tasks, ok := m.jobTasks[runID]
	if !ok {
		return Instance{}, ErrNotFound
	}
	if run.StartDeadlineAt != nil && workpolicy.DeadlineMissed(run.StartDeadlineAt, now) && !jobRunHasStartedTask(tasks) {
		return Instance{}, ErrNotFound
	}
	task, ok := tasks[taskIndex]
	if !ok || task.Status != "queued" {
		return Instance{}, ErrNotFound
	}
	if runHasPermanentFailure(run, tasks, job) {
		return Instance{}, ErrNotFound
	}
	plan := api.PlanHobby // Narrow tests may seed a job without an account row.
	if account, ok := m.accounts[job.AccountID]; ok {
		plan = account.Plan
	}
	accountCap := jobLiveConcurrencyCap(plan)
	if live := m.jobConcurrentByAccountLocked(job.AccountID); live >= accountCap {
		return Instance{}, &JobQuotaError{Scope: JobQuotaScopeConcurrent, Limit: accountCap, Observed: live + 1}
	}
	runCap := run.Parallelism
	claimed := 0
	for _, candidate := range tasks {
		if candidate.Status == "claimed" {
			claimed++
		}
	}
	if claimed >= runCap {
		return Instance{}, &JobQuotaError{Scope: JobQuotaScopeParallelism, Limit: runCap, Observed: claimed + 1}
	}
	if instanceID == "" {
		instanceID = newID()
	}
	if _, exists := m.instances[instanceID]; exists {
		return Instance{}, ErrConflict
	}
	ins := Instance{ID: instanceID, State: instanceState, RAMMB: ramMB, NodeID: computeNodeID,
		StartedAt: now, Mode: string(InstanceModeJob), Kind: "job_task", JobID: jobID,
		JobRunID: runID, JobTaskIndex: taskIndex}
	if wakeID != "" {
		ins.WakeID = wakeID
	} else {
		ins.WakeID = newID()
	}
	task.Status = "claimed"
	task.InstanceID = &instanceID
	task.LeaseToken = &leaseToken
	expires := leaseExpiresAt.UTC()
	task.LeaseExpiresAt = &expires
	task.LastLeaseNode = &leaseOwnerNodeID
	if task.StartedAt == nil {
		task.StartedAt = &now
	}
	m.instances[instanceID] = ins
	tasks[taskIndex] = task
	m.jobTasks[runID] = tasks
	return ins, nil
}

// JobTaskMarkTerminal transitions a single task to a terminal status
// AND stamps exit_code + error_class + error_message + finished_at.
//
// Returns ErrNotFound when (run_id, task_index) does not resolve OR
// when the task is already terminal.
func (m *MemStore) JobTaskMarkTerminal(_ context.Context, runID string, taskIndex int, status string, exitCode int, errorClass, errorMessage string, finishedAt time.Time) error {
	return m.jobTaskMarkTerminal(runID, taskIndex, "", "", false, status, exitCode, errorClass, errorMessage, "", false, false, finishedAt, nil)
}

// JobTaskMarkTerminalWithLogs is the in-memory mirror of the PostgreSQL
// atomic terminal transition and log persistence operation.
func (m *MemStore) JobTaskMarkTerminalWithLogs(_ context.Context, runID string, taskIndex int, status string, exitCode int, errorClass, errorMessage, logContent string, logTruncated bool, finishedAt time.Time) error {
	return m.jobTaskMarkTerminal(runID, taskIndex, "", "", false, status, exitCode, errorClass, errorMessage, logContent, logTruncated, true, finishedAt, nil)
}

func (m *MemStore) JobTaskCompleteClaimedWithLogs(_ context.Context, runID string, taskIndex int, instanceID, leaseToken, status string, exitCode int, errorClass, errorMessage, logContent string, logTruncated bool, finishedAt time.Time, outputManifest ...json.RawMessage) error {
	var output json.RawMessage
	if len(outputManifest) > 0 {
		output = outputManifest[0]
	}
	return m.jobTaskMarkTerminal(runID, taskIndex, instanceID, leaseToken, true, status, exitCode, errorClass, errorMessage, logContent, logTruncated, true, finishedAt, output)
}

func (m *MemStore) jobTaskMarkTerminal(runID string, taskIndex int, expectedInstanceID, expectedLeaseToken string, requireClaim bool, status string, exitCode int, errorClass, errorMessage, logContent string, logTruncated, persistLogs bool, finishedAt time.Time, outputManifest json.RawMessage, completions ...JobTaskCompletion) error {
	var decision *workpolicy.Decision
	var outcomeCode string
	if len(completions) > 0 {
		decision = completions[0].Decision
		outcomeCode = completions[0].OutcomeCode
	}
	if len(outputManifest) > 0 {
		if status != "succeeded" {
			return fmt.Errorf("state: output manifest requires successful task")
		}
		if _, err := jobresult.Validate(outputManifest); err != nil {
			return fmt.Errorf("state: invalid output manifest: %w", err)
		}
	}
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
	if requireClaim && (t.Status != "claimed" || t.InstanceID == nil || *t.InstanceID != expectedInstanceID || t.LeaseToken == nil || *t.LeaseToken != expectedLeaseToken) {
		return ErrNotFound
	}
	if run, exists := m.jobRuns[runID]; !exists {
		return ErrNotFound
	} else if !m.exclusiveJobRunCurrentLocked(run) {
		return exclusivework.ErrStaleOwner
	}
	t.Status = status
	t.WorkDecision = workpolicy.Clone(decision)
	t.OutcomeCode = outcomeCode
	t.ExitCode = &exitCode
	t.ErrorClass = nil
	t.ErrorMessage = nil
	if errorClass != "" {
		t.ErrorClass = &errorClass
	}
	if errorMessage != "" {
		t.ErrorMessage = &errorMessage
	}
	if persistLogs {
		t.LogContent = logContent
		t.LogTruncated = logTruncated
	}
	t.OutputManifest = append(json.RawMessage(nil), outputManifest...)
	fin := finishedAt.UTC()
	t.FinishedAt = &fin
	t.LeaseToken = nil
	t.LeaseExpiresAt = nil
	m.recordJobTaskAttemptLocked(t)
	tasks[taskIndex] = t
	m.jobTasks[runID] = tasks
	return nil
}

// JobTaskRetry reverses a failed/timeout/oom/cancelled transition back to
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
	if run, exists := m.jobRuns[runID]; !exists {
		return ErrNotFound
	} else if !m.exclusiveJobRunCurrentLocked(run) {
		return exclusivework.ErrStaleOwner
	}
	if t.Status != "failed" && t.Status != "timeout" && t.Status != "oom" && t.Status != "cancelled" {
		return ErrConflict
	}
	if t.WorkDecision != nil && t.WorkDecision.Action != "retry" {
		return ErrConflict
	}
	if run, ok := m.jobRuns[runID]; ok && run.ImageStorageKeySnapshot != "" {
		job := m.jobs[run.JobID]
		if job.ImageMaterializationStatus != "ready" || job.ImageStorageKey != run.ImageStorageKeySnapshot {
			return ErrConflict
		}
	}
	m.recordJobTaskAttemptLocked(t)
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
	t.LogContent = ""
	t.LogTruncated = false
	t.OutputManifest = nil
	t.WorkDecision = nil
	t.OutcomeCode = ""
	t.LeaseToken = nil
	t.LeaseExpiresAt = nil
	t.LastLeaseNode = nil
	tasks[taskIndex] = t
	m.jobTasks[runID] = tasks
	return nil
}

// JobTaskFailBoot atomically fences and accounts for a pre-execution VM boot
// failure. Unlike JobTaskRequeue, this consumes the configured retry budget.
func (m *MemStore) JobTaskFailBoot(_ context.Context, runID string, taskIndex int, instanceID, leaseToken string, retryMax int, nextAttemptAt time.Time, errorMessage string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks, ok := m.jobTasks[runID]
	if !ok {
		return false, ErrNotFound
	}
	t, ok := tasks[taskIndex]
	if !ok || t.Status != "claimed" || t.InstanceID == nil || *t.InstanceID != instanceID || t.LeaseToken == nil || *t.LeaseToken != leaseToken {
		return false, ErrNotFound
	}
	if run, exists := m.jobRuns[runID]; !exists {
		return false, ErrNotFound
	} else if !m.exclusiveJobRunCurrentLocked(run) {
		return false, exclusivework.ErrStaleOwner
	}
	class := "infra"
	t.ErrorClass = &class
	t.ErrorMessage = &errorMessage
	t.LeaseToken = nil
	t.LeaseExpiresAt = nil
	t.LastLeaseNode = nil
	if t.Attempt <= retryMax {
		failed := t
		failed.Status = "failed"
		code := 1
		failed.ExitCode = &code
		finished := time.Now().UTC()
		failed.FinishedAt = &finished
		m.recordJobTaskAttemptLocked(failed)
		t.Status = "queued"
		t.Attempt++
		t.InstanceID = nil
		t.StartedAt = nil
		t.FinishedAt = nil
		t.ExitCode = nil
		next := nextAttemptAt.UTC()
		t.NextAttemptAt = &next
		tasks[taskIndex] = t
		return true, nil
	}
	t.Status = "failed"
	code := 1
	t.ExitCode = &code
	finished := time.Now().UTC()
	t.FinishedAt = &finished
	t.NextAttemptAt = nil
	m.recordJobTaskAttemptLocked(t)
	tasks[taskIndex] = t
	return false, nil
}

// JobTaskDeferQueued only moves the due time of the same eligible queued
// attempt. It cannot clear a lease or shorten a newer retry's backoff.
func (m *MemStore) JobTaskDeferQueued(_ context.Context, runID string, taskIndex, expectedAttempt int, nextAttemptAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks, ok := m.jobTasks[runID]
	if !ok {
		return ErrNotFound
	}
	task, ok := tasks[taskIndex]
	if !ok || task.Status != "queued" || task.Attempt != expectedAttempt || task.InstanceID != nil || task.LeaseToken != nil ||
		(task.NextAttemptAt != nil && task.NextAttemptAt.After(time.Now().UTC())) {
		return ErrNotFound
	}
	next := nextAttemptAt.UTC()
	task.NextAttemptAt = &next
	tasks[taskIndex] = task
	return nil
}

// JobTaskRequeue reverses a CLAIMED-but-not-executed task back to
// queued WITHOUT incrementing attempt. Mirrors JobTaskRetry's
// column-reset contract (clears instance_id + lease columns +
// started_at) but preserves the attempt counter — the customer's
// retry budget is not consumed by transient dispatch-side failures
// (admission denied, run-lookup race). A claimed vmmd boot failure is
// accounted by JobTaskFailBoot instead.
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
	if run, exists := m.jobRuns[runID]; !exists {
		return ErrNotFound
	} else if !m.exclusiveJobRunCurrentLocked(run) {
		return exclusivework.ErrStaleOwner
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
	m.recordJobTaskAttemptLocked(t)
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

// JobTaskReapClaimed mirrors the fenced PostgreSQL transition under m.mu.
func (m *MemStore) JobTaskReapClaimed(_ context.Context, runID string, taskIndex int, leaseToken string, cutoff time.Time, retryMax int, nextAttemptAt time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks, ok := m.jobTasks[runID]
	if !ok {
		return false, ErrNotFound
	}
	task, ok := tasks[taskIndex]
	if !ok || task.Status != "claimed" || task.LeaseToken == nil || *task.LeaseToken != leaseToken || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.Before(cutoff) {
		return false, ErrNotFound
	}
	run := m.jobRuns[runID]
	var decision *workpolicy.Decision
	if run.FailureRules != nil {
		evaluated := workpolicy.Evaluate(run.FailureRules, workpolicy.Evidence{Uncertain: true})
		decision = &evaluated
	}
	retry := task.Attempt <= retryMax && (decision == nil || decision.Action == "retry")
	task.LeaseToken = nil
	task.LeaseExpiresAt = nil
	task.LastLeaseNode = nil
	code := 124
	class := "infra"
	message := "reaper reclaimed stale lease"
	if decision != nil {
		class = "uncertain"
		message = "completion receipt missing; the partition outcome is uncertain"
	}
	if retry {
		timedOut := task
		timedOut.Status = "timeout"
		finished := time.Now().UTC()
		timedOut.ExitCode, timedOut.ErrorClass, timedOut.ErrorMessage = &code, &class, &message
		timedOut.WorkDecision = workpolicy.Clone(decision)
		timedOut.FinishedAt = &finished
		m.recordJobTaskAttemptLocked(timedOut)
		task.Status = "queued"
		task.Attempt++
		task.InstanceID = nil
		next := nextAttemptAt.UTC()
		task.NextAttemptAt = &next
		task.StartedAt = nil
		task.FinishedAt = nil
		task.ExitCode = nil
		task.ErrorClass = nil
		task.ErrorMessage = nil
		task.LogContent = ""
		task.LogTruncated = false
		task.WorkDecision = nil
		task.OutcomeCode = ""
	} else {
		task.Status = "timeout"
		task.NextAttemptAt = nil
		now := time.Now().UTC()
		task.FinishedAt = &now
		task.ExitCode = &code
		task.ErrorClass = &class
		task.ErrorMessage = &message
		task.WorkDecision = workpolicy.Clone(decision)
		m.recordJobTaskAttemptLocked(task)
	}
	tasks[taskIndex] = task
	run = m.jobRuns[runID]
	run = recomputeJobRun(run, tasks, time.Now().UTC())
	if !retry {
		run.DeadLetterCount++
		run = recomputeJobRun(run, tasks, time.Now().UTC())
	}
	m.jobRuns[runID] = run
	return retry, nil
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

func (m *MemStore) ListOrphanedJobInstances(_ context.Context, limit int) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 64
	}
	owned := make(map[string]struct{})
	for _, tasks := range m.jobTasks {
		for _, task := range tasks {
			if task.Status == "claimed" && task.InstanceID != nil {
				owned[*task.InstanceID] = struct{}{}
			}
		}
	}
	out := make([]Instance, 0)
	for _, ins := range m.instances {
		if ins.Kind != "job_task" || (ins.State != "waking" && ins.State != "cold_booting" && ins.State != "running") {
			continue
		}
		if _, ok := owned[ins.ID]; ok {
			continue
		}
		out = append(out, ins)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) CompleteJobTaskAttempt(ctx context.Context, p JobTaskCompletion) error {
	return m.jobTaskMarkTerminal(p.RunID, p.TaskIndex, p.InstanceID, p.LeaseToken, true,
		p.Status, p.ExitCode, p.ErrorClass, p.ErrorMessage, p.LogContent, p.LogTruncated,
		true, p.FinishedAt, p.OutputManifest, p)
}
