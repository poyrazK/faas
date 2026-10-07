package s3gateway

import (
	"encoding/xml"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type versionRetentionXML struct {
	XMLName         xml.Name                   `xml:"Retention"`
	XMLNS           string                     `xml:"xmlns,attr"`
	Mode            string                     `xml:"Mode,omitempty"`
	RetainUntilDate string                     `xml:"RetainUntilDate,omitempty"`
	EventHold       string                     `xml:"EventHold,omitempty"`
	Duration        *bucketObjectLockPeriodXML `xml:"EventHoldDuration,omitempty"`
}
type versionLegalHoldXML struct {
	XMLName xml.Name `xml:"LegalHold"`
	XMLNS   string   `xml:"xmlns,attr"`
	Status  string   `xml:"Status"`
}

func (h *Handler) versionProtectionService(req requestContext) objectstorage.VersionProtectionService {
	st, _ := h.store.(state.ObjectVersionProtectionStore)
	refs, _ := h.store.(state.ObjectVersionReferenceStore)
	lock, _ := h.store.(state.ObjectBucketObjectLockStore)
	return objectstorage.VersionProtectionService{Store: st, References: refs, BucketLock: lock, Provider: req.provider, EventHolds: req.objectLockConfig.PublicCapabilities(req.provider).VersionEventHold, BeforeRequest: objectstorage.VersioningRequestRecorder(h.requestMetrics, req.bucket.ID)}
}
func (h *Handler) objectVersionProtection(w http.ResponseWriter, r *http.Request, req requestContext, key, kind string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		h.unsupported(w, r, req.requestID)
		return
	}
	permission := state.ObjectBucketPermissionRead
	if r.Method == http.MethodPut {
		permission = state.ObjectBucketPermissionWrite
	}
	if !h.require(w, req, permission, r.URL.Path) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	version, err := protectionSelector(r, kind)
	if err != nil {
		h.protectionError(w, r, req, err)
		return
	}
	if err := objectstorage.ValidateObjectTaggingRequest(r); err != nil {
		h.protectionError(w, r, req, err)
		return
	}
	svc := h.versionProtectionService(req)
	if r.Method == http.MethodGet {
		h.getVersionProtection(w, r, req, svc, key, version, kind)
		return
	}
	if !req.objectLockConfig.PublicCapabilities(req.provider).VersionRetention {
		h.unsupported(w, r, req.requestID)
		return
	}
	h.putVersionProtection(w, r, req, svc, key, version, kind)
}
func protectionSelector(r *http.Request, kind string) (string, error) {
	q := operationQuery(r.URL.Query())
	q.Del("x-id")
	subresource := "retention"
	if kind == "legal_hold" {
		subresource = "legal-hold"
	}
	if !queryKeysOnly(q, subresource, "versionId") || len(q[subresource]) != 1 || q.Get(subresource) != "" || len(q["versionId"]) != 1 || !state.ValidObjectVersionID(q.Get("versionId")) {
		return "", objectstorage.ErrInvalid
	}
	return q.Get("versionId"), nil
}
func (h *Handler) getVersionProtection(w http.ResponseWriter, r *http.Request, req requestContext, svc objectstorage.VersionProtectionService, key, version, kind string) {
	if _, err := readVerifiedRequestBody(w, r, req.signature.PayloadHash, 0); err != nil {
		h.protectionError(w, r, req, err)
		return
	}
	retention, hold, err := svc.Read(r.Context(), req.bucket, key, version, kind)
	if err != nil {
		h.protectionError(w, r, req, err)
		return
	}
	w.Header().Set("X-Amz-Version-Id", version)
	if kind == "legal_hold" {
		writeS3XML(w, 200, req.requestID, versionLegalHoldXML{XMLNS: s3XMLNamespace, Status: hold.Status})
		return
	}
	out := versionRetentionXML{XMLNS: s3XMLNamespace, Mode: retention.Mode, EventHold: retention.EventHold}
	if retention.RetainUntilDate != nil {
		out.RetainUntilDate = retention.RetainUntilDate.UTC().Format(time.RFC3339Nano)
	}
	if retention.EventHoldDuration != nil {
		out.Duration = &bucketObjectLockPeriodXML{Days: retention.EventHoldDuration.Days, Years: retention.EventHoldDuration.Years}
	}
	writeS3XML(w, 200, req.requestID, out)
}
func (h *Handler) putVersionProtection(w http.ResponseWriter, r *http.Request, req requestContext, svc objectstorage.VersionProtectionService, key, version, kind string) {
	body, err := readVerifiedRequestBody(w, r, req.signature.PayloadHash, api.MaxObjectLockBodyBytes)
	if err != nil {
		h.protectionError(w, r, req, err)
		return
	}
	id, err := protectionRequestID(r, req)
	if err != nil {
		h.protectionError(w, r, req, err)
		return
	}
	j, err := decodeProtectionIntent(body, id, key, version, kind)
	if err == nil {
		j, err = svc.Request(r.Context(), req.bucket, j)
	}
	if err == nil {
		j, err = svc.Reconcile(r.Context(), req.bucket, j.ID)
	}
	if j.AccountID == req.bucket.AccountID && j.BucketID == req.bucket.ID && !j.CreatedAt.IsZero() {
		w.Header().Set("X-Gregale-Protection-Id", j.ID)
	}
	if err != nil {
		h.protectionError(w, r, req, err)
		return
	}
	if j.State != "ready" {
		h.protectionError(w, r, req, state.ErrConflict)
		return
	}
	w.Header().Set("X-Amz-Version-Id", version)
	w.WriteHeader(http.StatusOK)
}
func protectionRequestID(r *http.Request, req requestContext) (string, error) {
	values := r.Header.Values("X-Gregale-Protection-Id")
	if len(values) == 0 {
		return uuid.NewString(), nil
	}
	if len(values) != 1 || !slices.Contains(strings.Split(req.signature.SignedHeader, ";"), "x-gregale-protection-id") {
		return "", objectstorage.ErrInvalid
	}
	id, err := uuid.Parse(values[0])
	if err != nil || id.String() != values[0] || id.Version() != 4 || id.Variant() != uuid.RFC4122 {
		return "", objectstorage.ErrInvalid
	}
	return id.String(), nil
}
func decodeProtectionIntent(body []byte, id, key, version, kind string) (state.ObjectVersionProtection, error) {
	j := state.ObjectVersionProtection{ObjectVersionProtection: api.ObjectVersionProtection{ID: id, Key: key, VersionID: version, Kind: kind}}
	var err error
	if kind == "retention" {
		v, e := objectstorage.DecodeObjectVersionRetention(body)
		j.Retention = &v
		err = e
	} else {
		v, e := objectstorage.DecodeObjectVersionLegalHold(body)
		j.LegalHold = &v
		err = e
	}
	return j, err
}
func (h *Handler) protectionError(w http.ResponseWriter, r *http.Request, req requestContext, err error) {
	if h.writeAWSChunkedError(w, r, req.requestID, err) {
		return
	}
	status, code, message := 503, "ServiceUnavailable", "Object protection could not be verified; inspect its operation receipt."
	var oversized *http.MaxBytesError
	switch {
	case errors.Is(err, state.ErrObjectBucketWriteFenced):
		message = "Bucket writes are temporarily paused for checkpoint capture."
	case errors.Is(err, objectstorage.ErrUnsupported):
		h.unsupported(w, r, req.requestID)
		return
	case errors.Is(err, objectstorage.ErrInvalid), errors.As(err, &oversized):
		status, code, message = 400, "InvalidArgument", "An exact owned version and valid protection policy are required."
	case errors.Is(err, objectstorage.ErrNotFound), errors.Is(err, state.ErrNotFound):
		status, code, message = 404, "NoSuchVersion", "The specified owned version does not exist."
	case errors.Is(err, objectstorage.ErrProtectionRejected):
		status, code, message = 403, "AccessDenied", "The provider rejected the protection change."
	case errors.Is(err, objectstorage.ErrConflict), errors.Is(err, state.ErrConflict):
		status, code, message = 409, "OperationAborted", "Protection is pending or conflicts with an active policy or mutation."
	}
	if status == 503 || status == 409 {
		w.Header().Set("Retry-After", "30")
	}
	writeS3Error(w, status, code, message, r.URL.Path, req.requestID)
}
