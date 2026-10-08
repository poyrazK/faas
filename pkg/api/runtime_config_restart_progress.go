package api

// ProgressMessage and ProgressNextStep present the same safe status explanation
// in the CLI and dashboard. Processing completion is separate from app health.
func (r RuntimeConfigRestartStatusResponse) ProgressMessage() string {
	message, _ := runtimeConfigRestartProgress(r)
	return message
}

func (r RuntimeConfigRestartStatusResponse) ProgressNextStep() string {
	_, next := runtimeConfigRestartProgress(r)
	return next
}

func runtimeConfigRestartProgress(r RuntimeConfigRestartStatusResponse) (string, string) {
	switch r.Status {
	case "completed":
		return "Restart processing is complete. Application health has not been verified.", "Check Current operations with gregale inspect."
	case "failed":
		message := "Restart processing failed."
		switch r.FailureReason {
		case "requests_active":
			message = "Restart failed before active requests finished."
		case "quiet_period_not_elapsed":
			message = "Restart failed before a quiet period was observed."
		case "telemetry_missing":
			message = "Restart failed without request activity information."
		}
		return message, "Inspect app logs and instances before requesting another restart."
	case "queued":
		return "Restart request is queued.", "Wait for processing to begin or check this restart again."
	case "running":
		return "Restart is being processed.", "Wait for completion or check this restart again."
	case "retrying":
		switch r.FailureReason {
		case "requests_active":
			return "Waiting for active requests to finish.", "Allow requests to finish, then check this restart again."
		case "quiet_period_not_elapsed":
			return "Waiting for a quiet period before replacing an instance.", "Check this restart again after traffic settles."
		case "telemetry_missing":
			return "Waiting for request activity information.", "Inspect app logs and instances if this keeps retrying."
		case "restart_attempt_failed":
			return "A restart attempt was unsuccessful; processing will retry.", "Inspect app logs and instances if this keeps retrying."
		}
		return "Restart is being processed.", "Wait for completion or check this restart again."
	default:
		return "Restart status is unavailable.", "Check this restart again; an unavailable status does not establish failure."
	}
}
