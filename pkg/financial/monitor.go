package financial

import "time"

// NotificationOnly identifies the first operational budget response. It does
// not imply admission control or a ceiling on the customer's invoice.
func NotificationOnly(p BudgetSpec) bool {
	return p.Mode == "monitored" && p.Action == "notify"
}

// BudgetEvaluation retains the evidence boundary used by a monitored policy.
// A nil KnownMillicents means the cost read failed, never zero spending.
type BudgetEvaluation struct {
	PeriodStart       time.Time `json:"period_start"`
	PeriodEnd         time.Time `json:"period_end"`
	AsOf              time.Time `json:"as_of"`
	EvaluatedAt       time.Time `json:"evaluated_at"`
	EvidenceThroughID int64     `json:"evidence_through_id"`
	KnownMillicents   *int64    `json:"known_millicents,omitempty"`
	CoverageComplete  bool      `json:"coverage_complete"`
	Fresh             bool      `json:"fresh"`
	Reasons           []string  `json:"reasons"`
	// ProjectedMillicents is the month-end amount at the current run rate
	// (ADR-20261019193500). It is nil, with ProjectionReason, when a selected
	// meter has no forecast or the evidence is incomplete or stale.
	ProjectedMillicents *int64 `json:"projected_millicents,omitempty"`
	ProjectionReason    string `json:"projection_reason,omitempty"`
}

// BudgetAlertProjection marks a projected-overrun alert (ADR-20261019193500):
// the month-end projection reached the limit while known usage had not. Its
// ThresholdMillicents is the limit.
const BudgetAlertProjection = "projection"

type BudgetAlert struct {
	ID string `json:"id"`
	// Kind is empty for a crossed threshold and BudgetAlertProjection for a
	// projected overrun.
	Kind                string           `json:"kind,omitempty"`
	PolicyRevision      int64            `json:"policy_revision"`
	ThresholdMillicents int64            `json:"threshold_millicents"`
	Evaluation          BudgetEvaluation `json:"evaluation"`
	State               string           `json:"state"`
	Attempts            int              `json:"attempts"`
	NextAttemptAt       time.Time        `json:"next_attempt_at"`
	DeliveredAt         *time.Time       `json:"delivered_at,omitempty"`
	LastError           string           `json:"last_error,omitempty"`
}

// BudgetRuntime uses the current policy revision. Evaluation and alerts cover
// the current UTC month; active manual stops may carry over from earlier months.
type BudgetRuntime struct {
	Compute    *ComputeBalance   `json:"compute,omitempty"`
	Evaluation *BudgetEvaluation `json:"evaluation,omitempty"`
	Alerts     []BudgetAlert     `json:"alerts"`
	// Projection is this month's projected-overrun alert, if one was raised.
	Projection *BudgetAlert `json:"projection,omitempty"`
	Stops      []BudgetStop `json:"stops,omitempty"`
}
