package s3gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func (h *DiskHandler) replicationEndpoint(w http.ResponseWriter, r *http.Request, payload, id string) {
	w.Header().Set("Cache-Control", "no-store")
	q := operationQuery(r.URL.Query())
	if r.URL.Path != "/" || r.Header.Get("Range") != "" || r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" || r.Header.Get("If-Unmodified-Since") != "" || r.Header.Values(objectstorage.OwnedNodeReceiptHeader) != nil || r.Header.Values(objectstorage.OwnedNodeMultipartReceiptHeader) != nil || r.Header.Values(objectstorage.OwnedNodeAdmissionHeader) != nil {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	if r.Method == http.MethodPost && (q.Get(objectnode.ReplicationQuery) == "confirm" || q.Get(objectnode.ReplicationQuery) == "cursor") {
		h.replicationConfirm(w, r, payload, id)
		return
	}
	if r.ContentLength != 0 {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	if r.Method == http.MethodPost && q.Get(objectnode.ReplicationQuery) == "fence" {
		h.replicationFence(w, r, payload, id)
		return
	}
	if r.Method != http.MethodGet {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	h.replicationRead(w, r, id)
}

func (h *DiskHandler) replicationConfirm(w http.ResponseWriter, r *http.Request, payload, id string) {
	if payload == "UNSIGNED-PAYLOAD" || r.Header.Get("Content-Type") != "application/json" || r.ContentLength < 1 || r.ContentLength > api.ObjectNodeReplicaAckMaxBytes || !diskMultipartQuery(operationQuery(r.URL.Query()), objectnode.ReplicationQuery) {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	body, err := readVerifiedRequestBody(w, r, payload, api.ObjectNodeReplicaAckMaxBytes)
	if err != nil {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	var request any = &objectnode.ReplicaConfirmation{}
	if r.URL.Query().Get(objectnode.ReplicationQuery) == "cursor" {
		request = &objectnode.FollowerReport{}
	}
	if json.Unmarshal(body, request) != nil {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	canonical, err := json.Marshal(request)
	if err != nil || !bytes.Equal(body, canonical) {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	h.replicationReport(w, r, id, request)
}

func (h *DiskHandler) replicationReport(w http.ResponseWriter, r *http.Request, id string, request any) {
	switch report := request.(type) {
	case *objectnode.FollowerReport:
		result, err := h.config.Store.ReportFollower(r.Context(), *report)
		h.inventoryResult(w, r, id, result, err)
	case *objectnode.ReplicaConfirmation:
		result, err := h.config.Store.ConfirmReplica(r.Context(), *report)
		h.inventoryResult(w, r, id, result, err)
	}
}

func (h *DiskHandler) replicationRead(w http.ResponseWriter, r *http.Request, id string) {
	q := operationQuery(r.URL.Query())
	switch q.Get(objectnode.ReplicationQuery) {
	case "status":
		if !diskMultipartQuery(q, objectnode.ReplicationQuery) {
			h.failure(w, r, id, objectnode.ErrInvalid)
			return
		}
		w.Header().Set(objectnode.ReplicationBatchHeader, "1")
		status, err := h.config.Store.ReplicationStatus()
		h.inventoryResult(w, r, id, status, err)
	case "wait":
		h.replicationWait(w, r, id)
	case "entry":
		sequence, err := replicationNumber(q.Get("sequence"), 1, api.ObjectNodeReplicationMaxSequence)
		if err != nil || !diskMultipartQuery(q, objectnode.ReplicationQuery, "stream", "sequence") {
			h.failure(w, r, id, objectnode.ErrInvalid)
			return
		}
		entry, err := h.config.Store.ReplicationEntry(q.Get("stream"), sequence)
		h.inventoryResult(w, r, id, entry, err)
	case "entries":
		h.replicationEntries(w, r, id)
	case "blob":
		if !diskMultipartQuery(q, objectnode.ReplicationQuery, "stream", "sha256", "offset", "length") {
			h.failure(w, r, id, objectnode.ErrInvalid)
			return
		}
		h.replicationBlob(w, r, id)
	default:
		h.failure(w, r, id, objectnode.ErrInvalid)
	}
}

func (h *DiskHandler) replicationEntries(w http.ResponseWriter, r *http.Request, id string) {
	q := operationQuery(r.URL.Query())
	sequence, err := replicationNumber(q.Get("sequence"), 1, api.ObjectNodeReplicationMaxSequence)
	limit, limitErr := replicationNumber(q.Get("limit"), 1, api.ObjectNodeReplicationBatch)
	if err != nil || limitErr != nil || !diskMultipartQuery(q, objectnode.ReplicationQuery, "stream", "sequence", "limit") {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	select {
	case h.metrics.replication.entryBatches <- struct{}{}:
		defer func() { <-h.metrics.replication.entryBatches }()
	default:
		h.failure(w, r, id, objectnode.ErrCapacity)
		return
	}
	entries, err := h.config.Store.ReplicationEntries(r.Context(), q.Get("stream"), sequence, int(limit))
	h.inventoryResult(w, r, id, entries, err)
}

func (h *DiskHandler) replicationFence(w http.ResponseWriter, r *http.Request, payload, id string) {
	q := operationQuery(r.URL.Query())
	epoch, err := replicationNumber(q.Get("epoch"), 1, api.ObjectNodeReplicationMaxEpoch)
	if err != nil || !diskMultipartQuery(q, objectnode.ReplicationQuery, "stream", "primary", "target", "epoch") {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	if _, err = readVerifiedRequestBody(w, r, payload, 0); err != nil {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	handoff, err := h.config.Store.FencePrimary(r.Context(), objectnode.FenceRequest{StreamID: q.Get("stream"), PrimaryID: q.Get("primary"), TargetID: q.Get("target"), Epoch: epoch})
	h.inventoryResult(w, r, id, handoff, err)
}

func replicationNumber(raw string, minimum, maximum int64) (int64, error) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < minimum || n > maximum || strconv.FormatInt(n, 10) != raw {
		return 0, objectnode.ErrInvalid
	}
	return n, nil
}

func (h *DiskHandler) replicationBlob(w http.ResponseWriter, r *http.Request, id string) {
	q := r.URL.Query()
	offset, err := replicationNumber(q.Get("offset"), 0, api.MaxObjectUploadBytes)
	length, lengthErr := replicationNumber(q.Get("length"), 1, api.ObjectNodeReplicationChunkBytes)
	if err != nil || lengthErr != nil {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	release := h.replicationTransfer(w, r, id)
	if release == nil {
		return
	}
	defer release()
	f, err := h.config.Store.OpenReplicationBlob(q.Get("stream"), q.Get("sha256"))
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || offset > info.Size() || length > info.Size()-offset {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		h.failure(w, r, id, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.Header().Set("X-Gregale-Replication-Sha256", q.Get("sha256"))
	w.Header().Set("X-Gregale-Replication-Offset", q.Get("offset"))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, io.LimitReader(f, length))
}
