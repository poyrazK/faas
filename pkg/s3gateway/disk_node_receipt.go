package s3gateway

import (
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func (h *DiskHandler) receipt(w http.ResponseWriter, r *http.Request, bucket, key, receipt, id string) {
	w.Header().Set("Cache-Control", "no-store")
	proof, err := h.config.Store.Receipt(bucket, key, receipt)
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	w.Header().Set(objectstorage.OwnedNodeReceiptStatusHeader, proof.Status)
	w.Header().Set(objectnode.ReceiptMetadataHeader, proof.ID)
	w.Header().Set("ETag", proof.Object.ETag)
	w.Header().Set("Content-Length", strconv.FormatInt(proof.Object.Size, 10))
	w.Header().Set("Last-Modified", proof.Object.Modified.UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
}

func (h *DiskHandler) multipartReceipt(w http.ResponseWriter, r *http.Request, bucket, key, receipt, id string) {
	w.Header().Set("Cache-Control", "no-store")
	proof, err := h.config.Store.MultipartReceipt(bucket, key, receipt)
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	w.Header().Set(objectstorage.OwnedNodeReceiptStatusHeader, proof.Status)
	w.Header().Set(objectstorage.OwnedNodeMultipartManifestHeader, proof.ManifestHash)
	w.Header().Set(objectnode.MultipartMetadataHeader, proof.ID)
	w.Header().Set("ETag", proof.Object.ETag)
	w.Header().Set("Content-Length", strconv.FormatInt(proof.Object.Size, 10))
	w.Header().Set("Last-Modified", proof.Object.Modified.UTC().Format(http.TimeFormat))
	writeChecksumHeaders(w.Header(), objectchecksum.Digest{Policy: objectchecksum.Policy{Algorithm: proof.Object.ChecksumAlgorithm, Type: proof.Object.ChecksumType}, Value: proof.Object.Checksum}, true)
	w.WriteHeader(http.StatusOK)
}
