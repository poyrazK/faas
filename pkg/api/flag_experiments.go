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
	// Subject, Properties and Flags are product analytics (ADR
	// 20261117084505 §1): an opaque end-user subject, stored only as a
	// hash; up to AnalyticsEventMaxProperties string properties; and the
	// flag decisions of the event's request, flag key to served value.
	Subject    string            `json:"subject,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
	Flags      map[string]string `json:"flags,omitempty"`
}

// FlagSDKTrackV2Capability marks a runtime SDK whose track() events carry
// subjects, properties and flag decisions (ADR 20261117084505 §4).
const FlagSDKTrackV2Capability = "track-v2"

// FlagEventsRequest is POST /v1/runtime/flags/events.
type FlagEventsRequest struct {
	Events []FlagEvent `json:"events"`
	// Exposures are subject experiment exposures (ADR 20261119173702 §1),
	// at most FlagExposuresPerRequest; they do not count against the event
	// quota.
	Exposures []FlagExposure `json:"exposures,omitempty"`
}

// FlagExposure is one end user's exposure to a subject experiment's rule.
// Only the subject's hash is stored.
type FlagExposure struct {
	Flag       string    `json:"flag"`
	RuleID     string    `json:"rule_id"`
	Variant    string    `json:"variant"`
	CustomerID string    `json:"customer_id"`
	Subject    string    `json:"subject"`
	At         time.Time `json:"at,omitempty"`
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
	Key     string `json:"key"`
	Flag    string `json:"flag"`
	RuleID  string `json:"rule_id"`
	Control string `json:"control"`
	// MetricEvent is required for conversion and mean metrics; funnel and
	// retention metrics derive it.
	MetricEvent string `json:"metric_event,omitempty"`
	// MetricKind is conversion (the default) or mean (ADR 20261116190208 §2).
	MetricKind string   `json:"metric_kind,omitempty"`
	MixingLift *float64 `json:"mixing_lift,omitempty"`
	// Guardrails changes the default guardrails; omitted keeps both on with
	// their default tolerances.
	Guardrails *FlagExperimentGuardrailsRequest `json:"guardrails,omitempty"`
	Start      bool                             `json:"start,omitempty"`
	// Funnel and Retention define those metric kinds; metric_event is then
	// derived (ADR 20261119102741 §1). Cuped turns CUPED off when false.
	Funnel    *FlagExperimentFunnel    `json:"funnel,omitempty"`
	Retention *FlagExperimentRetention `json:"retention,omitempty"`
	Cuped     *bool                    `json:"cuped,omitempty"`
	// Unit is customer (the default) or subject; Layer and Traffic (basis
	// points) place the experiment in an exclusive layer slice; Allocation
	// is fixed (the default) or bandit; DenominatorEvent is a ratio
	// metric's denominator and Percentile a percentile metric's percentile
	// (ADR 20261119173702).
	Unit             string `json:"unit,omitempty"`
	Layer            string `json:"layer,omitempty"`
	Traffic          int    `json:"traffic,omitempty"`
	Allocation       string `json:"allocation,omitempty"`
	DenominatorEvent string `json:"denominator_event,omitempty"`
	Percentile       int    `json:"percentile,omitempty"`
	// NotifyChannelIDs are the notification channels the experiment's
	// decisions and problems go to; omitted uses the environment's
	// (ADR-20261125085913).
	NotifyChannelIDs []string `json:"notify_channel_ids,omitempty"`
}

// FlagExperimentFunnel is a funnel metric: 2 to 6 steps completed within
// Window (a Go duration, 1h to 720h) after first exposure.
type FlagExperimentFunnel struct {
	Steps  []InsightStepRequest `json:"steps"`
	Window string               `json:"window"`
}

// FlagExperimentRetention is a retention metric: the return event in
// period PeriodNumber (1 to 12) after first exposure; Period is day or week.
type FlagExperimentRetention struct {
	Return       InsightStepRequest `json:"return"`
	Period       string             `json:"period"`
	PeriodNumber int                `json:"period_number"`
}

// FlagExperimentCuped reports CUPED on an experiment's results (ADR
// 20261119102741 §2). Reason says why it did not apply: off,
// pre_period_pending, no_pre_period_variance or no_pre_period_data.
type FlagExperimentCuped struct {
	Applied           bool    `json:"applied"`
	PreDays           int     `json:"pre_days"`
	Theta             float64 `json:"theta,omitempty"`
	VarianceReduction float64 `json:"variance_reduction,omitempty"`
	Reason            string  `json:"reason,omitempty"`
}

// FlagExperimentComparison is a variant's comparison with control.
type FlagExperimentComparison struct {
	Lift         float64 `json:"lift"`
	RelativeLift float64 `json:"relative_lift"`
	Low          float64 `json:"low"`
	High         float64 `json:"high"`
	PValue       float64 `json:"p_value"`
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
	// Funnel or Retention define those metric kinds; Cuped is whether CUPED
	// is on (ADR 20261119102741).
	Funnel    *FlagExperimentFunnel    `json:"funnel,omitempty"`
	Retention *FlagExperimentRetention `json:"retention,omitempty"`
	Cuped     bool                     `json:"cuped"`
	// Unit, Allocation, the layer slice held while running, and the ratio
	// or percentile settings (ADR 20261119173702).
	Unit             string `json:"unit"`
	Allocation       string `json:"allocation"`
	Layer            string `json:"layer,omitempty"`
	Traffic          int    `json:"traffic,omitempty"`
	LayerFrom        *int   `json:"layer_from,omitempty"`
	LayerTo          *int   `json:"layer_to,omitempty"`
	DenominatorEvent string `json:"denominator_event,omitempty"`
	Percentile       int    `json:"percentile,omitempty"`
	// NotifyChannelIDs are the experiment's own notification channels
	// (ADR-20261125085913).
	NotifyChannelIDs []string `json:"notify_channel_ids,omitempty"`
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
	// Unadjusted is the comparison without CUPED when the lift above is
	// CUPED-adjusted.
	Unadjusted *FlagExperimentComparison `json:"unadjusted,omitempty"`
	// BestProbability and ExpectedLoss are a bandit arm's posterior
	// (ADR 20261119173702 §4).
	BestProbability *float64 `json:"best_probability,omitempty"`
	ExpectedLoss    *float64 `json:"expected_loss,omitempty"`
}

// FlagExperimentBandit is a bandit's posterior per arm, control included,
// and when it last re-weighted the rule.
type FlagExperimentBandit struct {
	Arms       []FlagExperimentBanditArm `json:"arms"`
	WeightedAt *time.Time                `json:"weighted_at,omitempty"`
}

// FlagExperimentBanditArm is one arm's best-arm probability and expected
// loss (the conversion rate given up by choosing it).
type FlagExperimentBanditArm struct {
	Variant         string  `json:"variant"`
	Weight          int     `json:"weight"`
	BestProbability float64 `json:"best_probability"`
	ExpectedLoss    float64 `json:"expected_loss"`
}

// FlagExperimentSegment is the results of a cohort's members, or of
// everyone else, with Benjamini–Hochberg adjusted p-values. Segments are
// exploratory and never drive the recommendation.
type FlagExperimentSegment struct {
	Cohort   string                        `json:"cohort"`
	Members  bool                          `json:"members"`
	Control  FlagExperimentArm             `json:"control"`
	Variants []FlagExperimentVariantResult `json:"variants"`
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
	Cuped          FlagExperimentCuped          `json:"cuped"`
	// FixedHorizon marks a percentile metric's bootstrap results, which
	// have no sequential guarantee; Bandit is a bandit's posterior and
	// Segments the results split by ?segment=COHORT (ADR 20261119173702).
	FixedHorizon bool                    `json:"fixed_horizon,omitempty"`
	Bandit       *FlagExperimentBandit   `json:"bandit,omitempty"`
	Segments     []FlagExperimentSegment `json:"segments,omitempty"`
}

// FlagExperimentLayer is an environment's experiment layer and the slices
// its running experiments hold.
type FlagExperimentLayer struct {
	Key    string                     `json:"key"`
	Slices []FlagExperimentLayerSlice `json:"slices"`
	// Free is the basis points no experiment holds.
	Free int `json:"free"`
}

// FlagExperimentLayerSlice is the [From, To) slice an experiment holds.
type FlagExperimentLayerSlice struct {
	Experiment string `json:"experiment"`
	From       int    `json:"from"`
	To         int    `json:"to"`
}

// FlagExperimentLayerList is GET .../experiment-layers.
type FlagExperimentLayerList struct {
	Layers []FlagExperimentLayer `json:"layers"`
}

// FlagExperimentLayerRequest creates a layer.
type FlagExperimentLayerRequest struct {
	Key string `json:"key"`
}

// FlagExperimentHoldout is the environment holdout: BasisPoints of
// customers see every experiment's control.
type FlagExperimentHoldout struct {
	BasisPoints int `json:"basis_points"`
}

// FlagExperimentHoldoutReport compares held-out customers with everyone
// else active in the range on one event: conversion rates, their difference
// and a 95% interval.
type FlagExperimentHoldoutReport struct {
	Event       string            `json:"event"`
	From        time.Time         `json:"from"`
	Until       time.Time         `json:"until"`
	BasisPoints int               `json:"basis_points"`
	Held        FlagExperimentArm `json:"held"`
	Exposed     FlagExperimentArm `json:"exposed"`
	Difference  float64           `json:"difference"`
	Low         float64           `json:"low"`
	High        float64           `json:"high"`
	Truncated   bool              `json:"truncated"`
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
	Accepted int `json:"accepted"`
	// Exposures is how many subject exposures matched a running subject
	// experiment (ADR 20261119173702 §1).
	Exposures int               `json:"exposures,omitempty"`
	Dropped   FlagEventsDropped `json:"dropped"`
	Month     FlagEventsMonth   `json:"month"`
}

// FlagNotificationSettings are an environment's notification channels for
// its experiments and flag lifecycle (ADR-20261125085913).
type FlagNotificationSettings struct {
	ChannelIDs []string   `json:"channel_ids"`
	UpdatedBy  string     `json:"updated_by,omitempty"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

// FlagNotificationSettingsRequest replaces an environment's channels; an
// empty list turns its notifications off.
type FlagNotificationSettingsRequest struct {
	ChannelIDs []string `json:"channel_ids"`
}
