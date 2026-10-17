package api

import "time"

// HostingPreview binds a private hostname to exactly one verified release.
// Publication is a provider receipt, not a claim that every edge has refreshed.
type HostingPreview struct {
	ID                  string     `json:"id"`
	AccountID           string     `json:"account_id"`
	SiteID              string     `json:"site_id"`
	ReleaseID           string     `json:"release_id"`
	Hostname            string     `json:"hostname"`
	OriginID            string     `json:"origin_id"`
	ExpiresAt           time.Time  `json:"expires_at"`
	Revoked             bool       `json:"revoked"`
	Generation          int64      `json:"generation"`
	PublishedGeneration int64      `json:"published_generation"`
	PublishedAt         *time.Time `json:"published_at,omitempty"`
	NextAttemptAt       time.Time  `json:"next_attempt_at"`
	LastErrorCode       string     `json:"last_error_code,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}
type CreateHostingPreviewRequest struct {
	ReleaseID  string `json:"release_id"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
}
type RevokeHostingPreviewRequest struct {
	ExpectedGeneration int64 `json:"expected_generation"`
}
