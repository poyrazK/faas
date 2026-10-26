package s3gateway

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Preflights cannot carry SigV4 credentials. Origin configuration is a browser
// policy; all subsequent data requests still require bucket-scoped SigV4 auth.
// Operator origins (the console) are allowed on every bucket. Any other origin
// needs a rule of the bucket's own CORS configuration (ADR-20261025202805),
// which an actual request meets once its bucket is known (applyBucketCORS).
func (h *Handler) handleCORS(w http.ResponseWriter, r *http.Request, id string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	w.Header().Add("Vary", "Origin")
	if !h.operatorOrigin(origin) {
		if r.Method == http.MethodOptions {
			return h.bucketCORSPreflight(w, r, origin, id)
		}
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Expose-Headers", "ETag, X-Amz-Server-Side-Encryption, X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id, X-Amz-Server-Side-Encryption-Bucket-Key-Enabled, X-Gregale-Upload-ID, X-Amz-Version-Id, X-Amz-Copy-Source-Version-Id, X-Amz-Delete-Marker, Last-Modified, Content-Length, Content-Range, Accept-Ranges, x-amz-request-id, x-amz-checksum-crc32, x-amz-checksum-crc32c, x-amz-checksum-crc64nvme, x-amz-checksum-sha1, x-amz-checksum-sha256, x-amz-checksum-type")
	if r.Method != http.MethodOptions {
		return false
	}
	return h.corsPreflight(w, r, id)
}

func (h *Handler) corsPreflight(w http.ResponseWriter, r *http.Request, id string) bool {
	method := r.Header.Get("Access-Control-Request-Method")
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPost, http.MethodDelete:
	default:
		writeS3Error(w, http.StatusForbidden, "AccessDenied", "The requested method is not allowed.", r.URL.Path, id)
		return true
	}
	headers := r.Header.Get("Access-Control-Request-Headers")
	for _, name := range strings.Split(headers, ",") {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if !allowedCORSHeader(strings.ToLower(strings.TrimSpace(name))) {
			writeS3Error(w, http.StatusForbidden, "AccessDenied", "A requested header is not allowed.", r.URL.Path, id)
			return true
		}
	}
	w.Header().Add("Vary", "Access-Control-Request-Method")
	w.Header().Add("Vary", "Access-Control-Request-Headers")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, PUT, POST, DELETE")
	if headers != "" {
		w.Header().Set("Access-Control-Allow-Headers", headers)
	}
	w.Header().Set("Access-Control-Max-Age", strconv.Itoa(api.ObjectS3CORSMaxAgeSeconds))
	w.WriteHeader(http.StatusNoContent)
	return true
}
func (h *Handler) operatorOrigin(origin string) bool {
	for _, b := range h.registry.Backends() {
		for _, candidate := range b.AllowedOrigins {
			if candidate == origin {
				return true
			}
		}
	}
	return false
}

// bucketCORSPreflight answers a preflight from a customer origin with the
// first matching rule of the bucket the URL names: through its upload grant
// or its presigned access key. A header-signed request names no bucket a
// preflight can see, so only operator origins reach it.
func (h *Handler) bucketCORSPreflight(w http.ResponseWriter, r *http.Request, origin, id string) bool {
	method := r.Header.Get("Access-Control-Request-Method")
	headers := r.Header.Get("Access-Control-Request-Headers")
	var requested []string
	for _, name := range strings.Split(headers, ",") {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			requested = append(requested, name)
		}
	}
	bucket, ok := h.preflightBucket(r)
	rule, matched := api.ObjectBucketCORSRule{}, false
	if ok {
		rule, matched = h.bucketCORSRule(r, bucket, origin, method, requested)
	}
	if !matched {
		writeS3Error(w, http.StatusForbidden, "AccessDenied", "The request origin is not allowed.", r.URL.Path, id)
		return true
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Add("Vary", "Access-Control-Request-Method")
	w.Header().Add("Vary", "Access-Control-Request-Headers")
	w.Header().Set("Access-Control-Allow-Methods", strings.Join(rule.AllowedMethods, ", "))
	if headers != "" {
		w.Header().Set("Access-Control-Allow-Headers", headers)
	}
	if rule.MaxAgeSeconds > 0 {
		w.Header().Set("Access-Control-Max-Age", strconv.Itoa(rule.MaxAgeSeconds))
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

// preflightBucket finds the bucket a preflight's URL is for, without any
// signature: the bucket of its upload grant or presigned access key, when the
// path names that bucket.
func (h *Handler) preflightBucket(r *http.Request) (state.ObjectBucket, bool) {
	query := r.URL.Query()
	var bucket state.ObjectBucket
	switch token := query.Get(UploadGrantQueryParameter); {
	case token != "":
		grants, ok := h.store.(state.ObjectUploadGrantStore)
		if !ok || len(token) != 43 {
			return bucket, false
		}
		g, err := grants.ResolveObjectUploadGrant(r.Context(), UploadGrantTokenHash(token))
		if err != nil {
			return bucket, false
		}
		bucket = g.Bucket
	case query.Get("X-Amz-Credential") != "":
		key, _, _ := strings.Cut(query.Get("X-Amz-Credential"), "/")
		_, b, err := h.store.ResolveObjectS3Credential(r.Context(), key)
		if key == "" || err != nil {
			return bucket, false
		}
		bucket = b
	default:
		return bucket, false
	}
	name, _, hasBucket, _, err := parsePath(r.URL.EscapedPath())
	return bucket, err == nil && hasBucket && name == bucket.Name
}

func (h *Handler) bucketCORSRule(r *http.Request, bucket state.ObjectBucket, origin, method string, requested []string) (api.ObjectBucketCORSRule, bool) {
	reader, ok := h.store.(state.ObjectBucketCORSReader)
	if !ok {
		return api.ObjectBucketCORSRule{}, false
	}
	rules, err := reader.ObjectBucketCORSRules(r.Context(), bucket.ID)
	if err != nil {
		h.log.Warn("bucket CORS rules unavailable", "bucket_id", bucket.ID)
		return api.ObjectBucketCORSRule{}, false
	}
	return api.MatchObjectBucketCORS(rules, origin, method, requested)
}

// applyBucketCORS decides an actual request from a customer origin once its
// bucket is known. Operator origins were answered before authentication.
func (h *Handler) applyBucketCORS(w http.ResponseWriter, r *http.Request, bucket state.ObjectBucket, id string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || w.Header().Get("Access-Control-Allow-Origin") != "" {
		return true
	}
	rule, ok := h.bucketCORSRule(r, bucket, origin, r.Method, nil)
	if !ok {
		writeS3Error(w, http.StatusForbidden, "AccessDenied", "The request origin is not allowed.", r.URL.Path, id)
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	if len(rule.ExposeHeaders) > 0 {
		w.Header().Set("Access-Control-Expose-Headers", strings.Join(rule.ExposeHeaders, ", "))
	}
	return true
}

func allowedCORSHeader(name string) bool {
	switch name {
	case "authorization", "content-type", "content-md5", "cache-control", "content-disposition", "content-encoding", "content-language", "range", "if-match", "if-none-match", "if-modified-since", "if-unmodified-since":
		return true
	}
	return strings.HasPrefix(name, "x-amz-")
}
