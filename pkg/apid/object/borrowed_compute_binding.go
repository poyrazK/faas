package object

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Borrowed compute bindings (ADR-20261121170000 amendment 4): a route split's
// app holds its own key on the source app's bucket, with the source binding's
// permission, variable prefix and key scope, so the route reads the same
// <PREFIX>_* variables it read before. Only a split makes one; the bucket API
// still binds the bucket's owner alone.

// HeldComputeBindings lists the active compute bindings app holds, on its own
// buckets and on buckets it borrowed.
func (s *Service) HeldComputeBindings(ctx context.Context, app state.App) ([]state.HeldObjectS3ComputeBinding, error) {
	store, ok := s.Host.Store().(state.ObjectS3HeldBindingStore)
	if !ok {
		return nil, nil
	}
	return store.ObjectS3ComputeBindingsHeldBy(ctx, app.AccountID, app.ID)
}

// BorrowComputeBinding gives holder its own key on the bucket of from, with
// from's permission, variable prefix and key scope.
func (s *Service) BorrowComputeBinding(ctx context.Context, acct state.Account, holder state.App, from state.HeldObjectS3ComputeBinding) (state.ObjectS3Credential, *api.Problem) {
	source := from.Credential
	buckets, ok := s.Host.Store().(state.ObjectBucketStore)
	if !ok || !s.ObjectStorageEnabled() {
		return state.ObjectS3Credential{}, api.ErrCapacity("object storage is unavailable")
	}
	bucket, err := buckets.GetObjectBucket(ctx, holder.AccountID, from.BucketAppID, source.BucketID)
	if err != nil {
		return state.ObjectS3Credential{}, borrowProblem(source.ManagedPrefix, source.BucketID, err)
	}
	if bucket.State != "ready" {
		return state.ObjectS3Credential{}, borrowProblem(source.ManagedPrefix, bucket.Name, state.ErrConflict)
	}
	if bucket.AppID == holder.ID {
		return state.ObjectS3Credential{}, api.ErrValidation("an app cannot borrow a binding on its own bucket")
	}
	credential, prob, err := s.newObjectStorageComputeBinding(ctx, acct, holder, bucket, api.CreateObjectStorageComputeBindingRequest{
		Label: source.Label, Permission: source.Permission, Prefix: source.ManagedPrefix, KeyPrefix: source.KeyPrefix,
	}, "")
	if prob != nil {
		return state.ObjectS3Credential{}, prob
	}
	if err != nil {
		return state.ObjectS3Credential{}, borrowProblem(source.ManagedPrefix, bucket.Name, err)
	}
	s.publishObjectStorageComputeBinding(ctx, acct, holder, bucket, credential)
	return credential, nil
}

func borrowProblem(prefix, bucket string, err error) *api.Problem {
	detail := fmt.Sprintf("could not bind bucket %s (variables %s_*)", bucket, prefix)
	switch {
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Conflict", detail+": the bucket is not ready or has no room for another key")
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", detail+": the bucket is gone")
	}
	return api.ErrCapacity(detail)
}

// BorrowedComputeBindings are the bindings holder holds on other apps'
// buckets.
func (s *Service) BorrowedComputeBindings(ctx context.Context, holder state.App) ([]state.HeldObjectS3ComputeBinding, error) {
	held, err := s.HeldComputeBindings(ctx, holder)
	if err != nil {
		return nil, err
	}
	var borrowed []state.HeldObjectS3ComputeBinding
	for _, binding := range held {
		if binding.BucketAppID != holder.ID {
			borrowed = append(borrowed, binding)
		}
	}
	return borrowed, nil
}

// RevokeBorrowedComputeBindings revokes every binding holder borrowed on
// another app's bucket; the bindings on its own buckets stay.
func (s *Service) RevokeBorrowedComputeBindings(ctx context.Context, acct state.Account, holder state.App) error {
	borrowed, err := s.BorrowedComputeBindings(ctx, holder)
	if err != nil || len(borrowed) == 0 {
		return err
	}
	store, ok := s.Host.Store().(state.ObjectS3CredentialBindingStore)
	if !ok {
		return errors.New("object storage bindings are unavailable")
	}
	for _, binding := range borrowed {
		c := binding.Credential
		changed, err := store.RevokeObjectS3ComputeBinding(ctx, holder.AccountID, c.BucketID, c.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("revoke borrowed binding %s: %w", c.ID, err)
		}
		if changed {
			s.Host.NotifyRuntimeConfigChange(ctx, db.NotifySecretRotated, acct, holder, "binding_revoked", c.ManagedScope, "")
		}
		s.emitRevocation(ctx, "object_storage.compute_binding_revoked", acct.ID,
			map[string]any{"app_id": holder.ID, "bucket_id": c.BucketID, "binding_id": c.ID, "borrowed": true})
	}
	return nil
}
