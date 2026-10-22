package state

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// JobEnsureImageRun mirrors PgStore.JobEnsureImageRun under the MemStore
// mutex, which stands in for the account row lock.
func (m *MemStore) JobEnsureImageRun(_ context.Context, in ImageRunEnsure) (ImageRunEnsureResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, job := range m.jobs {
		if job.AccountID != in.AccountID || job.Name != in.Name || job.Status == "deleted" {
			continue
		}
		if job.Kind != JobKindImageRun || job.ImageRef != in.ImageRef {
			return ImageRunEnsureResult{}, fmt.Errorf("%w: job name %q is taken", ErrConflict, in.Name)
		}
		requeue := imageRunNeedsRequeue(job, in.PullAlways)
		job.RAMMB, job.TaskTimeoutS, job.UpdatedAt = in.RAMMB, in.TaskTimeoutSec, time.Now().UTC()
		if requeue {
			job.ImageResolvedDigest, job.ImageStorageKey = "", ""
			job.ImageMaterializationStatus, job.ImageMaterializationError = "pending", ""
			job.ImageMaterializedAt, job.ImageMaterializationNextAttemptAt = nil, nil
			job.ImageMaterializationAttempts = 0
			job.ImageEntrypoint, job.ImageCmd, job.ImageConfigRecorded = nil, nil, false
			delete(m.jobMaterializationClaims, id)
		}
		m.jobs[id] = job
		return ImageRunEnsureResult{Job: job, Requeued: requeue}, nil
	}
	var cached []Job
	for _, job := range m.jobs {
		if job.AccountID == in.AccountID && job.Kind == JobKindImageRun && job.Status != "deleted" {
			cached = append(cached, job)
		}
	}
	var res ImageRunEnsureResult
	if excess := len(cached) - in.MaxCached + 1; excess > 0 {
		sort.Slice(cached, func(i, k int) bool {
			if cached[i].UpdatedAt.Equal(cached[k].UpdatedAt) {
				return cached[i].ID < cached[k].ID
			}
			return cached[i].UpdatedAt.Before(cached[k].UpdatedAt)
		})
		var evict []Job
		for _, job := range cached {
			if len(evict) == excess {
				break
			}
			if !m.jobHasActiveTasksLocked(job.ID) {
				evict = append(evict, job)
			}
		}
		if len(evict) < excess {
			return ImageRunEnsureResult{}, &ImageRunCacheFullError{Limit: in.MaxCached, Observed: len(cached)}
		}
		now := time.Now().UTC()
		for _, job := range evict {
			job.Status, job.UpdatedAt = "deleted", now
			m.jobs[job.ID] = job
			res.Evicted = append(res.Evicted, job)
		}
	}
	created, err := m.jobCreateLocked(in.AccountID, in.Name, JobKindImageRun, in.ImageRef, []string{},
		in.RAMMB, in.TaskTimeoutSec, 1, 0, nil)
	if err != nil {
		return ImageRunEnsureResult{}, err
	}
	res.Job, res.Created = created, true
	return res, nil
}

// jobHasActiveTasksLocked mirrors JobSoftDelete's active-work predicate:
// any queued or claimed task of any run of the job.
func (m *MemStore) jobHasActiveTasksLocked(jobID string) bool {
	for _, tasks := range m.jobTasks {
		for _, t := range tasks {
			if t.Status != "queued" && t.Status != "claimed" {
				continue
			}
			if run, ok := m.jobRuns[t.RunID]; ok && run.JobID == jobID {
				return true
			}
		}
	}
	return false
}

// JobRecordImageConfig mirrors the PostgreSQL claim fence.
func (m *MemStore) JobRecordImageConfig(_ context.Context, id, sourceRef, owner string, attempt int, entrypoint, cmd []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok || job.Status == "deleted" || job.ImageRef != sourceRef ||
		job.ImageMaterializationStatus != "pending" || job.ImageMaterializationAttempts != attempt {
		return ErrConflict
	}
	claim, ok := m.jobMaterializationClaims[id]
	if !ok || claim.owner != owner || !claim.leaseUntil.After(time.Now()) {
		return ErrConflict
	}
	job.ImageEntrypoint, job.ImageCmd, job.ImageConfigRecorded = nonNilArgs(entrypoint), nonNilArgs(cmd), true
	m.jobs[id] = job
	return nil
}
