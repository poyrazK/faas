package api

// Notification health counts describe jobs, independently for admission and
// execution notices. Overdue, dead, unknown and no-receiver counts may overlap.

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
