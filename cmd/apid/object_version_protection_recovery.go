package main

import (
	"context"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) reconcileObjectVersionProtection(ctx context.Context, observe func(string, string)) error {
	st, ok := s.store.(state.ObjectVersionProtectionStore)
	buckets, owned := s.store.(state.ObjectBucketStore)
	if !ok || !owned {
		return nil
	}
	rows, err := st.DueObjectVersionProtection(ctx, api.ObjectVersionProtectionBatch)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		b, e := buckets.GetObjectBucket(ctx, row.AccountID, row.AppID, row.BucketID)
		if protectionRecoveryIgnored(e) {
			continue
		}
		if e != nil {
			return e
		}
		if s.objectStorage == nil {
			if e = s.deferVersionProtection(ctx, st, row); e != nil {
				return e
			}
			continue
		}
		backend, e := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
		if e != nil {
			if e = s.deferVersionProtection(ctx, st, row); e != nil {
				return e
			}
			continue
		}
		j, e := s.versionProtectionService(b, backend.Provider).Reconcile(ctx, b, row.ID)
		if protectionRecoveryIgnored(e) {
			continue
		}
		outcome := j.State
		if e != nil {
			outcome = "deferred"
		}
		if observe != nil {
			observe("version_protection", outcome)
		}
		if e == nil && j.State == "ready" {
			s.audit.Emit(ctx, "object_storage.version_protection_ready", &b.AccountID, map[string]any{"bucket_id": b.ID, "operation_id": j.ID, "kind": j.Kind})
		}
	}
	return nil
}
func (s *server) deferVersionProtection(ctx context.Context, st state.ObjectVersionProtectionStore, j state.ObjectVersionProtection) error {
	j, err := st.ClaimObjectVersionProtection(ctx, j.ID, uuid.NewString())
	if protectionRecoveryIgnored(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return st.RetryObjectVersionProtection(ctx, j.ID, j.Token, "provider_unsupported")
}
