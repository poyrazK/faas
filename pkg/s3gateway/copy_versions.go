package s3gateway

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type gatewayCopySource struct{ Bucket, Key, VersionID string }

func parseCopySource(value string) (gatewayCopySource, error) {
	source := gatewayCopySource{}
	path, query, selected := strings.Cut(value, "?")
	if value == "" || strings.ContainsRune(value, '#') {
		return source, objectstorage.ErrInvalid
	}
	path, err := url.PathUnescape(path)
	if err != nil {
		return source, objectstorage.ErrInvalid
	}
	source.Bucket, source.Key, _ = strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if source.Bucket == "" || !objectstorage.ValidKey(source.Key) {
		return source, objectstorage.ErrInvalid
	}
	if selected {
		q, err := url.ParseQuery(query)
		if err != nil || len(q) != 1 || len(q["versionId"]) != 1 || q.Get("versionId") == "" {
			return source, objectstorage.ErrInvalid
		}
		source.VersionID = q.Get("versionId")
	}
	return source, nil
}

func (h *Handler) resolveCopyVersion(w http.ResponseWriter, r *http.Request, req requestContext, source gatewayCopySource, part bool) (string, bool) {
	if source.VersionID == "" {
		return "", true
	}
	_, capable := req.provider.(objectstorage.VersionedTrackedObjectCopier)
	if part {
		_, capable = req.provider.(objectstorage.VersionedMultipartPartCopier)
	}
	st, stored := h.store.(state.ObjectVersionReferenceStore)
	if !capable || !stored {
		h.unsupported(w, r, req.requestID)
		return "", false
	}
	native, err := st.ResolveObjectVersion(r.Context(), req.bucket.AccountID, req.bucket.ID, source.Key, source.VersionID)
	if errors.Is(err, state.ErrNotFound) {
		writeS3Error(w, http.StatusNotFound, "NoSuchVersion", "The specified copy source version does not exist.", r.URL.Path, req.requestID)
		return "", false
	}
	if err != nil || native == "" {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, source.Key)
		return "", false
	}
	return native, true
}

func snapshotGatewayCopySource(ctx context.Context, req requestContext, c objectstorage.CopyObjectRequest, copier objectstorage.TrackedObjectCopier) (objectstorage.CopySourceSnapshot, error) {
	if c.SourceProviderVersionID != "" {
		s, err := req.provider.(objectstorage.VersionedTrackedObjectCopier).SnapshotVersionCopySource(ctx, req.bucket.PhysicalName, c.SourceKey, c.SourceProviderVersionID)
		if err == nil && s.ProviderVersionID != c.SourceProviderVersionID {
			return objectstorage.CopySourceSnapshot{}, objectstorage.ErrUnavailable
		}
		return s, err
	}
	return copier.SnapshotCopySource(ctx, req.bucket.PhysicalName, c.SourceKey)
}

func snapshotGatewayPartCopySource(ctx context.Context, req requestContext, c objectstorage.MultipartPartCopyRequest, copier objectstorage.MultipartPartCopier) (objectstorage.CopySourceSnapshot, error) {
	if c.SourceProviderVersionID != "" {
		s, err := req.provider.(objectstorage.VersionedMultipartPartCopier).SnapshotVersionMultipartCopySource(ctx, req.bucket.PhysicalName, c.SourceKey, c.SourceProviderVersionID)
		if err == nil && s.ProviderVersionID != c.SourceProviderVersionID {
			return objectstorage.CopySourceSnapshot{}, objectstorage.ErrUnavailable
		}
		return s, err
	}
	return copier.SnapshotMultipartCopySource(ctx, req.bucket.PhysicalName, c.SourceKey)
}
