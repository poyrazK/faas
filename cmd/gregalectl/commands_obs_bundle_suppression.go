package main

import "github.com/onebox-faas/faas/pkg/api"

func validObsBundleSuppression(e api.ObsIncidentEvidenceResponse) bool {
	s := e.Suppression
	if s == nil {
		return true
	} // Earlier servers keep coverage explicitly unknown.
	if e.Incident.Type != "platform_alert" || s.CheckedAt.IsZero() {
		return false
	}
	switch s.State {
	case "active", "suppressed":
		return s.UpdatedAt != nil && s.Silenced != nil && s.Inhibited != nil && (s.State == "active" && !*s.Silenced && !*s.Inhibited || s.State == "suppressed" && (*s.Silenced || *s.Inhibited))
	case "stale", "unprocessed":
		return s.UpdatedAt != nil && s.Silenced == nil && s.Inhibited == nil
	case "unknown", "unmatched", "unavailable", "disabled", "not_applicable":
		return s.Silenced == nil && s.Inhibited == nil
	default:
		return false
	}
}
