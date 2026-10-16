package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) RecordOperatorAlertRetentionFailure(ctx context.Context, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if at.IsZero() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	at = at.UTC().Truncate(time.Microsecond)
	if at.After(m.operatorAlertRetention.LastAttemptAt) {
		m.operatorAlertRetention.LastAttemptAt = at
	}
	return nil
}

func (m *MemStore) OperatorAlertRetentionStatus(ctx context.Context, now time.Time) (OperatorAlertRetention, error) {
	if err := ctx.Err(); err != nil {
		return OperatorAlertRetention{}, err
	}
	if now.IsZero() {
		return OperatorAlertRetention{}, ErrInvalidArgument
	}
	now = now.UTC().Truncate(time.Microsecond)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.operatorAlertRetentionStatusLocked(now), nil
}

func (m *MemStore) operatorAlertRetentionStatusLocked(now time.Time) OperatorAlertRetention {
	out := m.operatorAlertRetention
	out.ExpiredEpisodes, out.OldestExpiredAt = 0, time.Time{}
	cutoff := now.Add(-api.ObsAlertHistoryRetention)
	for _, e := range m.operatorAlertEpisodes {
		at := e.LastSeenAt
		if e.ResolvedAt != nil {
			at = *e.ResolvedAt
		}
		if at.Before(cutoff) {
			out.ExpiredEpisodes++
			if out.OldestExpiredAt.IsZero() || at.Before(out.OldestExpiredAt) {
				out.OldestExpiredAt = at
			}
		}
	}
	return out
}
