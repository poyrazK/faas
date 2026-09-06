package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func multipartLayout(size int64) (int64, int32, error) {
	if size < 1 || size > api.MaxObjectUploadBytes {
		return 0, 0, objectstorage.ErrInvalid
	}
	partSize := api.DefaultMultipartPartBytes
	if needed := (size + api.MaxMultipartParts - 1) / api.MaxMultipartParts; needed > partSize {
		const mib = int64(1 << 20)
		partSize = ((needed + mib - 1) / mib) * mib
	}
	if partSize < api.MinMultipartPartBytes || partSize > api.MaxObjectSinglePutBytes {
		return 0, 0, objectstorage.ErrInvalid
	}
	count := int32((size + partSize - 1) / partSize)
	if count < 1 || count > api.MaxMultipartParts {
		return 0, 0, objectstorage.ErrInvalid
	}
	return partSize, count, nil
}

func multipartPartSize(upload state.ObjectMultipartUpload, part int32) (int64, error) {
	if part < 1 || part > upload.PartCount {
		return 0, objectstorage.ErrInvalid
	}
	offset := int64(part-1) * upload.PartSizeBytes
	return min(upload.PartSizeBytes, upload.SizeBytes-offset), nil
}

func viewMultipartUpload(upload state.ObjectMultipartUpload) api.ObjectMultipartUpload {
	return api.ObjectMultipartUpload{
		ID: upload.ID, Key: upload.Key, SizeBytes: upload.SizeBytes, PartSizeBytes: upload.PartSizeBytes,
		PartCount: upload.PartCount, ContentType: upload.ContentType, State: upload.State,
		ExpiresAt: upload.ExpiresAt, CreatedAt: upload.CreatedAt,
	}
}

func decodeMultipartCompletion(w http.ResponseWriter, r *http.Request, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(out) != nil || d.Decode(&extra) != io.EOF {
		bucketProblem(w, objectstorage.ErrInvalid)
		return false
	}
	return true
}

func validateMultipartParts(parts []api.ObjectMultipartCompletedPart, count int32) error {
	if len(parts) != int(count) {
		return objectstorage.ErrInvalid
	}
	for i, part := range parts {
		if part.PartNumber != int32(i+1) || len(part.ETag) < 1 || len(part.ETag) > 256 || !utf8.ValidString(part.ETag) {
			return objectstorage.ErrInvalid
		}
		for _, c := range part.ETag {
			if c < 32 || c == 127 {
				return objectstorage.ErrInvalid
			}
		}
	}
	return nil
}

func (s *server) multipartUploadStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, state.ObjectMultipartUploadStore, objectstorage.Provider, bool) {
	bucket, _, provider, ok := s.loadBucket(w, r, acct, true)
	if !ok {
		return state.ObjectBucket{}, nil, nil, false
	}
	if !s.authorizeBucketData(w, r, bucket, state.ObjectBucketPermissionWrite) {
		return state.ObjectBucket{}, nil, nil, false
	}
	store, ok := s.store.(state.ObjectMultipartUploadStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return state.ObjectBucket{}, nil, nil, false
	}
	return bucket, store, provider, true
}

func (s *server) loadMultipartUpload(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, state.ObjectMultipartUpload, state.ObjectMultipartUploadStore, objectstorage.Provider, bool) {
	bucket, store, provider, ok := s.multipartUploadStore(w, r, acct)
	if !ok {
		return state.ObjectBucket{}, state.ObjectMultipartUpload{}, nil, nil, false
	}
	id := r.PathValue("upload")
	if _, err := uuid.Parse(id); err != nil {
		bucketProblem(w, state.ErrNotFound)
		return state.ObjectBucket{}, state.ObjectMultipartUpload{}, nil, nil, false
	}
	upload, err := store.GetObjectMultipartUpload(r.Context(), acct.ID, bucket.AppID, bucket.ID, id)
	if err != nil {
		bucketProblem(w, err)
		return state.ObjectBucket{}, state.ObjectMultipartUpload{}, nil, nil, false
	}
	return bucket, upload, store, provider, true
}

