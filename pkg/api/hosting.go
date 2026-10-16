package api

import "time"

// HostingCapabilities is an operator-only view of the enforced pilot limits.
// UploadBaseURL identifies private storage; it conveys no access credentials.
type HostingCapabilities struct {
	AnalyticsEnabled         bool   `json:"analytics_enabled"`
	AccountStorageMaxBytes   int64  `json:"account_storage_max_bytes"`
	AccountPendingUploadsMax int64  `json:"account_pending_uploads_max"`
	DomainsEnabled           bool   `json:"domains_enabled"`
	DomainsPerSite           int    `json:"domains_per_site"`
	DomainHostnameMaxBytes   int    `json:"domain_hostname_max_bytes"`
	DomainCNAMETarget        string `json:"domain_cname_target,omitempty"`
	UploadBaseURL            string `json:"upload_base_url"`
	FilesMax                 int    `json:"files_max"`
	FileMaxBytes             int64  `json:"file_max_bytes"`
	ReleaseMaxBytes          int64  `json:"release_max_bytes"`
	ManifestMaxBytes         int64  `json:"manifest_max_bytes"`
	PathMaxBytes             int    `json:"path_max_bytes"`
	ContentTypeMaxBytes      int    `json:"content_type_max_bytes"`
	NameMaxBytes             int    `json:"name_max_bytes"`
	SitesPerAccount          int    `json:"sites_per_account"`
	ReleasesPerSite          int    `json:"releases_per_site"`
	UploadTTLSeconds         int    `json:"upload_ttl_seconds"`
	StorageTimeoutSeconds    int    `json:"storage_timeout_seconds"`
	FinalizeTimeoutSeconds   int    `json:"finalize_timeout_seconds"`
}

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
	ArtifactBytes         int64           `json:"artifact_bytes"`
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
