package object

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) configureObjectUploadRoute(ctx context.Context, b state.ObjectBucket, store state.ObjectUploadRouteStore, req api.CreateObjectUploadRouteRequest) (state.ObjectUploadRoute, int, error) {
	req.Name = strings.TrimSpace(strings.ToLower(req.Name))
	req.KeyPrefix = strings.Trim(strings.TrimSpace(req.KeyPrefix), "/")
	if !validObjectUploadRouteName(req.Name) || !validObjectUploadPrefix(req.KeyPrefix) || req.Encryption != nil && (!req.Encryption.Valid() || req.Encryption.Empty()) {
		return state.ObjectUploadRoute{}, 0, objectstorage.ErrInvalid
	}
	limit := s.Host.ObjectStorage().MaxUploadBytes
	if req.Encryption != nil {
		limit = min(limit, s.Host.ObjectStorage().MaxSinglePutBytes)
	}
	if req.MaxBytes == 0 {
		req.MaxBytes = limit
	}
	if req.MaxBytes < 1 || req.MaxBytes > limit || len(req.AllowedContentTypes) > 32 {
		return state.ObjectUploadRoute{}, 0, objectstorage.ErrInvalid
	}
	for i, value := range req.AllowedContentTypes {
		if !validUploadContentType(value) {
			return state.ObjectUploadRoute{}, 0, objectstorage.ErrInvalid
		}
		req.AllowedContentTypes[i] = strings.ToLower(strings.TrimSpace(value))
		// A bucket served on the app's hostname never takes an active file
		// from the app's users (ADR-20261104221012); the edge refuses the
		// upload, so naming the type would only promise what it refuses.
		if objectstorage.PublicServed(b) && objectstorage.ActiveContentType(req.AllowedContentTypes[i]) {
			return state.ObjectUploadRoute{}, 0, objectstorage.ErrInvalid
		}
	}
	encryption, err := s.resolveObjectURLEncryption(b, objectstorage.SignRequest{Encryption: req.Encryption})
	if err != nil {
		return state.ObjectUploadRoute{}, 0, err
	}
	if !encryption.Empty() {
		backend, e := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
		if e != nil {
			return state.ObjectUploadRoute{}, 0, e
		}
		if _, ok := backend.Provider.(objectstorage.TrackedObjectWriter); !ok {
			return state.ObjectUploadRoute{}, 0, objectstorage.ErrUnsupported
		}
	}
	status := http.StatusOK
	route, err := store.GetObjectUploadRoute(ctx, b.AccountID, b.AppID, req.Name)
	if errors.Is(err, state.ErrNotFound) {
		route = state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, Name: req.Name}
		status = http.StatusCreated
	} else if err != nil {
		return route, 0, err
	}
	route.BucketID, route.KeyPrefix, route.MaxBytes = b.ID, req.KeyPrefix, req.MaxBytes
	route.AllowedContentTypes = append([]string(nil), req.AllowedContentTypes...)
	route.Enabled = req.Enabled == nil || *req.Enabled
	route.Encryption = encryption
	route, err = store.UpsertObjectUploadRoute(ctx, route)
	return route, status, err
}
