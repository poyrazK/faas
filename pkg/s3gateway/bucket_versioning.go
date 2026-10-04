package s3gateway

import (
	"bytes"
	"encoding/xml"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
)

type bucketVersioningXML struct {
	XMLName   xml.Name `xml:"VersioningConfiguration"`
	XMLNS     string   `xml:"xmlns,attr,omitempty"`
	Status    string   `xml:"Status,omitempty"`
	MFADelete string   `xml:"MfaDelete,omitempty"`
}

func (h *Handler) bucketVersioning(w http.ResponseWriter, r *http.Request, req requestContext) {
	permission := state.ObjectBucketPermissionRead
	if r.Method == http.MethodPut {
		permission = state.ObjectBucketPermissionWrite
	}
	if !h.require(w, req, permission, r.URL.Path) {
		return
	}
	st, ok := h.store.(state.ObjectBucketVersioningStore)
	p, supported := req.provider.(objectstorage.BucketVersioningProvider)
	if !ok || !supported {
		h.unsupported(w, r, req.requestID)
		return
	}
	svc := objectstorage.BucketVersioningService{Store: st, Provider: p, BeforeRequest: objectstorage.VersioningRequestRecorder(h.requestMetrics, req.bucket.ID)}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		_, v, err := svc.Read(r.Context(), req.bucket)
		if err != nil {
			h.versioningError(w, r, req, err)
			return
		}
		writeS3XML(w, http.StatusOK, req.requestID, bucketVersioningXML{XMLNS: s3XMLNamespace, Status: v.Status, MFADelete: v.MFADelete})
		return
	}
	h.putBucketVersioning(w, r, req, svc, st)
}
func (h *Handler) putBucketVersioning(w http.ResponseWriter, r *http.Request, req requestContext, svc objectstorage.BucketVersioningService, st state.ObjectBucketVersioningStore) {
	status, err := decodeBucketVersioning(w, r, req.signature.PayloadHash)
	if err != nil {
		h.versioningError(w, r, req, err)
		return
	}
	j, err := svc.Request(r.Context(), req.bucket, status)
	if err != nil {
		h.versioningError(w, r, req, err)
		return
	}
	if j.State != "ready" {
		j, err = svc.Reconcile(r.Context(), req.bucket)
		if errors.Is(err, state.ErrConflict) {
			j, err = st.GetObjectBucketVersioning(r.Context(), req.bucket.AccountID, req.bucket.AppID, req.bucket.ID)
		}
	}
	if err != nil {
		h.versioningError(w, r, req, err)
		return
	}
	if j.ObservedStatus != status {
		h.versioningError(w, r, req, objectstorage.ErrUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}
func decodeBucketVersioning(w http.ResponseWriter, r *http.Request, hash string) (string, error) {
	body, err := readVerifiedRequestBody(w, r, hash, api.MaxObjectBucketVersioningBodyBytes)
	if err != nil {
		return "", err
	}
	var in struct {
		XMLName  xml.Name
		Statuses []string                     `xml:"Status"`
		MFA      []string                     `xml:"MfaDelete"`
		Extra    []struct{ XMLName xml.Name } `xml:",any"`
	}
	d := xml.NewDecoder(bytes.NewReader(body))
	if d.Decode(&in) != nil || in.XMLName.Local != "VersioningConfiguration" || in.XMLName.Space != "" && in.XMLName.Space != s3XMLNamespace {
		return "", objectstorage.ErrInvalid
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return "", objectstorage.ErrInvalid
	}
	if len(in.MFA) > 0 {
		return "", objectstorage.ErrUnsupported
	}
	if len(in.Extra) > 0 || len(in.Statuses) != 1 || !state.ValidObjectBucketVersioningStatus(in.Statuses[0]) {
		return "", objectstorage.ErrInvalid
	}
	return in.Statuses[0], nil
}
func (h *Handler) versioningError(w http.ResponseWriter, r *http.Request, req requestContext, err error) {
	status, code, message := 503, "ServiceUnavailable", "Versioning configuration is pending; retry or inspect its status in the control API."
	switch {
	case errors.Is(err, objectstorage.ErrUnsupported):
		h.unsupported(w, r, req.requestID)
		return
	case errors.Is(err, objectstorage.ErrInvalid):
		status, code, message = 400, "InvalidArgument", "The versioning configuration is invalid."
	case errors.Is(err, state.ErrConflict):
		status, code, message = 409, "OperationAborted", "The bucket is busy or contains unresolved legacy writes."
	case errors.Is(err, state.ErrNotFound), errors.Is(err, objectstorage.ErrNotFound):
		status, code, message = 404, "NoSuchBucket", "The specified bucket does not exist."
	}
	if status == 503 {
		w.Header().Set("Retry-After", "30")
	}
	writeS3Error(w, status, code, message, r.URL.Path, req.requestID)
}
