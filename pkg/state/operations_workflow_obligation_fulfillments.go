package state

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func prepareObligationFulfillment(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, sequence int64, req api.OperationWorkflowReadinessCoordinationRequest, operator bool) (*api.OperationWorkflowObligationFulfillment, error) {
	input := req.Fulfillment
	if input == nil {
		return nil, nil
	}
	if req.Reopening != nil || req.ReviewCoordination != nil || req.Reaffirmation != nil || req.TargetBinding != nil || req.Completion != nil || req.ReviewResponse != nil || input.Reason == "" || len(input.Reason) > 2048 || strings.TrimSpace(input.Reason) != input.Reason || !utf8.ValidString(input.Reason) || strings.IndexFunc(input.Reason, unicode.IsControl) >= 0 || input.EvidenceReportID == "" || len(input.EvidenceReportID) > 128 {
		return nil, ErrInvalidArgument
	}
	operationID, err := uuid.Parse(input.EvidenceOperationID)
	if err != nil || operationID == uuid.Nil {
		return nil, ErrInvalidArgument
	}
	binding := input.Binding
	c := binding.Case
	if !operator && c.PlatformTenantID != "" || operator && c.PlatformTenantID == "" {
		return nil, ErrInvalidArgument
	}
	page, err := store.readReadinessInbox(ctx, account, tenant, app, scope, id, sequence-1, 1, operator)
	if err != nil {
		return nil, err
	}
	if len(page.Items) != 1 || page.Items[0].Sequence != sequence {
		return nil, ErrNotFound
	}
	n := page.Items[0]
	if n.Coordination == nil || n.Coordination.Revision != req.Revision {
		return nil, ErrConflict
	}
	if req.Status != n.Coordination.Status || req.Owner != n.Coordination.Owner || req.Note != n.Coordination.Note || readinessDigest(req.FollowUpAt) != readinessDigest(n.Coordination.FollowUpAt) {
		return nil, ErrInvalidArgument
	}
	found := false
	for _, stored := range n.Coordination.TargetBindings {
		if readinessDigest(stored) == readinessDigest(binding) {
			found = true
		}
	}
	if !found {
		return nil, ErrConflict
	}
	cases, ok := store.(OperationWorkflowCaseStore)
	if !ok {
		return nil, ErrInvalidArgument
	}
	opts := api.OperationWorkflowCoordinationTimelineOptions{AppID: app, Scope: scope, SubjectType: c.Subject.Type, SubjectID: c.Subject.ID, Workflow: c.Workflow, InstanceID: c.InstanceID}
	evidenceTenant := tenant
	if operator {
		opts.TenantID = c.PlatformTenantID
		evidenceTenant = c.PlatformTenantID
	}
	current, err := cases.WorkflowCase(ctx, account, tenant, opts, operator)
	if err != nil {
		return nil, err
	}
	result := &api.OperationWorkflowObligationFulfillment{Binding: binding, Reason: input.Reason}
	active := false
	for _, action := range current.NextActions {
		if action.SubscriptionID == id && action.Sequence == sequence && action.Obligation != nil && action.Obligation.ID == binding.ObligationID && bindableObligationKind(action.Kind) {
			active = true
			result.ActionKind = action.Kind
			result.Owner = action.Owner
		}
	}
	if !active {
		return nil, ErrConflict
	}
	op, err := store.OperationByID(ctx, account, evidenceTenant, operationID.String())
	if err != nil {
		return nil, err
	}
	if !sameOperationHistoryIdentity(op.AppID, app) || op.Scope != scope || !sameOperationHistoryIdentity(op.PlatformTenantID, evidenceTenant) || op.Subject == nil || *op.Subject != c.Subject || op.Name != binding.Transition.Operation {
		return nil, ErrNotFound
	}
	evidenceAfter := binding.BoundAt
	for _, reopening := range n.Coordination.Reopenings {
		if reopening.Binding.ObligationID == binding.ObligationID && reopening.Binding.Case == binding.Case && reopening.ReopenedAt.After(evidenceAfter) {
			evidenceAfter = reopening.ReopenedAt
		}
	}
	// Scan a bounded exact-case feed. The named report must itself record the bound
	// edge after binding, rather than a ready assessment or unrelated successful run.
	projection := api.OperationMilestoneListOptions{AppID: app, Scope: scope, SubjectType: c.Subject.Type, SubjectID: c.Subject.ID, Workflow: c.Workflow, WorkflowInstanceID: c.InstanceID, Limit: api.OperationHistoryPageMax}
	if operator {
		projection.TenantID = evidenceTenant
	}
	for scanned := 0; scanned < 1000; {
		var evidence api.OperationMilestonesResponse
		if operator {
			evidence, err = store.ListAccountOperationMilestones(ctx, account, projection)
		} else {
			evidence, err = store.ListPlatformTenantOperationMilestones(ctx, account, evidenceTenant, projection)
		}
		if err != nil {
			return nil, err
		}
		for _, report := range evidence.WorkflowStateHistory {
			scanned++
			if report.ID != input.EvidenceReportID || report.OperationID != operationID.String() {
				continue
			}
			target := binding.Transition
			if report.OperationID != operationID.String() || report.Workflow != c.Workflow || report.InstanceID != c.InstanceID || report.FromState != target.From || report.State != target.To || report.ContractVersion != target.ContractVersion || report.PublishedAt.Before(evidenceAfter) || report.BlockersOnly || report.DeadlineOnly || report.OutcomeOnly || report.DependenciesOnly {
				return nil, ErrInvalidArgument
			}
			report.PlatformTenantID = c.PlatformTenantID
			result.Evidence = report
			result.EvidenceCheckedAt = time.Now().UTC().Truncate(time.Microsecond)
			return result, nil
		}
		if evidence.NextWorkflowStateCursor == "" || len(evidence.WorkflowStateHistory) == 0 {
			break
		}
		projection.WorkflowStateCursor = evidence.NextWorkflowStateCursor
	}
	return nil, ErrNotFound
}

