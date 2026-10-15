package state

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// JobRunAttach is an interactive image run's session (ADR-1330). Only the
// SHA-256 digest of the one-time attach token is ever stored.
type JobRunAttach struct {
	TTY              bool
	TokenSHA256      []byte
	AttachTimeoutSec int
}

// JobRunAttachMaxTimeoutSec caps the attach window, matching the CHECK on
// job_run_attach_sessions.attach_timeout_s.
const JobRunAttachMaxTimeoutSec = 300

// JobTaskAttachTarget is what a gateway needs to route one attach: the task's
// status and the instance, state and node of its current VM.
type JobTaskAttachTarget struct {
	Status        string
	TTY           bool
	InstanceID    string
	InstanceState string
	NodeID        string
}

// Terminal reports whether the task will not run again.
func (t JobTaskAttachTarget) Terminal() bool {
	return JobTaskLogTarget{Status: t.Status}.Terminal()
}

// Ready reports whether the task VM has booted on a known node. schedd
// publishes the instance as running only after vmmd registered the session.
func (t JobTaskAttachTarget) Ready() bool {
	return t.Status == "claimed" && t.InstanceID != "" && t.NodeID != "" && t.InstanceState == string(StateRunning)
}

// JobRunAttachStore holds interactive image-run sessions. The record is
// created atomically with its run by JobRunCreate (JobRunOptions.Attach).
type JobRunAttachStore interface {
	// JobRunAttachSession returns a run's session, or ErrNotFound for an
	// ordinary run.
	JobRunAttachSession(ctx context.Context, runID string) (JobRunAttach, error)
	// JobTaskAttachTarget resolves an interactive task by account, job name,
	// run and task index; any other task is ErrNotFound.
	JobTaskAttachTarget(ctx context.Context, accountID, jobName, runID string, taskIndex int) (JobTaskAttachTarget, error)
}

// validateJobRunAttach admits a session only on a plain one-task run with no
// retries: the token opens exactly one session of one VM.
func validateJobRunAttach(opts JobRunOptions, tasks int, retryMaxOverride *int) error {
	attach := opts.Attach
	if attach == nil {
		return nil
	}
	switch {
	case tasks != 1 || retryMaxOverride == nil || *retryMaxOverride != 0:
		return fmt.Errorf("%w: an interactive run has one task and no retries", ErrInvalidArgument)
	case len(opts.Inputs) != 0 || opts.ExclusiveOperationID != "" || opts.ExecutionClass == "flexible":
		return fmt.Errorf("%w: an interactive run is a plain manual run", ErrInvalidArgument)
	case len(attach.TokenSHA256) != sha256.Size:
		return fmt.Errorf("%w: attach token digest must be SHA-256", ErrInvalidArgument)
	case attach.AttachTimeoutSec < 1 || attach.AttachTimeoutSec > JobRunAttachMaxTimeoutSec:
		return fmt.Errorf("%w: attach timeout must be 1..%d seconds", ErrInvalidArgument, JobRunAttachMaxTimeoutSec)
	}
	return nil
}

func insertJobRunAttach(ctx context.Context, tx pgx.Tx, runID string, attach *JobRunAttach) error {
	if attach == nil {
		return nil
	}
	_, err := tx.Exec(ctx, `insert into job_run_attach_sessions (run_id, tty, attach_token_sha256, attach_timeout_s)
		values ($1::uuid, $2, $3, $4)`, runID, attach.TTY, attach.TokenSHA256, attach.AttachTimeoutSec)
	if err != nil {
		return fmt.Errorf("state: record interactive session for run %s: %w", runID, err)
	}
	return nil
}

// JobRunAttachSession implements JobRunAttachStore.
func (s *PgStore) JobRunAttachSession(ctx context.Context, runID string) (JobRunAttach, error) {
	run, err := uuid.Parse(runID)
	if err != nil {
		return JobRunAttach{}, ErrNotFound
	}
	row, err := sqlc.New().JobRunAttachSession(ctx, s.pool, pgtype.UUID{Bytes: run, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return JobRunAttach{}, ErrNotFound
	}
	if err != nil {
		return JobRunAttach{}, err
	}
	return JobRunAttach{TTY: row.Tty, TokenSHA256: row.AttachTokenSha256, AttachTimeoutSec: int(row.AttachTimeoutS)}, nil
}

// JobTaskAttachTarget implements JobRunAttachStore.
func (s *PgStore) JobTaskAttachTarget(ctx context.Context, accountID, jobName, runID string, taskIndex int) (JobTaskAttachTarget, error) {
	account, err := uuid.Parse(accountID)
	if err != nil {
		return JobTaskAttachTarget{}, ErrNotFound
	}
	run, err := uuid.Parse(runID)
	if err != nil || taskIndex < 0 || taskIndex > math.MaxInt32 {
		return JobTaskAttachTarget{}, ErrNotFound
	}
	row, err := sqlc.New().JobTaskAttachTarget(ctx, s.pool, sqlc.JobTaskAttachTargetParams{
		AccountID: pgtype.UUID{Bytes: account, Valid: true},
		JobName:   jobName,
		RunID:     pgtype.UUID{Bytes: run, Valid: true},
		TaskIndex: int32(taskIndex),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return JobTaskAttachTarget{}, ErrNotFound
	}
	if err != nil {
		return JobTaskAttachTarget{}, err
	}
	return JobTaskAttachTarget{Status: row.Status, TTY: row.Tty, InstanceID: row.InstanceID,
		InstanceState: row.InstanceState, NodeID: row.NodeID}, nil
}

// JobRunAttachSession mirrors the PostgreSQL row.
func (m *MemStore) JobRunAttachSession(_ context.Context, runID string) (JobRunAttach, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.jobRunAttach[runID]
	if !ok {
		return JobRunAttach{}, ErrNotFound
	}
	record.TokenSHA256 = append([]byte(nil), record.TokenSHA256...)
	return record, nil
}

// JobTaskAttachTarget mirrors the PostgreSQL join.
func (m *MemStore) JobTaskAttachTarget(_ context.Context, accountID, jobName, runID string, taskIndex int) (JobTaskAttachTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.jobRunAttach[runID]
	if !ok {
		return JobTaskAttachTarget{}, ErrNotFound
	}
	run, ok := m.jobRuns[runID]
	if !ok || run.AccountID != accountID {
		return JobTaskAttachTarget{}, ErrNotFound
	}
	job, ok := m.jobs[run.JobID]
	if !ok || job.AccountID != accountID || job.Name != jobName || job.Status == "deleted" {
		return JobTaskAttachTarget{}, ErrNotFound
	}
	task, ok := m.jobTasks[runID][taskIndex]
	if !ok {
		return JobTaskAttachTarget{}, ErrNotFound
	}
	target := JobTaskAttachTarget{Status: task.Status, TTY: record.TTY}
	if task.InstanceID != nil {
		target.InstanceID = *task.InstanceID
		if ins, ok := m.instances[target.InstanceID]; ok {
			target.NodeID, target.InstanceState = ins.NodeID, string(ins.State)
		}
	}
	return target, nil
}
