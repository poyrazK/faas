package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *uploadHandler) performTrackedUpload(w http.ResponseWriter, r *http.Request, st state.ObjectTrackedUploadStore, writer TrackedObjectWriter, bucket state.ObjectBucket, c state.ObjectUploadCompletion) {
	var ok bool
	c, ok = h.beginTrackedUpload(w, r, st, bucket, c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ObjectTransferTimeout)
	result, err := writer.WriteTrackedObject(ctx, bucket.PhysicalName, c.Key, c.ID, io.LimitReader(r.Body, c.Bytes), c.Bytes, ObjectMetadata{ContentType: c.ContentType})
	cancel()
	if err != nil && !errors.Is(err, ErrWriteRejected) || err == nil && !validUploadETag(result.ETag) {
		w.Header().Set("X-Gregale-Upload-ID", c.ID)
		uploadProblem(w, http.StatusBadGateway, "upload outcome is pending provider confirmation; retry with the same Idempotency-Key")
		return
	}
	c.Status = "completed"
	c.ETag = result.ETag
	if err != nil {
		c.Status = "failed"
		c.ETag = ""
		c.ErrorCode = "provider_write_rejected"
	}
	done, finishErr := h.finishTrackedUpload(r.Context(), st, c)
	if finishErr != nil {
		uploadProblem(w, http.StatusServiceUnavailable, "object storage completion is temporarily unavailable")
		return
	}
	if done.Status == "failed" {
		uploadProblem(w, http.StatusBadGateway, "object storage upload failed")
		return
	}
	writeUploadJSON(w, http.StatusCreated, uploadResponse(done))
}
func (h *uploadHandler) beginTrackedUpload(w http.ResponseWriter, r *http.Request, st state.ObjectTrackedUploadStore, bucket state.ObjectBucket, c state.ObjectUploadCompletion) (state.ObjectUploadCompletion, bool) {
	intent, created, err := st.BeginTrackedObjectUpload(r.Context(), c, h.registry.Accounting)
	if err != nil {
		uploadAccountingProblem(w, err)
		return c, false
	}
	if !created {
		h.replayIdempotent(w, intent, c.RequestFingerprint)
		return c, false
	}
	c = intent
	w.Header().Set("X-Gregale-Upload-ID", c.ID)
	if !h.recordUploadAttempt(w, r, bucket.ID) {
		c.Status = "failed"
		c.ErrorCode = "usage_unavailable"
		_, _ = h.finishTrackedUpload(r.Context(), st, c)
		return c, false
	}
	intent, err = st.DispatchTrackedObjectUpload(r.Context(), c.AccountID, c.BucketID, c.ID)
	if err != nil {
		// No provider call was made, even if the DB dispatch acknowledgment was lost.
		c.Status = "failed"
		c.ErrorCode = "dispatch_failed"
		_, _ = h.finishTrackedUpload(r.Context(), st, c)
		uploadProblem(w, http.StatusServiceUnavailable, "upload dispatch is temporarily unavailable")
		return c, false
	}
	c = intent
	return c, true
}

func (h *uploadHandler) finishTrackedUpload(parent context.Context, st state.ObjectTrackedUploadStore, c state.ObjectUploadCompletion) (state.ObjectUploadCompletion, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), api.ObjectUploadSettlementTimeout)
	defer cancel()
	done, err := st.FinishTrackedObjectUpload(ctx, c)
	if err != nil {
		h.log.Warn("object upload settlement deferred", "receipt_id", c.ID)
	}
	return done, err
}