func obligationFulfilled(coord api.OperationWorkflowReadinessCoordination, obligation string, c api.OperationWorkflowNextActionCase, sourceTenant string) bool {
	for i := len(coord.Fulfillments) - 1; i >= 0; i-- {
		receipt := coord.Fulfillments[i]
		b := receipt.Binding
		actual, expected := b.Case.PlatformTenantID, c.PlatformTenantID
		if actual == "" {
			actual = sourceTenant
		}
		if expected == "" {
			expected = sourceTenant
		}
		if b.ObligationID == obligation && b.Case.Subject == c.Subject && b.Case.Workflow == c.Workflow && b.Case.InstanceID == c.InstanceID && (actual == expected || actual != "" && expected != "" && sameOperationHistoryIdentity(actual, expected)) {
			return !fulfillmentReopened(coord, receipt)
		}
	}
	return false
}

func applyObligationFulfillment(ctx context.Context, n api.OperationWorkflowReadinessNotification, req readinessCoordinationUpdate, actor string, now time.Time) (api.OperationWorkflowReadinessNotification, error) {
	if req.Fulfillment == nil {
		return n, nil
	}
	receipt := req.PreparedFulfillment
	if receipt == nil || n.Coordination == nil || len(n.Coordination.Fulfillments) >= 128 {
		return n, ErrConflict
	}
	binding := receipt.Binding
	found := false
	for _, stored := range n.Coordination.TargetBindings {
		if readinessDigest(stored) == readinessDigest(binding) {
			found = true
		}
	}
	if !found || obligationFulfilled(*n.Coordination, binding.ObligationID, binding.Case, req.TargetSourceTenant) {
		return n, ErrConflict
	}
	active := false
	for _, item := range sourceObligationActions(ctx, req.TargetSubscriptionID, req.TargetScope, n, req.TargetSourceTenant) {
		if item.Action.Obligation == nil || item.Action.Obligation.ID != binding.ObligationID || item.Action.Kind != receipt.ActionKind || item.Action.Owner != receipt.Owner {
			continue
		}
		for _, c := range item.Cases {
			if c.Subject == binding.Case.Subject && c.Workflow == binding.Case.Workflow && c.InstanceID == binding.Case.InstanceID && (binding.Case.PlatformTenantID == "" || sameOperationHistoryIdentity(c.PlatformTenantID, binding.Case.PlatformTenantID)) {
				active = true
			}
		}
	}
	if !active {
		return n, ErrConflict
	}
	value := *receipt
	value.CompletedAt, value.CompletedBy = now, actor
	coord := *n.Coordination
	coord.Fulfillments = append(slices.Clone(coord.Fulfillments), value)
	n.Coordination = &coord
	return n, nil
}

