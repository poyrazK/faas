package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type AppTaskObservationClass int

const (
	AppTaskObservationQueued AppTaskObservationClass = iota
	AppTaskObservationRetryWait
	AppTaskObservationHeld
	AppTaskObservationDeadlineOverdue
	AppTaskObservationRestoring
	AppTaskObservationRunning
	AppTaskObservationCancellationPending
	AppTaskObservationLeaseExpired
	AppTaskObservationUnknown
)

func (c AppTaskObservationClass) String() string {
	switch c {
	case AppTaskObservationQueued:
		return "queued"
	case AppTaskObservationRetryWait:
		return "retry_wait"
	case AppTaskObservationHeld:
		return "held"
	case AppTaskObservationDeadlineOverdue:
		return "deadline_overdue"
	case AppTaskObservationRestoring:
		return "restoring"
	case AppTaskObservationRunning:
		return "running"
	case AppTaskObservationCancellationPending:
		return "cancellation_pending"
	case AppTaskObservationLeaseExpired:
		return "lease_expired"
	default:
		return "unknown"
	}
}

func AppTaskObservationKind(i int) AppTaskKind {
	switch i {
	case 0:
		return AppTaskKindRelease
	case 1:
		return AppTaskKindManual
	default:
		return AppTaskKindCron
	}
}

type AppTaskObservationCohort struct {
	Pending, UnknownAge int64
	OldestAt            time.Time
}

// AppTaskObservationSnapshot covers every nonterminal task in the shared claim
// queue. Replicas overlap: never sum them. Boundaries are not continuous class
// residence. In particular renewal overwrites updated_at, so restoring age is
// unknown; neither lease duration nor task creation can recover its start time.
type AppTaskObservationSnapshot [3][9]AppTaskObservationCohort

type AppTaskObservationStore interface {
	AppTaskObservationSnapshot(context.Context, time.Time) (AppTaskObservationSnapshot, error)
}

var (
	_ AppTaskObservationStore = (*PgStore)(nil)
	_ AppTaskObservationStore = (*MemStore)(nil)
)

func (s *PgStore) AppTaskObservationSnapshot(ctx context.Context, at time.Time) (AppTaskObservationSnapshot, error) {
	if !at.After(time.Unix(0, 0)) {
		return AppTaskObservationSnapshot{}, ErrAppTaskInvalid
	}
	rows, err := sqlc.New().OperatorAppTaskSnapshot(ctx, s.pool, pgtype.Timestamptz{Time: at.UTC(), Valid: true})
	if err != nil {
		return AppTaskObservationSnapshot{}, fmt.Errorf("state: observe app tasks: %w", err)
	}
	var out AppTaskObservationSnapshot
	for _, r := range rows {
		found := false
		for k := range out {
			for c := range out[k] {
				if string(AppTaskObservationKind(k)) != r.Kind || AppTaskObservationClass(c).String() != r.Class {
					continue
				}
				if r.OldestAt.Valid && r.OldestAt.InfinityModifier != pgtype.Finite {
					return AppTaskObservationSnapshot{}, fmt.Errorf("state: invalid app task observation timing")
				}
				p := AppTaskObservationCohort{Pending: r.Pending, UnknownAge: r.UnknownAge}
				if r.OldestAt.Valid {
					p.OldestAt = r.OldestAt.Time
				}
				out[k][c], found = p, true
			}
		}
		if !found {
			return AppTaskObservationSnapshot{}, fmt.Errorf("state: invalid app task observation class or kind")
		}
	}
	return out, nil
}

func (m *MemStore) AppTaskObservationSnapshot(ctx context.Context, at time.Time) (AppTaskObservationSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return AppTaskObservationSnapshot{}, err
	}
	if !at.After(time.Unix(0, 0)) {
		return AppTaskObservationSnapshot{}, ErrAppTaskInvalid
	}
	var out AppTaskObservationSnapshot
	for _, task := range m.appTasks {
		if task.Status.Terminal() {
			continue
		}
		if !task.Kind.Valid() {
			return AppTaskObservationSnapshot{}, fmt.Errorf("state: invalid observed app task kind")
		}
		app, exists := m.apps[task.AppID]
		held := !exists || app.Status == AppDeleted || !m.appTaskExclusiveCurrentLocked(task, at)
		started := m.appTaskOccurrenceStartedLocked(task)
		c, boundary := appTaskObservationClass(task, at, held, started)
		for k := range out {
			if AppTaskObservationKind(k) != task.Kind {
				continue
			}
			p := &out[k][c]
			p.Pending++
			if boundary.IsZero() {
				p.UnknownAge++
			} else if p.OldestAt.IsZero() || boundary.Before(p.OldestAt) {
				p.OldestAt = boundary
			}
		}
	}
	return out, nil
}

func appTaskObservationClass(t AppTask, now time.Time, held, occurrenceStarted bool) (AppTaskObservationClass, time.Time) {
	valid := func(at time.Time) bool { return at.After(time.Unix(0, 0)) && !at.After(now) }
	if !valid(t.CreatedAt) || !valid(t.UpdatedAt) || t.UpdatedAt.Before(t.CreatedAt) || t.FinishedAt != nil ||
		(t.RetryAt != nil && !t.RetryAt.After(time.Unix(0, 0))) ||
		(t.StartDeadlineAt != nil && !t.StartDeadlineAt.After(time.Unix(0, 0))) ||
		(t.CancelRequested != nil && (!valid(*t.CancelRequested) || t.CancelRequested.Before(t.CreatedAt))) {
		return AppTaskObservationUnknown, time.Time{}
	}
	if t.Status == AppTaskQueued {
		if t.LeaseToken != nil || t.LeaseOwner != nil || t.LeaseExpiresAt != nil || t.StartedAt != nil {
			return AppTaskObservationUnknown, time.Time{}
		}
		if t.CancelRequested != nil {
			return AppTaskObservationCancellationPending, *t.CancelRequested
		}
		if workpolicy.DeadlineMissed(t.StartDeadlineAt, now) && !occurrenceStarted {
			return AppTaskObservationDeadlineOverdue, *t.StartDeadlineAt
		}
		if held {
			return AppTaskObservationHeld, time.Time{}
		}
		if t.RetryAt != nil && t.RetryAt.After(now) {
			return AppTaskObservationRetryWait, time.Time{}
		}
		boundary := primeLater(t.CreatedAt, t.UpdatedAt)
		if t.RetryAt != nil {
			boundary = primeLater(boundary, *t.RetryAt)
		}
		return AppTaskObservationQueued, boundary
	}
	if (t.Status != AppTaskRestoring && t.Status != AppTaskRunning) || t.LeaseToken == nil || *t.LeaseToken == "" ||
		t.LeaseOwner == nil || *t.LeaseOwner == "" || t.LeaseExpiresAt == nil || !t.LeaseExpiresAt.After(t.CreatedAt) ||
		(t.Status == AppTaskRestoring && t.StartedAt != nil) ||
		(t.Status == AppTaskRunning && (t.StartedAt == nil || !valid(*t.StartedAt) || t.StartedAt.Before(t.CreatedAt))) {
		return AppTaskObservationUnknown, time.Time{}
	}
	if !t.LeaseExpiresAt.After(now) {
		return AppTaskObservationLeaseExpired, *t.LeaseExpiresAt
	}
	if t.CancelRequested != nil {
		return AppTaskObservationCancellationPending, *t.CancelRequested
	}
	if t.Status == AppTaskRestoring {
		return AppTaskObservationRestoring, time.Time{}
	}
	return AppTaskObservationRunning, *t.StartedAt
}
