package api

import "time"

// MCPOAuthReauthorizationNeedsConfirmation identifies uncertain renewal or
// authorization outcomes without exposing journal credentials.
func MCPOAuthReauthorizationNeedsConfirmation(view MCPCatalogMonitorSnapshot) bool {
	status := view.ObserverStatus
	if status == nil {
		return false
	}
	journal := status.Renewal
	return journal != nil && (journal.Outcome == "issuance_unknown" || journal.State == "pending" || journal.State == "reauthorization_required" || journal.Result != nil && journal.Result.State == "reauthorization_required") || status.OAuthFlow != nil && status.OAuthFlow.Consumed
}

// MCPOAuthRenewalRetryAllowed schedules an observer only when another exchange
// is known safe, or when a recorded successful response can be installed.
func MCPOAuthRenewalRetryAllowed(view MCPCatalogMonitorSnapshot, at time.Time) bool {
	cfg, status := view.Config.Observer, view.ObserverStatus
	if !view.Config.Enabled || cfg == nil || !cfg.Enabled || cfg.Paused || cfg.OAuth == nil || status == nil || status.LeaseUntil != nil && status.LeaseUntil.After(at) || status.OAuthFlow != nil && (status.OAuthFlow.Consumed || status.OAuthFlow.ExpiresAt.After(at)) {
		return false
	}
	if j := status.Renewal; j != nil {
		if j.Outcome == "issuance_unknown" {
			return false
		}
		if j.State == "saved" {
			return j.Outcome == "response_recorded" && j.Result != nil && j.Result.State == "connected"
		}
		if j.State != "failed" && j.State != "installed" {
			return false
		}
	}
	if status.OAuth != nil && status.OAuth.State == "reauthorization_required" {
		return false
	}
	if cfg.OAuth.Guided && (status.OAuth == nil || status.OAuth.State != "connected") {
		return false
	}
	if status.Renewal != nil && status.Renewal.State == "failed" {
		return true
	}

	return status.FailureReason == "credential_unavailable"
}