func fulfillmentEvent(receipt api.OperationWorkflowObligationFulfillment) api.OperationWorkflowObligationEvent {
	value := receipt
	return api.OperationWorkflowObligationEvent{Fulfillment: &value, ObligationID: receipt.Binding.ObligationID, ActionKind: receipt.ActionKind, Cases: []api.OperationWorkflowNextActionCase{receipt.Binding.Case}, Kind: "fulfilled", At: receipt.CompletedAt, Actor: receipt.CompletedBy, Explanation: "Explicit case obligation fulfillment: " + receipt.Reason}
}

// Coordination retains fulfillment even if the bounded source event journal prunes it.
func appendFulfillmentEvents(events []api.OperationWorkflowObligationEvent, coord *api.OperationWorkflowReadinessCoordination, obligation string) []api.OperationWorkflowObligationEvent {
	if coord == nil {
		return events
	}
	for _, receipt := range coord.Fulfillments {
		if obligation != "" && receipt.Binding.ObligationID != obligation {
			continue
		}
		found := false
		for _, event := range events {
			if event.Fulfillment != nil && readinessDigest(*event.Fulfillment) == readinessDigest(receipt) {
				found = true
				break
			}
		}
		if !found {
			events = append(events, fulfillmentEvent(receipt))
		}
	}
	for _, reopening := range coord.Reopenings {
		if obligation != "" && reopening.Binding.ObligationID != obligation {
			continue
		}
		found := false
		for _, event := range events {
			if event.Reopening != nil && readinessDigest(*event.Reopening) == readinessDigest(reopening) {
				found = true
				break
			}
		}
		if !found {
			events = append(events, reopeningEvent(reopening))
		}
	}
	for _, r := range coord.Reaffirmations {
		if obligation != "" && r.Binding.ObligationID != obligation {
			continue
		}
		if !slices.ContainsFunc(events, func(e api.OperationWorkflowObligationEvent) bool {
			return e.Reaffirmation != nil && readinessDigest(*e.Reaffirmation) == readinessDigest(r)
		}) {
			events = append(events, reaffirmationEvent(r))
		}
	}
	for _, review := range coord.FulfillmentReviews {
		if !review.ReviewRequired || obligation != "" && review.Binding.ObligationID != obligation {
			continue
		}
		if !slices.ContainsFunc(events, func(e api.OperationWorkflowObligationEvent) bool {
			return e.FulfillmentReview != nil && fulfillmentReviewKey(e.FulfillmentReview.Binding, e.FulfillmentReview.FulfilledAt) == fulfillmentReviewKey(review.Binding, review.FulfilledAt) && e.FulfillmentReview.Fingerprint == review.Fingerprint
		}) {
			events = append(events, fulfillmentReviewEvent(review, "fulfillment_review_required", review.CheckedAt, "system:fulfillment-review"))
		}
	}
	for _, change := range coord.ReviewCoordinationChanges {
		if obligation != "" && change.ReviewID != obligation {
			continue
		}
		if !slices.ContainsFunc(events, func(e api.OperationWorkflowObligationEvent) bool {
			return e.ReviewCoordination != nil && readinessDigest(*e.ReviewCoordination) == readinessDigest(change)
		}) {
			events = append(events, reviewCoordinationEvent(change))
		}
	}

	for _, reminder := range coord.FulfillmentReviewReminders {
		if obligation != "" && reminder.ReviewID != obligation {
			continue
		}
		if !slices.ContainsFunc(events, func(e api.OperationWorkflowObligationEvent) bool {
			return e.FulfillmentReviewReminder != nil && e.FulfillmentReviewReminder.NotificationID == reminder.NotificationID
		}) {
			events = append(events, fulfillmentReviewReminderEvent(reminder))
		}
	}

	for _, escalation := range coord.FulfillmentReviewEscalations {
		if obligation != "" && escalation.ReviewID != obligation {
			continue
		}
		if !slices.ContainsFunc(events, func(e api.OperationWorkflowObligationEvent) bool {
			return e.FulfillmentReviewEscalation != nil && e.FulfillmentReviewEscalation.NotificationID == escalation.NotificationID
		}) {
			events = append(events, fulfillmentReviewEscalationEvent(escalation))
		}
	}
	return events
}
