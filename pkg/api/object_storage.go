package api

import (
	"strings"
	"time"
	"unicode/utf8"
)

// ObjectBucket is a customer-owned logical bucket. Physical upstream names,
// operator identities, credentials and operation leases are not public fields.
type ObjectBucket struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Scope     string    `json:"scope"`
	Region    string    `json:"region"`
	State     string    `json:"state"`
	Public    bool      `json:"public"`
	ServeAt   string    `json:"serve_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Retry is set while a create or delete has failed at least once and
	// Gregale is retrying it, so a bucket stuck in provisioning or deleting
	// says why and when the next attempt runs.
	Retry *ObjectBucketRetry `json:"retry,omitempty"`
}

// ObjectBucketRetry explains a bucket operation Gregale is retrying. Reason
// is one of the ObjectBucketRetry* codes; Detail is the matching sentence.
type ObjectBucketRetry struct {
	Reason   string    `json:"reason"`
	Detail   string    `json:"detail"`
	Attempts int32     `json:"attempts"`
	RetryAt  time.Time `json:"retry_at"`
}

// Bucket retry reasons, the bounded codes apid records for a failed create or
// delete. They never carry provider messages.
const (
	ObjectBucketRetryTemporary      = "temporary"
	ObjectBucketRetryConflict       = "conflict"
	ObjectBucketRetryConfiguration  = "configuration"
	ObjectBucketRetryInvalid        = "invalid"
	ObjectBucketRetryProtected      = "protected"
	ObjectBucketRetryCleanupPending = "cleanup_pending"
)

// ObjectBucketRetryDetail is the customer-facing sentence for a retry reason.
func ObjectBucketRetryDetail(reason string) string {
	switch reason {
	case ObjectBucketRetryTemporary:
		return "The storage provider did not complete the request; Gregale retries automatically."
	case ObjectBucketRetryConflict:
		return "The storage provider reported a conflicting operation on this bucket; Gregale retries automatically."
	case ObjectBucketRetryConfiguration:
		return "The storage provider is refusing Gregale's credentials. Operators have been alerted; Gregale retries every hour."
	case ObjectBucketRetryInvalid:
		return "The storage provider rejected the request; Gregale retries every hour. Contact support if the bucket stays stuck."
	case ObjectBucketRetryProtected:
		return "The bucket still holds objects under retention or a legal hold; deletion continues after they expire or the hold is removed."
	case ObjectBucketRetryCleanupPending:
		return "Gregale is still removing the bucket's objects before deleting it."
	default:
		return "Gregale is retrying this bucket operation."
	}
}

type ObjectBucketList struct {
	Items                  []ObjectBucket `json:"items"`
	Enabled                bool           `json:"enabled"`
	Regions                []string       `json:"regions"`
	DefaultRegion          string         `json:"default_region"`
	MaxUploadBytes         int64          `json:"max_upload_bytes"`
	MaxBucketsPerApp       int            `json:"max_buckets_per_app"`
	MaxSinglePutBytes      int64          `json:"max_single_put_bytes,omitempty"`
	MaxPartBytes           int64          `json:"max_part_bytes,omitempty"`
	TransferTimeoutSeconds int64          `json:"transfer_timeout_seconds,omitempty"`
	UploadProfile          string         `json:"upload_profile,omitempty"`
}

type ObjectSignRequest struct {
	Method             string                 `json:"method"`
	Key                string                 `json:"key"`
	VersionID          string                 `json:"version_id,omitempty"`
	IfMatch            string                 `json:"if_match,omitempty"`
	IfNoneMatch        string                 `json:"if_none_match,omitempty"`
	ExpiresIn          int64                  `json:"expires_in,omitempty"`
	SizeBytes          *int64                 `json:"size_bytes,omitempty"`
	ContentType        string                 `json:"content_type,omitempty"`
	CacheControl       string                 `json:"cache_control,omitempty"`
	ContentDisposition string                 `json:"content_disposition,omitempty"`
	ContentEncoding    string                 `json:"content_encoding,omitempty"`
	ContentLanguage    string                 `json:"content_language,omitempty"`
	Metadata           map[string]string      `json:"metadata,omitempty"`
	Tags               map[string]string      `json:"tags,omitempty"`
	Encryption         *ObjectEncryption      `json:"encryption,omitempty"`
	Protection         *ObjectWriteProtection `json:"protection,omitempty"`
}

type ObjectSignedRequest struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
	UploadID  string            `json:"upload_id,omitempty"`
}

// ObjectMultipartUpload is Gregale's durable upload session. The upstream S3
// upload ID is intentionally never exposed.
type ObjectMultipartUpload struct {
	ID                  string            `json:"id"`
	Key                 string            `json:"key"`
	SizeBytes           int64             `json:"size_bytes"`
	PartSizeBytes       int64             `json:"part_size_bytes"`
	PartCount           int32             `json:"part_count"`
	ContentType         string            `json:"content_type"`
	State               string            `json:"state"`
	CompletionErrorCode string            `json:"completion_error_code,omitempty"`
	ETag                string            `json:"etag,omitempty"`
	VersionID           string            `json:"version_id,omitempty"`
	Encryption          *ObjectEncryption `json:"encryption,omitempty"`
	ExpiresAt           time.Time         `json:"expires_at"`
	CreatedAt           time.Time         `json:"created_at"`
}

type ObjectMultipartUploadList struct {
	Items      []ObjectMultipartUpload `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

