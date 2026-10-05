package s3gateway

import (
	"encoding/xml"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type bucketObjectLockXML struct {
	XMLName xml.Name                 `xml:"ObjectLockConfiguration"`
	XMLNS   string                   `xml:"xmlns,attr"`
	Enabled string                   `xml:"ObjectLockEnabled"`
	Rule    *bucketObjectLockRuleXML `xml:"Rule,omitempty"`
}
type bucketObjectLockRuleXML struct {
	Default bucketObjectLockDefaultXML `xml:"DefaultRetention"`
}
type bucketObjectLockDefaultXML struct {
	Mode      string                     `xml:"Mode"`
	Days      *int32                     `xml:"Days,omitempty"`
	Years     *int32                     `xml:"Years,omitempty"`
	EventHold *bucketObjectLockPeriodXML `xml:"DefaultEventHold,omitempty"`
}
type bucketObjectLockPeriodXML struct {
	Days  *int32 `xml:"Days,omitempty"`
	Years *int32 `xml:"Years,omitempty"`
}

func bucketObjectLockResponse(c api.ObjectBucketObjectLockConfiguration) bucketObjectLockXML {
	out := bucketObjectLockXML{XMLNS: s3XMLNamespace, Enabled: "Enabled"}
	if d := c.DefaultRetention; d != nil {
		rule := bucketObjectLockDefaultXML{Mode: d.Mode, Days: d.Days, Years: d.Years}
		if d.DefaultEventHold != nil {
			rule.EventHold = &bucketObjectLockPeriodXML{Days: d.DefaultEventHold.Days, Years: d.DefaultEventHold.Years}
		}
		out.Rule = &bucketObjectLockRuleXML{Default: rule}
	}
	return out
}

func (h *Handler) bucketObjectLock(w http.ResponseWriter, r *http.Request, req requestContext) {
	permission := state.ObjectBucketPermissionRead
	if r.Method == http.MethodPut {
		permission = state.ObjectBucketPermissionWrite
	}
	if !h.require(w, req, permission, r.URL.Path) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	st, stored := h.store.(state.ObjectBucketObjectLockStore)
	native, supported := req.provider.(objectstorage.BucketObjectLockProvider)
	versioning, versioned := req.provider.(objectstorage.BucketVersioningProvider)
	if !stored || !supported || !versioned || !objectstorage.SupportsNativeObjectLock(req.provider) {
		h.unsupported(w, r, req.requestID)
		return
	}
	svc := objectstorage.BucketObjectLockService{Store: st, Provider: native, Versioning: versioning, BeforeRequest: objectstorage.VersioningRequestRecorder(h.requestMetrics, req.bucket.ID)}
	if r.Method == http.MethodGet {
		h.getBucketObjectLock(w, r, req, svc, st)
		return
	}
	h.putBucketObjectLock(w, r, req, svc, st)
}

func (h *Handler) getBucketObjectLock(w http.ResponseWriter, r *http.Request, req requestContext, svc objectstorage.BucketObjectLockService, st state.ObjectBucketObjectLockStore) {
	if _, err := readVerifiedRequestBody(w, r, req.signature.PayloadHash, 0); err != nil {
		h.bucketObjectLockError(w, r, req, err)
		return
	}
	if !req.objectLockConfig.PublicCapabilities(req.provider).BucketConfiguration {
		j, err := st.GetObjectBucketObjectLock(r.Context(), req.bucket.AccountID, req.bucket.AppID, req.bucket.ID)
		if err != nil {
			h.bucketObjectLockError(w, r, req, err)
			return
		}
		if j.Revision == 0 && !j.EnabledRequired && !j.ObservedKnown && j.State == "ready" {
			h.unsupported(w, r, req.requestID)
			return
		}
	}
	_, native, err := svc.Read(r.Context(), req.bucket)
	if err != nil {
		h.bucketObjectLockError(w, r, req, err)
		return
	}
	if !native.Enabled {
		writeS3Error(w, 404, "ObjectLockConfigurationNotFoundError", "The bucket has no Object Lock configuration.", r.URL.Path, req.requestID)
		return
	}
	writeS3XML(w, http.StatusOK, req.requestID, bucketObjectLockResponse(native))
}

func (h *Handler) putBucketObjectLock(w http.ResponseWriter, r *http.Request, req requestContext, svc objectstorage.BucketObjectLockService, st state.ObjectBucketObjectLockStore) {
	body, err := readVerifiedRequestBody(w, r, req.signature.PayloadHash, api.MaxObjectLockBodyBytes)
	if err != nil {
		h.bucketObjectLockError(w, r, req, err)
		return
	}
	configuration, err := objectstorage.DecodeBucketObjectLockConfiguration(body)
	if err == nil {
		err = req.objectLockConfig.ValidateConfiguration(req.provider, configuration)
	}
	if err != nil {
		h.bucketObjectLockError(w, r, req, err)
		return
	}
	j, err := svc.Request(r.Context(), req.bucket, configuration)
	if err == nil && j.State != "ready" {
		j, err = svc.Reconcile(r.Context(), req.bucket)
		if errors.Is(err, state.ErrConflict) {
			j, err = st.GetObjectBucketObjectLock(r.Context(), req.bucket.AccountID, req.bucket.AppID, req.bucket.ID)
		}
	}
	if err != nil {
		h.bucketObjectLockError(w, r, req, err)
		return
	}
	if j.State != "ready" || !j.ObservedKnown || j.ObservedConfiguration == nil || !j.ObservedConfiguration.Equal(configuration) {
		h.bucketObjectLockError(w, r, req, state.ErrConflict)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) bucketObjectLockError(w http.ResponseWriter, r *http.Request, req requestContext, err error) {
	if h.writeAWSChunkedError(w, r, req.requestID, err) {
		return
	}
	var sizeErr *http.MaxBytesError
	if errors.As(err, &sizeErr) {
		err = objectstorage.ErrInvalid
	}
	status, code, message := 503, "ServiceUnavailable", "The native Object Lock configuration could not be verified."
	switch {
	case errors.Is(err, objectstorage.ErrUnsupported):
		h.unsupported(w, r, req.requestID)
		return
	case errors.Is(err, objectstorage.ErrInvalid):
		status, code, message = 400, "InvalidArgument", "The Object Lock configuration is invalid."
	case errors.Is(err, state.ErrConflict):
		status, code, message = 409, "OperationAborted", "Object Lock configuration is pending; retry or inspect its control API status."
	case errors.Is(err, state.ErrNotFound), errors.Is(err, objectstorage.ErrNotFound):
		status, code, message = 404, "NoSuchBucket", "The specified bucket does not exist."
	}
	if status == 503 || status == 409 {
		w.Header().Set("Retry-After", "30")
	}
	writeS3Error(w, status, code, message, r.URL.Path, req.requestID)
}
