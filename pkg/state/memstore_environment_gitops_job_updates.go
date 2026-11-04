package state

import (
	"context"
	"maps"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsJobUpdateStore = (*MemStore)(nil)

func (m *MemStore) PrepareEnvironmentGitOpsJobUpdates(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentGitOpsStep, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return nil, err
	}
	desired, err := desiredEnvironmentRevision(lease.Revision)
	if err != nil {
		return nil, err
	}
	snapshot := m.gitOpsSnapshotLocked(memory)
	observed, err := compileGitOpsObservation(snapshot, desired)
	if err != nil {
		return nil, err
	}
	plan, err := environmentGitOpsPlan(memory.source, lease.Revision, desired, observed, false)
	if err != nil {
		return nil, err
	}
	if memory.source.Spec.Mode != "enforce" || !plan.CanApply() || plan.Hash != reviewed.Hash {
		return nil, ErrConflict
	}
	jobs := maps.Clone(m.jobs)
	steps := []EnvironmentGitOpsStep{}
	for _, target := range gitOpsJobUpdates(snapshot, plan, observed.State.ResourceIDs) {
		job, exists := jobs[target.JobID]
		if !exists || job.AccountID != memory.source.AccountID {
			return nil, ErrConflict
		}
		if job.Status == "deleted" {
			continue // A detached, deleted Job can be replaced during intent apply.
		}
		active := false
		for _, tasks := range m.jobTasks {
			for _, task := range tasks {
				if m.jobRuns[task.RunID].JobID == job.ID && (task.Status == "queued" || task.Status == "claimed") {
					active = true
				}
			}
		}
		steps = append(steps, gitOpsJobUpdateStep(target, job.Status, active)...)
		if job.Status == "active" {
			job.Status, job.UpdatedAt = "paused", time.Now().UTC()
			jobs[job.ID] = job
		}
	}
	m.jobs = jobs
	return steps, nil
}
