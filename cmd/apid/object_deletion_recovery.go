package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) reconcileObjectDeletions(ctx context.Context, observe func(string, string)) error {
	st, ok := s.store.(state.ObjectDeletionStore)
	if !ok {
		return nil
	}
	buckets, ok := s.store.(state.ObjectBucketStore)
	if !ok || s.objectStorage == nil {
		return nil
	}
	rows, e := st.DueObjectDeletions(ctx, api.ObjectDeletionBatch)
	if e != nil {
		return e
	}
	for _, row := range rows {
		if e = ctx.Err(); e != nil {
			return e
		}
		b, e := buckets.GetObjectBucket(ctx, row.AccountID, row.AppID, row.BucketID)
		if e != nil {
			return e
		}
		backend, e := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
		if e != nil {
			// Claim and durably defer missing placements so a stuck batch
			// cannot monopolize recovery. Prepared rows need no provider.
			backend.Provider = nil
		}
		j, e := s.deletionService(b, backend.Provider).Recover(ctx, b, row.ID)
		if errors.Is(e, state.ErrConflict) || errors.Is(e, state.ErrNotFound) {
			continue
		}
		outcome := j.State
		if e != nil {
			outcome = "deferred"
		}
		if observe != nil {
			observe("deletion", outcome)
		}
		if e == nil && j.State == "completed" {
			s.audit.Emit(ctx, "object_storage.deletion_completed", &b.AccountID, map[string]any{"bucket_id": b.ID, "deletion_id": j.ID, "delete_marker": j.DeleteMarker})
		}
	}
	return nil
}
