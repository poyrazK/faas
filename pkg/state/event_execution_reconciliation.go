package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventExecutionReconciliationStore interface {
	EventExecutionReconciliation(context.Context, string, string, string, bool) (api.EventExecutionReconciliation, error)
	AdvanceEventExecutionReconciliations(context.Context, int) (int, error)
}

func reconciliationStatus(app, sub string, known bool, requested, started *time.Time, blocked bool) api.EventExecutionReconciliation {
	out := api.EventExecutionReconciliation{AppID: app, SubscriptionID: sub, RequestedAt: cloneEventReceiptTime(requested), UnattributedExecutionPresent: blocked, Status: "not_requested"}
	if requested != nil {
		out.Status = "waiting_for_lineage"
	}
	if known {
		out.Status = "not_required"
		if requested != nil {
			out.Status = "completed"
		}
		out.CollectionStartedAt = cloneEventReceiptTime(started)
	}
	return out
}

func (s *PgStore) EventExecutionReconciliation(ctx context.Context, account, app, sub string, request bool) (api.EventExecutionReconciliation, error) {
	var out api.EventExecutionReconciliation
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	_, err = q.EventConsumerExecutionTarget(ctx, tx, sqlc.EventConsumerExecutionTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if request {
		err = q.RequestEventExecutionReconciliation(ctx, tx, sqlc.RequestEventExecutionReconciliationParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
		if err != nil {
			return out, err
		}
	}
	row, err := q.GetEventExecutionReconciliation(ctx, tx, sqlc.GetEventExecutionReconciliationParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		out = reconciliationStatus(app, sub, false, nil, nil, false)
	} else if err != nil {
		return out, err
	} else {
		out = reconciliationStatus(app, sub, row.LedgerBaselineKnown, timestamptzToTimePtr(row.ReconciliationRequestedAt), timestamptzToTimePtr(row.LedgerStartedAt), row.UnattributedExecutionPresent)
		progress, decodeErr := decodeLineageRepairProgress(row.LineageRepairProgress)
		if decodeErr != nil {
			return out, decodeErr
		}
		applyLineageRepairProgress(&out, progress)
	}
	if err = tx.Commit(ctx); err != nil {
		return api.EventExecutionReconciliation{}, err
	}
	return out, nil
}
func (s *PgStore) AdvanceEventExecutionReconciliations(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > api.EventExecutionReconciliationBatch {
		limit = api.EventExecutionReconciliationBatch
	}
	if err := s.repairEventExecutionLineage(ctx, limit); err != nil {
		return 0, err
	}
	n, err := sqlc.New().AdvanceEventExecutionReconciliations(ctx, s.pool, int32(limit))
	return int(n), err
}

// Unknown ownership cannot be repaired by trusting delivery headers. Any retained
// unmapped execution in this app conservatively blocks forward reconciliation.
func (m *MemStore) unattributedExecutionLocked(account, app string) bool {
	for _, inv := range m.invocations {
		if !sameMemUUID(inv.AccountID, account) || !sameMemUUID(inv.AppID, app) {
			continue
		}
		if (inv.ReplayRootInvocationID == "") != (inv.ReplayRootCreatedAt == nil) {
			return true
		}
		root, ok := m.eventExecutionRoots[invocationLedgerRoot(inv)]
		if !ok || !sameMemUUID(root.account, account) || !sameMemUUID(root.app, app) {
			return true
		}
	}
	return false
}
func (m *MemStore) EventExecutionReconciliation(ctx context.Context, account, app, sub string, request bool) (api.EventExecutionReconciliation, error) {
	var out api.EventExecutionReconciliation
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	id := canonicalMemUUID(sub)
	c := m.eventExecutionCoverage[id]
	if request && !c.ledgerBaseline && c.reconciliationRequested == nil {
		now := time.Now().UTC()
		c.reconciliationRequested = &now
		if c.started.IsZero() {
			c.app = canonicalMemUUID(app)
			c.started = now
			c.ledgerStarted = now
		}
		if m.eventExecutionCoverage == nil {
			m.eventExecutionCoverage = map[string]executionCoverage{}
		}
		m.eventExecutionCoverage[id] = c
	}
	var started *time.Time
	if !c.ledgerStarted.IsZero() {
		started = &c.ledgerStarted
	}
	out = reconciliationStatus(canonicalMemUUID(app), id, c.ledgerBaseline, c.reconciliationRequested, started, m.unattributedExecutionLocked(account, app))
	applyLineageRepairProgress(&out, c.repair)
	return out, nil
}
func (m *MemStore) AdvanceEventExecutionReconciliations(ctx context.Context, limit int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if limit <= 0 || limit > api.EventExecutionReconciliationBatch {
		limit = api.EventExecutionReconciliationBatch
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := []string{}
	for id, c := range m.eventExecutionCoverage {
		if c.reconciliationRequested != nil && !c.ledgerBaseline {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.eventExecutionCoverage[ids[i]], m.eventExecutionCoverage[ids[j]]
		x, y := a.reconciliationRequested, b.reconciliationRequested
		if a.repair.LastScan != nil {
			x = a.repair.LastScan
		}
		if b.repair.LastScan != nil {
			y = b.repair.LastScan
		}
		if !x.Equal(*y) {
			return x.Before(*y)
		}
		return ids[i] < ids[j]
	})
	n := 0
	remaining := api.EventExecutionLineageRepairBatch
	checked := 0
	for _, id := range ids {

		c := m.eventExecutionCoverage[id]
		app, ok := m.apps[c.app]
		if !ok || app.Status == AppDeleted {
			continue
		}
		if remaining > 0 && checked < limit {
			checked++
			remaining -= m.repairEventExecutionLineageLocked(app.AccountID, c.app, id, remaining)
			c = m.eventExecutionCoverage[id]
		}
		if m.unattributedExecutionLocked(app.AccountID, c.app) {
			continue
		}
		boundary := time.Now().UTC()
		if c.ledgerStarted.After(boundary) {
			boundary = c.ledgerStarted
		}
		if c.reconciliationRequested.After(boundary) {
			boundary = *c.reconciliationRequested
		}
		if c.ledgerLostThrough != nil && !boundary.After(*c.ledgerLostThrough) {
			boundary = c.ledgerLostThrough.Add(time.Microsecond)
		}
		c.ledgerStarted = boundary
		c.ledgerBaseline = true
		m.eventExecutionCoverage[id] = c
		n++
		if n == limit {
			break
		}
	}
	return n, nil
}

var _ EventExecutionReconciliationStore = (*PgStore)(nil)
var _ EventExecutionReconciliationStore = (*MemStore)(nil)
