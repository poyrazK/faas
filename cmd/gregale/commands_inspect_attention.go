package main

import (
	"fmt"
	"io"
	"strings"
)

// Present existing observations first; do not add a health claim or JSON fields.
func renderInspectAttention(w io.Writer, summary inspectSummary) {
	state, reason, command := inspectAttention(summary)
	if summary.Failure != nil && !summary.Failure.Candidate && summary.Failure.Cause != "" && reason == "The latest deployment failed." {
		reason = summary.Failure.Cause
	}
	_, _ = fmt.Fprintf(w, "%s: %s\n  Reason: %s\n", oneLine(summary.App.Slug), state, oneLine(redactErrorText(reason)))
	_, _ = fmt.Fprintf(w, "  Next step (POSIX shells):\n    %s\n\n", redactErrorText(command))
}

func inspectAttention(summary inspectSummary) (state, reason, command string) {
	command = recoveryCLI() + " inspect " + recoveryArgument(summary.App.Slug)
	for _, recommendation := range summary.Recommendations {
		if recommendation.Severity == "error" {
			return "Needs attention", recommendation.Message, inspectAttentionCommand(summary, recommendation.Code)
		}
	}
	if summary.Operational != nil {
		for _, operation := range summary.Operational.Recovery.Rollbacks {
			if operation.Status == "blocked" {
				return "Needs attention", "A rollback is blocked; its target is not confirmed as serving.", recoveryCLI() + " rollback status " + recoveryArgument(summary.App.Slug) + " --operation " + recoveryArgument(operation.ID)
			}
		}
		for _, operation := range summary.Operational.Recovery.Restarts {
			if operation.Status == "queued" || operation.Status == "running" || operation.Status == "retrying" {
				return "In progress", operation.ProgressMessage(), inspectRestartCommand(summary.App.Slug, operation.WakeID)
			}
		}
		for _, operation := range summary.Operational.Recovery.Rollbacks {
			if operation.Status == "preparing" || operation.Status == "ready" || operation.Status == "routing" {
				return "In progress", "A rollback is still being processed; recovery is not confirmed.", recoveryCLI() + " rollback status " + recoveryArgument(summary.App.Slug) + " --operation " + recoveryArgument(operation.ID)
			}
		}
	}
	switch summary.Release.Status {
	case "queued", "pending", "building", "imaging", "snapshotting":
		return "In progress", "Deployment processing has not completed (" + summary.Release.Status + ").", recoveryCLI() + " deployment get " + recoveryArgument(summary.Release.DeploymentID)
	}
	if len(summary.Unavailable) > 0 {
		return "Incomplete information", "Could not read: " + strings.Join(summary.Unavailable, ", ") + ". Current health is not established.", command
	}
	for _, recommendation := range summary.Recommendations {
		if recommendation.Severity == "warning" {
			return "Review recommended", recommendation.Message, inspectAttentionCommand(summary, recommendation.Code)
		}
	}
	for _, recommendation := range summary.Recommendations {
		if recommendation.Code == "app_parked" {
			return "Parked", recommendation.Message, inspectAttentionCommand(summary, recommendation.Code)
		}
	}
	return "No urgent issues reported", "This snapshot has no error or warning recommendations; it does not verify every route or workload.", recoveryCLI() + " app " + recoveryArgument(summary.App.Slug) + " health"
}

// Construct commands from known observations, rather than executing or parsing
// free-form server advice. Unknown recommendation codes keep a read-only fallback.
func inspectAttentionCommand(summary inspectSummary, code string) string {
	cli, slug := recoveryCLI(), recoveryArgument(summary.App.Slug)
	switch code {
	case "deployment_failed", "newer_candidate_failed", "no_live_deployment":
		if summary.Failure != nil {
			return cli + " deployment get " + recoveryArgument(summary.Failure.DeploymentID)
		}
		return cli + " inspect " + slug + " --errors"
	case "health_gate_firing":
		return cli + " alerts list --app " + slug
	case "app_parked":
		if summary.App.ParkedReason == "liveness_exhausted" {
			return cli + " logs " + slug + " --since 1h"
		}
		return cli + " wake " + slug
	case "production_health_violated", "production_health_unknown", "production_incident_open":
		command := cli + " routes monitor report " + slug
		if code == "production_incident_open" && summary.Operational != nil && summary.Operational.Monitoring.Incident != nil {
			command = cli + " routes monitor explain " + slug + " --incident " + recoveryArgument(summary.Operational.Monitoring.Incident.ID)
		}
		return command
	}
	if summary.Operational != nil {
		for _, operation := range summary.Operational.Recovery.Restarts {
			if code == "restart_"+operation.Status {
				return inspectRestartCommand(summary.App.Slug, operation.WakeID)
			}
		}
		for _, operation := range summary.Operational.Recovery.Rollbacks {
			if code == "rollback_"+operation.Status {
				return cli + " rollback status " + slug + " --operation " + recoveryArgument(operation.ID)
			}
		}
	}
	if summary.Release.DeploymentID != "" {
		return cli + " deployment get " + recoveryArgument(summary.Release.DeploymentID)
	}
	return cli + " app " + slug
}

func inspectRestartCommand(slug, wakeID string) string {
	return recoveryCLI() + " app " + recoveryArgument(slug) + " restart status --wake-id " + recoveryArgument(wakeID)
}
