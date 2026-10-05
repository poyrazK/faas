package s3gateway

import (
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func supportedCopyHeader(r *http.Request, name string) bool {
	if !isCopyRequest(r) {
		return false
	}
	switch strings.ToLower(name) {
	case "x-amz-copy-source-if-match", "x-amz-copy-source-if-none-match", "x-amz-copy-source-if-modified-since", "x-amz-copy-source-if-unmodified-since":
		return true
	case "x-amz-copy-source-range":
		return r.URL.Query().Get("uploadId") != "" && r.URL.Query().Get("partNumber") != ""
	default:
		return false
	}
}

func isCopyRequest(r *http.Request) bool {
	if r.Method != http.MethodPut || r.Header.Get("X-Amz-Copy-Source") == "" {
		return false
	}
	_, _, hasBucket, hasKey, err := parsePath(r.URL.EscapedPath())
	if err != nil || !hasBucket || !hasKey {
		return false
	}
	q := operationQuery(r.URL.Query())
	q.Del("x-id")
	return len(q) == 0 || q.Get("uploadId") != "" && q.Get("partNumber") != "" && queryKeysOnly(q, "uploadId", "partNumber")
}

func copySourceConditions(r *http.Request) (objectstorage.CopySourceConditions, bool) {
	c := objectstorage.CopySourceConditions{IfMatch: r.Header.Get("X-Amz-Copy-Source-If-Match"), IfNoneMatch: r.Header.Get("X-Amz-Copy-Source-If-None-Match")}
	for _, date := range []struct {
		name   string
		target **time.Time
	}{
		{"X-Amz-Copy-Source-If-Modified-Since", &c.IfModifiedSince},
		{"X-Amz-Copy-Source-If-Unmodified-Since", &c.IfUnmodifiedSince},
	} {
		if value, present := r.Header[http.CanonicalHeaderKey(date.name)]; present {
			if len(value) != 1 || len(value[0]) == 0 || len(value[0]) > api.MaxObjectCopyDateHeaderBytes {
				return c, false
			}
			t, err := http.ParseTime(value[0])
			if err != nil {
				return c, false
			}
			*date.target = &t
		}
	}
	return c, c.Valid()
}

func (h *Handler) validCopyBody(w http.ResponseWriter, r *http.Request, req requestContext) bool {
	for name := range r.Header {
		if lower := strings.ToLower(name); strings.HasPrefix(lower, "x-amz-copy-source") && !strings.Contains(";"+req.signature.SignedHeader+";", ";"+lower+";") {
			writeS3Error(w, http.StatusForbidden, "SignatureDoesNotMatch", "Copy source headers must be included in the request signature.", r.URL.Path, req.requestID)
			return false
		}
	}
	if r.ContentLength != 0 || len(r.Header.Values("X-Amz-Copy-Source")) != 1 {
		writeS3Error(w, http.StatusBadRequest, "InvalidRequest", "Copy requests require one source and an empty body.", r.URL.Path, req.requestID)
		return false
	}
	if _, err := readVerifiedRequestBody(w, r, req.signature.PayloadHash, 0); err != nil {
		if !h.writeAWSChunkedError(w, r, req.requestID, err) {
			writeS3Error(w, http.StatusBadRequest, "InvalidRequest", "The copy request body is invalid.", r.URL.Path, req.requestID)
		}
		return false
	}
	return true
}

func gatewayCopyConditions(w http.ResponseWriter, r *http.Request, req requestContext) (objectstorage.CopySourceConditions, bool) {
	c, valid := copySourceConditions(r)
	for _, name := range []string{"X-Amz-Copy-Source-If-Match", "X-Amz-Copy-Source-If-None-Match"} {
		if values, present := r.Header[http.CanonicalHeaderKey(name)]; present && (len(values) != 1 || values[0] == "") {
			valid = false
		}
	}
	if !valid {
		writeS3Error(w, http.StatusBadRequest, "InvalidArgument", "The copy source conditions are invalid.", r.URL.Path, req.requestID)
	}
	return c, valid
}

func (h *Handler) tryTrackedGatewayCopy(w http.ResponseWriter, r *http.Request, req requestContext, copy objectstorage.CopyObjectRequest) bool {
	conditions, valid := gatewayCopyConditions(w, r, req)
	if !valid {
		return true
	}
	copier, capable := req.provider.(objectstorage.TrackedObjectCopier)
	if conditions.HasDates() {
		if _, supports := req.provider.(objectstorage.DateConditionalTrackedObjectCopier); !supports {
			h.unsupported(w, r, req.requestID)
			return true
		}
	}
	if !conditions.Empty() {
		if _, supports := req.provider.(objectstorage.ConditionalTrackedObjectCopier); !supports {
			h.unsupported(w, r, req.requestID)
			return true
		}
	}
	if !capable {
		return false
	}
	h.performTrackedGatewayCopy(w, r, req, copier, copy, conditions)
	return true
}
