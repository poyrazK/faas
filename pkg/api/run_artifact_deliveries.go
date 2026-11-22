package api

import "time"

// DeliverRunArtifactRequest binds a destination write to the exact output the
// caller observed. SHA256 accepts the Run receipt's sha256:<hex> representation.
type DeliverRunArtifactRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	ExecutionID    string `json:"execution_id"`
	ArtifactName   string `json:"artifact_name"`
	SizeBytes      int64  `json:"size_bytes"`
	SHA256         string `json:"sha256"`
	ContentType    string `json:"content_type"`
}

type RunArtifactObject struct {
	BucketID  string `json:"bucket_id"`
	Key       string `json:"key"`
	VersionID string `json:"version_id,omitempty"`
}

// RunArtifactDelivery is a storage receipt, not an execution receipt or a bearer
// capability. Object is present only after the write has settled successfully.
type RunArtifactDelivery struct {
	ID          string             `json:"id"`
	Status      string             `json:"status"`
	SizeBytes   int64              `json:"size_bytes"`
	ContentType string             `json:"content_type"`
	ErrorCode   string             `json:"error_code,omitempty"`
	ReceiptURL  string             `json:"receipt_url"`
	Object      *RunArtifactObject `json:"object,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
}
