package api

import "time"

// ObjectS3CopySource is a copy-only source grant on one destination credential.
type ObjectS3CopySource struct {
	SourceBucketID string    `json:"source_bucket_id"`
	Prefix         string    `json:"prefix"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type ObjectS3CopySourceList struct {
	Items []ObjectS3CopySource `json:"items"`
}
type SetObjectS3CopySourceRequest struct {
	Prefix string `json:"prefix"`
}
