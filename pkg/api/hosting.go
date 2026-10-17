package api

import "time"

// HostingCapabilities is an operator-only view of the enforced pilot limits.
// UploadBaseURL identifies private storage; it conveys no access credentials.
type HostingCapabilities struct {
	CancellationEnabled           bool     `json:"cancellation_enabled"`
	PreviewReservationsEnabled    bool     `json:"preview_reservations_enabled"`
	ActivityEnabled               bool     `json:"activity_enabled"`
	ActivityPageSize              int      `json:"activity_page_size"`
	CISetupEnabled                bool     `json:"ci_setup_enabled"`
	DiagnosticsEnabled            bool     `json:"diagnostics_enabled"`
	OIDCEnabled                   bool     `json:"oidc_enabled"`
	RetentionEnabled              bool     `json:"retention_enabled"`
	RetentionAgeDefaultSeconds    int64    `json:"retention_age_default_seconds"`
	RetentionAgeMinSeconds        int64    `json:"retention_age_min_seconds"`
	RetentionAgeMaxSeconds        int64    `json:"retention_age_max_seconds"`
	AutomationSiteID              string   `json:"automation_site_id,omitempty"`
	AutomationOperations          []string `json:"automation_operations,omitempty"`
	AutomationEnabled             bool     `json:"automation_enabled"`
	AutomationCredentialsPerSite  int      `json:"automation_credentials_per_site"`
	AutomationTTLMaxSeconds       int64    `json:"automation_ttl_max_seconds"`
	CIRunsPerSite                 int      `json:"ci_runs_per_site"`
	HistoryEnabled                bool     `json:"history_enabled"`
	HistoryPerSite                int      `json:"history_per_site"`
	HistoryPageSize               int      `json:"history_page_size"`
	ProvenanceRepositoryMaxBytes  int      `json:"provenance_repository_max_bytes"`
	ProvenanceBranchMaxBytes      int      `json:"provenance_branch_max_bytes"`
	ProvenanceDescriptionMaxBytes int      `json:"provenance_description_max_bytes"`
	PreviewsEnabled               bool     `json:"previews_enabled"`
	PreviewsPerSite               int      `json:"previews_per_site"`
	PreviewTTLDefaultSeconds      int64    `json:"preview_ttl_default_seconds"`
	PreviewTTLMinSeconds          int64    `json:"preview_ttl_min_seconds"`
	PreviewTTLMaxSeconds          int64    `json:"preview_ttl_max_seconds"`
	StagedReleasesSupported       bool     `json:"staged_releases_supported"`
	ConfigEnabled                 bool     `json:"config_enabled"`
	ConfigMaxBytes                int      `json:"config_max_bytes"`
	ConfigRedirectsMax            int      `json:"config_redirects_max"`
	ConfigHeaderRulesMax          int      `json:"config_header_rules_max"`
	ConfigHeadersPerRuleMax       int      `json:"config_headers_per_rule_max"`
	ConfigHeaderValueMaxBytes     int      `json:"config_header_value_max_bytes"`
	AnalyticsEnabled              bool     `json:"analytics_enabled"`
	AccountStorageMaxBytes        int64    `json:"account_storage_max_bytes"`
	AccountPendingUploadsMax      int64    `json:"account_pending_uploads_max"`
	DomainsEnabled                bool     `json:"domains_enabled"`
	DomainsPerSite                int      `json:"domains_per_site"`
	DomainHostnameMaxBytes        int      `json:"domain_hostname_max_bytes"`
	DomainCNAMETarget             string   `json:"domain_cname_target,omitempty"`
	UploadBaseURL                 string   `json:"upload_base_url"`
	FilesMax                      int      `json:"files_max"`
	FileMaxBytes                  int64    `json:"file_max_bytes"`
	ReleaseMaxBytes               int64    `json:"release_max_bytes"`
	ManifestMaxBytes              int64    `json:"manifest_max_bytes"`
	PathMaxBytes                  int      `json:"path_max_bytes"`
	ContentTypeMaxBytes           int      `json:"content_type_max_bytes"`
	NameMaxBytes                  int      `json:"name_max_bytes"`
	SitesPerAccount               int      `json:"sites_per_account"`
	ReleasesPerSite               int      `json:"releases_per_site"`
	UploadTTLSeconds              int      `json:"upload_ttl_seconds"`
	StorageTimeoutSeconds         int      `json:"storage_timeout_seconds"`
	FinalizeTimeoutSeconds        int      `json:"finalize_timeout_seconds"`
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
	Config      *HostingReleaseConfig `json:"config,omitempty"`
	Files       []HostingFile         `json:"files"`
	SPAFallback bool                  `json:"spa_fallback"`
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
	LastActivityAt        time.Time                 `json:"last_activity_at"`
	Provenance            *HostingReleaseProvenance `json:"provenance,omitempty"`
	FirstPromotedAt       *time.Time                `json:"first_promoted_at,omitempty"`
	ArtifactBytes         int64                     `json:"artifact_bytes"`
	ID                    string                    `json:"id"`
	SiteID                string                    `json:"site_id"`
	State                 string                    `json:"state"`
	Manifest              HostingManifest           `json:"manifest"`
	CreatedAt             time.Time                 `json:"created_at"`
	RetiredAt             *time.Time                `json:"retired_at,omitempty"`
	RetirementGeneration  int64                     `json:"retirement_generation,omitempty"`
	RetirementPublishedAt *time.Time                `json:"retirement_published_at,omitempty"`
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

// HostingReleaseConfig is immutable release metadata, never a public file.
type HostingReleaseConfig struct {
	Version   int                 `json:"version"`
	Redirects []HostingRedirect   `json:"redirects,omitempty"`
	Headers   []HostingHeaderRule `json:"headers,omitempty"`
	NotFound  string              `json:"not_found,omitempty"`
}
type HostingRedirect struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Status int    `json:"status"`
}
type HostingHeaderRule struct {
	Path   string            `json:"path"`
	Values map[string]string `json:"values"`
}
