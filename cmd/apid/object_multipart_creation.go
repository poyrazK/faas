package main

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createControlMultipart(ctx context.Context, b state.ObjectBucket, store state.ObjectMultipartUploadStore, req api.CreateObjectMultipartUploadRequest) (state.ObjectMultipartUpload, int, error) {
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
	if err = s.executeObjectMultipartOperation(ctx, store, b, u); err != nil {
		return u, 0, err
	}
	u, err = store.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	return u, http.StatusCreated, err
}

func (s *server) reserveControlMultipart(ctx context.Context, b state.ObjectBucket, store state.ObjectMultipartUploadStore, req api.CreateObjectMultipartUploadRequest) (state.ObjectMultipartUpload, error) {
	if !objectstorage.ValidKey(req.Key) || req.SizeBytes > s.objectStorage.MaxUploadBytes || objectstorage.ValidateContentType(req.ContentType) != nil || req.Encryption != nil && (!req.Encryption.Valid() || req.Encryption.Empty()) {
		return state.ObjectMultipartUpload{}, objectstorage.ErrInvalid
	}
	partSize, partCount, err := multipartLayoutWithLimit(req.SizeBytes, s.objectStorage.MaxPartBytes)
	if err != nil {
		return state.ObjectMultipartUpload{}, err
	}
	encryption, err := s.resolveObjectURLEncryption(b, objectstorage.SignRequest{Encryption: req.Encryption})
	if err != nil {
		return state.ObjectMultipartUpload{}, err
	}
	// Capture enrollment before creating any billable native parts. The full
	// fixed object remains reserved through the existing admission ledger.
	size := req.SizeBytes
	if err = s.admitObjectURL(ctx, b, objectstorage.SignRequest{Method: http.MethodPut, Key: req.Key, SizeBytes: &size, ContentType: req.ContentType}); err != nil {
		return state.ObjectMultipartUpload{}, err
	}
	return store.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: req.Key, SizeBytes: req.SizeBytes, PartSizeBytes: partSize, PartCount: partCount, ContentType: req.ContentType, Encryption: encryption, ExpiresAt: time.Now().UTC().Add(api.ObjectMultipartUploadTTL)}, api.MaxActiveMultipartUploadsPerBucket)
}
