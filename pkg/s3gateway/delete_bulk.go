package s3gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) deleteBulkTarget(ctx context.Context, req requestContext, target deleteObjectTarget, id string) (deletedObjectResult, error) {
	result := deletedObjectResult{Key: target.Key, VersionID: target.VersionID}
	j, err := h.deleteObjectIntent(ctx, req, target.Key, target.VersionID, id)
	result.DeleteMarker = j.DeleteMarker
	if j.DeleteMarker {
		result.DeleteMarkerVersionID = j.VersionID
	}
	return result, err
}

func (h *Handler) readDeleteObjects(w http.ResponseWriter, r *http.Request, req requestContext) (deleteObjectsRequest, bool) {
	if r.ContentLength <= 0 || r.ContentLength > maxDeleteObjectsBodyBytes {
		writeS3Error(w, http.StatusBadRequest, "MalformedXML", "The XML you provided was not well-formed or did not validate against the published schema.", r.URL.Path, req.requestID)
		return deleteObjectsRequest{}, false
	}
	body, err := readVerifiedRequestBody(w, r, req.signature.PayloadHash, maxDeleteObjectsBodyBytes)
	if err != nil {
		if h.writeAWSChunkedError(w, r, req.requestID, err) {
			return deleteObjectsRequest{}, false
		}
		writeS3Error(w, http.StatusBadRequest, "MalformedXML", "The XML you provided was not well-formed or did not validate against the published schema.", r.URL.Path, req.requestID)
		return deleteObjectsRequest{}, false
	}
	checksum, _, checksumErr := requestChecksum(r.Header)
	if checksumErr != nil {
		h.writeAWSChunkedError(w, r, req.requestID, checksumErr)
		return deleteObjectsRequest{}, false
	}
	if r.Header.Get("Content-MD5") == "" && checksum == nil && req.streaming == nil {
		writeS3Error(w, http.StatusBadRequest, "MissingContentMD5", "Missing required Content-MD5 or x-amz-checksum header for this request.", r.URL.Path, req.requestID)
		return deleteObjectsRequest{}, false
	}

	request, err := parseDeleteObjects(body)
	if err != nil {
		writeS3Error(w, http.StatusBadRequest, "MalformedXML", "The delete request contains invalid or unsupported fields.", r.URL.Path, req.requestID)
		return deleteObjectsRequest{}, false
	}

	return request, true
}

func bulkDeleteError(target deleteObjectTarget, err error) deleteObjectError {
	code, message := "ServiceUnavailable", "Gregale could not reach this bucket's storage placement."
	if errors.Is(err, objectstorage.ErrInvalid) {
		code, message = "InvalidRequest", "The request is invalid."
	} else if errors.Is(err, objectstorage.ErrUnsupported) {
		code, message = "NotImplemented", "This S3 operation is not implemented by the storage provider."
	} else if errors.Is(err, state.ErrNotFound) {
		code, message = "NoSuchVersion", "The specified version does not exist."
	}
	return deleteObjectError{Key: target.Key, VersionID: target.VersionID, Code: code, Message: message}
}
