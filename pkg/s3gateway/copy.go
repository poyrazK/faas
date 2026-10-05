package s3gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
)

func gatewayCopyRequest(w http.ResponseWriter, r *http.Request, req requestContext, source, destination string) (objectstorage.CopyObjectRequest, bool) {
	c := objectstorage.CopyObjectRequest{SourceKey: source, DestinationKey: destination}
	var ok bool
	c.MetadataDirective, ok = copyDirective(w, r, req, "X-Amz-Metadata-Directive")
	if !ok {
		return c, false
	}
	metadata, err := copyMetadata(r, c.MetadataDirective)
	if err != nil {
		writeS3Error(w, http.StatusBadRequest, "InvalidRequest", "The object metadata is invalid.", r.URL.Path, req.requestID)
		return c, false
	}
	c.TaggingDirective, ok = copyDirective(w, r, req, "X-Amz-Tagging-Directive")
	if !ok {
		return c, false
	}
	metadata.Tags, err = objectstorage.ParseObjectTags(r.Header.Get("X-Amz-Tagging"))
	if err != nil {
		writeS3Error(w, http.StatusBadRequest, "InvalidTag", "The object tags are invalid.", r.URL.Path, req.requestID)
		return c, false
	}
	if c.TaggingDirective == "COPY" && len(metadata.Tags) != 0 {
		writeS3Error(w, http.StatusBadRequest, "InvalidRequest", "x-amz-tagging requires REPLACE tagging directive.", r.URL.Path, req.requestID)
		return c, false
	}
	c.Metadata = metadata
	return c, true
}

func copyDirective(w http.ResponseWriter, r *http.Request, req requestContext, header string) (string, bool) {
	directive := strings.ToUpper(strings.TrimSpace(r.Header.Get(header)))
	if directive == "" {
		directive = "COPY"
	}
	if directive != "COPY" && directive != "REPLACE" {
		writeS3Error(w, http.StatusBadRequest, "InvalidDirective", "The copy directive is invalid.", r.URL.Path, req.requestID)
		return directive, false
	}
	return directive, true
}

func (h *Handler) performLegacyGatewayCopy(w http.ResponseWriter, r *http.Request, req requestContext, copier objectstorage.ObjectCopier, c objectstorage.CopyObjectRequest) {
	size := int64(0)
	if sizer, ok := req.provider.(objectstorage.ObjectSizer); ok {
		var err error
		size, err = sizer.ObjectSize(r.Context(), req.bucket.PhysicalName, c.SourceKey)
		if err != nil {
			h.providerError(w, r, req, err, c.SourceKey)
			return
		}
	}
	if !h.admit(w, r, req, c.DestinationKey, size, true) || !h.recordProviderRequest(w, r, req) {
		return
	}
	result, err := objectstorageactivity.Execute(r.Context(), h.store, req.bucket, func(mutationCtx context.Context) (objectstorage.CopyObjectResult, error) {
		return copier.CopyObject(mutationCtx, req.bucket.PhysicalName, c)
	})
	if err != nil {
		h.providerError(w, r, req, err, c.SourceKey)
		return
	}
	if !h.publicVersionHeader(w, r, req, c.DestinationKey, result.ProviderVersionID, false) {
		return
	}
	writeGatewayCopyResult(w, req.requestID, result)
}

func writeGatewayCopyResult(w http.ResponseWriter, requestID string, result objectstorage.CopyObjectResult) {
	lastModified := ""
	if !result.LastModified.IsZero() {
		lastModified = result.LastModified.UTC().Format(time.RFC3339Nano)
	}
	writeS3XML(w, http.StatusOK, requestID, copyObjectResult{XMLNS: s3XMLNamespace, LastModified: lastModified, ETag: result.ETag})
}
