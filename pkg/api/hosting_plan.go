package api

import "time"

// HostingDeploymentPlan is advisory evidence for one prospective new release.
// It is not an admission reservation, deployment token or serving-health check.
type HostingDeploymentPlan struct {
	Version                 int                 `json:"version"`
	APIOrigin               string              `json:"api_origin"`
	SiteID                  string              `json:"site_id"`
	ExpectedRevision        int64               `json:"expected_revision"`
	BaseReleaseID           string              `json:"base_release_id"`
	ManifestFingerprint     string              `json:"manifest_fingerprint"`
	StartedAt               time.Time           `json:"started_at"`
	ObservedAt              time.Time           `json:"observed_at"`
	UsageObservedAt         time.Time           `json:"usage_observed_at"`
	Advisory                bool                `json:"advisory"`
	State                   string              `json:"state"`
	FileCount               int                 `json:"file_count"`
	PayloadBytes            int64               `json:"payload_bytes"`
	ManifestBytes           int64               `json:"manifest_bytes"`
	AdditionalReservedBytes int64               `json:"additional_reserved_bytes"`
	Changes                 HostingManifestDiff `json:"changes"`
	Quotas                  []HostingPlanQuota  `json:"quotas"`
	Blockers                []string            `json:"blockers"`
	Warnings                []string            `json:"warnings"`
}

type HostingPlanQuota struct {
	Resource   string `json:"resource"`
	Current    int64  `json:"current"`
	Additional int64  `json:"additional"`
	Projected  int64  `json:"projected"`
	Limit      int64  `json:"limit"`
	Blocked    bool   `json:"blocked"`
}
