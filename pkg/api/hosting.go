package api

import "time"

const (
	HostingRouteErrorOrigin   = "origin_unavailable"
	HostingRouteErrorProvider = "publication_unavailable"
	HostingRouteErrorStale    = "stale_generation"
)

// HostingSiteStatus reports accepted intent and provider publication separately.
// Published routing is not a claim that every edge location has observed it.
type HostingSiteStatus struct {
	Site        HostingSite              `json:"site"`
	Publication HostingPublicationStatus `json:"publication"`
}

type HostingPublicationStatus struct {
	State               string     `json:"state"`
	Generation          int64      `json:"generation,omitempty"`
	PublishedGeneration int64      `json:"published_generation,omitempty"`
	PublishedAt         *time.Time `json:"published_at,omitempty"`
	PendingSince        *time.Time `json:"pending_since,omitempty"`
	NextAttemptAt       *time.Time `json:"next_attempt_at,omitempty"`
	LastAttemptAt       *time.Time `json:"last_attempt_at,omitempty"`
	RetryCount          int64      `json:"retry_count"`
	LastErrorCode       string     `json:"last_error_code,omitempty"`
}

// HostingManifest describes an already-built static site, never source code.
type HostingManifest struct {
	Files       []HostingFile `json:"files"`
	SPAFallback bool          `json:"spa_fallback"`
}

type HostingFile struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

type HostingSite struct {
	ID              string    `json:"id"`
	AccountID       string    `json:"account_id"`
	ProjectID       string    `json:"project_id"`
	Environment     string    `json:"environment"`
	Name            string    `json:"name"`
	Hostname        string    `json:"hostname"`
	OriginID        string    `json:"origin_id"`
	Status          string    `json:"status"`
	ActiveReleaseID string    `json:"active_release_id"`
	Revision        int64     `json:"revision"`
	DurableRouting  bool      `json:"durable_routing"`
	Suspended       bool      `json:"suspended"`
	CreatedAt       time.Time `json:"created_at"`
}

type HostingRelease struct {
	ID                    string          `json:"id"`
	SiteID                string          `json:"site_id"`
	State                 string          `json:"state"`
	Manifest              HostingManifest `json:"manifest"`
	CreatedAt             time.Time       `json:"created_at"`
	RetiredAt             *time.Time      `json:"retired_at,omitempty"`
	RetirementGeneration  int64           `json:"retirement_generation,omitempty"`
	RetirementPublishedAt *time.Time      `json:"retirement_published_at,omitempty"`
}

type CreateHostingSiteRequest struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Name        string `json:"name"`
}

type HostingUpload struct {
	Path    string              `json:"path"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
}

type HostingUploadResponse struct {
	Release HostingRelease  `json:"release"`
	Uploads []HostingUpload `json:"uploads"`
}

type PromoteHostingReleaseRequest struct {
	ReleaseID        string `json:"release_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type SetHostingSuspendedRequest struct {
	Suspended        bool  `json:"suspended"`
	ExpectedRevision int64 `json:"expected_revision"`
}
