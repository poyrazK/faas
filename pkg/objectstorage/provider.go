// Package objectstorage implements customer object storage independently of
// the image/snapshot backend in pkg/storage. The customer API is portable;
// provider authentication, usage, and billing APIs are not.
package objectstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrWriteRejected       = errors.New("object storage write was rejected")
	ErrDeletionRejected    = errors.New("object storage deletion was rejected")
	ErrPreconditionFailed  = errors.New("object storage precondition failed")
	ErrConditionalConflict = errors.New("object storage conditional completion requires a new upload")
	ErrConditionalNotFound = errors.New("object storage conditional destination not found")
	ErrUnavailable         = errors.New("object storage unavailable")
	ErrNotFound            = errors.New("object storage resource not found")
	ErrConflict            = errors.New("object storage resource conflict")
	ErrNotEmpty            = errors.New("bucket is not empty")
	ErrInvalid             = errors.New("invalid object storage request")
	ErrUnsupported         = errors.New("object storage operation is not supported")
	ErrConfiguration       = errors.New("object storage provider configuration requires attention")
	ErrObjectNotTaggable   = errors.New("the object version does not support tagging")
)

// Provider owns data operations for a single immutable backend placement.
// Implementations must never return provider credentials in errors or results.
// Provider credentials deliberately are not part of this interface: adding a
// provider's IAM adapter must not change the portable bucket/data service.
type Provider interface {
	CreateBucket(context.Context, string) error
	DeleteBucket(context.Context, string) error
	ListObjects(context.Context, string, string, string, int32) (ObjectPage, error)
	DeleteObject(context.Context, string, string) error
	Presign(context.Context, string, SignRequest) (SignedRequest, error)
	EnsureMultipartUpload(context.Context, string, MultipartCreateRequest) (string, error)
	PresignMultipartPart(context.Context, string, MultipartPartRequest) (SignedRequest, error)
	ListMultipartParts(context.Context, string, MultipartListPartsRequest) (MultipartPartsPage, error)
	CompleteMultipartUpload(context.Context, string, MultipartCompleteRequest) error
	AbortMultipartUpload(context.Context, string, MultipartAbortRequest) error
}

// ObjectReadPresigner is the optional provider capability used by Gregale's
// proxying data planes. Unlike customer-facing download URLs, these reads must
// preserve the object's stored response metadata (not force an attachment or
// application/octet-stream response).
type ObjectReadPresigner interface {
	PresignObjectRead(context.Context, string, string, string, int64) (SignedRequest, error)
}

// VersionReadPresigner keeps the native selector on Gregale's private URL.
// Successful version reads must return the selected native version header.
type VersionReadPresigner interface {
	PresignVersionRead(context.Context, string, string, string, string, bool, int64) (SignedRequest, error)
}

type ObjectVersionListRequest struct {
	Prefix, Delimiter, KeyMarker string
	ProviderVersionMarker        string `json:"-"`
	Limit                        int32
}

type ListedObjectVersion struct {
	Object
	ProviderVersionID string `json:"-"`
	IsLatest          bool
	DeleteMarker      bool
	StorageClass      string
}

type ObjectVersionListPage struct {
	Items                     []ListedObjectVersion
	CommonPrefixes            []string
	NextKeyMarker             string
	NextProviderVersionMarker string `json:"-"`
}

type ObjectVersionLister interface {
	ListObjectVersionPage(context.Context, string, ObjectVersionListRequest) (ObjectVersionListPage, error)
}

// PresignObjectRead keeps existing third-party providers source-compatible
// while allowing built-in providers to distinguish transparent proxy reads
// from customer-facing forced-download URLs.
func PresignObjectRead(ctx context.Context, provider Provider, bucket, method, key string, expiresIn int64) (SignedRequest, error) {
	if signer, ok := provider.(ObjectReadPresigner); ok {
		return signer.PresignObjectRead(ctx, bucket, method, key, expiresIn)
	}
	return provider.Presign(ctx, bucket, SignRequest{Method: method, Key: key, ExpiresIn: expiresIn})
}

