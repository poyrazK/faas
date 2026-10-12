package state

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Publication is fenced by the existing exact progress revision and subscription lease.
func publishPlaybookStepAssignments(r *readinessSubscriptionRecord, index int, p api.OperationWorkflowPlaybookProgress) []api.OperationWorkflowReadinessNotification {
	w := &r.Subscription.PlaybookEvidenceWatches[index]
	notices := []api.OperationWorkflowReadinessNotification{}
	for _, follow := range p.OutcomeFollowUps {
		for itemIndex, item := range follow.Items {
			if item.DueAt == nil || item.Status == "completed" || len(w.StepAssignments) >= api.OperationWorkflowPlaybookEvidenceMax {
				continue
			}
			if slices.ContainsFunc(w.StepAssignments, func(ref api.OperationWorkflowPlaybookStepAssignmentReference) bool {
				return ref.OutcomeCode == follow.OutcomeCode && ref.ItemIndex == itemIndex
			}) {
				continue
			}
			n := readinessSubscriptionNotify(r, "playbook_outcome_step_assigned", "An outcome follow-up step is assigned for human review. Its deadline is pinned to list enrollment; completion does not establish application execution.", nil)
			n.PlaybookEvidence = &api.OperationWorkflowPlaybookEvidenceEvent{WatchID: w.ID, ProgressID: w.ProgressID, PlatformTenantID: w.PlatformTenantID, Subject: w.Subject, Workflow: w.Workflow, InstanceID: w.InstanceID, ContractVersion: w.ContractVersion, PlaybookID: w.PlaybookID, PlaybookVersion: w.PlaybookVersion, CheckedAt: w.EvaluatedAt, Changes: []api.OperationWorkflowPlaybookEvidenceChange{}, BusinessOutcome: &follow.Observation}
			n.PlaybookStep = &api.OperationWorkflowPlaybookStepAssignment{ProgressID: p.ID, OutcomeCode: follow.OutcomeCode, ItemIndex: itemIndex, Label: item.Label, Status: item.Status, DueAt: *item.DueAt, LastStepOwner: item.Owner, History: []api.OperationWorkflowReadinessAssignmentChange{}}
			n.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: item.Owner, FollowUpAt: item.DueAt, UpdatedAt: n.CreatedAt, UpdatedBy: "system:playbook-step-assignment"}
			w.StepAssignments = append(w.StepAssignments, api.OperationWorkflowPlaybookStepAssignmentReference{OutcomeCode: follow.OutcomeCode, ItemIndex: itemIndex, Notification: api.OperationWorkflowReadinessOutcomeEscalationReference{NotificationID: n.ID, Sequence: n.Sequence, CreatedAt: n.CreatedAt}})
			notices = append(notices, n)
		}
	}
	return notices
}

func appendPlaybookStepAssignmentHistory(step *api.OperationWorkflowPlaybookStepAssignment, change api.OperationWorkflowReadinessAssignmentChange) {
	step.History = append(slices.Clone(step.History), change)
	if len(step.History) > api.OperationWorkflowReadinessAssignmentHistoryMax {
		step.History = step.History[len(step.History)-api.OperationWorkflowReadinessAssignmentHistoryMax:]
		step.HistoryTruncated = true
	}
}

func syncPlaybookStepAssignment(n api.OperationWorkflowReadinessNotification, p api.OperationWorkflowPlaybookProgress, ref api.OperationWorkflowPlaybookStepAssignmentReference) (api.OperationWorkflowReadinessNotification, bool) {
	if n.PlaybookStep == nil || n.Coordination == nil || n.ID != ref.Notification.NotificationID || n.Sequence != ref.Notification.Sequence || n.PlaybookStep.ProgressID != p.ID || n.PlaybookStep.OutcomeCode != ref.OutcomeCode || n.PlaybookStep.ItemIndex != ref.ItemIndex {
		return n, false
	}
	var item *api.OperationWorkflowPlaybookProgressItem
	for _, follow := range p.OutcomeFollowUps {
		if follow.OutcomeCode == ref.OutcomeCode && ref.ItemIndex >= 0 && ref.ItemIndex < len(follow.Items) {
			value := follow.Items[ref.ItemIndex]
			item = &value
			break
		}
	}
	if item == nil {
		return n, false
	}
	step := *n.PlaybookStep
	c := *n.Coordination
	now := time.Now().UTC().Truncate(time.Microsecond)
	changed := false
	if step.LastStepOwner != item.Owner {
		if !step.ManualOwner && c.Completion == nil && c.Revision < api.OperationWorkflowReadinessCoordinationRevisionMax {
			appendPlaybookStepAssignmentHistory(&step, api.OperationWorkflowReadinessAssignmentChange{FromOwner: c.Owner, ToOwner: item.Owner, At: now, Actor: item.ReviewedBy, Reason: "step_assignment"})
			c.Owner = item.Owner
			changed = true
		}
		step.LastStepOwner = item.Owner
	}
	step.Status = item.Status
	if item.Status == "completed" && c.FollowUpAt != nil && c.Completion == nil && c.Revision < api.OperationWorkflowReadinessCoordinationRevisionMax {
		c.FollowUpAt = nil
		n.Reminder = nil
		changed = true
	}
	if changed {
		c.Revision++
		c.UpdatedAt = now
		c.UpdatedBy = "system:playbook-step-sync"
		n.Coordination = &c
	}
	n.PlaybookStep = &step
	return n, true
}
