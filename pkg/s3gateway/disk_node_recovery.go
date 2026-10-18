package s3gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func (h *DiskHandler) recoverySettlement(w http.ResponseWriter, r *http.Request, payload, id string) {
	w.Header().Set("Cache-Control", "no-store")
	q := operationQuery(r.URL.Query())
	if r.Method != http.MethodPost || r.URL.Path != "/" || len(q) != 1 || len(q[objectrecovery.Query]) != 1 || q.Get(objectrecovery.Query) != "settle" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Range") != "" || r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" || r.Header.Get("If-Unmodified-Since") != "" || r.Header.Values(objectstorage.OwnedNodeReceiptHeader) != nil || r.Header.Values(objectstorage.OwnedNodeMultipartReceiptHeader) != nil || r.Header.Values(objectstorage.OwnedNodeAdmissionHeader) != nil {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	if err := h.config.Store.CanMutate(); err != nil {
		h.failure(w, r, id, err)
		return
	}
	body, err := readVerifiedRequestBody(w, r, payload, api.ObjectNodeRecoveryRequestMaxBytes)
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	var a objectrecovery.Settlement
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&a) != nil || decoder.Decode(new(any)) != io.EOF || !canonicalSettlement(body, a) {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	result, err := h.config.Store.AcknowledgeRecovery(a)
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func canonicalSettlement(body []byte, a objectrecovery.Settlement) bool {
	canonical, err := json.Marshal(a)
	var compact bytes.Buffer
	return err == nil && json.Compact(&compact, body) == nil && bytes.Equal(compact.Bytes(), canonical)
}
