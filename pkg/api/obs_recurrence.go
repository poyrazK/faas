package api

import "time"

// ObsAlertRecurrence describes observed repeats in a retained activation cohort.
type ObsAlertRecurrence struct {
	Coverage         *ObsAlertCollectionCoverage `json:"coverage,omitempty"`
	AsOf             time.Time                   `json:"as_of"`
	Since            time.Time                   `json:"since"`
	Scope            string                      `json:"scope"`
	PercentileMethod string                      `json:"percentile_method"`
	Limitations      []string                    `json:"limitations"`
	Collection       ObsAlertCollectionHealth    `json:"collection"`
	Retention        ObsAlertRetentionHealth     `json:"retention"`
	Totals           *ObsAlertRecurrenceTotals   `json:"totals"`
	Items            []ObsAlertRecurrenceSeries  `json:"items"`
	Truncated        bool                        `json:"truncated"`
}

type ObsAlertRecurrenceTotals struct {
	Series         int64                     `json:"series"`
	RepeatedSeries int64                     `json:"repeated_series"`
	Metrics        ObsAlertRecurrenceMetrics `json:"metrics"`
}

type ObsAlertRecurrenceMetrics struct {
	Episodes           int64                      `json:"episodes"`
	RepeatActivations  int64                      `json:"repeat_activations"`
	Unresolved         int64                      `json:"unresolved"`
	UnknownObservation int64                      `json:"unknown_observation"`
	Duration           ObsAlertRecurrenceDuration `json:"duration"`
	RecoveryGap        ObsAlertRecurrenceGap      `json:"recovery_gap"`
}

// Duration classifications partition episodes; gaps partition repeat activations.
// Statistics use only valid samples and retain null when no sample exists.
type ObsAlertRecurrenceDuration struct {
	Samples      int64  `json:"samples"`
	NotObserved  int64  `json:"not_observed"`
	InvalidClock int64  `json:"invalid_clock"`
	MinSeconds   *int64 `json:"min_seconds"`
	P50Seconds   *int64 `json:"p50_seconds"`
}

type ObsAlertRecurrenceGap struct {
	Samples             int64  `json:"samples"`
	UnconfirmedRecovery int64  `json:"unconfirmed_recovery"`
	ResolutionOverlap   int64  `json:"resolution_overlap"`
	InvalidClock        int64  `json:"invalid_clock"`
	MinSeconds          *int64 `json:"min_seconds"`
	P50Seconds          *int64 `json:"p50_seconds"`
}

type ObsAlertRecurrenceSeries struct {
	RuleKey                 string                      `json:"rule_key"`
	SeriesID                string                      `json:"series_id"`
	Name                    string                      `json:"name"`
	Severity                string                      `json:"severity"`
	Component               string                      `json:"component"`
	AccountID               string                      `json:"account_id,omitempty"`
	AppID                   string                      `json:"app_id,omitempty"`
	NodeID                  string                      `json:"node_id,omitempty"`
	FirstActivationAt       time.Time                   `json:"first_activation_at"`
	LastActivationAt        time.Time                   `json:"last_activation_at"`
	Metrics                 ObsAlertRecurrenceMetrics   `json:"metrics"`
	RecentEpisodes          []ObsAlertRecurrenceEpisode `json:"recent_episodes"`
	RecentEpisodesTruncated bool                        `json:"recent_episodes_truncated"`
}

type ObsAlertRecurrenceEpisode struct {
	EpisodeID    string     `json:"episode_id"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	ResolvedAt   *time.Time `json:"resolved_at"`
	EvidencePath string     `json:"evidence_path"`
}