func (s *server) createObjectMultipartUpload(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.objectStorageEnabled() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	bucket, store, _, ok := s.multipartUploadStore(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateObjectMultipartUploadRequest
	if !decodeBucketRequest(w, r, &req) {
		return
	}
	if !objectstorage.ValidKey(req.Key) || req.SizeBytes > s.objectStorage.MaxUploadBytes || objectstorage.ValidateContentType(req.ContentType) != nil {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	partSize, partCount, err := multipartLayout(req.SizeBytes)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	// Reserve the full final object before creating billable upstream parts.
	size := req.SizeBytes
	if err = s.admitObjectURL(r.Context(), bucket, objectstorage.SignRequest{Method: http.MethodPut, Key: req.Key, SizeBytes: &size, ContentType: req.ContentType}); err != nil {
		bucketProblem(w, err)
		return
	}
	upload, err := store.ReserveObjectMultipartUpload(r.Context(), state.ObjectMultipartUpload{
		ID: uuid.NewString(), AccountID: acct.ID, AppID: bucket.AppID, BucketID: bucket.ID,
		Key: req.Key, SizeBytes: req.SizeBytes, PartSizeBytes: partSize, PartCount: partCount,
		ContentType: req.ContentType, ExpiresAt: time.Now().UTC().Add(api.ObjectMultipartUploadTTL),
	}, api.MaxActiveMultipartUploadsPerBucket)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	if upload.State == state.ObjectMultipartActive {
		writeJSON(w, http.StatusOK, viewMultipartUpload(upload))
		return
	}
	if upload.State != state.ObjectMultipartInitiating {
		bucketProblem(w, state.ErrConflict)
		return
	}
	upload, err = store.ClaimObjectMultipartUpload(r.Context(), acct.ID, bucket.AppID, bucket.ID, upload.ID, uuid.NewString(), state.ObjectMultipartInitiating, nil, false)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	if err = s.executeObjectMultipartOperation(r.Context(), store, bucket, upload); err != nil {
		bucketProblem(w, err)
		return
	}
	upload, err = store.GetObjectMultipartUpload(r.Context(), acct.ID, bucket.AppID, bucket.ID, upload.ID)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, viewMultipartUpload(upload))
}

func (s *server) listObjectMultipartUploads(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	bucket, store, _, ok := s.multipartUploadStore(w, r, acct)
	if !ok {
		return
	}
	limit := int64(100)
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 32)
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" {
		if _, err = uuid.Parse(cursor); err != nil {
			bucketProblem(w, objectstorage.ErrInvalid)
			return
		}
	}
	if err != nil || limit < 1 || limit > 100 || len(cursor) > 64 {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	rows, next, err := store.ListObjectMultipartUploads(r.Context(), acct.ID, bucket.AppID, bucket.ID, int32(limit), cursor)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	out := api.ObjectMultipartUploadList{Items: make([]api.ObjectMultipartUpload, 0, len(rows)), NextCursor: next}
	for _, row := range rows {
		out.Items = append(out.Items, viewMultipartUpload(row))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getObjectMultipartUpload(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	_, upload, _, _, ok := s.loadMultipartUpload(w, r, acct)
	if ok {
		writeJSON(w, http.StatusOK, viewMultipartUpload(upload))
	}
}

func (s *server) listObjectMultipartParts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	bucket, upload, _, provider, ok := s.loadMultipartUpload(w, r, acct)
	if !ok {
		return
	}
	if upload.State != state.ObjectMultipartActive && upload.State != state.ObjectMultipartCompleting && upload.State != state.ObjectMultipartAborting {
		bucketProblem(w, state.ErrConflict)
		return
	}
	marker := int64(0)
	limit := int64(1000)
	var err error
	if raw := r.URL.Query().Get("part_number_marker"); raw != "" {
		marker, err = strconv.ParseInt(raw, 10, 32)
		if err != nil {
			bucketProblem(w, objectstorage.ErrInvalid)
			return
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 32)
		if err != nil {
			bucketProblem(w, objectstorage.ErrInvalid)
			return
		}
	}
	if marker < 0 || marker > 10000 || limit < 1 || limit > 1000 {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	page, err := provider.ListMultipartParts(r.Context(), bucket.PhysicalName, objectstorage.MultipartListPartsRequest{
		Key: upload.Key, ProviderUploadID: upload.ProviderUploadID, PartNumberMarker: int32(marker), Limit: int32(limit),
	})
	if err != nil {
		bucketProblem(w, err)
		return
	}
	out := api.ObjectMultipartPartList{Items: make([]api.ObjectMultipartPart, 0, len(page.Items)), NextPartNumberMarker: page.NextPartNumberMarker}
	for _, part := range page.Items {
		out.Items = append(out.Items, api.ObjectMultipartPart{PartNumber: part.PartNumber, ETag: part.ETag, SizeBytes: part.SizeBytes, LastModified: part.LastModified})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) signObjectMultipartPart(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.objectStorageEnabled() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	bucket, upload, _, provider, ok := s.loadMultipartUpload(w, r, acct)
	if !ok {
		return
	}
	if upload.State != state.ObjectMultipartActive || !upload.ExpiresAt.After(time.Now()) {
		bucketProblem(w, state.ErrConflict)
		return
	}
	part64, err := strconv.ParseInt(r.PathValue("part"), 10, 32)
	if err != nil {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	part := int32(part64)
	partBytes, err := multipartPartSize(upload, part)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	var req api.ObjectMultipartPartSignRequest
	if !decodeBucketRequest(w, r, &req) {
		return
	}
	if req.ExpiresIn < 0 || req.ExpiresIn > 900 {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if err = s.admitObjectMultipartPartURL(r.Context(), bucket, upload.Key); err != nil {
		bucketProblem(w, err)
		return
	}
	out, err := provider.PresignMultipartPart(r.Context(), bucket.PhysicalName, objectstorage.MultipartPartRequest{
		Key: upload.Key, ProviderUploadID: upload.ProviderUploadID, PartNumber: part, SizeBytes: partBytes, ExpiresIn: req.ExpiresIn,
	})
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) completeObjectMultipartUpload(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	bucket, upload, store, _, ok := s.loadMultipartUpload(w, r, acct)
	if !ok {
		return
	}
	var req api.CompleteObjectMultipartUploadRequest
	if !decodeMultipartCompletion(w, r, &req) {
		return
	}
	if err := validateMultipartParts(req.Parts, upload.PartCount); err != nil {
		bucketProblem(w, err)
		return
	}
	if upload.State == state.ObjectMultipartCompleted {
		writeJSON(w, http.StatusOK, viewMultipartUpload(upload))
		return
	}
	if upload.State == state.ObjectMultipartAborted || upload.State == state.ObjectMultipartAborting {
		bucketProblem(w, state.ErrConflict)
		return
	}
	if upload.State == state.ObjectMultipartCompleting && !slices.Equal(upload.Parts, req.Parts) {
		bucketProblem(w, state.ErrConflict)
		return
	}
	claimed, err := store.ClaimObjectMultipartUpload(r.Context(), acct.ID, bucket.AppID, bucket.ID, upload.ID, uuid.NewString(), state.ObjectMultipartCompleting, req.Parts, false)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	if err = s.executeObjectMultipartOperation(r.Context(), store, bucket, claimed); err != nil {
		bucketProblem(w, err)
		return
	}
	claimed.State = state.ObjectMultipartCompleted
	writeJSON(w, http.StatusOK, viewMultipartUpload(claimed))
}

func (s *server) abortObjectMultipartUpload(w http.ResponseWriter, r *http.Request, acct state.Account) {
	bucket, upload, store, _, ok := s.loadMultipartUpload(w, r, acct)
	if !ok {
		return
	}
	if upload.State == state.ObjectMultipartAborted {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if upload.State == state.ObjectMultipartCompleted || upload.State == state.ObjectMultipartCompleting {
		bucketProblem(w, state.ErrConflict)
		return
	}
	if upload.State == state.ObjectMultipartInitiating {
		claimed, err := store.ClaimObjectMultipartUpload(r.Context(), acct.ID, bucket.AppID, bucket.ID, upload.ID, uuid.NewString(), state.ObjectMultipartInitiating, nil, false)
		if err != nil {
			bucketProblem(w, err)
			return
		}
		if err = s.executeObjectMultipartOperation(r.Context(), store, bucket, claimed); err != nil {
			bucketProblem(w, err)
			return
		}
		upload, err = store.GetObjectMultipartUpload(r.Context(), acct.ID, bucket.AppID, bucket.ID, upload.ID)
		if err != nil {
			bucketProblem(w, err)
			return
		}
	}
	claimed, err := store.ClaimObjectMultipartUpload(r.Context(), acct.ID, bucket.AppID, bucket.ID, upload.ID, uuid.NewString(), state.ObjectMultipartAborting, nil, false)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	if err = s.executeObjectMultipartOperation(r.Context(), store, bucket, claimed); err != nil {
		bucketProblem(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) executeObjectMultipartOperation(ctx context.Context, store state.ObjectMultipartUploadStore, bucket state.ObjectBucket, upload state.ObjectMultipartUpload) error {
	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	backend, err := s.objectStorage.Resolve(bucket.BackendID, bucket.BackendFingerprint)
	if err != nil {
		err = objectstorage.ErrConfiguration
	} else {
		switch upload.State {
		case state.ObjectMultipartInitiating:
			var providerID string
			providerID, err = backend.Provider.EnsureMultipartUpload(callCtx, bucket.PhysicalName, objectstorage.MultipartCreateRequest{
				SessionID: upload.ID, Key: upload.Key, SizeBytes: upload.SizeBytes, ContentType: upload.ContentType,
			})
			if err == nil {
				err = finishObjectMultipartOperation(ctx, func(finishCtx context.Context) error {
					return store.ActivateObjectMultipartUpload(finishCtx, upload.ID, upload.LeaseToken, providerID)
				})
			}
		case state.ObjectMultipartCompleting:
			parts := make([]objectstorage.CompletedPart, 0, len(upload.Parts))
			for _, part := range upload.Parts {
				parts = append(parts, objectstorage.CompletedPart{PartNumber: part.PartNumber, ETag: part.ETag})
			}
			err = backend.Provider.CompleteMultipartUpload(callCtx, bucket.PhysicalName, objectstorage.MultipartCompleteRequest{
				SessionID: upload.ID, Key: upload.Key, ProviderUploadID: upload.ProviderUploadID, SizeBytes: upload.SizeBytes, Parts: parts,
			})
			if err == nil {
				err = finishObjectMultipartOperation(ctx, func(finishCtx context.Context) error {
					return store.FinishObjectMultipartUpload(finishCtx, upload.ID, upload.LeaseToken, state.ObjectMultipartCompleted)
				})
			}
		case state.ObjectMultipartAborting:
			err = backend.Provider.AbortMultipartUpload(callCtx, bucket.PhysicalName, objectstorage.MultipartAbortRequest{Key: upload.Key, ProviderUploadID: upload.ProviderUploadID})
			if err == nil {
				err = finishObjectMultipartOperation(ctx, func(finishCtx context.Context) error {
					return store.FinishObjectMultipartUpload(finishCtx, upload.ID, upload.LeaseToken, state.ObjectMultipartAborted)
				})
			}
		default:
			err = state.ErrConflict
		}
	}
	if err != nil && !errors.Is(err, state.ErrConflict) {
		return s.retryObjectMultipartOperation(ctx, store, upload, err)
	}
	if err == nil {
		var event string
		switch upload.State {
		case state.ObjectMultipartCompleting:
			event = "object_storage.multipart_upload_completed"
		case state.ObjectMultipartAborting:
			event = "object_storage.multipart_upload_aborted"
		default:
			event = "object_storage.multipart_upload_started"
		}
		s.audit.Emit(ctx, event, &upload.AccountID, map[string]any{"app_id": upload.AppID, "bucket_id": upload.BucketID, "upload_id": upload.ID})
	}
	return err
}

func finishObjectMultipartOperation(ctx context.Context, finish func(context.Context) error) error {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return finish(finishCtx)
}

func (s *server) retryObjectMultipartOperation(ctx context.Context, store state.ObjectMultipartUploadStore, upload state.ObjectMultipartUpload, cause error) error {
	code, delay := objectRetryPolicy(cause, upload.AttemptCount)
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := store.RetryObjectMultipartUpload(finishCtx, upload.ID, upload.LeaseToken, code, delay); err != nil {
		return err
	}
	s.log.Warn("object storage multipart operation deferred", "bucket_id", upload.BucketID, "upload_id", upload.ID, "operation", upload.State, "error_code", code, "attempt", upload.AttemptCount, "retry_in", delay, "needs_attention", upload.AttemptCount >= 5 || code == "configuration" || code == "invalid")
	return cause
}
