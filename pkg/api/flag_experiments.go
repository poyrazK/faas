package api

import "time"

// FlagSDKTrackCapability marks a runtime SDK that reports conversion events
// with track() (ADR 20261115163113 §2).
const FlagSDKTrackCapability = "track-v1"

// FlagEventsMaxBodyBytes bounds one runtime event batch body.
const FlagEventsMaxBodyBytes = 64 << 10

// FlagEventMaxAge and FlagEventMaxSkew bound an event's time against the
// API's clock: older events are dropped as invalid, and so are events more
// than FlagEventMaxSkew in the future.
const (
	FlagEventMaxAge  = 24 * time.Hour
	FlagEventMaxSkew = 5 * time.Minute
	// FlagEventMaxAbsValue bounds an event value (an integer, such as cents).
	FlagEventMaxAbsValue int64 = 1_000_000_000_000
)

// FlagEvent is one conversion event in a runtime batch. CustomerID is the
// verified customer the gateway attached to the request the event belongs
// to; apid keeps it only when it is an active platform tenant of the
// workload's account.
type FlagEvent struct {
	Name       string    `json:"name"`
	CustomerID string    `json:"customer_id"`
	Value      int64     `json:"value,omitempty"`
	At         time.Time `json:"at,omitempty"`
}

// FlagEventsRequest is POST /v1/runtime/flags/events.
type FlagEventsRequest struct {
	Events []FlagEvent `json:"events"`
}

// FlagEventsDropped counts a batch's dropped events by reason.
type FlagEventsDropped struct {
	Quota           int `json:"quota"`
	UnknownCustomer int `json:"unknown_customer"`
	NameLimit       int `json:"name_limit"`
	Invalid         int `json:"invalid"`
}

// FlagEventsMonth is the account's conversion event usage this month.
type FlagEventsMonth struct {
	Limit    int64 `json:"limit"`
	Accepted int64 `json:"accepted"`
	Dropped  int64 `json:"dropped"`
}

// FlagExperimentCreateRequest creates an experiment on a variant flag's
// weighted rule (ADR 20261115163113 §1). Start starts it at once.
type FlagExperimentCreateRequest struct {
	Key         string `json:"key"`
	Flag        string `json:"flag"`
	RuleID      string `json:"rule_id"`
	Control     string `json:"control"`
	MetricEvent string `json:"metric_event"`
	// MetricKind is conversion (the default) or mean (ADR 20261116190208 §2).
	MetricKind string   `json:"metric_kind,omitempty"`
	MixingLift *float64 `json:"mixing_lift,omitempty"`
	// Guardrails changes the default guardrails; omitted keeps both on with
	// their default tolerances.
	Guardrails *FlagExperimentGuardrailsRequest `json:"guardrails,omitempty"`
	Start      bool                             `json:"start,omitempty"`
}

// FlagExperimentGuardrailRequest turns one guardrail off or sets its
// tolerance.
type FlagExperimentGuardrailRequest struct {
	Disabled    bool     `json:"disabled,omitempty"`
	MaxIncrease *float64 `json:"max_increase,omitempty"`
}

// FlagExperimentGuardrailsRequest sets an experiment's guardrails.
type FlagExperimentGuardrailsRequest struct {
	ServerErrorRate *FlagExperimentGuardrailRequest `json:"server_error_rate,omitempty"`
	P95Latency      *FlagExperimentGuardrailRequest `json:"p95_latency,omitempty"`
}

// FlagExperimentGuardrailSetting is one guardrail as configured.
type FlagExperimentGuardrailSetting struct {
	Enabled     bool    `json:"enabled"`
	MaxIncrease float64 `json:"max_increase"`
}

// FlagExperimentGuardrailSettings are an experiment's guardrails.
type FlagExperimentGuardrailSettings struct {
	ServerErrorRate FlagExperimentGuardrailSetting `json:"server_error_rate"`
	P95Latency      FlagExperimentGuardrailSetting `json:"p95_latency"`
}

// FlagExperimentShipRequest ships one variant to every customer of the
// experiment's rule.
type FlagExperimentShipRequest struct {
	Variant string `json:"variant"`
}

// FlagExperimentShipResponse is the stopped experiment and the published
// flag configuration version.
type FlagExperimentShipResponse struct {
	Experiment    FlagExperiment `json:"experiment"`
	ConfigVersion int64          `json:"config_version"`
}

