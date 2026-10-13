package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Outcome watches have an independent cursor; readiness discovery and verification cannot starve them.
func evaluateReadinessOutcomeWatches(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord) ([]readinessWatchUpdate, error) {
	page, err := store.readReadinessInbox(ctx, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, r.Subscription.ID, 0, api.OperationWorkflowReadinessInboxMax, r.Operator)
	if err != nil {
		return nil, err
	}
	candidates := []api.OperationWorkflowReadinessNotification{}
	for _, n := range page.Items {
		if (n.PlaybookReview != nil && n.PlaybookReview.Status == "active") || (n.BusinessOutcomeWatch != nil && readinessNotificationBaseline(n) != nil) {
			candidates = append(candidates, n)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].Sequence > r.OutcomeCursor, candidates[j].Sequence > r.OutcomeCursor
		if a != b {
			return a
		}
		return candidates[i].Sequence < candidates[j].Sequence
	})
	if len(candidates) > api.OperationWorkflowReadinessOutcomeWatchesPerPassMax {
		candidates = candidates[:api.OperationWorkflowReadinessOutcomeWatchesPerPassMax]
	}
	updates := readinessEscalationResponseUpdates(page.Items, r)
	updates = append(updates, reviewResponseCheckInUpdates(page.Items, r)...)
	updates = append(updates, reviewDecisionDeadlineUpdates(page.Items, r)...)
	updates = append(updates, reviewHandoffDeadlineUpdates(page.Items, r)...)
	updates = append(updates, reviewEscalationCommitmentUpdates(page.Items, r)...)
	updates = append(updates, reviewHandoffDeclineUpdates(page.Items, r)...)
	updates = append(updates, reviewHandoffReassignmentUpdates(page.Items, r)...)
	cursor := r.OutcomeCursor
	var firstErr error
	for _, n := range candidates {
		if n.PlaybookReview != nil && n.PlaybookReview.Status == "active" {
			cursor = n.Sequence
			update, err := observePlaybookCompletionReview(ctx, store, r, n)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				if ctx.Err() != nil {
					break
				}
				continue
			}
			updates = append(updates, update)
			continue
		}
		current, err := readReadinessBusinessOutcome(ctx, store, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, n, n.BusinessOutcomeWatch.Current.ExpectedOutcomeCode, r.Operator)
		cursor = n.Sequence
		// A failing source retains its last observation and rotates back for retry without starving other sources.
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if ctx.Err() != nil {
				break
			}
			continue
		}
		updates = append(updates, readinessOutcomeWatchObservation(n, *current))
	}
	r.OutcomeCursor = cursor
	return updates, firstErr
}

func readinessOutcomeWatchObservation(n api.OperationWorkflowReadinessNotification, current api.OperationWorkflowReadinessBusinessOutcome) readinessWatchUpdate {
	w := *n.BusinessOutcomeWatch
	previous := w.Current.Status
	saved := readinessNotificationBaseline(n)
	if current.ContractVersion == effectiveWorkflowContractVersion(saved.ContractVersion) && current.StateRevision > 0 {
		if current.StateRevision < w.StateRevision {
			current.Status, current.Explanation = "unknown", "Retained application state precedes a previously observed watch revision; no business result is inferred."
		} else {
			w.StateRevision = current.StateRevision
		}
	}
	out := readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, WatchID: w.ID, Revision: w.Revision}
	event := api.OperationWorkflowReadinessBusinessOutcomeEvent{WatchID: w.ID, SourceNotificationID: n.ID, SourceSequence: n.Sequence, Source: *saved, PreviousStatus: previous, DeadlineAt: w.DeadlineAt, Current: current, Escalation: w.Escalation}
	if current.Status != "pending" && (current.Status != previous || w.LastNotifiedStatus == "") {
		kind := "outcome_" + current.Status
		if current.Status == "unknown" {
			kind = "outcome_unavailable"
		}
		out.Kinds = append(out.Kinds, kind)
		out.OutcomeEvents = append(out.OutcomeEvents, event)
		w.LastNotifiedStatus = current.Status
	}
	// This is an observation at the first successful due poll, not a business failure or a historical deadline verdict.
	if w.DeadlineAt != nil && !w.DeadlineNotified && w.Current.CheckedAt.Before(*w.DeadlineAt) && !current.CheckedAt.Before(*w.DeadlineAt) && current.Status != "achieved" {
		out.Kinds = append(out.Kinds, "outcome_deadline_reached")
		out.OutcomeEvents = append(out.OutcomeEvents, event)
		w.DeadlineNotified = true
	}
	w.Current = current
	w.Revision++
	out.OutcomeWatch = &w
	return out
}

