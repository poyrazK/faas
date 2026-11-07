package objectstorage

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Some object providers ignore If-Range. Check the selected representation
// before exposing a partial body; a stale or invalid validator needs a full
// read. Admit the second attempt too, and reserve only the body we serve.
func (h *publicReadHandler) readPublicObject(w http.ResponseWriter, r, upstream *http.Request, bucket state.ObjectBucket, key string, policy api.ObjectStoragePolicy, read *AdmittedRead) (*http.Response, *AdmittedRead, bool) {
	response, ok := h.requestPublicObject(w, upstream, bucket, policy, read)
	if !ok || !publicRangeNeedsFull(r, response) {
		return response, read, ok
	}
	_ = response.Body.Close()
	read, ok = h.admit(w, r, bucket, key, policy)
	if !ok {
		return nil, nil, false
	}
	full := upstream.Clone(upstream.Context())
	full.Header.Del("Range")
	full.Header.Del("If-Range")
	response, ok = h.requestPublicObject(w, full, bucket, policy, read)
	if ok && response.StatusCode == http.StatusPartialContent {
		_ = response.Body.Close()
		http.Error(w, "object storage is temporarily unavailable", http.StatusServiceUnavailable)
		return nil, nil, false
	}
	return response, read, ok
}

func publicRangeNeedsFull(r *http.Request, response *http.Response) bool {
	if r.Method != http.MethodGet || r.Header.Get("Range") == "" || len(r.Header.Values("If-Range")) == 0 {
		return false
	}
	if response.StatusCode != http.StatusPartialContent && response.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		return false
	}
	return !publicIfRangeMatches(r.Header.Values("If-Range"), response.Header)
}

func (h *publicReadHandler) requestPublicObject(w http.ResponseWriter, upstream *http.Request, bucket state.ObjectBucket, policy api.ObjectStoragePolicy, read *AdmittedRead) (*http.Response, bool) {
	if h.requestMetrics != nil && read == nil {
		if err := RecordGatewayProviderRequest(upstream.Context(), h.requestMetrics, bucket.ID, h.now(), policy); err != nil {
			writePublicReadRefusal(w, err)
			return nil, false
		}
	}
	response, err := h.client.Do(upstream)
	if err != nil {
		http.Error(w, "object storage is temporarily unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	return response, true
}

func publicIfRangeMatches(values []string, headers http.Header) bool {
	if len(values) != 1 {
		return false
	}
	value := strings.TrimSpace(values[0])
	if strings.HasPrefix(value, `"`) {
		// If-Range requires strong comparison; a weak ETag never matches.
		return len(value) >= 2 && len(headers.Values("ETag")) == 1 && strings.HasSuffix(value, `"`) && value == headers.Get("ETag")
	}
	date, err := http.ParseTime(value)
	modified, modifiedErr := http.ParseTime(headers.Get("Last-Modified"))
	return err == nil && modifiedErr == nil && len(headers.Values("Last-Modified")) == 1 && date.Equal(modified)
}
