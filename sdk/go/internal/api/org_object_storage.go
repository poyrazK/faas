package api

import "time"

type CreateObjectBucketRequest struct {
	Name    string `json:"name"`
	Scope   string `json:"scope,omitempty"`
	Region  string `json:"region,omitempty"`
	Public  bool   `json:"public,omitempty"`
	ServeAt string `json:"serve_at,omitempty"`
}

type BucketObject struct {
	Key          string    `json:"key"`
	SizeBytes    int64     `json:"size_bytes"`
	LastModified time.Time `json:"last_modified"`
}

type BucketObjectPage struct {
	Items      []BucketObject `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

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

type ObjectS3CredentialSecret struct {
	ObjectS3Credential
	SecretAccessKey string `json:"secret_access_key"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	AddressingStyle string `json:"addressing_style"`
}

type CreateObjectS3CredentialRequest struct {
	Label      string `json:"label"`
	Permission string `json:"permission"`
}

type ObjectStorageComputeBinding struct {
	ID              string                                `json:"id"`
	BucketID        string                                `json:"bucket_id"`
	Scope           string                                `json:"scope"`
	Prefix          string                                `json:"prefix"`
	Credential      ObjectS3Credential                    `json:"credential"`
	SecretKeys      ObjectStorageComputeBindingSecretKeys `json:"secret_keys"`
	RotationPending bool                                  `json:"rotation_pending,omitempty"`
}

type ObjectStorageComputeBindingList struct {
	Items []ObjectStorageComputeBinding `json:"items"`
}

type CreateObjectStorageComputeBindingRequest struct {
	Label      string `json:"label,omitempty"`
	Permission string `json:"permission"`
	Prefix     string `json:"prefix,omitempty"`
}

type ObjectStorageComputeBindingSecretKeys struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	AddressingStyle string `json:"addressing_style"`
}