type CreateObjectMultipartUploadRequest struct {
	Key         string                 `json:"key"`
	SizeBytes   int64                  `json:"size_bytes"`
	ContentType string                 `json:"content_type,omitempty"`
	Encryption  *ObjectEncryption      `json:"encryption,omitempty"`
	Protection  *ObjectWriteProtection `json:"protection,omitempty"`
}

type ObjectMultipartPartSignRequest struct {
	ExpiresIn int64 `json:"expires_in,omitempty"`
}

type ObjectMultipartCompletedPart struct {
	PartNumber int32  `json:"part_number"`
	ETag       string `json:"etag"`
}

// ObjectMultipartPart is provider-confirmed state for an in-progress upload.
// The provider upload ID remains private; clients use the ETag when completing
// the Gregale session.
type ObjectMultipartPart struct {
	PartNumber   int32     `json:"part_number"`
	ETag         string    `json:"etag"`
	SizeBytes    int64     `json:"size_bytes"`
	LastModified time.Time `json:"last_modified"`
}

type ObjectMultipartPartList struct {
	Items                []ObjectMultipartPart `json:"items"`
	NextPartNumberMarker int32                 `json:"next_part_number_marker,omitempty"`
}

type CompleteObjectMultipartUploadRequest struct {
	Parts []ObjectMultipartCompletedPart `json:"parts"`
}

const (
	ObjectBucketPermissionRead      = "read"
	ObjectBucketPermissionWrite     = "write"
	ObjectBucketPermissionReadWrite = "read_write"
)

