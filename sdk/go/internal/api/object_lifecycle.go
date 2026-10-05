package api

import "time"

type ObjectBucketLifecycleRequest struct {
	Rules []ObjectLifecycleRule `json:"rules"`
}

type ObjectLifecycleFilter struct {
	Prefix string            `json:"prefix,omitempty"`
	Tags   map[string]string `json:"tags,omitempty"`
}

type ObjectLifecycleExpiration struct {
	Days                      *int32     `json:"days,omitempty"`
	Date                      *time.Time `json:"date,omitempty"`
	ExpiredObjectDeleteMarker *bool      `json:"expired_object_delete_marker,omitempty"`
}

type ObjectLifecycleNoncurrentExpiration struct {
	NoncurrentDays          int32  `json:"noncurrent_days"`
	NewerNoncurrentVersions *int32 `json:"newer_noncurrent_versions,omitempty"`
}

type ObjectLifecycleRule struct {
	ID                           string                               `json:"id"`
	Status                       string                               `json:"status"`
	Filter                       ObjectLifecycleFilter                `json:"filter"`
	Expiration                   *ObjectLifecycleExpiration           `json:"expiration,omitempty"`
	NoncurrentVersionExpiration  *ObjectLifecycleNoncurrentExpiration `json:"noncurrent_version_expiration,omitempty"`
	AbortIncompleteMultipartDays *int32                               `json:"abort_incomplete_multipart_days,omitempty"`
}

type ObjectBucketLifecycle struct {
	BucketID  string                `json:"bucket_id"`
	Revision  int64                 `json:"revision"`
	Rules     []ObjectLifecycleRule `json:"rules"`
	UpdatedAt time.Time             `json:"updated_at"`
}

type ObjectLifecycleScan struct {
	ID             string     `json:"id"`
	BucketID       string     `json:"bucket_id"`
	Revision       int64      `json:"revision"`
	State          string     `json:"state"`
	Phase          string     `json:"phase"`
	ScannedKeys    int64      `json:"scanned_keys"`
	ScannedUploads int64      `json:"scanned_uploads"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}
