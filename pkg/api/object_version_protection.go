package api

import "time"

// ObjectVersionProtection contains only owned selectors. Provider identities and
// worker leases are deliberately excluded from the customer inspection surface.
type ObjectVersionProtection struct {
	ID            string                  `json:"id"`
	BucketID      string                  `json:"bucket_id"`
	Key           string                  `json:"key"`
	VersionID     string                  `json:"version_id"`
	Kind          string                  `json:"kind"`
	State         string                  `json:"state"`
	Retention     *ObjectVersionRetention `json:"retention,omitempty"`
	LegalHold     *ObjectVersionLegalHold `json:"legal_hold,omitempty"`
	LastErrorCode string                  `json:"last_error_code,omitempty"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
}

type ObjectVersionRetentionRequest struct {
	ID        string                 `json:"id"`
	Retention ObjectVersionRetention `json:"retention"`
}
type ObjectVersionLegalHoldRequest struct {
	ID        string                 `json:"id"`
	LegalHold ObjectVersionLegalHold `json:"legal_hold"`
}
type ObjectVersionRetentionResult struct {
	VersionID string                 `json:"version_id"`
	Retention ObjectVersionRetention `json:"retention"`
}
type ObjectVersionLegalHoldResult struct {
	VersionID string                 `json:"version_id"`
	LegalHold ObjectVersionLegalHold `json:"legal_hold"`
}
