package s3gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// A single provider attempt is journaled before dispatch. Only its final
// response or a known pre-dispatch failure settles it; expiry is insufficient.
func (h *Handler) beginTrackedWrite(w http.ResponseWriter, r *http.Request, req requestContext, key string, size int64) (func(context.Context), bool) {
	st, ok := h.store.(state.ObjectCapacityStore)
	if !ok {
		return func(context.Context) {}, h.admit(w, r, req, key, size, true)
	}
	token := uuid.NewString()
	err := st.BeginObjectWrite(r.Context(), req.bucket.AccountID, req.bucket.ID, token, key, size, h.registry.Accounting)
	if !h.writeAdmissionError(w, r, req, err) {
		return func(context.Context) {}, false
	}
	return func(parent context.Context) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancel()
		if err := st.SettleObjectWrite(ctx, req.bucket.AccountID, req.bucket.ID, token); err != nil {
			h.log.Warn("S3 write settlement deferred", "bucket_id", req.bucket.ID, "request_id", req.requestID)
		}
	}, true
}
