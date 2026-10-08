package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventRecoveryHealthStore interface {
	GetEventRecoveryHealth(context.Context, string, string, time.Time) (api.EventRecoveryHealth, error)
}

func recoveryHealthResponse(app string, now time.Time) api.EventRecoveryHealth {
	return api.EventRecoveryHealth{CapacityWaitWarningSeconds: int64(api.EventRecoveryCapacityWaitWarning / time.Second), AppID: canonicalMemUUID(app), ObservedAt: now, StallGraceSeconds: int64(api.EventRecoveryStallGrace / time.Second), ExpiryWarningSeconds: int64(api.EventRecoveryExpiryWarning / time.Second), Jobs: []api.EventRecoveryJobHealth{}}
}
func appendRecoveryHealth(out *api.EventRecoveryHealth, job api.EventRecoveryJobHealth, created, window time.Time, spent int) {
	now := out.ObservedAt
	job.EligibleAt = job.NextAttemptAt
	if spent >= job.RatePerSecond {
		job.EligibleAt = maxRecoveryTime(job.EligibleAt, window.Add(time.Second))
	}
	baseline := created
	if job.LastProgressAt != nil {
		baseline = *job.LastProgressAt
		job.ProgressKnown = true
	}
	job.ProgressAgeSeconds = max(0, now.Sub(baseline).Seconds())
	job.OverdueSeconds = max(0, now.Sub(job.EligibleAt).Seconds())
	job.Expiring = job.PendingCount > 0 && !job.ExpiresAt.After(now.Add(api.EventRecoveryExpiryWarning))
	switch {
	case job.State == "paused":
		job.Status = "paused"
		out.PausedJobs++
		if job.Expiring {
			out.PausedExpiringJobs++
		}
	default:
		out.RunningJobs++
		if job.Expiring {
			out.ExpiringJobs++
		}
		switch {
		case job.PendingCount == 0:
			job.Status = "finishing"
		case !job.ExpiresAt.After(now):
			job.Status = "expired"
		case now.Sub(baseline) >= api.EventRecoveryStallGrace && now.Sub(job.EligibleAt) >= api.EventRecoveryStallGrace:
			job.Status = "stalled"
			out.StalledJobs++
		case job.WaitReason == "capacity":
			job.Status = "capacity_wait"
		case job.WaitReason == "legacy_claim":
			job.Status = "legacy_claim_wait"
		case job.EligibleAt.After(now):
			job.Status = "paced"
		default:
			job.Status = "running"
		}
	}
	if job.CapacityWait != nil {
		job.CapacityWait.AgeSeconds = max(0, now.Sub(job.CapacityWait.StartedAt).Seconds())
		if job.Status == "capacity_wait" && now.Sub(job.CapacityWait.ObservedAt) <= api.EventRecoveryStallGrace && !job.CapacityWait.ObservedAt.After(now) {
			out.CapacityWaitingJobs++
			if now.Sub(job.CapacityWait.StartedAt) >= api.EventRecoveryCapacityWaitWarning {
				out.ProlongedCapacityWaitJobs++
			}
		}
	}
	if job.Mode == "" {
		job.Mode = "routing"
	}
	out.Jobs = append(out.Jobs, job)
}
func (s *PgStore) GetEventRecoveryHealth(ctx context.Context, account, app string, now time.Time) (api.EventRecoveryHealth, error) {
	out := recoveryHealthResponse(app, now)
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	if now.IsZero() {
		return out, ErrEventRecoveryQuery
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	q := sqlc.New()
	if _, err = q.EventRecoveryListApp(ctx, tx, sqlc.EventRecoveryListAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); err != nil {
		return out, mapErr(err)
	}
	rows, err := q.EventRecoveryHealth(ctx, tx, sqlc.EventRecoveryHealthParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		var selection api.EventRecoveryRequest
		if err := json.Unmarshal(r.Selection, &selection); err != nil {
			return out, err
		}
		appendRecoveryHealth(&out, api.EventRecoveryJobHealth{JobID: uuidString(r.ID), Mode: selection.Mode, State: r.State, PendingCount: r.PendingCount, RatePerSecond: int(r.RatePerSecond), LastProgressAt: timestamptzToTimePtr(r.LastProgressAt), NextAttemptAt: timeFromPgtype(r.NextAttemptAt), ExpiresAt: timeFromPgtype(r.ExpiresAt), WaitReason: r.WaitReason, CapacityWait: recoveryCapacityWait(r.CapacityScope, timestamptzToTimePtr(r.CapacityWaitStartedAt), timestamptzToTimePtr(r.CapacityWaitObservedAt))}, timeFromPgtype(r.CreatedAt), timeFromPgtype(r.WindowStartedAt), int(r.WindowCount))
	}
	return out, tx.Commit(ctx)
}
func (m *MemStore) GetEventRecoveryHealth(ctx context.Context, account, app string, now time.Time) (api.EventRecoveryHealth, error) {
	out := recoveryHealthResponse(app, now)
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	if now.IsZero() {
		return out, ErrEventRecoveryQuery
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventRecoveryAppLocked(account, app) {
		return out, ErrNotFound
	}
	entries := []*memEventRecoveryJob{}
	for _, job := range m.eventRecoveryJobs {
		if sameMemUUID(job.AccountID, account) && sameMemUUID(job.Job.AppID, app) && eventRecoveryActive(job.Job.State) {
			entries = append(entries, job)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Job.CreatedAt.Equal(entries[j].Job.CreatedAt) {
			return entries[i].Job.ID < entries[j].Job.ID
		}
		return entries[i].Job.CreatedAt.Before(entries[j].Job.CreatedAt)
	})
	for _, entry := range entries {
		pending := int64(0)
		for _, item := range entry.Items {
			if item.State == "pending" {
				pending++
			}
		}
		var progress *time.Time
		if entry.LastProgressAt != nil {
			t := *entry.LastProgressAt
			progress = &t
		}
		appendRecoveryHealth(&out, api.EventRecoveryJobHealth{JobID: entry.Job.ID, Mode: entry.Job.Selection.Mode, State: entry.Job.State, PendingCount: pending, RatePerSecond: entry.Job.RatePerSecond, LastProgressAt: progress, NextAttemptAt: entry.NextAttemptAt, ExpiresAt: entry.Job.ExpiresAt, WaitReason: entry.WaitReason, CapacityWait: recoveryCapacityWait(entry.CapacityScope, entry.CapacityWaitStartedAt, entry.CapacityWaitObservedAt)}, entry.Job.CreatedAt, entry.WindowStartedAt, entry.WindowCount)
	}
	return out, nil
}

func recoveryCapacityWait(scope string, started, observed *time.Time) *api.EventRecoveryCapacityWait {
	if scope == "" || started == nil || observed == nil {
		return nil
	}
	gate := "pending_delivery_limit"
	explanation := map[string]string{"account": "Account pending-delivery admission limit reached.", "app": "Application pending-delivery admission limit reached.", "consumer": "Captured consumer pending-delivery admission limit reached."}[scope]
	if explanation == "" {
		gate = "unknown"
		explanation = "Admission reported capacity exhaustion without a known scope."
	}
	return &api.EventRecoveryCapacityWait{Scope: scope, Gate: gate, Explanation: explanation, StartedAt: *started, ObservedAt: *observed}
}
