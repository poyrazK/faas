package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventExecutionLedgerRetentionStore interface {
	PruneEventExecutionLedger(context.Context, time.Time, int) (int, error)
}

func ledgerPruneLimit(limit int) int {
	if limit <= 0 || limit > api.EventExecutionLedgerPruneBatchMax {
		return api.EventExecutionLedgerPruneBatchDefault
	}
	return limit
}
func (s *PgStore) PruneEventExecutionLedger(ctx context.Context, now time.Time, limit int) (int, error) {
	if now.IsZero() {
		return 0, ErrInvalidArgument
	}
	limit = ledgerPruneLimit(limit)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	n, err := q.PruneEventExecutionOutcomes(ctx, tx, sqlc.PruneEventExecutionOutcomesParams{NowAt: pgtypeFromTime(now), BatchLimit: int32(limit)})
	if err != nil {
		return 0, err
	}
	roots, err := q.PruneEventExecutionRoots(ctx, tx, sqlc.PruneEventExecutionRootsParams{BeforeAt: pgtypeFromTime(now.Add(-api.EventExecutionLedgerRetention)), BatchLimit: int32(limit)})
	if err != nil {
		return 0, err
	}
	cases, err := q.PruneEventExecutionLineageCases(ctx, tx, sqlc.PruneEventExecutionLineageCasesParams{NowAt: pgtypeFromTime(now), BatchLimit: int32(limit)})
	if err != nil {
		return 0, err
	}
	retries, err := q.PruneEventExecutionLineageRetries(ctx, tx, sqlc.PruneEventExecutionLineageRetriesParams{NowAt: pgtypeFromTime(now), BatchLimit: int32(limit)})
	if err != nil {
		return 0, err
	}
	jobs, err := q.PruneEventExecutionLineageRetryJobs(ctx, tx, sqlc.PruneEventExecutionLineageRetryJobsParams{NowAt: pgtypeFromTime(now), BatchLimit: int32(limit)})
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int(n + roots + cases + retries + jobs), nil
}
func (m *MemStore) PruneEventExecutionLedger(ctx context.Context, now time.Time, limit int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if now.IsZero() {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	limit = ledgerPruneLimit(limit)
	casePruned := m.pruneLineageCasesLocked(now, limit)
	jobPruned := 0
	for sub, c := range m.eventExecutionCoverage {
		for id, job := range c.jobs {
			if jobPruned < limit && !job.RetainUntil.After(now) {
				delete(c.jobs, id)
				delete(c.controlReceipts, id)
				jobPruned++
			}
		}
		m.eventExecutionCoverage[sub] = c
	}
	casePruned += jobPruned
	keys := []executionLedgerOutcomeKey{}
	for k, o := range m.eventExecutionOutcomes {
		if !o.retainUntil.After(now) {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := m.eventExecutionOutcomes[keys[i]], m.eventExecutionOutcomes[keys[j]]
		if !a.retainUntil.Equal(b.retainUntil) {
			return a.retainUntil.Before(b.retainUntil)
		}
		x, y := keys[i], keys[j]
		if x.id != y.id {
			return x.id < y.id
		}
		if x.created != y.created {
			return x.created < y.created
		}
		if x.generation != y.generation {
			return x.generation < y.generation
		}
		if x.attempt != y.attempt {
			return x.attempt < y.attempt
		}
		return x.kind < y.kind
	})
	keys = keys[:min(len(keys), limit)]
	for _, k := range keys {
		o := m.eventExecutionOutcomes[k]
		m.markEventExecutionLedgerLossLocked(o.root.subscription, o.finished)
		delete(m.eventExecutionOutcomes, k)
	}
	roots := []executionLedgerRootKey{}
	for k, r := range m.eventExecutionRoots {
		if !r.created.After(now.Add(-api.EventExecutionLedgerRetention)) && !m.eventExecutionRootHeldLocked(r) {
			roots = append(roots, k)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].created != roots[j].created {
			return roots[i].created < roots[j].created
		}
		return roots[i].id < roots[j].id
	})
	roots = roots[:min(len(roots), limit)]
	for _, k := range roots {
		delete(m.eventExecutionRoots, k)
	}
	return len(keys) + len(roots) + casePruned, nil
}
func (m *MemStore) eventExecutionRootHeldLocked(root executionLedgerRoot) bool {
	for _, o := range m.eventExecutionOutcomes {
		if o.root.key == root.key {
			return true
		}
	}
	for _, inv := range m.invocations {
		if sameMemUUID(inv.AccountID, root.account) && sameMemUUID(inv.AppID, root.app) && (invocationLedgerRoot(inv) == root.key || sameMemUUID(inv.ReplayedFromInvocationID, root.key.id)) {
			return true
		}
	}
	for _, receipt := range m.eventFanout {
		if receipt.ID == root.outbox {
			return true
		}
	}
	for _, job := range m.eventRecoveryJobs {
		if sameMemUUID(job.AccountID, root.account) && sameMemUUID(job.Job.AppID, root.app) && (job.Job.State == "running" || job.Job.State == "paused" || (job.Job.Selection.Mode == "execution" && job.Job.ExecutionFinishedAt == nil)) {
			return true
		}
	}
	return false
}
func (m *MemStore) deleteEventExecutionLedgerAppLocked(app string) {
	for k, o := range m.eventExecutionOutcomes {
		if sameMemUUID(o.root.app, app) {
			delete(m.eventExecutionOutcomes, k)
		}
	}
	for k, r := range m.eventExecutionRoots {
		if sameMemUUID(r.app, app) {
			delete(m.eventExecutionRoots, k)
		}
	}
	for id, c := range m.eventExecutionCoverage {
		if sameMemUUID(c.app, app) {
			delete(m.eventExecutionCoverage, id)
		}
	}
}

var _ EventExecutionLedgerRetentionStore = (*PgStore)(nil)
var _ EventExecutionLedgerRetentionStore = (*MemStore)(nil)
