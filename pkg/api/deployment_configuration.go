package api

// DeploymentConfigurationStatus describes server-managed cron activation only.
// CLI-owned lifecycle/scaling steps are additionally reported in CLI receipts.
type DeploymentConfigurationStatus struct {
	Complete bool                           `json:"complete"`
	Crons    DeploymentCronActivationStatus `json:"crons"`
}

type DeploymentCronActivationStatus struct {
	SessionID string `json:"session_id,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
}

// NewDeploymentConfigurationStatus keeps API and CLI diagnostics consistent.
func NewDeploymentConfigurationStatus(status, reason string) DeploymentConfigurationStatus {
	c := DeploymentCronActivationStatus{Reason: reason}
	switch status {
	case "prepared":
		c.Status = "pending"
	case "conflict":
		c.Status = "conflicted"
	case "canceled", "discarded":
		c.Status = "discarded"
	case "activated":
		c.Status = "activated"
	default:
		c.Status = "not_managed"
	}
	switch reason {
	case "awaiting_readiness":
		c.Message = "Cron changes are waiting for the release to become live."
	case "awaiting_full_traffic":
		c.Message = "Cron changes are waiting for this release to serve 100% of production traffic."
	case "awaiting_rollout":
		c.Message = "Cron changes are waiting for the rollout to complete."
	case "competing_traffic":
		c.Message = "Another live release still serves production traffic."
	case "awaiting_activation":
		c.Message = "The release is ready; cron activation is pending."
	case "cron_changed":
		c.Message = "Cron configuration changed after this deployment was prepared."
	case "quota_changed":
		c.Message = "The desired cron set no longer fits the current plan quota."
	case "identity_conflict":
		c.Message = "A cron identity changed or belongs to another configuration."
	case "plan_unavailable":
		c.Message = "The current account plan could not be used to validate cron activation."
	case "intent_superseded":
		c.Message = "A newer deployment attempt replaced these pending cron changes."
	case "deployment_superseded":
		c.Message = "A newer production release superseded this deployment."
	case "deployment_failed":
		c.Message = "The deployment failed before cron activation."
	case "deployment_canceled":
		c.Message = "The deployment was canceled before cron activation."
	case "rollout_aborted":
		c.Message = "The rollout was aborted before cron activation."
	case "non_production":
		c.Message = "This release does not activate production cron changes."
	case "activation_discarded":
		c.Message = "Cron changes were discarded before activation; no detailed reason was retained."
	case "cron_guard_conflict":
		c.Message = "Cron activation could not satisfy the configuration guards."
	}
	switch c.Status {
	case "not_managed":
		c.Message = "No deferred cron activation is recorded for this deployment."
	case "activated":
		c.Message = "Cron changes were activated for this deployment."
	case "pending":
		c.Hint = "The server continues automatically. Inspect deployment status and production traffic; do not submit another build to check progress."
	case "conflicted", "discarded":
		c.Hint = "Active schedules were preserved. Inspect current crons and the stated reason, then start a new deployment attempt if changes are still needed. Resume only checks status."
	}
	return DeploymentConfigurationStatus{Complete: c.Status == "activated" || c.Status == "not_managed", Crons: c}
}
