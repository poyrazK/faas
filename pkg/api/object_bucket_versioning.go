package api

import "time"

type ObjectBucketVersioningRequest struct {
	Status string `json:"status"`
}

// ObservedStatus reports provider truth; DesiredStatus is durable intent. Writes
// remain fenced until State is ready, including after suspension.
type ObjectBucketVersioning struct {
	BucketID         string     `json:"bucket_id"`
	DesiredStatus    string     `json:"desired_status"`
	ObservedStatus   string     `json:"observed_status"`
	State            string     `json:"state"`
	Revision         int64      `json:"revision"`
	VersionsRequired bool       `json:"versions_required"`
	PropagationUntil *time.Time `json:"propagation_until,omitempty"`
	CapacityJobID    string     `json:"capacity_job_id,omitempty"`
	LastErrorCode    string     `json:"last_error_code,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