// ObjectBucketAccessGrant binds one Gregale API key to one logical bucket.
// No provider credential or physical bucket identifier is exposed.
type ObjectBucketAccessGrant struct {
	KeyID      string    `json:"key_id"`
	KeyLabel   string    `json:"key_label"`
	KeyStatus  string    `json:"key_status"`
	Permission string    `json:"permission"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type ObjectBucketAccessGrantList struct {
	Items []ObjectBucketAccessGrant `json:"items"`
}

// ObjectUploadRoute is the customer-facing declaration for an authenticated
// upload endpoint. The route is served by Gregale's edge and never wakes the
// application deployment.
type ObjectUploadRoute struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	BucketID            string            `json:"bucket_id"`
	KeyPrefix           string            `json:"key_prefix,omitempty"`
	MaxBytes            int64             `json:"max_bytes"`
	AllowedContentTypes []string          `json:"allowed_content_types,omitempty"`
	Enabled             bool              `json:"enabled"`
	Encryption          *ObjectEncryption `json:"encryption,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

type ObjectUploadRouteList struct {
	Items []ObjectUploadRoute `json:"items"`
}

type CreateObjectUploadRouteRequest struct {
	Name                string            `json:"name"`
	BucketID            string            `json:"bucket_id"`
	KeyPrefix           string            `json:"key_prefix,omitempty"`
	MaxBytes            int64             `json:"max_bytes"`
	AllowedContentTypes []string          `json:"allowed_content_types,omitempty"`
	Enabled             *bool             `json:"enabled,omitempty"`
	Encryption          *ObjectEncryption `json:"encryption,omitempty"`
}

type SetObjectBucketAccessGrantRequest struct {
	Permission string `json:"permission"`
}

const MaxObjectS3CredentialsPerBucket = 10

// ObjectS3Credential is a Gregale-issued, bucket-scoped S3 credential. The
// secret is returned only by the create endpoint and is never persisted in
// plaintext or included in list responses.
type ObjectS3Credential struct {
	ID          string     `json:"id"`
	BucketID    string     `json:"bucket_id"`
	AccessKeyID string     `json:"access_key_id"`
	Label       string     `json:"label"`
	Permission  string     `json:"permission"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type ObjectS3CredentialList struct {
	Items []ObjectS3Credential `json:"items"`
}

type CreateObjectS3CredentialRequest struct {
	Label      string `json:"label"`
	Permission string `json:"permission"`
}

// CreateObjectStorageComputeBindingRequest provisions a bucket-scoped S3
// credential and injects its provider-neutral connection settings into the
// app's sealed environment. Prefix is optional; when omitted the API derives
// a stable prefix from the logical bucket name.
type CreateObjectStorageComputeBindingRequest struct {
	Label      string `json:"label,omitempty"`
	Permission string `json:"permission"`
	Prefix     string `json:"prefix,omitempty"`
	// KeyPrefix limits the binding's key to object keys under it, for
	// example "segments/L0/" (ADR-20261106114500). It is a literal prefix,
	// not a pattern. Empty allows the whole bucket.
	KeyPrefix string `json:"key_prefix,omitempty"`
}

// ValidObjectKeyScope reports whether prefix can scope a binding
// (ADR-20261106114500): a literal key prefix of at most
// MaxObjectKeyScopeBytes that ends with "/", so "tenant1/" never reaches
// "tenant10/". It has no leading "/", no empty, "." or ".." segment, no
// control character and no pattern character (*, ?, [), so "v1/*/L0/" is
// refused instead of matching nothing. apid also requires a valid object key.
func ValidObjectKeyScope(prefix string) bool {
	if len(prefix) < 2 || len(prefix) > MaxObjectKeyScopeBytes || !utf8.ValidString(prefix) ||
		!strings.HasSuffix(prefix, "/") || strings.HasPrefix(prefix, "/") || strings.ContainsAny(prefix, "*?[") {
		return false
	}
	for _, r := range prefix {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, segment := range strings.Split(strings.TrimSuffix(prefix, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// ObjectStorageKeyScopesCapability releases key-scoped bindings to an
// account once its S3 gateway enforces them (ADR-20261106114500).
const ObjectStorageKeyScopesCapability = "object-storage-key-scopes"

// ObjectStorageComputeBindingSecretKeys names the sealed app secrets written
// for a compute binding. Values are never returned by this API.
type ObjectStorageComputeBindingSecretKeys struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	AddressingStyle string `json:"addressing_style"`
}

// ObjectStorageComputeBinding is the control-plane view of an app-to-bucket
// binding. The binding ID is also the managed credential ID, but callers must
// treat it as opaque.
type ObjectStorageComputeBinding struct {
	ID              string                                `json:"id"`
	BucketID        string                                `json:"bucket_id"`
	Scope           string                                `json:"scope"`
	Prefix          string                                `json:"prefix"`
	KeyPrefix       string                                `json:"key_prefix,omitempty"`
	Credential      ObjectS3Credential                    `json:"credential"`
	SecretKeys      ObjectStorageComputeBindingSecretKeys `json:"secret_keys"`
	RotationPending bool                                  `json:"rotation_pending,omitempty"`
}

type ObjectStorageComputeBindingList struct {
	Items []ObjectStorageComputeBinding `json:"items"`
}

// ObjectS3CredentialSecret is the one-time creation response. Endpoint and
// addressing metadata let callers configure an AWS SDK without learning
// which upstream provider stores the bucket.
type ObjectS3CredentialSecret struct {
	ObjectS3Credential
	SecretAccessKey string `json:"secret_access_key"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	AddressingStyle string `json:"addressing_style"`
}

// ObjectWriteConditions are atomic destination-write preconditions.
type ObjectWriteConditions struct {
	IfMatch     string
	IfNoneMatch string
}

func (c ObjectWriteConditions) Empty() bool { return c.IfMatch == "" && c.IfNoneMatch == "" }

func (c ObjectWriteConditions) Valid() bool {
	if len(c.IfMatch) > MaxObjectWriteETagBytes || c.IfMatch != "" && c.IfNoneMatch != "" || c.IfNoneMatch != "" && c.IfNoneMatch != "*" {
		return false
	}
	for _, b := range []byte(c.IfMatch) {
		if b < 32 || b == 127 {
			return false
		}
	}
	return true
}
