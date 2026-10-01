package s3gateway

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Preflights cannot carry SigV4 credentials. Origin configuration is a browser
// policy; all subsequent data requests still require bucket-scoped SigV4 auth.
func (h *Handler) handleCORS(w http.ResponseWriter, r *http.Request, id string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	w.Header().Add("Vary", "Origin")
	allowed := false
	for _, b := range h.registry.Backends() {
		for _, candidate := range b.AllowedOrigins {
			if candidate == origin {
				allowed = true
			}
		}
	}
	if !allowed {
		writeS3Error(w, http.StatusForbidden, "AccessDenied", "The request origin is not allowed.", r.URL.Path, id)
		return true
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Expose-Headers", "ETag, X-Gregale-Upload-ID, Content-Length, Content-Range, Accept-Ranges, x-amz-request-id, x-amz-checksum-crc32, x-amz-checksum-crc32c, x-amz-checksum-crc64nvme, x-amz-checksum-sha1, x-amz-checksum-sha256, x-amz-checksum-type")
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
func allowedCORSHeader(name string) bool {
	switch name {
	case "authorization", "content-type", "content-md5", "cache-control", "content-disposition", "content-encoding", "content-language", "range", "if-match", "if-none-match", "if-modified-since", "if-unmodified-since":
		return true
	}
	return strings.HasPrefix(name, "x-amz-")
}
