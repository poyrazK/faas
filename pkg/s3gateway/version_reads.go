package s3gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) publicVersionHeader(w http.ResponseWriter, r *http.Request, req requestContext, key, native string, marker bool) bool {
	if native == "" {
		return true
	}
	st, ok := h.store.(state.ObjectVersionReferenceStore)
	if !ok {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), api.ObjectUploadSettlementTimeout)
	defer cancel()
	refs, err := st.RecordObjectVersions(ctx, req.bucket.AccountID, req.bucket.ID, []state.ObjectVersionIdentity{{Key: key, ProviderVersionID: native, DeleteMarker: marker}})
	if err != nil || len(refs) != 1 || !validReturnedVersionIdentity(refs[0], key, native) {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return false
	}
	w.Header().Set("X-Amz-Version-Id", refs[0].ID)
	return true
}

func (h *Handler) resolveReadVersion(w http.ResponseWriter, r *http.Request, req requestContext, key string) (string, bool) {
	if !r.URL.Query().Has("versionId") {
		return "", true
	}
	st, ok := h.store.(state.ObjectVersionReferenceStore)
	if _, capable := req.provider.(objectstorage.VersionReadPresigner); !ok || !capable {
		h.unsupported(w, r, req.requestID)
		return "", false
	}
	id := r.URL.Query().Get("versionId")
	native, err := st.ResolveObjectVersion(r.Context(), req.bucket.AccountID, req.bucket.ID, key, id)
	if errors.Is(err, state.ErrNotFound) {
		writeS3Error(w, http.StatusNotFound, "NoSuchVersion", "The specified version does not exist.", r.URL.Path, req.requestID)
		return "", false
	}
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return "", false
	}
	return native, true
}

func (h *Handler) presignGatewayRead(r *http.Request, req requestContext, key, native string) (objectstorage.SignedRequest, error) {
	mode := headerOrQueryValue(r, "x-amz-checksum-mode")
	if native != "" {
		return req.provider.(objectstorage.VersionReadPresigner).PresignVersionRead(r.Context(), req.bucket.PhysicalName, r.Method, key, native, mode == "ENABLED", 60)
	}
	return presignChecksumRead(r.Context(), req.provider, req.bucket.PhysicalName, r.Method, key, mode)
}

func (h *Handler) prepareGatewayRead(r *http.Request, req requestContext, key, native string) (*http.Request, error) {
	signed, err := h.presignGatewayRead(r, req, key, native)
	if err != nil {
		return nil, err
	}
	upstream, err := http.NewRequestWithContext(r.Context(), r.Method, signed.URL, nil)
	if err != nil {
		return nil, objectstorage.ErrUnavailable
	}
	for name, value := range signed.Headers {
		upstream.Header.Set(name, value)
	}
	for _, name := range []string{"Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
		if value := r.Header.Get(name); value != "" {
			upstream.Header.Set(name, value)
		}
	}
	return upstream, nil
}

func (h *Handler) downloadVersionHeaders(w http.ResponseWriter, r *http.Request, req requestContext, key, expected string, response *http.Response) bool {
	native := response.Header.Get("X-Amz-Version-Id")
	markerValue := response.Header.Get("X-Amz-Delete-Marker")
	marker := markerValue == "true"
	if markerValue != "" && markerValue != "true" && markerValue != "false" || len(response.Header.Values("X-Amz-Version-Id")) > 1 || len(response.Header.Values("X-Amz-Delete-Marker")) > 1 || marker && (native == "" || expected == "" && response.StatusCode != http.StatusNotFound || expected != "" && response.StatusCode != http.StatusMethodNotAllowed) {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return false
	}
	success := response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
	// The mutable null version can be served by an unversioned backend that
	// omits version response headers. The signed request still selects null.
	if success && !marker && expected == "null" && native == "" {
		native = "null"
	}
	if expected != "" && (native != "" && native != expected || (success || marker) && native != expected) {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return false
	}
	if success || marker || response.StatusCode == http.StatusNotModified || response.StatusCode == http.StatusPreconditionFailed {
		if !h.publicVersionHeader(w, r, req, key, native, marker) {
			return false
		}
		if native == "" && expected != "" {
			w.Header().Set("X-Amz-Version-Id", r.URL.Query().Get("versionId"))
		}
	}
	if marker {
		w.Header().Set("X-Amz-Delete-Marker", "true")
		if expected != "" && response.StatusCode == http.StatusMethodNotAllowed {
			if value := response.Header.Get("Last-Modified"); value != "" {
				w.Header().Set("Last-Modified", value)
			}
			writeS3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified version is a delete marker.", r.URL.Path, req.requestID)
			return false
		}
	}
	if expected != "" && response.StatusCode == http.StatusNotFound {
		writeS3Error(w, http.StatusNotFound, "NoSuchVersion", "The specified version does not exist.", r.URL.Path, req.requestID)
		return false
	}
	return true
}

func validReturnedVersionIdentity(v state.ObjectVersionIdentity, key, native string) bool {
	return v.Key == key && v.ProviderVersionID == native && state.ValidObjectVersionID(v.ID) && (v.ID == "null") == (native == "null")
}
