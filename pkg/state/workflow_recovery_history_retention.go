package state

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

type WorkflowRecoveryHistoryRetentionStore interface {
	PruneWorkflowRecoveryHistory(context.Context, time.Time) (int64, error)
}

func (s *PgStore) PruneWorkflowRecoveryHistory(ctx context.Context, now time.Time) (int64, error) {
	if now.IsZero() {
		return 0, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	n, err := sqlc.New().WorkflowRecoveryHistoryExpire(ctx, s.pool, sqlc.WorkflowRecoveryHistoryExpireParams{BeforeAt: pgtypeFromTime(now.Add(-api.WorkflowRecoveryHistoryRetention)), BatchLimit: int32(api.WorkflowRecoveryHistoryPruneBatch)})
	if err != nil {
		return 0, fmt.Errorf("expire durable recovery history: %w", err)
	}
	more, err := sqlc.New().WorkflowRecoveryFailureExpire(ctx, s.pool, sqlc.WorkflowRecoveryFailureExpireParams{BeforeAt: pgtypeFromTime(now.Add(-api.WorkflowRecoveryFailureRetention)), BatchLimit: int32(api.WorkflowRecoveryFailurePruneBatch)})
	if err != nil {
		return n, fmt.Errorf("expire policy-independent failure evidence: %w", err)
	}
	return n + more, nil
}
func (m *MemStore) PruneWorkflowRecoveryHistory(ctx context.Context, now time.Time) (int64, error) {
	if now.IsZero() {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	cutoff := now.Add(-api.WorkflowRecoveryHistoryRetention)
	var n int64
	for _, p := range m.recoveryPolicies {
		retained := p.History[:0]
		for _, e := range p.History {
			if n < int64(api.WorkflowRecoveryHistoryPruneBatch) && !e.RecordedAt.After(cutoff) {
				n++
			} else {
				retained = append(retained, e)
			}
		}
		clear(p.History[len(retained):])
		p.History = retained
	}
	var more int64
	for app, records := range m.workflowFailureEvidence {
		retained := records[:0]
		_, owned := m.eventSubscriptionAppLocked(app)
		for _, e := range records {
			if more < int64(api.WorkflowRecoveryFailurePruneBatch) && (!owned || !e.ObservedAt.After(now.Add(-api.WorkflowRecoveryFailureRetention))) {
				more++
			} else {
				retained = append(retained, e)
			}
		}
		clear(records[len(retained):])
		if len(retained) == 0 {
			delete(m.workflowFailureEvidence, app)
		} else {
			m.workflowFailureEvidence[app] = retained
		}
	}
	return n + more, nil
}
