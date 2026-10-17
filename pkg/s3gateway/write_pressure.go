package s3gateway

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func (h *Handler) checkWritePressure(w http.ResponseWriter, r *http.Request, req requestContext, kind string, size int64) bool {
	checker, ok := req.provider.(objectstorage.ObjectWriteAdmissionChecker)
	if !ok {
		return true
	}
	if err := checker.CheckObjectWriteAdmission(r.Context(), req.bucket.PhysicalName, kind, size); err != nil {
		if errors.Is(err, objectstorage.ErrWritePressure) {
			closeUnreadUploadConnection(w, r)
			w.Header().Set("Retry-After", strconv.Itoa(int(api.ObjectNodeAdmissionRetryAfter.Seconds())))
			writeS3Error(w, http.StatusServiceUnavailable, "SlowDown", "New writes are temporarily throttled by storage pressure.", r.URL.Path, req.requestID)
		} else {
			h.providerError(w, r, req, err, req.signatureKey(r))
		}
		return false
	}
	return true
}
