package object

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) createControlMultipart(ctx context.Context, b state.ObjectBucket, store state.ObjectMultipartUploadStore, req api.CreateObjectMultipartUploadRequest) (state.ObjectMultipartUpload, int, error) {
	u, err := s.reserveControlMultipart(ctx, b, store, req)
	if err != nil {
		return u, 0, err
	}
	if u.State == state.ObjectMultipartActive {
		return u, http.StatusOK, nil
	}
	if u.State != state.ObjectMultipartInitiating {
		return u, 0, state.ErrConflict
	}
	u, err = store.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, uuid.NewString(), state.ObjectMultipartInitiating, nil, false)
	if err != nil {
		return u, 0, err
	}
	if err = s.ExecuteObjectMultipartOperation(ctx, store, b, u); err != nil {
		return u, 0, err
	}
	u, err = store.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	return u, http.StatusCreated, err
}

func (s *Service) reserveControlMultipart(ctx context.Context, b state.ObjectBucket, store state.ObjectMultipartUploadStore, req api.CreateObjectMultipartUploadRequest) (state.ObjectMultipartUpload, error) {
	if !objectstorage.ValidKey(req.Key) || req.SizeBytes > s.Host.ObjectStorage().MaxUploadBytes || objectstorage.ValidateContentType(req.ContentType) != nil || req.Encryption != nil && (!req.Encryption.Valid() || req.Encryption.Empty()) {
		return state.ObjectMultipartUpload{}, objectstorage.ErrInvalid
	}
	partSize, partCount, err := MultipartLayoutWithLimit(req.SizeBytes, s.Host.ObjectStorage().MaxPartBytes)
	if err != nil {
		return state.ObjectMultipartUpload{}, err
	}
	protection, err := s.resolveObjectWriteProtection(ctx, b, req.Protection)
	if err != nil {
		return state.ObjectMultipartUpload{}, err
	}
	encryption, err := s.resolveObjectURLEncryption(b, objectstorage.SignRequest{Encryption: req.Encryption})
	if err != nil {
		return state.ObjectMultipartUpload{}, err
	}
	admitted, ok := store.(state.ObjectFixedMultipartAdmissionStore)
	if !ok {
		return state.ObjectMultipartUpload{}, objectstorage.ErrUnsupported
	}
	// Session, captured enrollment and declared object capacity commit together
	// before a native initialization can be attempted.
	enroll, err := s.objectNodeSettlementEnrollment(b)
	if err != nil {
		return state.ObjectMultipartUpload{}, err
	}
	return admitted.ReserveAdmittedObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: objectstorage.NewWriteReceiptID(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: req.Key, SizeBytes: req.SizeBytes, PartSizeBytes: partSize, PartCount: partCount, ContentType: req.ContentType, QueueNodeSettlement: enroll, Protection: protection, Encryption: encryption, ExpiresAt: time.Now().UTC().Add(api.ObjectMultipartUploadTTL)}, api.MaxActiveMultipartUploadsPerBucket, s.Host.ObjectStorage().Accounting)
}
