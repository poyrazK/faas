package api

import "time"

// ObsInputHealth is a bounded read of selected critical telemetry inputs.
// Fresh readiness samples say nothing about the readiness value itself.
type ObsInputHealth struct {
	GeneratedAt     time.Time     `json:"generated_at"`
	Scope           string        `json:"scope"`
	ExpectedRole    string        `json:"expected_role"`
	AvailableRoles  []string      `json:"available_roles"`
	State           string        `json:"state"`
	Reason          string        `json:"reason,omitempty"`
	MaxAgeSeconds   int           `json:"max_age_seconds"`
	LookbackSeconds int           `json:"lookback_seconds"`
	Jobs            []ObsInputJob `json:"jobs"`
	Limitations     []string      `json:"limitations"`
}

type ObsInputJob struct {
	Job         string              `json:"job"`
	State       string              `json:"state"`
	Expectation ObsInputExpectation `json:"expectation"`
	Targets     ObsInputTargets     `json:"targets"`
	Metrics     []ObsInputMetric    `json:"metrics"`
}

type ObsInputExpectation struct {
	Source         string     `json:"source"`
	State          string     `json:"state"`
	Reason         string     `json:"reason,omitempty"`
	MinimumTargets *int       `json:"minimum_targets"`
	InvalidTargets *int       `json:"invalid_targets"`
	ObservedAt     *time.Time `json:"observed_at"`
}

type ObsInputTargets struct {
	State  string                `json:"state"`
	Reason string                `json:"reason,omitempty"`
	Counts *ObsInputTargetCounts `json:"counts"`
}

type ObsInputTargetCounts struct {
	Observed       int  `json:"observed"`
	FreshUp        int  `json:"fresh_up"`
	Down           int  `json:"down"`
	Stale          int  `json:"stale"`
	MissingMinimum *int `json:"missing_minimum"`
	Extra          *int `json:"extra"`
}

type ObsInputMetric struct {
	Metric string                `json:"metric"`
	State  string                `json:"state"`
	Reason string                `json:"reason,omitempty"`
	Counts *ObsInputMetricCounts `json:"counts"`
}

type ObsInputMetricCounts struct {
	Expected int `json:"expected"`
	Fresh    int `json:"fresh"`
	Missing  int `json:"missing"`
	Stale    int `json:"stale"`
}
