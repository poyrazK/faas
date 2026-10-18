package s3gateway

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// DiskConfig describes a private, operator-authenticated storage node. Customer
// authorization stays in the branded gateway; these keys have node-wide access.
type DiskConfig struct {
	Store               *objectnode.Store
	AccessKeyID         string
	SecretAccessKey     string
	Region              string
	MaxObjectBytes      int64
	MaxPartBytes        int64
	MaxMultipartBytes   int64
	MaxConcurrentWrites int
	MinFreeBytes        int64
	WriteAdmission      *objectnode.WriteAdmissionOptions
}

// DiskHandler reuses the branded gateway's SigV4 and XML wire implementation.
// It is a deliberately small unversioned S3 backend, not a second tenant gateway.
type DiskHandler struct {
	config   DiskConfig
	writes   chan struct{}
	mu       sync.Mutex
	reserved uint64
	metrics  *diskMetrics
}

func NewDiskHandler(c DiskConfig) (*DiskHandler, error) {
	if c.Store == nil || c.AccessKeyID == "" || c.SecretAccessKey == "" || c.Region == "" {
		return nil, errors.New("object node: store, operator credentials and region are required")
	}
	if c.MaxObjectBytes == 0 {
		c.MaxObjectBytes = api.DefaultObjectUploadBytes
	}
	if c.MaxConcurrentWrites == 0 {
		c.MaxConcurrentWrites = api.DefaultObjectConcurrentUploads
	}
	if c.MaxPartBytes == 0 {
		c.MaxPartBytes = api.DefaultMultipartPartBytes
	}
	if c.MaxMultipartBytes == 0 {
		c.MaxMultipartBytes = api.MaxObjectUploadBytes
	}
	if c.MinFreeBytes == 0 {
		c.MinFreeBytes = api.ObjectUploadSpoolMinFreeBytes
	}
	if c.MaxObjectBytes < 1 || c.MaxObjectBytes > api.MaxObjectSinglePutBytes || c.MaxPartBytes < 1 || c.MaxPartBytes > api.MaxObjectSinglePutBytes || c.MaxMultipartBytes < 1 || c.MaxMultipartBytes > api.MaxObjectUploadBytes || c.MaxConcurrentWrites < 1 || c.MaxConcurrentWrites > api.MaxObjectConcurrentUploads || c.MinFreeBytes < 0 {
		return nil, errors.New("object node: invalid transfer limits")
	}
	h := &DiskHandler{config: c, writes: make(chan struct{}, c.MaxConcurrentWrites)}
	if c.WriteAdmission != nil {
		if err := c.Store.ConfigureWriteAdmission(*c.WriteAdmission); err != nil {
			return nil, err
		}
	}
	h.metrics = newDiskMetrics(h)
	status, err := c.Store.ScrubStatus()
	if err != nil {
		return nil, err
	}
	h.observeScrub(status)
	return h, nil
}

func (h *DiskHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w, finish := h.measureRequest(w, r)
	defer finish()
	id := uuid.NewString()
	w.Header().Set("X-Amz-Request-Id", id)
	payload, err := h.authenticate(r)
	if err != nil {
		writeS3Error(w, 403, "SignatureDoesNotMatch", "The request signature is invalid.", r.URL.Path, id)
		return
	}
	if isStreamingPayloadHash(payload) || diskUnsupportedHeaders(r) {
		h.unsupported(w, r, id)
		return
	}
	h.dispatch(w, r, payload, id)
}

