package api

import "time"

type HostingRetentionRules struct {
	PendingAgeSeconds int64 `json:"pending_age_seconds"`
	StagedAgeSeconds  int64 `json:"staged_age_seconds"`
}
type HostingRetentionPolicy struct {
	HostingRetentionRules
	SiteID    string     `json:"site_id"`
	Revision  int64      `json:"revision"`
	Enabled   bool       `json:"enabled"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}
type SetHostingRetentionRequest struct {
	HostingRetentionRules
	ExpectedRevision int64 `json:"expected_revision"`
	Enabled          bool  `json:"enabled"`
}
type HostingCleanupEntry struct {
	ReleaseID     string     `json:"release_id"`
	State         string     `json:"state"`
	Eligible      bool       `json:"eligible"`
	Reason        string     `json:"reason"`
	ReservedBytes int64      `json:"reserved_bytes"`
	EligibleAt    *time.Time `json:"eligible_at,omitempty"`
}
type HostingCleanupPlan struct {
	Policy                   HostingRetentionPolicy `json:"policy"`
	Rules                    HostingRetentionRules  `json:"rules"`
	Proposed                 bool                   `json:"proposed"`
	EvaluatedAt              time.Time              `json:"evaluated_at"`
	Entries                  []HostingCleanupEntry  `json:"entries"`
	ReclaimableReservedBytes int64                  `json:"reclaimable_reserved_bytes"`
	RetiringReservedBytes    int64                  `json:"retiring_reserved_bytes"`
}
