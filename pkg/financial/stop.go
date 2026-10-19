package financial

import "time"

// PreviewStopping is a delayed response to recorded compute spending. It does
// not reserve charges or promise a strict monetary ceiling.
func PreviewStopping(p BudgetSpec) bool {
	return p.Mode == "monitored" && p.Action == "stop_previews" &&
		len(p.Meters) == 1 && p.Meters[0] == "compute"
}

func Monitored(p BudgetSpec) bool { return NotificationOnly(p) || PreviewStopping(p) }

// BudgetStop is the immutable threshold decision. Corrections and unavailable
// evidence never silently resume work after this decision has been committed.
type BudgetStop struct {
	ID            string             `json:"id"`
	PolicyID      string             `json:"policy_id"`
	Revision      int64              `json:"revision"`
	PeriodStart   time.Time          `json:"period_start"`
	PeriodEnd     time.Time          `json:"period_end"`
	RequestedAt   time.Time          `json:"requested_at"`
	DrainDeadline time.Time          `json:"drain_deadline"`
	ResumeRule    string             `json:"resume_rule"`
	Evaluation    BudgetEvaluation   `json:"evaluation"`
	Targets       []BudgetStopTarget `json:"targets"`
}

// Enforced is recorded only after vmmd confirms teardown and the instance
// records no longer own resident resources. It is separate from notification.
type BudgetStopTarget struct {
	AppID         string    `json:"app_id"`
	EnvironmentID string    `json:"environment_id,omitempty"`
	State         string    `json:"state"`
	ObservedAt    time.Time `json:"observed_at"`
	ResidentCount int       `json:"resident_count"`
	LastError     string    `json:"last_error,omitempty"`
}
