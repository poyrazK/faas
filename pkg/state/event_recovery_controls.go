package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func eventRecoveryActive(state string) bool { return state == "running" || state == "paused" }
func (s *PgStore) PauseEventRecovery(ctx context.Context, account, job string) (api.EventRecoveryJob, error) {
	return s.controlEventRecovery(ctx, account, job, "pause", 0)
}
func (s *PgStore) ResumeEventRecovery(ctx context.Context, account, job string) (api.EventRecoveryJob, error) {
	return s.controlEventRecovery(ctx, account, job, "resume", 0)
}
func (s *PgStore) SetEventRecoveryRate(ctx context.Context, account, job string, req api.EventRecoveryRateRequest) (api.EventRecoveryJob, error) {
	if err := req.Validate(); err != nil {
		return api.EventRecoveryJob{}, errors.Join(ErrEventRecoveryQuery, err)
	}
	ctx, err := WithEventRecoveryReason(ctx, req.Reason)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	return s.controlEventRecovery(ctx, account, job, "rate", req.RatePerSecond)
}
func (s *PgStore) controlEventRecovery(ctx context.Context, account, id, action string, rate int) (api.EventRecoveryJob, error) {
	if err := eventRecoveryIDs(account, id); err != nil {
		return api.EventRecoveryJob{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	job, err := q.EventRecoveryLock(ctx, tx, sqlc.EventRecoveryLockParams{AccountID: mustPgUUID(account), JobID: mustPgUUID(id)})
	if err != nil {
		return api.EventRecoveryJob{}, mapErr(err)
	}
	if _, err = getEventRecoveryMetadata(ctx, q, tx, account, id); err != nil {
		return api.EventRecoveryJob{}, err
	}
	if !eventRecoveryActive(job.State) {
		return api.EventRecoveryJob{}, ErrEventRecoveryState
	}
	now := time.Now().UTC()
	if !timeFromPgtype(job.ExpiresAt).After(now) {
		if err = cancelEventRecoveryTx(ctx, q, tx, id, now, "expired"); err != nil {
			return api.EventRecoveryJob{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return api.EventRecoveryJob{}, err
		}
		return api.EventRecoveryJob{}, ErrEventRecoveryState
	}
	switch action {
	case "pause":
		err = q.EventRecoveryPause(ctx, tx, sqlc.EventRecoveryPauseParams{JobID: job.ID, NowAt: pgtypeFromTime(now)})
	case "resume":
		err = q.EventRecoveryResume(ctx, tx, sqlc.EventRecoveryResumeParams{JobID: job.ID, NowAt: pgtypeFromTime(now)})
	case "rate":
		err = q.EventRecoverySetRate(ctx, tx, sqlc.EventRecoverySetRateParams{JobID: job.ID, NowAt: pgtypeFromTime(now), RatePerSecond: int32(rate)})
	}
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	out, err := getEventRecovery(ctx, q, tx, account, id)
	if err != nil {
		return out, err
	}
	if out.State != job.State || out.RatePerSecond != int(job.RatePerSecond) {
		auditAction := map[string]string{"pause": "paused", "resume": "resumed", "rate": "rate_changed"}[action]
		if err = insertRecoveryHistory(ctx, q, tx, id, auditAction, job.State, int(job.RatePerSecond), now); err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}
func (m *MemStore) PauseEventRecovery(ctx context.Context, account, job string) (api.EventRecoveryJob, error) {
	return m.controlEventRecovery(ctx, account, job, "pause", 0)
}
func (m *MemStore) ResumeEventRecovery(ctx context.Context, account, job string) (api.EventRecoveryJob, error) {
	return m.controlEventRecovery(ctx, account, job, "resume", 0)
}
func (m *MemStore) SetEventRecoveryRate(ctx context.Context, account, job string, req api.EventRecoveryRateRequest) (api.EventRecoveryJob, error) {
	if err := req.Validate(); err != nil {
		return api.EventRecoveryJob{}, errors.Join(ErrEventRecoveryQuery, err)
	}
	ctx, err := WithEventRecoveryReason(ctx, req.Reason)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	return m.controlEventRecovery(ctx, account, job, "rate", req.RatePerSecond)
}
func (m *MemStore) controlEventRecovery(ctx context.Context, account, id, action string, rate int) (api.EventRecoveryJob, error) {
	if err := ctx.Err(); err != nil {
		return api.EventRecoveryJob{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, err := m.eventRecoveryJobLocked(account, id)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	if !eventRecoveryActive(job.Job.State) {
		return api.EventRecoveryJob{}, ErrEventRecoveryState
	}
	now := time.Now().UTC()
	if !job.Job.ExpiresAt.After(now) {
		m.memCancelEventRecovery(ctx, job, now, "expired")
		return api.EventRecoveryJob{}, ErrEventRecoveryState
	}
	previous, previousRate := job.Job.State, job.Job.RatePerSecond
	switch {
	case action == "pause" && job.Job.State == "running":
		job.Job.State = "paused"
		job.Job.PausedAt = &now
		job.Job.UpdatedAt = now
	case action == "resume" && job.Job.State == "paused":
		job.Job.State = "running"
		job.Job.PausedAt = nil
		job.WaitReason = ""
		job.CapacityScope = ""
		job.CapacityWaitStartedAt = nil
		job.CapacityWaitObservedAt = nil
		job.Job.UpdatedAt = now
		job.NextAttemptAt = maxRecoveryTime(job.NextAttemptAt, now)
	case action == "rate" && rate != job.Job.RatePerSecond:
		job.Job.RatePerSecond = rate
		job.Job.UpdatedAt = now
		job.NextAttemptAt = maxRecoveryTime(job.NextAttemptAt, now)
	}
	if job.WindowStartedAt.Add(time.Second).After(now) && job.WindowCount >= job.Job.RatePerSecond {
		job.NextAttemptAt = maxRecoveryTime(job.NextAttemptAt, job.WindowStartedAt.Add(time.Second))
	}
	if job.Job.State != previous || job.Job.RatePerSecond != previousRate {
		memRecoveryHistory(ctx, job, map[string]string{"pause": "paused", "resume": "resumed", "rate": "rate_changed"}[action], previous, previousRate, now)
	}
	return m.eventRecoveryObservedResponseLocked(job, now), nil
}
func maxRecoveryTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
func memRecoveryDueAt(job *memEventRecoveryJob, now time.Time) time.Time {
	if !job.Job.ExpiresAt.After(now) {
		return job.Job.ExpiresAt
	}
	return job.NextAttemptAt
}
