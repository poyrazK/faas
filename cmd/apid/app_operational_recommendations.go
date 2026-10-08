package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func appOperationalRecommendations(slug string, summary api.AppOperationalSummary) []api.AppOperationalRecommendation {
	out := []api.AppOperationalRecommendation{}
	add := func(code, severity, message, next string) {
		out = append(out, api.AppOperationalRecommendation{Code: code, Severity: severity, Message: message, Next: next})
	}
	monitor := summary.Monitoring
	if monitor.Available && monitor.Status == "violated" {
		add("production_health_violated", "error", "Observed production routes exceed their configured budgets.", "Run `gregale routes monitor report "+slug+"` to inspect the current evidence.")
	} else if !monitor.Available || monitor.Status == "unknown" {
		add("production_health_unknown", "warning", "Current production route health is not established; deployment verification is a separate result.", "Run `gregale routes monitor report "+slug+"` and review its coverage and observation windows.")
	}
	if monitor.Incident != nil {
		add("production_incident_open", "error", "A saved production-route incident remains open; this read does not declare recovery.", "Run `gregale routes monitor explain "+slug+" --incident "+monitor.Incident.ID+"`.")
	}
	for _, operation := range summary.Recovery.Rollbacks {
		severity := "info"
		if operation.Status == "blocked" {
			severity = "warning"
		}
		add("rollback_"+operation.Status, severity,
			fmt.Sprintf("Rollback %s is %s; target %s is not yet confirmed as the recovered serving release.", operation.ID, operation.Status, operation.TargetDeploymentID),
			"Run `gregale rollback status "+slug+" --operation "+operation.ID+"` for progress and blockers.")
	}
	for _, operation := range summary.Recovery.Restarts {
		severity := "info"
		switch operation.Status {
		case "failed":
			severity = "error"
		case "retrying", "unknown":
			severity = "warning"
		}
		add("restart_"+operation.Status, severity,
			fmt.Sprintf("Restart %s: %s", operation.WakeID, operation.ProgressMessage()),
			"Run `gregale app "+slug+" restart status --wake-id "+operation.WakeID+" --wait` for progress.")
	}
	return out
}
