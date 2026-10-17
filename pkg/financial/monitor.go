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
}

type BudgetAlert struct {
	ID                  string           `json:"id"`
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
	Stops      []BudgetStop      `json:"stops,omitempty"`
}
