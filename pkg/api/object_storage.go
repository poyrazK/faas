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
	Method      string `json:"method"`
	Key         string `json:"key"`
	ExpiresIn   int64  `json:"expires_in,omitempty"`
	SizeBytes   *int64 `json:"size_bytes,omitempty"`
	ContentType string `json:"content_type,omitempty"`
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

type SetObjectBucketAccessGrantRequest struct {
	Permission string `json:"permission"`
}
