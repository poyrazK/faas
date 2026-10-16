package api

import "time"

// ObsAlertResponseTimes describes retained activation-cohort timing, not an SLA.
type ObsAlertResponseTimes struct {
	Coverage         *ObsAlertCollectionCoverage `json:"coverage,omitempty"`
	AsOf             time.Time                   `json:"as_of"`
	Since            time.Time                   `json:"since"`
	Scope            string                      `json:"scope"`
	DurationBasis    string                      `json:"duration_basis"`
	PercentileMethod string                      `json:"percentile_method"`
	Limitations      []string                    `json:"limitations"`
	Collection       ObsAlertCollectionHealth    `json:"collection"`
	Retention        ObsAlertRetentionHealth     `json:"retention"`
	Totals           *ObsAlertResponseAggregate  `json:"totals"`
	Groups           []ObsAlertResponseGroup     `json:"groups"`
}

type ObsAlertResponseAggregate struct {
	Episodes           int64                    `json:"episodes"`
	Unresolved         int64                    `json:"unresolved"`
	UnknownObservation int64                    `json:"unknown_observation"`
	Acknowledgment     ObsAlertResponseMetric   `json:"acknowledgment"`
	Resolution         ObsAlertResponseMetric   `json:"resolution"`
	ReceiptCoverage    ObsAlertResponseCoverage `json:"receipt_coverage"`
}

// Metric classifications partition the cohort; percentiles use only valid samples.
type ObsAlertResponseMetric struct {
	Samples      int64  `json:"samples"`
	NotObserved  int64  `json:"not_observed"`
	Unknown      int64  `json:"unknown"`
	InvalidClock int64  `json:"invalid_clock"`
	P50Seconds   *int64 `json:"p50_seconds"`
	P95Seconds   *int64 `json:"p95_seconds"`
}

type ObsAlertResponseCoverage struct {
	CompleteEpisodes       int64 `json:"complete_episodes"`
	GappedEpisodes         int64 `json:"gapped_episodes"`
	InvalidReceiptEpisodes int64 `json:"invalid_receipt_episodes"`
	MissingRevisions       int64 `json:"missing_revisions"`
}

type ObsAlertResponseGroup struct {
	Severity  string                    `json:"severity"`
	Component string                    `json:"component"`
	Metrics   ObsAlertResponseAggregate `json:"metrics"`
}
