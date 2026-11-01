package s3gateway

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// writeMeteredS3XML sends a listing the way writeGatewayRead sends an object
// body: it counts against the account's monthly egress and is paced to the
// gateway's share of its node's port (ADR-20261031122757 §3, §6). The body
// is encoded first, so gateway-safety accounting reserves its exact length
// before the first byte leaves; other modes record the bytes delivered.
func (h *Handler) writeMeteredS3XML(w http.ResponseWriter, r *http.Request, req requestContext, value any) {
	var body bytes.Buffer
	body.WriteString(xml.Header)
	if err := xml.NewEncoder(&body).Encode(value); err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, "")
		return
	}
	safety := h.registry.Accounting.GatewaySafety()
	if safety && !h.writeAdmissionError(w, r, req, h.reserveResponseEgress(r, req, int64(body.Len()))) {
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Length", strconv.Itoa(body.Len()))
	w.Header().Set("x-amz-request-id", req.requestID)
	w.WriteHeader(http.StatusOK)
	n, _ := io.Copy(w, h.transfers.Reader(r.Context(), &body, objectstorage.TransferDownload))
	if !safety {
		if err := objectstorage.RecordDeliveredEgress(r.Context(), h.requestMetrics, req.bucket.ID, n, h.now().UTC()); err != nil {
			h.log.Warn("S3 egress metric write failed", "request_id", req.requestID)
		}
	}
}

// reserveResponseEgress reserves a response body of size bytes against the
// bucket's egress ceiling: from the cached read decision when one admitted
// the request, otherwise in the database, like a locking-admitted GET.
func (h *Handler) reserveResponseEgress(r *http.Request, req requestContext, size int64) error {
	if req.read != nil {
		return h.readAdmitter.ReserveEgress(*req.read, size)
	}
	store, ok := h.requestMetrics.(state.ObjectStorageGatewayEgressStore)
	if !ok {
		return state.ErrObjectUsageStale
	}
	return store.ReserveObjectStorageGatewayEgress(r.Context(), req.bucket.ID, size, h.now().UTC(), h.registry.Accounting)
}
