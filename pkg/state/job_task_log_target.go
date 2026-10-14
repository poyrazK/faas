package state

import (
	"context"
	"errors"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// JobTaskLogTarget is what a live job task log stream follows (ADR-1163):
// the task's status and attempt, and the instance and node of its current
// VM. InstanceID or NodeID is empty before dispatch and after the instance
// row is gone.
type JobTaskLogTarget struct {
	Status     string
	Attempt    int
	InstanceID string
	NodeID     string
}

// Terminal reports whether the task will not run again.
func (t JobTaskLogTarget) Terminal() bool {
	switch t.Status {
	case "queued", "claimed":
		return false
	default:
		return true
	}
}

// JobTaskLogTargetStore resolves a task by account, job name, run and task
// index. A task outside the account or job is ErrNotFound.
type JobTaskLogTargetStore interface {
	JobTaskLogTarget(ctx context.Context, accountID, jobName, runID string, taskIndex int) (JobTaskLogTarget, error)
}

// JobTaskLogTarget implements JobTaskLogTargetStore.
func (s *PgStore) JobTaskLogTarget(ctx context.Context, accountID, jobName, runID string, taskIndex int) (JobTaskLogTarget, error) {
	account, err := uuid.Parse(accountID)
	if err != nil {
		return JobTaskLogTarget{}, ErrNotFound
	}
	run, err := uuid.Parse(runID)
	if err != nil || taskIndex < 0 || taskIndex > math.MaxInt32 {
		return JobTaskLogTarget{}, ErrNotFound
	}
	row, err := sqlc.New().JobTaskLogTarget(ctx, s.pool, sqlc.JobTaskLogTargetParams{
		AccountID: pgtype.UUID{Bytes: account, Valid: true},
		JobName:   jobName,
		RunID:     pgtype.UUID{Bytes: run, Valid: true},
		TaskIndex: int32(taskIndex),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return JobTaskLogTarget{}, ErrNotFound
	}
	if err != nil {
		return JobTaskLogTarget{}, err
	}
	return JobTaskLogTarget{Status: row.Status, Attempt: int(row.Attempt), InstanceID: row.InstanceID, NodeID: row.NodeID}, nil
}

// JobTaskLogTarget mirrors the PostgreSQL join.
func (m *MemStore) JobTaskLogTarget(_ context.Context, accountID, jobName, runID string, taskIndex int) (JobTaskLogTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.jobRuns[runID]
	if !ok || run.AccountID != accountID {
		return JobTaskLogTarget{}, ErrNotFound
	}
	job, ok := m.jobs[run.JobID]
	if !ok || job.AccountID != accountID || job.Name != jobName || job.Status == "deleted" {
		return JobTaskLogTarget{}, ErrNotFound
	}
	task, ok := m.jobTasks[runID][taskIndex]
	if !ok {
		return JobTaskLogTarget{}, ErrNotFound
	}
	target := JobTaskLogTarget{Status: task.Status, Attempt: task.Attempt}
	if task.InstanceID != nil {
		target.InstanceID = *task.InstanceID
		if ins, ok := m.instances[target.InstanceID]; ok {
			target.NodeID = ins.NodeID
		}
	}
	return target, nil
}
