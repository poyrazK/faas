package api

import "time"

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
}

type ObjectBucketList struct {
	Items            []ObjectBucket `json:"items"`
	Enabled          bool           `json:"enabled"`
	Regions          []string       `json:"regions"`
	DefaultRegion    string         `json:"default_region"`
	MaxUploadBytes   int64          `json:"max_upload_bytes"`
	MaxBucketsPerApp int            `json:"max_buckets_per_app"`
}

type ObjectSignRequest struct {
	Method             string            `json:"method"`
	Key                string            `json:"key"`
	ExpiresIn          int64             `json:"expires_in,omitempty"`
	SizeBytes          *int64            `json:"size_bytes,omitempty"`
	ContentType        string            `json:"content_type,omitempty"`
	CacheControl       string            `json:"cache_control,omitempty"`
	ContentDisposition string            `json:"content_disposition,omitempty"`
	ContentEncoding    string            `json:"content_encoding,omitempty"`
	ContentLanguage    string            `json:"content_language,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
}

type ObjectSignedRequest struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// ObjectMultipartUpload is Gregale's durable upload session. The upstream S3
// upload ID is intentionally never exposed.
type ObjectMultipartUpload struct {
	ID            string    `json:"id"`
	Key           string    `json:"key"`
	SizeBytes     int64     `json:"size_bytes"`
	PartSizeBytes int64     `json:"part_size_bytes"`
	PartCount     int32     `json:"part_count"`
	ContentType   string    `json:"content_type"`
	State         string    `json:"state"`
	ExpiresAt     time.Time `json:"expires_at"`
	CreatedAt     time.Time `json:"created_at"`
}

type ObjectMultipartUploadList struct {
	Items      []ObjectMultipartUpload `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

type CreateObjectMultipartUploadRequest struct {
	Key         string `json:"key"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentType string `json:"content_type,omitempty"`
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
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	BucketID            string    `json:"bucket_id"`
	KeyPrefix           string    `json:"key_prefix,omitempty"`
	MaxBytes            int64     `json:"max_bytes"`
	AllowedContentTypes []string  `json:"allowed_content_types,omitempty"`
	Enabled             bool      `json:"enabled"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type ObjectUploadRouteList struct {
	Items []ObjectUploadRoute `json:"items"`
}

type CreateObjectUploadRouteRequest struct {
	Name                string   `json:"name"`
	BucketID            string   `json:"bucket_id"`
	KeyPrefix           string   `json:"key_prefix,omitempty"`
	MaxBytes            int64    `json:"max_bytes"`
	AllowedContentTypes []string `json:"allowed_content_types,omitempty"`
	Enabled             *bool    `json:"enabled,omitempty"`
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
}

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
	ID         string                                `json:"id"`
	BucketID   string                                `json:"bucket_id"`
	Scope      string                                `json:"scope"`
	Prefix     string                                `json:"prefix"`
	Credential ObjectS3Credential                    `json:"credential"`
	SecretKeys ObjectStorageComputeBindingSecretKeys `json:"secret_keys"`
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