// FlagExperiment is an experiment as the API reports it.
type FlagExperiment struct {
	Key         string         `json:"key"`
	Flag        string         `json:"flag"`
	RuleID      string         `json:"rule_id"`
	Control     string         `json:"control"`
	MetricEvent string         `json:"metric_event"`
	MetricKind  string         `json:"metric_kind"`
	Status      string         `json:"status"`
	MixingLift  float64        `json:"mixing_lift"`
	Weights     map[string]int `json:"weights"`
	CreatedBy   string         `json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	StoppedAt   *time.Time     `json:"stopped_at,omitempty"`
	// AnalyzedUntil is the end of the request telemetry the results cover.
	AnalyzedUntil *time.Time                      `json:"analyzed_until,omitempty"`
	Guardrails    FlagExperimentGuardrailSettings `json:"guardrails"`
	// LatencyThresholdMS is control's p95 latency, fixed once control has
	// enough requests; the p95 guardrail compares requests slower than it.
	LatencyThresholdMS *int `json:"latency_threshold_ms,omitempty"`
	// StopReason is manual, guardrail_breach or shipped once stopped.
	StopReason string `json:"stop_reason,omitempty"`
	// Breach describes the guardrail breach that stopped the experiment.
	Breach map[string]any `json:"breach,omitempty"`
}

// FlagExperimentList is the experiments of an environment.
type FlagExperimentList struct {
	Experiments []FlagExperiment `json:"experiments"`
}

// FlagExperimentArm is one variant's exposed and converted customers. Rate
// is the conversion rate, or the mean value per exposed customer for mean
// metrics, where Converted counts customers with a non-zero value.
type FlagExperimentArm struct {
	Variant   string  `json:"variant"`
	Exposed   int64   `json:"exposed"`
	Converted int64   `json:"converted"`
	Rate      float64 `json:"rate"`
}

// FlagExperimentVariantResult compares one variant with control. Lift is an
// absolute difference in conversion rate; Low and High bound it with a 95%
// confidence sequence, valid however often the results are read.
type FlagExperimentVariantResult struct {
	FlagExperimentArm
	Lift         *float64 `json:"lift,omitempty"`
	RelativeLift *float64 `json:"relative_lift,omitempty"`
	Low          *float64 `json:"low,omitempty"`
	High         *float64 `json:"high,omitempty"`
	PValue       *float64 `json:"p_value,omitempty"`
	// Status is significant, not_significant, insufficient_data or
	// sample_ratio_mismatch.
	Status string `json:"status"`
}

// FlagExperimentResults is GET .../experiments/{key}/results.
type FlagExperimentResults struct {
	Experiment          FlagExperiment                `json:"experiment"`
	Control             FlagExperimentArm             `json:"control"`
	Variants            []FlagExperimentVariantResult `json:"variants"`
	SampleRatioPValue   float64                       `json:"sample_ratio_p_value"`
	SampleRatioMismatch bool                          `json:"sample_ratio_mismatch"`
	// CrossedUnits saw more than one variant and are left out.
	CrossedUnits int64 `json:"crossed_units"`
	// Guardrails compare each variant's server error rate and slow request
	// share with control.
	Guardrails []FlagExperimentGuardrail `json:"guardrails"`
	// Latency is each variant's p95 latency over the experiment.
	Latency        []FlagExperimentLatency      `json:"latency"`
	Recommendation FlagExperimentRecommendation `json:"recommendation"`
}

// FlagExperimentGuardrail is one guardrail across the variants. Status is
// ok, breached or insufficient_data.
type FlagExperimentGuardrail struct {
	Name      string                           `json:"name"`
	Tolerance float64                          `json:"tolerance"`
	Status    string                           `json:"status"`
	Variants  []FlagExperimentGuardrailVariant `json:"variants"`
}

// FlagExperimentGuardrailVariant compares one variant's rate with
// control's. Difference is variant minus control; Low and High bound it
// with a 99% confidence sequence.
type FlagExperimentGuardrailVariant struct {
	Variant      string   `json:"variant"`
	ControlRate  float64  `json:"control_rate"`
	VariantRate  float64  `json:"variant_rate"`
	ControlUnits int64    `json:"control_units"`
	VariantUnits int64    `json:"variant_units"`
	Difference   *float64 `json:"difference,omitempty"`
	Low          *float64 `json:"low,omitempty"`
	High         *float64 `json:"high,omitempty"`
	Status       string   `json:"status"`
}

// FlagExperimentLatency is one variant's p95 latency, from a histogram with
// 5% buckets.
type FlagExperimentLatency struct {
	Variant  string `json:"variant"`
	Requests int64  `json:"requests"`
	P95MS    int64  `json:"p95_ms"`
}

// FlagExperimentRecommendation says what to do with the results: action is
// rolled_back, fix_allocation, ship_variant, keep_control, keep_running or
// decide. Commands carry it out; Gregale never ships on its own.
type FlagExperimentRecommendation struct {
	Action   string   `json:"action"`
	Variant  string   `json:"variant,omitempty"`
	Summary  string   `json:"summary"`
	Commands []string `json:"commands,omitempty"`
}

// FlagEventsResponse answers a runtime event batch.
type FlagEventsResponse struct {
	Accepted int               `json:"accepted"`
	Dropped  FlagEventsDropped `json:"dropped"`
	Month    FlagEventsMonth   `json:"month"`
}
