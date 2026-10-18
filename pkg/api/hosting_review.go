package api

// HostingReleaseReview is a read-only comparison, not a promotion authorization.
// Promotion must still use ExpectedRevision through the existing CAS endpoint.
type HostingReleaseReview struct {
	HostingManifestDiff
	Provenance       *HostingReleaseProvenance `json:"provenance,omitempty"`
	SiteID           string                    `json:"site_id"`
	ExpectedRevision int64                     `json:"expected_revision"`
	BaseReleaseID    string                    `json:"base_release_id"`
	ReleaseID        string                    `json:"release_id"`
}

// HostingManifestDiff compares static files and serving settings independently
// of whether the target has been uploaded or assigned a release ID.
type HostingManifestDiff struct {
	Added           int                    `json:"added"`
	Changed         int                    `json:"changed"`
	Removed         int                    `json:"removed"`
	BeforeBytes     int64                  `json:"before_bytes"`
	AfterBytes      int64                  `json:"after_bytes"`
	Files           []HostingFileChange    `json:"files"`
	SettingsChanged bool                   `json:"settings_changed"`
	BeforeSettings  HostingReleaseSettings `json:"before_settings"`
	AfterSettings   HostingReleaseSettings `json:"after_settings"`
}
type HostingFileChange struct {
	Path   string       `json:"path"`
	Change string       `json:"change"`
	Before *HostingFile `json:"before"`
	After  *HostingFile `json:"after"`
}
type HostingReleaseSettings struct {
	SPAFallback bool                  `json:"spa_fallback"`
	Config      *HostingReleaseConfig `json:"config"`
}

// HostingPublicationObservation retains accepted intent even when waiting fails.
type HostingPublicationObservation struct {
	Site        HostingSite              `json:"site"`
	Publication HostingPublicationStatus `json:"publication"`
	WaitState   string                   `json:"wait_state"`
}