// The existing subscription lease and source-row lock fence metadata updates and deliveries atomically.
func applyReadinessOutcomeWatchUpdate(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	if n.ID != u.SourceID || n.BusinessOutcomeWatch == nil || n.BusinessOutcomeWatch.ID != u.WatchID || n.BusinessOutcomeWatch.Revision != u.Revision {
		return n, nil, false
	}
	n.BusinessOutcomeWatch = u.OutcomeWatch
	notifications := []api.OperationWorkflowReadinessNotification{}
	for i, event := range u.OutcomeEvents {
		explanation := "Watched application business outcome changed; consult the scoped application report. Coordination completion does not establish causation or authorize execution."
		if u.Kinds[i] == "outcome_deadline_reached" {
			explanation = "The outcome deadline has passed and the expected result is not observed at this check. This does not establish business failure."
		}
		notification := readinessSubscriptionNotify(r, u.Kinds[i], explanation, nil)
		if u.Kinds[i] == "outcome_deadline_reached" && n.BusinessOutcomeWatch.EscalationPolicy != nil && n.BusinessOutcomeWatch.Escalation == nil {
			policy := n.BusinessOutcomeWatch.EscalationPolicy
			due := notification.CreatedAt.Add(time.Duration(policy.FollowUpAfterSeconds) * time.Second)
			notification.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: policy.Owner, Note: policy.Note, FollowUpAt: &due, UpdatedAt: notification.CreatedAt, UpdatedBy: "system:outcome_escalation"}
			notification.OutcomeEscalation = &api.OperationWorkflowReadinessOutcomeEscalation{WatchID: event.WatchID, SourceNotificationID: event.SourceNotificationID, SourceSequence: event.SourceSequence, Current: event.Current}
			if policy.AcknowledgeAfterSeconds > 0 {
				notification.OutcomeEscalation.Response = &api.OperationWorkflowReadinessEscalationResponse{DeadlineAt: notification.CreatedAt.Add(time.Duration(policy.AcknowledgeAfterSeconds) * time.Second), InitialOwner: policy.Owner, FallbackOwner: policy.FallbackOwner, Status: "awaiting", History: []api.OperationWorkflowReadinessAssignmentChange{}}
			}
			ref := &api.OperationWorkflowReadinessOutcomeEscalationReference{NotificationID: notification.ID, Sequence: notification.Sequence, CreatedAt: notification.CreatedAt}
			n.BusinessOutcomeWatch.Escalation = ref
			event.Escalation = ref
		}
		notification.BusinessOutcome = &event
		notifications = append(notifications, notification)
	}
	return n, notifications, true
}

// Preserve acknowledgment, deadlines, reminders and immutable completion when refreshing watch evidence.
func syncReadinessOutcomeEscalation(n, source api.OperationWorkflowReadinessNotification) (api.OperationWorkflowReadinessNotification, bool) {
	w := source.BusinessOutcomeWatch
	if w == nil || w.Escalation == nil || n.ID != w.Escalation.NotificationID || n.Sequence != w.Escalation.Sequence || n.OutcomeEscalation == nil || n.OutcomeEscalation.WatchID != w.ID || n.OutcomeEscalation.SourceNotificationID != source.ID || n.OutcomeEscalation.SourceSequence != source.Sequence {
		return n, false
	}
	e := *n.OutcomeEscalation
	e.Current = w.Current
	if w.Current.Status == "achieved" && e.SuccessObservedAt == nil {
		at := w.Current.CheckedAt
		e.SuccessObservedAt = &at
	}
	n.OutcomeEscalation = &e
	return n, true
}
