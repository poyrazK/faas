package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DataRepairDraftExpiryStore settles only unapproved, expired drafts. It grants
// no execution authority and does not depend on artifact storage configuration.
type DataRepairDraftExpiryStore interface {
	ExpireDataRepairDrafts(context.Context, time.Time, int) error
}

// Only an unapproved draft with no active rehearsal can expire without reading
// the source. Apply outcomes and lifecycle reservations remain explicitly fenced.
func expiredRepairDraft(op Operation, now time.Time) bool {
	p := op.DataRepair
	if p == nil || p.Phase != api.DataRepairPrepared || p.Approval != nil || p.PlanExpiresAt.IsZero() || p.PlanExpiresAt.After(now) {
		return false
	}
	switch p.RehearsalStatus {
	case RepairRehearsalQueued, RepairRehearsalRunning, RepairRehearsalCleanup, RepairRehearsalPublishing:
		return false
	default:
		return true
	}
}

func operationCountsPending(op Operation, now time.Time) bool {
	return !op.State.Terminal() && !expiredRepairDraft(op, now)
}

func (s *PgStore) ExpireDataRepairDrafts(ctx context.Context, now time.Time, limit int) error {
	if limit < 1 || limit > api.OperationRetentionPageMax {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := sqlc.New().LockExpiredDataRepairDrafts(ctx, tx, sqlc.LockExpiredDataRepairDraftsParams{
		ObservedAt: operationPGTime(&now), PageLimit: int32(limit),
	})
	if err != nil {
		return err
	}
	for _, raw := range rows {
		op, err := operationPGRecord(raw)
		if err != nil {
			return err
		}
		if !expiredRepairDraft(op, now) {
			continue
		}
		event, err := advanceDataRepair(&op, DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: api.DataRepairFailed}, now)
		if err != nil {
			return err
		}
		if err := saveDataRepairTx(ctx, tx, op, event); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (m *MemStore) ExpireDataRepairDrafts(_ context.Context, now time.Time, limit int) error {
	if limit < 1 || limit > api.OperationRetentionPageMax {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	expired := 0
	for _, op := range m.operationMemoryLocked().operations {
		if expired >= limit {
			break
		}
		if op.State.Terminal() || !expiredRepairDraft(op, now) {
			continue
		}
		event, err := advanceDataRepair(&op, DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: api.DataRepairFailed}, now)
		if err != nil {
			return err
		}
		m.saveDataRepairLocked(op, event)
		expired++
	}
	return nil
}