// ObjectReader is an optional provider capability used by operator-owned
// access-log collectors and the API's verified job artifact paths. It is
// deliberately separate from Provider so a storage driver does not have to
// expose raw object bodies merely to support usage accounting.
type ObjectReader interface {
	ReadObject(context.Context, string, string) (io.ReadCloser, error)
}

// ObjectWriter is the narrow capability used by policy-controlled upload
// routes. It is deliberately optional, like ObjectReader, so a provider can
// ship the read/list surface before it has a safe streaming write path.
// Implementations must consume at most the declared size and must not buffer
// the complete request in memory.
type ObjectWriter interface {
	WriteObject(context.Context, string, string, io.Reader, int64, ObjectMetadata) (UploadResult, error)
}

// ObjectWriteConfirmer requires the exact private receipt, size and a valid ETag.
// Absence or elapsed time cannot prove settlement.
type ObjectWriteConfirmer interface {
	ConfirmTrackedObject(context.Context, string, string, string, int64) (UploadResult, error)
}

// HistoricalObjectWriteConfirmer probes one bounded page of retained native
// versions after current-object proof is absent. BeforeRequest must durably
// record each provider attempt before dispatch. Only an exact positive proof
// returns nil error; a completed sweep never proves failure.
type HistoricalObjectWriteConfirmer interface {
	ConfirmTrackedObjectHistory(context.Context, string, ObjectHistoryProofRequest) (ObjectHistoryProofPage, error)
}

type ObjectHistoryProofRequest struct {
	Key, Receipt, Cursor string
	SizeBytes            int64
	MultipartSession     bool `json:"-"`
	BeforeRequest        func(context.Context) error
}

// Cursor and native identities are private recovery data, never customer IDs.
// On a failed page Cursor remains unchanged; after a complete sweep it resets.
type ObjectHistoryProofPage struct {
	UploadResult
	Cursor           string `json:"-"`
	VersionsObserved bool   `json:"-"`
}

// TrackedObjectPresigner binds a private receipt to a gateway-owned PUT. The
// signed capability must stay inside Gregale and be used for one attempt only.
type TrackedObjectPresigner interface {
	ObjectWriteConfirmer
	PresignTrackedPut(context.Context, string, SignRequest, ObjectWriteConditions, string) (SignedRequest, error)
}

// TrackedObjectWriter binds a private receipt to exactly one provider write
// attempt. It must not retry writes after dispatch. ErrWriteRejected proves
// that no object was committed; all other errors are uncertain. Confirmation
// requires that receipt, exact size and a valid ETag on the stored object.
// Absence or elapsed time never proves settlement.
type TrackedObjectWriter interface {
	ObjectWriteConfirmer
	WriteTrackedObject(context.Context, string, string, string, io.Reader, int64, ObjectMetadata) (UploadResult, error)
}

type UploadResult struct {
	ETag              string
	ProviderVersionID string `json:"-"`
}

type Object struct {
	Key          string    `json:"key"`
	ETag         string    `json:"etag,omitempty"`
	Size         int64     `json:"size_bytes"`
	LastModified time.Time `json:"last_modified"`
}

type ObjectPage struct {
	Items          []Object `json:"items"`
	CommonPrefixes []string `json:"common_prefixes,omitempty"`
	NextCursor     string   `json:"next_cursor,omitempty"`
}

// ObjectVersionInventoryProvider lists every retained data version and delete
// marker for authoritative accounting. Native identities and cursors remain
// private; each call performs one bounded provider request without SDK retries.
type ObjectVersionInventoryProvider interface {
	ListObjectVersions(context.Context, string, string, int32) (ObjectVersionsPage, error)
}

type ObjectVersionInventoryEntry struct {
	Key               string
	ProviderVersionID string `json:"-"`
	SizeBytes         int64
	DeleteMarker      bool
}

type ObjectVersionsPage struct {
	Items      []ObjectVersionInventoryEntry
	NextCursor string `json:"-"`
}

