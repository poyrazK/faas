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
	ErrUnavailable   = errors.New("object storage unavailable")
	ErrNotFound      = errors.New("object storage resource not found")
	ErrConflict      = errors.New("object storage resource conflict")
	ErrNotEmpty      = errors.New("bucket is not empty")
	ErrInvalid       = errors.New("invalid object storage request")
	ErrUnsupported   = errors.New("object storage operation is not supported")
	ErrConfiguration = errors.New("object storage provider configuration requires attention")
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

// ObjectReader is an optional provider capability used by operator-owned
// access-log collectors. It is deliberately separate from Provider so a
// storage driver does not have to expose raw object bodies to customer API
// code merely to support usage accounting.
type ObjectReader interface {
	ReadObject(context.Context, string, string) (io.ReadCloser, error)
}

type Object struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size_bytes"`
	LastModified time.Time `json:"last_modified"`
}

type ObjectPage struct {
	Items          []Object `json:"items"`
	CommonPrefixes []string `json:"common_prefixes,omitempty"`
	NextCursor     string   `json:"next_cursor,omitempty"`
}

// DelimitedObjectLister is an optional provider capability for S3 directory
// views. Keeping it separate preserves the small inventory/listing contract
// used by accounting and older drivers.
type DelimitedObjectLister interface {
	ListObjectsDelimited(context.Context, string, string, string, string, int32) (ObjectPage, error)
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
	SourceKey         string
	DestinationKey    string
	MetadataDirective string
	TaggingDirective  string
	Metadata          ObjectMetadata
}

type CopyObjectResult struct {
	ETag         string
	LastModified time.Time
}

// ObjectCopier is an optional provider capability for the branded S3
// CopyObject operation. It is intentionally separate so a custom driver can
// opt in without weakening the basic Provider contract.
type ObjectCopier interface {
	CopyObject(context.Context, string, CopyObjectRequest) (CopyObjectResult, error)
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
	SessionID   string
	Key         string
	SizeBytes   int64
	ContentType string
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
	maxObjectTags            = 10
	maxObjectTagKey          = 128
	maxObjectTagValue        = 256
	maxObjectTaggingBytes    = 8 << 10
	// ReservedObjectTagsMetadataKey is used only by providers without a native
	// object-tagging API (currently the GCS adapter). It never crosses the
	// branded S3 response boundary as ordinary user metadata.
	ReservedObjectTagsMetadataKey = "gregale-s3-tags"
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
		if key == "" || len(key) > maxObjectMetadataKey || len(value) > maxObjectMetadataValue || !utf8.ValidString(key) || !utf8.ValidString(value) || strings.ContainsAny(key, "\r\n") || strings.ContainsAny(value, "\r\n") || strings.EqualFold(key, ReservedObjectTagsMetadataKey) {
			return ErrInvalid
		}
	}
	if len(metadata.Tags) > maxObjectTags {
		return ErrInvalid
	}
	total := 0
	for key, value := range metadata.Tags {
		if key == "" || len(key) > maxObjectTagKey || len(value) > maxObjectTagValue || !utf8.ValidString(key) || !utf8.ValidString(value) || strings.ContainsAny(key, "\r\n") || strings.ContainsAny(value, "\r\n") {
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
