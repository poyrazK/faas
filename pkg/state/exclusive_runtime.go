package state

import (
	"cmp"
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"slices"
	"time"
)

func ExclusiveIncarnation(i Instance) string { return i.ID + "/" + i.WakeID + "/" + i.NodeID }

func (s *MemStore) exclusiveTimeLocked() time.Time {
	if s.exclusiveNow != nil {
		return s.exclusiveNow().UTC()
	}
	return time.Now().UTC()
}

func (s *MemStore) hasExclusiveOwnerLocked(id string) bool {
	i := s.instances[id]
	now := s.exclusiveTimeLocked()
	for _, o := range s.exclusiveOperations {
		if o.State == "running" && o.IncarnationID == ExclusiveIncarnation(i) && o.LeaseExpiresAt != nil && o.LeaseExpiresAt.After(now) && o.AttemptDeadline != nil && o.AttemptDeadline.After(now) {
			return true
		}
	}
	return false
}

func (s *MemStore) BeginExclusiveSnapshot(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.instances[id]; !ok {
		return ErrNotFound
	}
	if s.exclusiveCaptures[id] || s.hasExclusiveOwnerLocked(id) {
		return exclusivework.ErrBusy
	}
	if s.exclusiveCaptures == nil {
		s.exclusiveCaptures = map[string]bool{}
	}
	s.exclusiveCaptures[id] = true
	return nil
}

func (s *MemStore) EndExclusiveSnapshot(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.exclusiveCaptures, id)
	return nil
}

// Called before committing any instance mutation while holding the store mutex.
// The PostgreSQL trigger enforces the same guard and revocation for every writer.
func (s *MemStore) exclusiveRuntimeTransitionLocked(old, next Instance) error {
	if old.State != next.State && (next.State == "snapshotting" || next.State == "parked") && s.hasExclusiveOwnerLocked(old.ID) {
		return exclusivework.ErrBusy
	}
	if old.WakeID == next.WakeID && old.NodeID == next.NodeID && (old.State == next.State || next.State == "running") {
		return nil
	}
	for id, op := range s.exclusiveOperations {
		if op.State != "running" || op.IncarnationID != ExclusiveIncarnation(old) {
			continue
		}
		op.State, op.LastError, op.DueAt = "pending", "runtime incarnation revoked", s.exclusiveTimeLocked()
		if op.QuotaReserved {
			r := s.accountAsyncQuota[op.AccountID]
			if r.CurrentInflight > 0 {
				r.CurrentInflight--
			}
			s.accountAsyncQuota[op.AccountID] = r
			op.QuotaReserved = false
		}
		clearExclusiveClaim(&op)
		s.exclusiveOperations[id] = op
	}
	return nil
}

func (s *PgStore) BeginExclusiveSnapshot(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	i, err := q.LockExclusiveSnapshotInstance(ctx, tx, id)
	if err != nil {
		return exclusiveSQLError(err)
	}
	if i.ExclusiveCaptureBlocked {
		return exclusivework.ErrBusy
	}
	busy, err := q.HasExclusiveSnapshotOwner(ctx, tx, id)
	if err != nil {
		return err
	}
	if busy {
		return exclusivework.ErrBusy
	}
	if err = q.SetExclusiveCaptureBarrier(ctx, tx, sqlc.SetExclusiveCaptureBarrierParams{InstanceID: id, Blocked: true}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) EndExclusiveSnapshot(ctx context.Context, id string) error {
	return sqlc.New().SetExclusiveCaptureBarrier(ctx, s.pool, sqlc.SetExclusiveCaptureBarrierParams{InstanceID: id, Blocked: false})
}

func (s *PgStore) ListDueExclusiveOperations(ctx context.Context, limit int) ([]ExclusiveOperation, error) {
	limit = min(max(limit, 1), api.MaxExclusiveInspectionRows)
	rows, err := sqlc.New().ListDueExclusiveWork(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]ExclusiveOperation, 0, len(rows))
	for _, r := range rows {
		o, err := exclusivePGOperation(r)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

func (s *MemStore) ListDueExclusiveOperations(ctx context.Context, limit int) ([]ExclusiveOperation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	heads := map[string]ExclusiveOperation{}
	for _, o := range s.exclusiveOperations {
		if o.State != "pending" && o.State != "running" {
			continue
		}
		if s.accounts[o.AccountID].Status != AccountActive {
			continue
		}
		if o.PlatformTenantID != "" && s.platformTenants[o.PlatformTenantID].Status != PlatformTenantActive {
			continue
		}
		if h, ok := heads[o.KeyID]; !ok || o.Sequence < h.Sequence {
			heads[o.KeyID] = o
		}
	}
	out := []ExclusiveOperation{}
	now := s.exclusiveTimeLocked()
	for _, o := range heads {
		if (o.State == "pending" && !o.DueAt.After(now)) || (o.State == "running" && o.LeaseExpiresAt != nil && !o.LeaseExpiresAt.After(now)) {
			out = append(out, cloneExclusiveOperation(o))
		}
	}
	slices.SortFunc(out, func(a, b ExclusiveOperation) int {
		if c := a.DueAt.Compare(b.DueAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	ranks := map[string]int{}
	counts := map[string]int{}
	for _, o := range out {
		counts[o.AccountID]++
		ranks[o.ID] = counts[o.AccountID]
	}
	slices.SortStableFunc(out, func(a, b ExclusiveOperation) int { return cmp.Compare(ranks[a.ID], ranks[b.ID]) })
	limit = min(max(limit, 1), api.MaxExclusiveInspectionRows)
	return out[:min(limit, len(out))], nil
}