func (h *DiskHandler) dispatch(w http.ResponseWriter, r *http.Request, payload, id string) {
	if r.URL.Query().Has(objectnode.ReplicationQuery) {
		h.replicationEndpoint(w, r, payload, id)
		return
	}
	if err := h.config.Store.CanServe(); err != nil {
		h.failure(w, r, id, err)
		return
	}
	if r.URL.Query().Has(objectrecovery.Query) {
		h.recoverySettlement(&replicaResponse{ResponseWriter: w, handler: h, request: r, id: id}, r, payload, id)
		return
	}
	if r.Header.Values(objectstorage.OwnedNodeAdmissionHeader) != nil {
		h.writeAdmissionProbe(w, r, id)
		return
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete {
		if err := h.config.Store.CanMutate(); err != nil {
			h.failure(w, r, id, err)
			return
		}
	}
	h.dispatchObjects(&replicaResponse{ResponseWriter: w, handler: h, request: r, id: id}, r, payload, id)
}

func (h *DiskHandler) dispatchObjects(w http.ResponseWriter, r *http.Request, payload, id string) {
	bucket, key, hasKey := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if r.URL.Query().Has(objectinventory.Query) {
		if hasKey && key != "" {
			h.unsupported(w, r, id)
			return
		}
		if bucket != "" && !objectnode.ValidBucket(bucket) {
			h.failure(w, r, id, objectnode.ErrInvalid)
			return
		}
		if kind := r.URL.Query().Get(objectinventory.Query); kind == objectinventory.ScrubKind || kind == objectinventory.ScrubFindings {
			h.scrub(w, r, bucket, payload, id)
			return
		}
		h.inventory(w, r, bucket, id)
		return
	}
	if !objectnode.ValidBucket(bucket) {
		writeS3Error(w, 400, "InvalidBucketName", "Use a valid bucket and path-style addressing.", r.URL.Path, id)
		return
	}
	if !hasKey || key == "" {
		if r.Header.Values(objectstorage.OwnedNodeReceiptHeader) != nil || r.Header.Values(objectstorage.OwnedNodeMultipartReceiptHeader) != nil {
			h.unsupported(w, r, id)
			return
		}
		h.bucket(w, r, bucket, id)
		return
	}
	h.object(w, r, bucket, key, payload, id)
}

func (h *DiskHandler) authenticate(r *http.Request) (string, error) {
	var parsed sigV4Request
	var err error
	presigned := r.URL.Query().Has("X-Amz-Algorithm")
	if presigned && r.Header.Get("Authorization") != "" {
		return "", errSignature
	}
	if presigned {
		parsed, err = parsePresignedSigV4(r, h.config.Region, time.Now().UTC())
	} else {
		parsed, err = parseSigV4(r, h.config.Region, time.Now().UTC())
	}
	if err != nil || subtle.ConstantTimeCompare([]byte(parsed.AccessKeyID), []byte(h.config.AccessKeyID)) != 1 || headerOrQueryValue(r, "X-Amz-Security-Token") != "" {
		return "", errSignature
	}
	if presigned {
		err = verifyPresignedSigV4(r.Context(), r, parsed, h.config.SecretAccessKey, h.config.Region)
	} else {
		err = verifySigV4(r.Context(), r, parsed, h.config.SecretAccessKey, h.config.Region)
	}
	if err == nil && (r.Method == http.MethodPut || r.Method == http.MethodPost) {
		err = diskSignedWriteHeaders(r, parsed.SignedHeader)
	}
	selectors := 0
	for _, header := range []string{objectstorage.OwnedNodeReceiptHeader, objectstorage.OwnedNodeMultipartReceiptHeader, objectstorage.OwnedNodeAdmissionHeader} {
		if values := r.Header.Values(header); err == nil && values != nil {
			selectors++
			if len(values) != 1 || values[0] == "" || !slices.Contains(strings.Split(parsed.SignedHeader, ";"), strings.ToLower(header)) {
				err = errSignature
			}
		}
	}
	if selectors > 1 {
		err = errSignature
	}
	if r.Header.Values(objectstorage.OwnedNodeAdmissionSizeHeader) != nil && (r.Header.Get(objectstorage.OwnedNodeAdmissionHeader) == "" || len(r.Header.Values(objectstorage.OwnedNodeAdmissionSizeHeader)) != 1 || !slices.Contains(strings.Split(parsed.SignedHeader, ";"), strings.ToLower(objectstorage.OwnedNodeAdmissionSizeHeader))) {
		err = errSignature
	}
	return parsed.PayloadHash, err
}

// A holder of a presigned upload must not add unsigned metadata or conditions
// that change the provider-private write receipt or the authorized operation.
func diskSignedWriteHeaders(r *http.Request, signed string) error {
	names := strings.Split(signed, ";")
	for name, values := range r.Header {
		name = strings.ToLower(name)
		bound := strings.HasPrefix(name, "x-amz-meta-") || strings.HasPrefix(name, "x-amz-checksum-") || name == "x-amz-sdk-checksum-algorithm"
		switch name {
		case "content-md5", "if-match", "if-none-match", "x-amz-mp-object-size":
			bound = true
		}
		if bound && (len(values) != 1 || !slices.Contains(names, name)) {
			return errSignature
		}
	}
	return nil
}

func diskUnsupportedHeaders(r *http.Request) bool {
	for _, values := range []map[string][]string{r.Header, r.URL.Query()} {
		for name := range values {
			name = strings.ToLower(name)
			for _, prefix := range []string{"x-amz-server-side-encryption", "x-amz-object-lock", "x-amz-grant-", "x-amz-copy-source"} {
				if strings.HasPrefix(name, prefix) {
					return true
				}
			}
			switch name {
			case "x-amz-acl", "x-amz-tagging", "x-amz-website-redirect-location", "x-amz-request-payer", "x-amz-expected-bucket-owner":
				return true
			case "x-amz-checksum-type":
				if r.Method != http.MethodPost || !r.URL.Query().Has("uploads") && !r.URL.Query().Has("uploadId") {
					return true
				}
			}
		}
	}
	return false
}

func (h *DiskHandler) unsupported(w http.ResponseWriter, r *http.Request, id string) {
	writeS3Error(w, http.StatusNotImplemented, "NotImplemented", "This operation is outside the object node's supported S3 subset.", r.URL.Path, id)
}

func (h *DiskHandler) failure(w http.ResponseWriter, r *http.Request, id string, err error) {
	status, code, message := http.StatusInternalServerError, "InternalError", "The storage operation could not be confirmed."
	switch {
	case errors.Is(err, objectnode.ErrCatalogIndex), errors.Is(err, objectnode.ErrCatalogBusy), errors.Is(err, objectnode.ErrUsageAudit):
		status, code, message = 503, "ServiceUnavailable", "The catalog index is temporarily unavailable."
	case errors.Is(err, objectnode.ErrWritePressure):
		w.Header().Set("Retry-After", strconv.Itoa(int(api.ObjectNodeAdmissionRetryAfter.Seconds())))
		status, code, message = 503, "SlowDown", "New writes are temporarily throttled by storage pressure."
	case errors.Is(err, objectnode.ErrReplicaPending):
		status, code, message = 503, "ReplicaDurabilityPending", "The operation's durability could not be confirmed. Retain its receipt and retry with the same request identity."
	case errors.Is(err, objectnode.ErrFenced), errors.Is(err, objectnode.ErrReplica):
		status, code, message = 409, "PrimaryFenced", "This node cannot serve object traffic; its write authority has been fenced."
	case errors.Is(err, objectnode.ErrNoBucket):
		status, code, message = 404, "NoSuchBucket", "The bucket does not exist."
	case errors.Is(err, objectnode.ErrNoObject):
		status, code, message = 404, "NoSuchKey", "The object does not exist."
	case errors.Is(err, objectnode.ErrNoUpload):
		status, code, message = 404, "NoSuchUpload", "The multipart upload does not exist."
	case errors.Is(err, objectnode.ErrInvalidPart):
		status, code, message = 400, "InvalidPart", "A selected part does not match the uploaded part."
	case errors.Is(err, objectnode.ErrPartOrder):
		status, code, message = 400, "InvalidPartOrder", "Parts must be selected in increasing order."
	case errors.Is(err, objectnode.ErrPartSmall):
		status, code, message = 400, "EntityTooSmall", "A non-final part is smaller than the multipart minimum."
	case errors.Is(err, objectnode.ErrUploadBusy):
		status, code, message = 409, "OperationAborted", "Multipart completion is in progress."
	case errors.Is(err, objectnode.ErrScrubBusy):
		status, code, message = 409, "OperationAborted", "A checksum scrub operation is already in progress."
	case errors.Is(err, objectnode.ErrInventoryChanged):
		status, code, message = 409, "InventoryChanged", "Restart the inventory scan against the current catalog revision."
	case errors.Is(err, objectnode.ErrCapacity):
		status, code, message = 503, "SlowDown", "The node's capacity limit has been reached."
	case errors.Is(err, objectnode.ErrNoReceipt):
		status, code, message = 404, "NoSuchWriteReceipt", "The write outcome is unknown."
	case errors.Is(err, objectnode.ErrReceiptReuse):
		status, code, message = 409, "WriteReceiptConflict", "The receipt belongs to a different write."
	case errors.Is(err, objectnode.ErrReceiptExpired):
		status, code, message = 409, "WriteReceiptExpired", "The receipt is outside the first-use window. Its previous outcome remains unconfirmed."
	case errors.Is(err, objectnode.ErrNotEmpty):
		status, code, message = 409, "BucketNotEmpty", "The bucket is not empty."
	case errors.Is(err, objectnode.ErrInvalid):
		status, code, message = 400, "InvalidRequest", "The request is invalid or its body has an unexpected length."
	case errors.Is(err, objectnode.ErrReplicationOff):
		status, code, message = 409, "ReplicationDisabled", "Replication is not enabled on this node."
	case errors.Is(err, objectnode.ErrReplicationFork):
		status, code, message = 409, "ReplicationConflict", "The replication stream or position does not match."
	case errors.Is(err, objectnode.ErrReplicaReseed):
		status, code, message = 410, "ReplicaReseedRequired", "Reseed the follower from a fresh verified backup; the requested history has been compacted."
	case errors.Is(err, objectnode.ErrDigest):
		status, code, message = 400, "BadDigest", "A supplied checksum does not match the request body."
	case errors.Is(err, objectnode.ErrPrecondition):
		status, code, message = 412, "PreconditionFailed", "The object no longer meets the write conditions."
	}
	writeS3Error(w, status, code, message, r.URL.Path, id)
}
