package api

import "time"

// ProjectPromotionAdmission preserves queue waiting separately from execution.
// While phase is queued, health started_at/deadline describe waiting only.
// StartedAt is set once, when the original intent is revalidated and dispatched.
type ProjectPromotionAdmission struct {
	QueuedAt     time.Time  `json:"queued_at"`
	WaitDeadline time.Time  `json:"wait_deadline"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	Reason       string     `json:"reason"`
}

const CodeProjectPromotionQueueFull = "project_promotion_queue_full"
