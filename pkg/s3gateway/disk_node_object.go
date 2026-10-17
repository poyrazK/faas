package s3gateway

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func (h *DiskHandler) object(w http.ResponseWriter, r *http.Request, bucket, key, payload, id string) {
	q := operationQuery(r.URL.Query())
	receipt := r.Header.Get(objectstorage.OwnedNodeReceiptHeader)
	multipartReceipt := r.Header.Get(objectstorage.OwnedNodeMultipartReceiptHeader)
	if receipt != "" || multipartReceipt != "" {
		if r.Method != http.MethodHead || len(q) != 0 || r.Header.Get("Range") != "" || r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" || r.Header.Get("If-Unmodified-Since") != "" || r.Header.Get("X-Amz-Checksum-Mode") != "" {
			h.unsupported(w, r, id)
			return
		}
		if multipartReceipt != "" {
			h.multipartReceipt(w, r, bucket, key, multipartReceipt, id)
		} else {
			h.receipt(w, r, bucket, key, receipt, id)
		}
		return
	}
	if q.Has("uploads") || q.Has("uploadId") {
		h.multipart(w, r, bucket, key, payload, id)
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if !queryKeysOnly(q, "response-content-type", "response-content-disposition", "response-cache-control", "response-content-encoding", "response-content-language", "response-expires") || headerOrQueryValue(r, "X-Amz-Checksum-Mode") != "" && headerOrQueryValue(r, "X-Amz-Checksum-Mode") != "ENABLED" {
			h.unsupported(w, r, id)
			return
		}
		h.read(w, r, bucket, key, id)
		return
	}
	if len(q) != 0 {
		h.unsupported(w, r, id)
		return
	}
	switch r.Method {
	case http.MethodPut:
		h.put(w, r, bucket, key, payload, id)
	case http.MethodDelete:
		if r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "" {
			h.unsupported(w, r, id)
			return
		}
		if err := h.config.Store.Delete(bucket, key); err != nil {
			h.failure(w, r, id, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		h.unsupported(w, r, id)
	}
}

func (h *DiskHandler) read(w http.ResponseWriter, r *http.Request, bucket, key, id string) {
	o, file, err := h.config.Store.Get(bucket, key)
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	defer func() { _ = file.Close() }()
	for name, values := range o.Headers {
		w.Header()[name] = append([]string(nil), values...)
	}
	w.Header().Set("ETag", o.ETag)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	if o.ChecksumAlgorithm != "" && headerOrQueryValue(r, "X-Amz-Checksum-Mode") == "ENABLED" {
		w.Header().Set("X-Amz-Checksum-"+o.ChecksumAlgorithm, o.Checksum)
		if o.ChecksumType != "" {
			w.Header().Set("X-Amz-Checksum-Type", o.ChecksumType)
		}
	}
	for query, header := range map[string]string{"response-content-type": "Content-Type", "response-content-disposition": "Content-Disposition", "response-cache-control": "Cache-Control", "response-content-encoding": "Content-Encoding", "response-content-language": "Content-Language", "response-expires": "Expires"} {
		if value := r.URL.Query().Get(query); value != "" {
			if !validDiskHeader(value) {
				h.failure(w, r, id, objectnode.ErrInvalid)
				return
			}
			w.Header().Set(header, value)
		}
	}
	http.ServeContent(w, r, key, o.Modified, file)
}

func (h *DiskHandler) put(w http.ResponseWriter, r *http.Request, bucket, key, payload, id string) {
	if r.ContentLength < 0 {
		// Some SDKs frame even a known-empty PUT as HTTP chunked. Accept only
		// the empty case; nonempty uploads require an explicit bounded length.
		var probe [1]byte
		if n, err := r.Body.Read(probe[:]); n != 0 || err != io.EOF {
			writeS3Error(w, 411, "MissingContentLength", "A nonempty upload requires Content-Length.", r.URL.Path, id)
			return
		}
		r.ContentLength = 0
	}
	if r.ContentLength > h.config.MaxObjectBytes {
		writeS3Error(w, 413, "EntityTooLarge", "The upload exceeds this node's single-PUT limit.", r.URL.Path, id)
		return
	}
	options, err := diskPutOptions(r, payload)
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	if !h.reserve(r.ContentLength) {
		writeS3Error(w, 503, "SlowDown", "The node has no upload slot or disk reservation available.", r.URL.Path, id)
		return
	}
	defer h.release(r.ContentLength)
	o, err := h.config.Store.Put(r.Context(), bucket, key, r.Body, r.ContentLength, options)
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	w.Header().Set("ETag", o.ETag)
	if o.ChecksumAlgorithm != "" {
		w.Header().Set("X-Amz-Checksum-"+o.ChecksumAlgorithm, o.Checksum)
	}
	w.WriteHeader(http.StatusOK)
}

func diskPutOptions(r *http.Request, payload string) (objectnode.PutOptions, error) {
	o := objectnode.PutOptions{Headers: make(http.Header), IfMatch: r.Header.Get("If-Match"), IfNoneMatch: r.Header.Get("If-None-Match"), ContentMD5: r.Header.Get("Content-MD5")}
	if payload != "UNSIGNED-PAYLOAD" {
		o.PayloadSHA256 = payload
	}
	if o.ContentMD5 != "" {
		b, err := base64.StdEncoding.DecodeString(o.ContentMD5)
		if err != nil || len(b) != 16 {
			return o, objectnode.ErrInvalid
		}
	}
	for _, values := range []map[string][]string{r.Header, r.URL.Query()} {
		for name := range values {
			if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") {
				value := headerOrQueryValue(r, name)
				if !validDiskHeader(value) {
					return o, objectnode.ErrInvalid
				}
				o.Headers.Set(name, value)
			}
		}
	}
	for _, name := range []string{"Content-Type", "Cache-Control", "Content-Disposition", "Content-Encoding", "Content-Language", "Expires"} {
		if value := r.Header.Get(name); value != "" {
			if !validDiskHeader(value) {
				return o, objectnode.ErrInvalid
			}
			o.Headers.Set(name, value)
		}
	}
	o.Receipt = o.Headers.Get(objectnode.ReceiptMetadataHeader)
	return diskChecksumOptions(r, o)
}

func diskChecksumOptions(r *http.Request, o objectnode.PutOptions) (objectnode.PutOptions, error) {
	for _, algorithm := range []string{"CRC32", "CRC32C", "CRC64NVME", "SHA1", "SHA256"} {
		if value := headerOrQueryValue(r, "X-Amz-Checksum-"+algorithm); value != "" {
			if o.ChecksumAlgorithm != "" || !objectchecksum.ValidPart(algorithm, value) {
				return o, objectnode.ErrInvalid
			}
			o.ChecksumAlgorithm, o.Checksum = algorithm, value
		}
	}
	if declared := headerOrQueryValue(r, "X-Amz-Sdk-Checksum-Algorithm"); declared != "" && declared != o.ChecksumAlgorithm {
		return o, objectnode.ErrInvalid
	}
	return o, nil
}

func validDiskHeader(value string) bool {
	return len(value) <= api.MaxObjectURLRequestBytes && utf8.ValidString(value) && !strings.ContainsFunc(value, func(c rune) bool { return c < 32 && c != '\t' || c == 127 })
}
