package s3gateway

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

func diskMultipartQuery(q url.Values, allowed ...string) bool {
	if !queryKeysOnly(q, allowed...) {
		return false
	}
	for _, values := range q {
		if len(values) != 1 {
			return false
		}
	}
	return true
}

func (h *DiskHandler) multipart(w http.ResponseWriter, r *http.Request, bucket, key, payload, requestID string) {
	q := operationQuery(r.URL.Query())
	if q.Has("uploads") && r.Method == http.MethodPost && diskMultipartQuery(q, "uploads") {
		h.createMultipart(w, r, bucket, key, payload, requestID)
		return
	}
	if !q.Has("uploadId") || q.Get("uploadId") == "" {
		h.unsupported(w, r, requestID)
		return
	}
	uploadID := q.Get("uploadId")
	switch {
	case r.Method == http.MethodPut && diskMultipartQuery(q, "uploadId", "partNumber") && q.Has("partNumber"):
		h.uploadPart(w, r, bucket, key, uploadID, payload, requestID)
	case r.Method == http.MethodGet && diskMultipartQuery(q, "uploadId", "part-number-marker", "max-parts"):
		h.listParts(w, r, bucket, key, uploadID, requestID)
	case r.Method == http.MethodPost && diskMultipartQuery(q, "uploadId"):
		h.completeMultipart(w, r, bucket, key, uploadID, payload, requestID)
	case r.Method == http.MethodDelete && diskMultipartQuery(q, "uploadId"):
		if r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "" {
			h.unsupported(w, r, requestID)
			return
		}
		if _, err := readVerifiedRequestBody(w, r, payload, 0); err != nil {
			h.failure(w, r, requestID, objectnode.ErrInvalid)
			return
		}
		if err := h.config.Store.AbortMultipart(bucket, key, uploadID); err != nil {
			h.failure(w, r, requestID, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		h.unsupported(w, r, requestID)
	}
}

func (h *DiskHandler) createMultipart(w http.ResponseWriter, r *http.Request, bucket, key, payload, id string) {
	if _, err := readVerifiedRequestBody(w, r, payload, 0); err != nil {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	policy, err := objectchecksum.NewPolicy(headerOrQueryValue(r, "X-Amz-Checksum-Algorithm"), headerOrQueryValue(r, "X-Amz-Checksum-Type"))
	if err != nil {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	options, err := diskPutOptions(r, "UNSIGNED-PAYLOAD")
	if err != nil || options.IfMatch != "" || options.IfNoneMatch != "" || options.ChecksumAlgorithm != "" {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	u, err := h.config.Store.CreateMultipart(bucket, key, objectnode.MultipartOptions{Headers: options.Headers, Checksum: policy, MaxBytes: h.config.MaxMultipartBytes})
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	if !policy.Empty() {
		w.Header().Set("X-Amz-Checksum-Algorithm", policy.Algorithm)
		w.Header().Set("X-Amz-Checksum-Type", policy.Type)
	}
	writeS3XML(w, http.StatusOK, id, initiateMultipartResult{XMLNS: s3XMLNamespace, Bucket: bucket, Key: key, UploadID: u.ID})
}

func (h *DiskHandler) uploadPart(w http.ResponseWriter, r *http.Request, bucket, key, uploadID, payload, id string) {
	number, err := strconv.ParseInt(r.URL.Query().Get("partNumber"), 10, 32)
	if err != nil || number < 1 || number > api.MaxMultipartParts || r.ContentLength < 0 {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	if r.ContentLength > h.config.MaxPartBytes {
		writeS3Error(w, 413, "EntityTooLarge", "The part exceeds this node's part limit.", r.URL.Path, id)
		return
	}
	options, err := diskPutOptions(r, payload)
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	for name := range options.Headers {
		if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") {
			h.failure(w, r, id, objectnode.ErrInvalid)
			return
		}
	}
	options.Headers = nil
	if !h.reserve(r.ContentLength) {
		h.failure(w, r, id, objectnode.ErrCapacity)
		return
	}
	defer h.release(r.ContentLength)
	part, err := h.config.Store.UploadPart(r.Context(), bucket, key, uploadID, int32(number), r.Body, r.ContentLength, options)
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	w.Header().Set("ETag", part.Object.ETag)
	if part.Object.ChecksumAlgorithm != "" {
		w.Header().Set("X-Amz-Checksum-"+part.Object.ChecksumAlgorithm, part.Object.Checksum)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *DiskHandler) completeMultipart(w http.ResponseWriter, r *http.Request, bucket, key, uploadID, payload, id string) {
	u, err := h.config.Store.MultipartInfo(bucket, key, uploadID)
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	request, err := diskCompletionRequest(w, r, payload, u.Checksum)
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	o, err := h.config.Store.CompleteMultipart(r.Context(), bucket, key, uploadID, request, func(size int64) (func(), error) {
		if !h.reserveBytes(size, api.MaxObjectUploadBytes) {
			return nil, objectnode.ErrCapacity
		}
		return func() { h.release(size) }, nil
	})
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	digest := objectchecksum.Digest{Policy: objectchecksum.Policy{Algorithm: o.ChecksumAlgorithm, Type: o.ChecksumType}, Value: o.Checksum}
	writeChecksumHeaders(w.Header(), digest, true)
	w.Header().Set("ETag", o.ETag)
	writeS3XML(w, http.StatusOK, id, completeMultipartResult{XMLNS: s3XMLNamespace, Bucket: bucket, Key: key, ETag: o.ETag, checksumXML: checksumXMLFor(digest), ChecksumType: digest.Type})
}

func diskCompletionRequest(w http.ResponseWriter, r *http.Request, payload string, policy objectchecksum.Policy) (objectnode.MultipartCompletion, error) {
	request := objectnode.MultipartCompletion{IfMatch: r.Header.Get("If-Match"), IfNoneMatch: r.Header.Get("If-None-Match")}
	clone := r.Clone(r.Context())
	for _, name := range awsChecksumHeaderNames {
		clone.Header.Del(name)
	}
	body, err := readVerifiedRequestBody(w, clone, payload, api.MaxObjectMultipartCheckpointBytes)
	if err != nil {
		return request, objectnode.ErrDigest
	}
	var input completeMultipartUploadRequest
	decoder := xml.NewDecoder(bytes.NewReader(body)) // #nosec G709 -- Bounded fixed S3 XML schema, no external entity resolution.
	if err = decoder.Decode(&input); err != nil || len(input.Parts) < 1 || len(input.Parts) > api.MaxMultipartParts {
		return request, objectnode.ErrInvalid
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return request, objectnode.ErrInvalid
	}
	for _, part := range input.Parts {
		value, err := part.selected(policy)
		if err != nil {
			return request, objectnode.ErrInvalidPart
		}
		request.Parts = append(request.Parts, objectmultipart.Part{PartNumber: part.PartNumber, ETag: part.ETag, Checksum: value})
	}
	if kind := r.Header.Get("X-Amz-Checksum-Type"); kind != "" && kind != policy.Type {
		return request, objectnode.ErrInvalid
	}
	for _, algorithm := range []string{"CRC32", "CRC32C", "CRC64NVME", "SHA1", "SHA256"} {
		if value := r.Header.Get("X-Amz-Checksum-" + algorithm); value != "" {
			if algorithm != policy.Algorithm || !request.Checksum.Empty() {
				return request, objectnode.ErrInvalid
			}
			request.Checksum = objectchecksum.Digest{Policy: policy, Value: value}
		}
	}
	if size := r.Header.Get("X-Amz-Mp-Object-Size"); size != "" {
		n, err := strconv.ParseInt(size, 10, 64)
		if err != nil || n < 0 || n > api.MaxObjectUploadBytes {
			return request, objectnode.ErrInvalid
		}
		request.ExpectedSize = &n
	}
	return request, nil
}
