package api

import "time"

// ObsIncidentContextResponse describes current relationships, not historical
// blast radius or causation. Timeline coverage is explicit and page bounded.
type ObsIncidentContextResponse struct {
	AsOf               time.Time                    `json:"as_of"`
	IncidentID         string                       `json:"incident_id"`
	IncidentStatus     string                       `json:"incident_status"`
	IncidentObservedAt time.Time                    `json:"incident_observed_at"`
	EvidencePath       string                       `json:"evidence_path"`
	Scope              ObsIncidentContextScope      `json:"scope"`
	Counts             *ObsIncidentContextCounts    `json:"counts"`
	Resources          []ObsIncidentRelatedResource `json:"resources"`
	Limit              int                          `json:"limit"`
	NextCursor         string                       `json:"next_cursor"`
	Since              time.Time                    `json:"since"`
	Until              time.Time                    `json:"until"`
	Timeline           []ObsIncidentRelatedEntry    `json:"timeline"`
	TimelineScope      string                       `json:"timeline_scope"`
	TimelineLimit      int                          `json:"timeline_limit"`
	TimelineTruncated  bool                         `json:"timeline_truncated"`
	UnavailableSources []string                     `json:"unavailable_sources"`
}

type ObsIncidentContextScope struct {
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
	Basis     string `json:"basis"`
	AppID     string `json:"app_id,omitempty"`
	AccountID string `json:"account_id,omitempty"`
	NodeID    string `json:"node_id,omitempty"`
	JobID     string `json:"job_id,omitempty"`
}

// Counts cover the complete current relationship set, independently of the
// resource page. FailedResources counts explicit failed/dead_letter states;
// it does not imply that the incident caused those failures.
type ObsIncidentContextCounts struct {
	Apps            int64 `json:"apps"`
	Tenants         int64 `json:"tenants"`
	Deployments     int64 `json:"deployments"`
	Nodes           int64 `json:"nodes"`
	Jobs            int64 `json:"jobs"`
	JobRuns         int64 `json:"job_runs"`
	FailedResources int64 `json:"failed_resources"`
}

type ObsIncidentRelatedResource struct {
	Kind             string `json:"kind"`
	ID               string `json:"id"`
	Status           string `json:"status"`
	Relationship     string `json:"relationship"`
	AccountID        string `json:"account_id,omitempty"`
	AppID            string `json:"app_id,omitempty"`
	ConfirmedFailure bool   `json:"confirmed_failure"`
}

type ObsIncidentRelatedEntry struct {
	ResourceKind string                   `json:"resource_kind"`
	ResourceID   string                   `json:"resource_id"`
	Match        string                   `json:"match"`
	Entry        ObsIncidentEvidenceEntry `json:"entry"`
}