// DelimitedObjectLister is an optional provider capability for S3 directory
// views. Keeping it separate preserves the small inventory/listing contract
// used by accounting and older drivers.
type DelimitedObjectLister interface {
	ListObjectsDelimited(context.Context, string, string, string, string, int32) (ObjectPage, error)
}

type ObjectListRequest struct {
	Prefix, Delimiter, Cursor, StartAfter string
	Limit                                 int32
}

// ObjectV2Lister preserves the S3 start-after boundary without scanning an
// entire bucket in the gateway. Older drivers explicitly decline this option.
type ObjectV2Lister interface {
	ListObjectsV2(context.Context, string, ObjectListRequest) (ObjectPage, error)
}

type ObjectWriteConditions = api.ObjectWriteConditions

// ConditionalObjectPresigner must bind the condition into the provider's
// atomic write. A HEAD followed by an unconditional PUT is not equivalent.
type ConditionalObjectPresigner interface {
	PresignConditionalPut(context.Context, string, SignRequest, ObjectWriteConditions) (SignedRequest, error)
}

// ConditionalMultipartCompleter must enforce conditions in the provider's
// atomic completion. Recovery must replay the identical conditions.
type ConditionalMultipartCompleter interface {
	CompleteConditionalMultipartUpload(context.Context, string, MultipartCompleteRequest, ObjectWriteConditions) error
}

// MultipartResultCompleter returns the actual committed identity or a bounded
// recovery cursor. A missing historical proof never proves write failure.
type MultipartResultCompleter interface {
	CompleteMultipartWithResult(context.Context, string, MultipartCompleteRequest, ObjectWriteConditions) (MultipartCompletionResult, error)
}

type MultipartCompletionResult struct {
	UploadResult
	RecoveryCursor   string `json:"-"`
	VersionsObserved bool   `json:"-"`
}

func CompleteMultipartWithResult(ctx context.Context, p Provider, bucket string, r MultipartCompleteRequest, c ObjectWriteConditions) (MultipartCompletionResult, error) {
	if !c.Valid() {
		return MultipartCompletionResult{}, ErrInvalid
	}
	if completer, ok := p.(MultipartResultCompleter); ok {
		return completer.CompleteMultipartWithResult(ctx, bucket, r, c)
	}
	if r.BeforeRequest != nil {
		if err := r.BeforeRequest(ctx); err != nil {
			return MultipartCompletionResult{}, err
		}
	}
	return MultipartCompletionResult{}, CompleteMultipart(ctx, p, bucket, r, c)
}

func CompleteMultipart(ctx context.Context, p Provider, bucket string, r MultipartCompleteRequest, c ObjectWriteConditions) error {
	if !c.Valid() {
		return ErrInvalid
	}
	if c.Empty() {
		return p.CompleteMultipartUpload(ctx, bucket, r)
	}
	completer, ok := p.(ConditionalMultipartCompleter)
	if !ok {
		return ErrUnsupported
	}
	return completer.CompleteConditionalMultipartUpload(ctx, bucket, r, c)
}

func MultipartCompletionFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrPreconditionFailed):
		return "precondition_failed"
	case errors.Is(err, ErrConditionalConflict):
		return "conditional_conflict"
	case errors.Is(err, ErrConditionalNotFound):
		return "conditional_not_found"
	default:
		return ""
	}
}

type ObjectChecksumReadPresigner interface {
	PresignChecksumRead(context.Context, string, string, string, int64) (SignedRequest, error)
}

