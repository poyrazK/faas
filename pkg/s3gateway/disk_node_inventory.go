package s3gateway

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// Private inventory queries are authenticated by the same operator SigV4 key.
// No tenant gateway route translates customer input into this protocol.
func (h *DiskHandler) inventory(w http.ResponseWriter, r *http.Request, bucket, id string) {
	w.Header().Set("Cache-Control", "no-store")
	q := operationQuery(r.URL.Query())
	if r.Method != http.MethodGet || r.ContentLength != 0 || !diskMultipartQuery(q, objectinventory.Query, "cursor", "max-keys") || r.Header.Values(objectstorage.OwnedNodeReceiptHeader) != nil || r.Header.Values(objectstorage.OwnedNodeMultipartReceiptHeader) != nil || r.Header.Get("Range") != "" || r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" || r.Header.Get("If-Unmodified-Since") != "" || r.Header.Get("X-Amz-Checksum-Mode") != "" {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ObjectCapacityInventoryTimeout)
	defer cancel()
	if q.Get(objectinventory.Query) == objectinventory.CountersKind {
		h.bucketCounters(w, r, bucket, id)
		return
	}
	if q.Get(objectinventory.Query) == objectinventory.UsageKind {
		if bucket != "" || q.Has("cursor") || q.Has("max-keys") {
			h.failure(w, r, id, objectnode.ErrInvalid)
			return
		}
		u, err := h.config.Store.Usage(ctx)
		h.inventoryResult(w, r, id, u, err)
		return
	}
	limit, err := listLimit(q, "max-keys")
	if err != nil {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	page, err := h.config.Store.Inventory(ctx, bucket, objectnode.InventoryRequest{Kind: q.Get(objectinventory.Query), Cursor: q.Get("cursor"), Limit: limit})
	h.inventoryResult(w, r, id, page, err)
}

func (h *DiskHandler) bucketCounters(w http.ResponseWriter, r *http.Request, bucket, id string) {
	q := r.URL.Query()
	if !objectinventory.ValidBucket(bucket) || q.Has("cursor") || q.Has("max-keys") {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	value, err := h.config.Store.BucketUsageCounters(r.Context(), bucket)
	h.inventoryResult(w, r, id, value, err)
}

func (h *DiskHandler) inventoryResult(w http.ResponseWriter, r *http.Request, id string, result any, err error) {
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	body, err := json.Marshal(result)
	if err != nil || len(body) > api.ObjectNodeInventoryMaxResponseBytes {
		h.failure(w, r, id, objectnode.ErrCapacity)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
