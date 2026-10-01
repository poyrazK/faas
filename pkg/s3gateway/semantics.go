package s3gateway

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func writeConditions(r *http.Request) objectstorage.ObjectWriteConditions {
	return objectstorage.ObjectWriteConditions{IfMatch: r.Header.Get("If-Match"), IfNoneMatch: r.Header.Get("If-None-Match")}
}
func presignConditionalPut(ctx context.Context, p objectstorage.Provider, bucket string, r objectstorage.SignRequest, c objectstorage.ObjectWriteConditions) (objectstorage.SignedRequest, error) {
	if c.IfMatch == "" && c.IfNoneMatch == "" {
		return p.Presign(ctx, bucket, r)
	}
	signer, ok := p.(objectstorage.ConditionalObjectPresigner)
	if !ok {
		return objectstorage.SignedRequest{}, objectstorage.ErrUnsupported
	}
	return signer.PresignConditionalPut(ctx, bucket, r, c)
}
func presignChecksumRead(ctx context.Context, p objectstorage.Provider, bucket, method, key, mode string) (objectstorage.SignedRequest, error) {
	if mode == "ENABLED" {
		if signer, ok := p.(objectstorage.ObjectChecksumReadPresigner); ok {
			return signer.PresignChecksumRead(ctx, bucket, method, key, 60)
		}
	}
	// S3 permits a successful response without a checksum when the object has none.
	return objectstorage.PresignObjectRead(ctx, p, bucket, method, key, 60)
}
func isCleanupRequest(r *http.Request, q url.Values, hasBucket, hasKey bool) bool {
	if !hasBucket {
		return false
	}
	return hasKey && r.Method == http.MethodDelete && (len(q) == 0 || q.Get("uploadId") != "" && queryKeysOnly(q, "uploadId")) || !hasKey && r.Method == http.MethodPost && q.Has("delete") && queryKeysOnly(q, "delete")
}
func (h *Handler) validateRequestSemantics(w http.ResponseWriter, r *http.Request, req requestContext, hasKey bool, q url.Values) bool {
	mode := headerOrQueryValue(r, "x-amz-checksum-mode")
	if mode != "" && (mode != "ENABLED" || !hasKey || (r.Method != http.MethodGet && r.Method != http.MethodHead) || len(q) != 0) {
		h.unsupported(w, r, req.requestID)
		return false
	}
	c := writeConditions(r)
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !c.Empty() {
		if !c.Valid() {
			writeS3Error(w, http.StatusBadRequest, "InvalidArgument", "The conditional write headers are invalid.", r.URL.Path, req.requestID)
			return false
		}
		if hasKey && r.Method == http.MethodPost && q.Get("uploadId") != "" && queryKeysOnly(q, "uploadId") {
			if _, ok := req.provider.(objectstorage.ConditionalMultipartCompleter); ok {
				return true
			}
		} else if hasKey && r.Method == http.MethodPut && len(q) == 0 && r.Header.Get("X-Amz-Copy-Source") == "" {
			if _, ok := req.provider.(objectstorage.ConditionalObjectPresigner); ok {
				return true
			}
		}
		h.unsupported(w, r, req.requestID)
		return false
	}
	return true
}