// ObjectMetadata contains the portable HTTP metadata that S3 CopyObject can
// preserve or replace. Provider-specific headers and ACLs deliberately stay
// outside this contract.
type ObjectMetadata struct {
	CacheControl       string            `json:"cache_control,omitempty"`
	ContentDisposition string            `json:"content_disposition,omitempty"`
	ContentEncoding    string            `json:"content_encoding,omitempty"`
	ContentLanguage    string            `json:"content_language,omitempty"`
	ContentType        string            `json:"content_type,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
}

type CopyObjectRequest struct {
	SourceKey               string
	SourceProviderVersionID string `json:"-"`
	DestinationKey          string
	MetadataDirective       string
	TaggingDirective        string
	Metadata                ObjectMetadata
}

type CopyObjectResult struct {
	ProviderVersionID string `json:"-"`
	ETag              string
	LastModified      time.Time
}

// ObjectCopier is an optional provider capability for the branded S3
// CopyObject operation. It is intentionally separate so a custom driver can
// opt in without weakening the basic Provider contract.
type ObjectCopier interface {
	CopyObject(context.Context, string, CopyObjectRequest) (CopyObjectResult, error)
}

// CopySourceSnapshot captures the source before capacity admission. The copy
// must select its immutable version or atomically require its ETag; metadata
// COPY uses this captured metadata.
type CopySourceSnapshot struct {
	SizeBytes int64
	ETag      string
	Metadata  ObjectMetadata
	Expires   *time.Time
	// A non-null native version is immutable. It remains private and binds
	// both metadata admission and the provider copy to the inspected object.
	ProviderVersionID string `json:"-"`
}

// TrackedObjectCopier must issue one copy with a fresh private receipt and the
// measured source condition. Only ErrWriteRejected proves no destination write.
type TrackedObjectCopier interface {
	ObjectWriteConfirmer
	SnapshotCopySource(context.Context, string, string) (CopySourceSnapshot, error)
	CopyTrackedObject(context.Context, string, string, CopyObjectRequest, CopySourceSnapshot) (CopyObjectResult, error)
}

// VersionedTrackedObjectCopier inspects the exact native selector before
// admission. CopyTrackedObject must retain that selector, including null.
type VersionedTrackedObjectCopier interface {
	TrackedObjectCopier
	SnapshotVersionCopySource(context.Context, string, string, string) (CopySourceSnapshot, error)
}

// ConditionalTrackedObjectCopier also enforces customer source conditions.
// Older copy adapters must decline these conditions instead of ignoring them.
type ConditionalTrackedObjectCopier interface {
	TrackedObjectCopier
	CopyConditionalTrackedObject(context.Context, string, string, CopyObjectRequest, CopySourceSnapshot, CopySourceConditions) (CopyObjectResult, error)
}

// DateConditionalTrackedObjectCopier explicitly opts into atomic date
// predicates without changing customer precedence. Older adapters cannot
// ignore dates. Independently restrictive dates require an immutable source.
type DateConditionalTrackedObjectCopier interface {
	ConditionalTrackedObjectCopier
	CopyDateConditionalTrackedObject(context.Context, string, string, CopyObjectRequest, CopySourceSnapshot, CopySourceConditions) (CopyObjectResult, error)
}

// MultipartPartCopier copies a measured source (or its inclusive byte range)
// into an existing upload with an atomic source identity fence and one attempt.
// Only ErrWriteRejected proves that the part write did not take place.
type MultipartPartCopier interface {
	SnapshotMultipartCopySource(context.Context, string, string) (CopySourceSnapshot, error)
	CopyMultipartPart(context.Context, string, MultipartPartCopyRequest, CopySourceSnapshot) (CopyObjectResult, error)
}

type VersionedMultipartPartCopier interface {
	MultipartPartCopier
	SnapshotVersionMultipartCopySource(context.Context, string, string, string) (CopySourceSnapshot, error)
}

type DateConditionalMultipartPartCopier interface {
	MultipartPartCopier
	CopyDateConditionalMultipartPart(context.Context, string, MultipartPartCopyRequest, CopySourceSnapshot) (CopyObjectResult, error)
}

type MultipartPartCopyRequest struct {
	SourceKey, Key, ProviderUploadID string
	SourceProviderVersionID          string `json:"-"`
	PartNumber                       int32
	Range                            *CopySourceRange
	Conditions                       CopySourceConditions
}

// CrossBucketObjectCopier is the optional provider capability used when an
// environment clone needs an isolated bucket. Providers must copy server-side
// and preserve the same metadata and tag directives as CopyObject.
type CrossBucketObjectCopier interface {
	CopyObjectBetweenBuckets(context.Context, string, string, CopyObjectRequest) (CopyObjectResult, error)
}

// ObjectSizer lets the gateway reserve the source object's bytes before a
// server-side copy. Drivers that cannot cheaply inspect an object may omit it;
// usage reconciliation remains authoritative in that case.
type ObjectSizer interface {
	ObjectSize(context.Context, string, string) (int64, error)
}

// ObjectTagger is an optional provider capability for the S3 object-tagging
// subresource. Providers that lack a native tag API may implement this using
// an isolated metadata representation, but must preserve the same limits and
// semantics at the Gregale boundary.
type ObjectTagger interface {
	GetObjectTags(context.Context, string, string) (map[string]string, error)
	PutObjectTags(context.Context, string, string, map[string]string) error
	DeleteObjectTags(context.Context, string, string) error
}

// ObjectVersionTagger changes tags in place without creating a data version or
// replacing Gregale's private write-completion metadata. An empty selector
// addresses the current object; null and native version IDs select a version.
type ObjectVersionTagger interface {
	GetObjectVersionTags(context.Context, string, string, string) (ObjectTaggingResult, error)
	PutObjectVersionTags(context.Context, string, string, string, map[string]string) (ObjectTaggingResult, error)
	DeleteObjectVersionTags(context.Context, string, string, string) (ObjectTaggingResult, error)
}

type ObjectTaggingResult struct {
	Tags              map[string]string
	ProviderVersionID string `json:"-"`
}

// PUT sizes are signed, not merely advisory client-side limits.
type SignRequest api.ObjectSignRequest

func (r SignRequest) Validate(maxBytes int64) error {
	if !ValidKey(r.Key) || (r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPut) || r.ExpiresIn < 0 || r.ExpiresIn > 900 {
		return ErrInvalid
	}
	if r.Method == http.MethodPut && (r.SizeBytes == nil || *r.SizeBytes < 0 || *r.SizeBytes > maxBytes) {
		return ErrInvalid
	}
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && (r.SizeBytes != nil || r.ContentType != "" || r.CacheControl != "" || r.ContentDisposition != "" || r.ContentEncoding != "" || r.ContentLanguage != "" || len(r.Metadata) != 0 || len(r.Tags) != 0) {
		return ErrInvalid
	}
	return ValidateObjectMetadata(ObjectMetadata{
		CacheControl: r.CacheControl, ContentDisposition: r.ContentDisposition, ContentEncoding: r.ContentEncoding,
		ContentLanguage: r.ContentLanguage, ContentType: r.ContentType, Metadata: r.Metadata, Tags: r.Tags,
	})
}

func ValidKey(key string) bool {
	if len(key) == 0 || len(key) > 1024 || !utf8.ValidString(key) {
		return false
	}
	for _, c := range key {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

type SignedRequest = api.ObjectSignedRequest

// MultipartCreateRequest contains only provider-facing data. SessionID is
// persisted as object metadata so a completion response lost between the
// provider and Gregale can be verified without exposing its upload ID.
type MultipartCreateRequest struct {
	SessionID string
	Key       string
	SizeBytes int64
	Metadata  ObjectMetadata
}

type MultipartPartRequest struct {
	Key              string
	ProviderUploadID string
	PartNumber       int32
	SizeBytes        int64
	ExpiresIn        int64
}

// MultipartListPartsRequest asks a provider for the parts that have actually
// reached the upstream upload. PartNumberMarker is zero for the first page;
// providers must not expose their native upload identity in the response.
type MultipartListPartsRequest struct {
	Key              string
	ProviderUploadID string
	PartNumberMarker int32
	Limit            int32
}

type MultipartPart struct {
	PartNumber   int32
	ETag         string
	SizeBytes    int64
	LastModified time.Time
}

type MultipartPartsPage struct {
	Items                []MultipartPart
	NextPartNumberMarker int32
}

type CompletedPart struct {
	PartNumber int32
	ETag       string
}

type MultipartCompleteRequest struct {
	Recovering       bool                        `json:"-"`
	RecoveryCursor   string                      `json:"-"`
	BeforeRequest    func(context.Context) error `json:"-"`
	SessionID        string
	Key              string
	ProviderUploadID string
	SizeBytes        int64
	Parts            []CompletedPart
}

type MultipartAbortRequest struct {
	Key              string
	ProviderUploadID string
}

func ValidateContentType(contentType string) error {
	if len(contentType) > 255 {
		return ErrInvalid
	}
	for _, c := range contentType {
		if c < 32 || c == 127 {
			return ErrInvalid
		}
	}
	return nil
}

const (
	maxObjectMetadataEntries = 90
	maxObjectMetadataKey     = 128
	maxObjectMetadataValue   = 2048
	maxObjectTags            = api.MaxObjectTags
	maxObjectTagKey          = api.MaxObjectTagKeyBytes
	maxObjectTagValue        = api.MaxObjectTagValueBytes
	maxObjectTaggingBytes    = api.MaxObjectTaggingBytes
	// ReservedObjectTagsMetadataKey is used only by providers without a native
	// object-tagging API (currently the GCS adapter). It never crosses the
	// branded S3 response boundary as ordinary user metadata.
	ReservedObjectTagsMetadataKey = "gregale-s3-tags"
	// ReservedMultipartSessionMetadataKey fences the provider-private recovery
	// marker written when Gregale initiates a multipart upload.
	ReservedMultipartSessionMetadataKey = "gregale-upload-id"
	ReservedUploadReceiptMetadataKey    = "gregale-upload-receipt"
)

// ValidateObjectMetadata applies the portable S3 metadata/tag limits before
// a provider-specific request is built. Values are intentionally kept to
// printable UTF-8 strings because they become signed HTTP headers or XML.
func ValidateObjectMetadata(metadata ObjectMetadata) error {
	for _, value := range []string{metadata.CacheControl, metadata.ContentDisposition, metadata.ContentEncoding, metadata.ContentLanguage, metadata.ContentType} {
		if err := ValidateContentType(value); err != nil {
			return err
		}
	}
	if len(metadata.Metadata) > maxObjectMetadataEntries {
		return ErrInvalid
	}
	for key, value := range metadata.Metadata {
		if key == "" || len(key) > maxObjectMetadataKey || len(value) > maxObjectMetadataValue || !utf8.ValidString(key) || !utf8.ValidString(value) || strings.ContainsAny(key, "\r\n") || strings.ContainsAny(value, "\r\n") || strings.EqualFold(key, ReservedObjectTagsMetadataKey) || strings.EqualFold(key, ReservedMultipartSessionMetadataKey) || strings.EqualFold(key, ReservedUploadReceiptMetadataKey) {
			return ErrInvalid
		}
	}
	if len(metadata.Tags) > maxObjectTags {
		return ErrInvalid
	}
	total := 0
	for key, value := range metadata.Tags {
		if key == "" || len(key) > maxObjectTagKey || len(value) > maxObjectTagValue || !validTagText(key) || !validTagText(value) {
			return ErrInvalid
		}
		total += len(key) + len(value) + 2
	}
	if total > maxObjectTaggingBytes {
		return ErrInvalid
	}
	return nil
}

type Backend struct {
	AllowedOrigins   []string
	ID               string
	Region           string
	Namespace        string
	Fingerprint      string
	Provider         Provider
	UsageReportsPath string
	Usage            UsageConfig
}

func fingerprint(c BackendConfig) string {
	endpoint := c.Endpoint
	if c.Driver == "gcs" && endpoint == "" {
		endpoint = gcsDefaultEndpoint
	}
	identity := endpoint + "\x00" + c.S3Region + "\x00" + c.Namespace + "\x00" + c.Driver + "\x00" + c.Region
	// Keep the established S3 identity byte-for-byte stable. GCS has no S3
	// signing region, so its immutable placement adds the native location and
	// storage class instead. Credentials are deliberately excluded for both.
	if c.Driver == "gcs" {
		storageClass := c.GCSStorageClass
		if storageClass == "" {
			storageClass = "STANDARD"
		}
		identity += "\x00" + c.GCSLocation + "\x00" + storageClass
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}
