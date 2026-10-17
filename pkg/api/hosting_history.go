package api

import "time"

// HostingReleaseProvenance is caller-provided context, not a verified attestation.
// It is never included in the published manifest or used for routing/authentication.
type HostingReleaseProvenance struct {
	Repository  string `json:"repository,omitempty"`
	CommitSHA   string `json:"commit_sha,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Description string `json:"description,omitempty"`
}

// CreateHostingReleaseRequest preserves the original manifest request shape.
type CreateHostingReleaseRequest struct {
	HostingManifest
	Provenance *HostingReleaseProvenance `json:"provenance,omitempty"`
}
type HostingPromotionActor struct {
	AccountID string `json:"account_id"`
	KeyID     string `json:"key_id,omitempty"`
}
type HostingPromotionEvent struct {
	SiteID            string                    `json:"site_id"`
	Revision          int64                     `json:"revision"`
	PreviousRevision  int64                     `json:"previous_revision"`
	ReleaseID         string                    `json:"release_id"`
	PreviousReleaseID string                    `json:"previous_release_id,omitempty"`
	Actor             HostingPromotionActor     `json:"actor"`
	AcceptedAt        time.Time                 `json:"accepted_at"`
	Provenance        *HostingReleaseProvenance `json:"provenance,omitempty"`
}
type HostingHistoryPage struct {
	Events             []HostingPromotionEvent `json:"events"`
	NextBeforeRevision int64                   `json:"next_before_revision,omitempty"`
	RetentionLimit     int                     `json:"retention_limit"`
}
