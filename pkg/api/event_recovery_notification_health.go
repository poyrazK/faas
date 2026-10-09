package api

import "time"

// Notification health counts describe jobs, independently for admission and
// execution notices. Overdue, dead, unknown and no-receiver counts may overlap.
type EventRecoveryNotificationsHealth struct {
	Coverage            string                                `json:"coverage"`
	ObservedJobs        int64                                 `json:"observed_jobs"`
	CountsComplete      bool                                  `json:"counts_complete"`
	JobLimit            int                                   `json:"job_limit"`
	OverdueGraceSeconds int64                                 `json:"overdue_grace_seconds"`
	Admission           EventRecoveryNotificationHealthCounts `json:"admission"`
	Execution           EventRecoveryNotificationHealthCounts `json:"execution"`
	Jobs                []EventRecoveryNotificationJobHealth  `json:"jobs"`
}
type EventRecoveryNotificationHealthCounts struct {
	CountsComplete  bool  `json:"counts_complete"`
	OverdueJobs     int64 `json:"overdue_jobs"`
	DeadJobs        int64 `json:"dead_jobs"`
	UnknownJobs     int64 `json:"unknown_jobs"`
	NoReceiversJobs int64 `json:"no_receivers_jobs"`
}
type EventRecoveryNotificationJobHealth struct {
	JobID                    string     `json:"job_id"`
	Kind                     string     `json:"kind"`
	Event                    string     `json:"event,omitempty"`
	CaptureStatus            string     `json:"capture_status"`
	AcknowledgementStatus    string     `json:"acknowledgement_status"`
	EvidenceSource           string     `json:"evidence_source"`
	CapturedAt               *time.Time `json:"captured_at,omitempty"`
	UnacknowledgedAgeSeconds *float64   `json:"unacknowledged_age_seconds,omitempty"`
	Overdue                  bool       `json:"overdue"`
	Dead                     bool       `json:"dead"`
	Unknown                  bool       `json:"unknown"`
	NoReceivers              bool       `json:"no_receivers"`
}

func IsEventRecoveryNotificationAlertMetric(metric string) bool {
	switch metric {
	case "event_recovery_notification_admission_overdue_jobs":
		return true
	case "event_recovery_notification_admission_dead_jobs":
		return true
	case "event_recovery_notification_admission_unknown_jobs":
		return true
	case "event_recovery_notification_admission_no_receivers_jobs":
		return true
	case "event_recovery_notification_execution_overdue_jobs":
		return true
	case "event_recovery_notification_execution_dead_jobs":
		return true
	case "event_recovery_notification_execution_unknown_jobs":
		return true
	case "event_recovery_notification_execution_no_receivers_jobs":
		return true
	}
	return false
}
