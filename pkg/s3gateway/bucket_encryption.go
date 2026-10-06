package s3gateway

import (
	"encoding/xml"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type bucketEncryptionXML struct {
	XMLName xml.Name                `xml:"ServerSideEncryptionConfiguration"`
	XMLNS   string                  `xml:"xmlns,attr,omitempty"`
	Rule    bucketEncryptionRuleXML `xml:"Rule"`
}
type bucketEncryptionRuleXML struct {
	Apply     bucketEncryptionDefaultXML `xml:"ApplyServerSideEncryptionByDefault"`
	BucketKey *bool                      `xml:"BucketKeyEnabled,omitempty"`
}
type bucketEncryptionDefaultXML struct {
	Algorithm string `xml:"SSEAlgorithm"`
	KeyID     string `xml:"KMSMasterKeyID,omitempty"`
}

func (h *Handler) bucketEncryption(w http.ResponseWriter, r *http.Request, req requestContext) {
	permission := state.ObjectBucketPermissionRead
	if r.Method != http.MethodGet {
		permission = state.ObjectBucketPermissionWrite
	}
	if !h.require(w, req, permission, r.URL.Path) {
		return
	}
	st, stored := h.store.(state.ObjectBucketEncryptionStore)
	native, supported := req.provider.(objectstorage.BucketEncryptionProvider)
	if !stored || !supported {
		h.unsupported(w, r, req.requestID)
		return
	}
	svc := objectstorage.BucketEncryptionService{Store: st, Provider: native, BeforeRequest: objectstorage.VersioningRequestRecorder(h.requestMetrics, req.bucket.ID)}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		h.getBucketEncryption(w, r, req, svc)
		return
	}
	h.changeBucketEncryption(w, r, req, svc, st)
}

func (h *Handler) getBucketEncryption(w http.ResponseWriter, r *http.Request, req requestContext, svc objectstorage.BucketEncryptionService) {
	if _, err := readVerifiedRequestBody(w, r, req.signature.PayloadHash, 0); err != nil {
		h.bucketEncryptionError(w, r, req, err)
		return
	}
	j, native, err := svc.Read(r.Context(), req.bucket)
	if err != nil {
		h.bucketEncryptionError(w, r, req, err)
		return
	}
	if j.State != "ready" {
		h.bucketEncryptionError(w, r, req, state.ErrConflict)
		return
	}
	selection := j.Encryption.Clone().Selection
	if selection.Empty() {
		if native.Algorithm == "" {
			writeS3Error(w, 404, "ServerSideEncryptionConfigurationNotFoundError", "The bucket has no encryption configuration.", r.URL.Path, req.requestID)
			return
		}
		if native.Algorithm != "AES256" {
			h.bucketEncryptionError(w, r, req, objectstorage.ErrConfiguration)
			return
		}
		selection.Algorithm = "AES256"
	}
	writeS3XML(w, http.StatusOK, req.requestID, bucketEncryptionXML{XMLNS: s3XMLNamespace, Rule: bucketEncryptionRuleXML{Apply: bucketEncryptionDefaultXML{Algorithm: selection.Algorithm, KeyID: selection.KeyID}, BucketKey: selection.BucketKeyEnabled}})
}

func (h *Handler) changeBucketEncryption(w http.ResponseWriter, r *http.Request, req requestContext, svc objectstorage.BucketEncryptionService, st state.ObjectBucketEncryptionStore) {
	var snapshot state.ObjectEncryptionSnapshot
	if r.Method == http.MethodPut {
		body, err := readVerifiedRequestBody(w, r, req.signature.PayloadHash, api.MaxObjectBucketEncryptionBodyBytes)
		if err != nil {
			h.bucketEncryptionError(w, r, req, err)
			return
		}
		selection, err := objectstorage.DecodeBucketEncryptionConfiguration(body)
		if err == nil {
			account, parseErr := uuid.Parse(req.bucket.AccountID)
			if parseErr != nil {
				err = objectstorage.ErrConfiguration
			} else {
				snapshot, err = req.encryptionConfig.Resolve(account.String(), selection)
			}
		}
		if err != nil {
			h.bucketEncryptionError(w, r, req, err)
			return
		}
	} else {
		if _, err := readVerifiedRequestBody(w, r, req.signature.PayloadHash, 0); err != nil {
			h.bucketEncryptionError(w, r, req, err)
			return
		}
	}
	j, err := svc.Request(r.Context(), req.bucket, snapshot)
	if err == nil && j.State != "ready" {
		j, err = svc.Reconcile(r.Context(), req.bucket)
		if errors.Is(err, state.ErrConflict) {
			j, err = st.GetObjectBucketEncryption(r.Context(), req.bucket.AccountID, req.bucket.AppID, req.bucket.ID)
		}
	}
	if err != nil {
		h.bucketEncryptionError(w, r, req, err)
		return
	}
	if j.State != "ready" || !j.Encryption.Equal(snapshot) {
		h.bucketEncryptionError(w, r, req, objectstorage.ErrUnavailable)
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

func (h *Handler) bucketEncryptionError(w http.ResponseWriter, r *http.Request, req requestContext, err error) {
	if h.writeAWSChunkedError(w, r, req.requestID, err) {
		return
	}
	var sizeErr *http.MaxBytesError
	if errors.As(err, &sizeErr) {
		err = objectstorage.ErrInvalid
	}
	status, code, message := 503, "ServiceUnavailable", "Bucket encryption configuration is pending; retry or inspect its control API status."
	switch {
	case errors.Is(err, objectstorage.ErrUnsupported):
		h.unsupported(w, r, req.requestID)
		return
	case errors.Is(err, objectstorage.ErrInvalid):
		status, code, message = 400, "InvalidArgument", "The bucket encryption configuration is invalid."
	case errors.Is(err, state.ErrConflict):
		status, code, message = 409, "OperationAborted", "The bucket encryption configuration is busy."
	case errors.Is(err, state.ErrNotFound), errors.Is(err, objectstorage.ErrNotFound):
		status, code, message = 404, "NoSuchBucket", "The specified bucket does not exist."
	}
	if status == 503 {
		w.Header().Set("Retry-After", "30")
	}
	writeS3Error(w, status, code, message, r.URL.Path, req.requestID)
}
