package api

import "time"

// ObsAlertWorkloadSummary describes all physically retained platform alert episodes.
// Counts is null before the first successful collection; Groups is always an array.
type ObsAlertWorkloadSummary struct {
	AsOf                 time.Time                `json:"as_of"`
	Scope                string                   `json:"scope"`
	AgeBasis             string                   `json:"age_basis"`
	Collection           ObsAlertCollectionHealth `json:"collection"`
	Retention            ObsAlertRetentionHealth  `json:"retention"`
	Counts               *ObsAlertWorkloadCounts  `json:"counts"`
	Groups               []ObsAlertWorkloadGroup  `json:"groups"`
	OldestUnacknowledged *ObsAlertWorkloadOldest  `json:"oldest_unacknowledged"`
	OldestUnowned        *ObsAlertWorkloadOldest  `json:"oldest_unowned"`
}

// ObsAlertWorkloadCounts includes unknown unresolved episodes in NeedsAttention.
type ObsAlertWorkloadCounts struct {
	Total          int64 `json:"total"`
	Firing         int64 `json:"firing"`
	Unknown        int64 `json:"unknown"`
	Resolved       int64 `json:"resolved"`
	NeedsAttention int64 `json:"needs_attention"`
}

// ObsAlertWorkloadGroup is an exact intersection of the five workflow dimensions.
type ObsAlertWorkloadGroup struct {
	Status       string `json:"status"`
	Severity     string `json:"severity"`
	Acknowledged bool   `json:"acknowledged"`
	Owned        bool   `json:"owned"`
	Snoozed      bool   `json:"snoozed"`
	Count        int64  `json:"count"`
}

// ObsAlertWorkloadOldest links an unsnoozed unresolved episode without actor identities.
type ObsAlertWorkloadOldest struct {
	IncidentID   string    `json:"incident_id"`
	Status       string    `json:"status"`
	Severity     string    `json:"severity"`
	StartedAt    time.Time `json:"started_at"`
	AgeSeconds   int64     `json:"age_seconds"`
	EvidencePath string    `json:"evidence_path"`
}
