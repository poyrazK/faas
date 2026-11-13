package api

import "time"

// ObsServingHealth is coverage evidence for expected serving probes. URL IDs
// fingerprint exact configured URLs; raw URLs and response bodies are excluded.
type ObsServingHealth struct {
	GeneratedAt   time.Time                `json:"generated_at"`
	Scope         string                   `json:"scope"`
	State         string                   `json:"state"`
	Reason        string                   `json:"reason,omitempty"`
	MaxAgeSeconds int                      `json:"max_age_seconds"`
	Inventory     ObsServingProbeInventory `json:"inventory"`
	Counts        *ObsServingProbeCounts   `json:"counts"`
	Probes        []ObsServingProbe        `json:"probes"`
	Limitations   []string                 `json:"limitations"`
}

// ObsServingProbeInventory describes the configuration and freshness of independent serving-probe intent.
type ObsServingProbeInventory struct {
	State         string     `json:"state"`
	Reason        string     `json:"reason,omitempty"`
	Configured    *bool      `json:"configured"`
	ExpectedPairs *int       `json:"expected_pairs"`
	ObservedAt    *time.Time `json:"observed_at"`
}

type ObsServingProbeCounts struct {
	Expected        int `json:"expected"`
	Reporting       int `json:"reporting"`
	Healthy         int `json:"healthy"`
	Missing         int `json:"missing"`
	Stale           int `json:"stale"`
	Invalid         int `json:"invalid"`
	ContentDisabled int `json:"content_disabled"`
	BudgetMismatch  int `json:"budget_mismatch"`
	Failing         int `json:"failing"`
	Unknown         int `json:"unknown"`
}

type ObsServingProbe struct {
	Node                     string     `json:"node"`
	URLID                    string     `json:"url_id"`
	State                    string     `json:"state"`
	Reason                   string     `json:"reason,omitempty"`
	Reporting                *bool      `json:"reporting"`
	EvaluatedAt              *time.Time `json:"evaluated_at"`
	LastRunAt                *time.Time `json:"last_run_at"`
	ExpectedFirstByteSeconds float64    `json:"expected_first_byte_seconds"`
	MaxResultAgeSeconds      float64    `json:"max_result_age_seconds"`
}
