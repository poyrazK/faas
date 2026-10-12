package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// History is already scoped by account, app, scope and subject. Never join runs or contracts.
func linkWorkflowOutcomes(rows []api.OperationWorkflowStateHistoryEntry) {
	for i := range rows {
		start := rows[i]
		link := &api.OperationWorkflowOutcomeLink{Status: "outcome_unavailable", Summary: "No later reported outcome is available on this retained page."}
		rows[i].OutcomeLink = link
		if start.Comparison != nil && start.Comparison.Status == "ambiguous_revision" {
			link.Status, link.Summary = "history_incomplete", "Duplicate revisions prevent outcome correlation."
			continue
		}
		previous := start
		var elapsed, blocked time.Duration
		for j := i; j < len(rows); j++ {
			current := rows[j]
			if current.PlatformTenantID != start.PlatformTenantID || current.Workflow != start.Workflow || current.InstanceID != start.InstanceID {
				continue
			}
			if j != i {
				if current.Comparison == nil || current.Comparison.Status != "compared" || current.Comparison.PreviousReportID != previous.ID || effectiveWorkflowContractVersion(current.ContractVersion) != effectiveWorkflowContractVersion(start.ContractVersion) || current.FromState != previous.State {
					link.Status, link.Summary = "history_incomplete", "Missing, ambiguous, or incompatible revisions prevent outcome correlation."
					break
				}
				if previous.OccurredAt.IsZero() || current.OccurredAt.IsZero() || current.OccurredAt.Before(previous.OccurredAt) || current.OccurredAt.Sub(start.OccurredAt) == time.Duration(1<<63-1) {
					link.Status, link.Summary = "timing_inconsistent", "Reported occurrence times cannot establish outcome duration."
					break
				}
				interval := current.OccurredAt.Sub(previous.OccurredAt)
				elapsed += interval
				if len(previous.Blockers) > 0 {
					blocked += interval
				}
			}
			if current.OutcomeCode != "" {
				link.Status = "linked"
				link.Summary = "Application reported this outcome at or after the retained report; durations cover only this observed path and do not establish causation."
				link.OutcomeReportID, link.OutcomeOperationID, link.OutcomeRevision, link.OutcomeCode = current.ID, current.OperationID, current.Revision, current.OutcomeCode
				link.ElapsedSeconds, link.BlockedSeconds = int64(elapsed/time.Second), int64(blocked/time.Second)
				break
			}
			previous = current
		}
	}
}
