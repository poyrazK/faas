package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ReadObjectMultipartInitiation(ctx context.Context, u ObjectMultipartUpload) (ObjectMultipartInitiation, error) {
	if !validMultipartInitiation(u) {
		return ObjectMultipartInitiation{}, ErrConflict
	}
	receipt, err := s.ReadObjectMultipartMutation(ctx, u)
	if err != nil {
		return ObjectMultipartInitiation{}, err
	}
	d, err := sqlc.New().ObjectMultipartInitiationRead(ctx, s.pool, mustPgUUID(u.ID))
	if err != nil {
		// A missing dispatch record for a bound receipt is never legacy admission.
		return ObjectMultipartInitiation{}, multipartMutationAuthorityError(err)
	}
	return ObjectMultipartInitiation{Receipt: receipt, Dispatched: d.Dispatched, DispatchToken: d.DispatchToken, ProviderUploadID: d.ProviderUploadID}, nil
}

func (s *PgStore) DispatchObjectMultipartInitiation(ctx context.Context, u ObjectMultipartUpload) error {
	d, err := s.ReadObjectMultipartInitiation(ctx, u)
	if err != nil {
		return err
	}
	if d.Dispatched {
		return ErrConflict
	}
	n, err := sqlc.New().ObjectMultipartInitiationDispatch(ctx, s.pool, sqlc.ObjectMultipartInitiationDispatchParams{MultipartUploadID: mustPgUUID(u.ID), DispatchToken: u.LeaseToken})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ObserveObjectMultipartInitiation(ctx context.Context, u ObjectMultipartUpload, id string) error {
	d, err := s.ReadObjectMultipartInitiation(ctx, u)
	if err != nil {
		return err
	}
	if !validMultipartInitiationResult(id) || !d.Dispatched || d.DispatchToken != u.LeaseToken || d.ProviderUploadID != "" && d.ProviderUploadID != id {
		return ErrConflict
	}
	if d.ProviderUploadID == id {
		return nil
	}
	n, err := sqlc.New().ObjectMultipartInitiationObserve(ctx, s.pool, sqlc.ObjectMultipartInitiationObserveParams{MultipartUploadID: mustPgUUID(u.ID), DispatchToken: u.LeaseToken, ProviderUploadID: id})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
