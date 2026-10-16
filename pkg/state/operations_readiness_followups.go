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

func validateReadinessNotificationIdentity(account, tenant, app, scope, id string, sequence int64, operator bool) error {
	if id == "" || sequence < 1 || sequence > 9007199254740991 {
		return ErrInvalidArgument
	}
	return readinessSubscriptionIdentity(account, tenant, app, scope, id, operator)
}
func updateReadinessNotification(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, sequence int64, actor string, req api.OperationWorkflowReadinessCoordinationRequest, operator bool) (api.OperationWorkflowReadinessNotification, error) {
	var out api.OperationWorkflowReadinessNotification
	if err := validateReadinessNotificationIdentity(account, tenant, app, scope, id, sequence, operator); err != nil {
		return out, err
	}
	if actor == "" || req.Revision < 0 || req.Revision >= 9007199254740991 || (req.Status != "unread" && req.Status != "read" && req.Status != "acknowledged") || len(req.Owner) > api.OperationWorkflowReadinessFollowUpOwnerMaxBytes || len(req.Note) > api.OperationWorkflowReadinessFollowUpNoteMaxBytes || !utf8.ValidString(req.Owner) || !utf8.ValidString(req.Note) || strings.TrimSpace(req.Owner) != req.Owner || strings.IndexFunc(req.Owner, unicode.IsControl) >= 0 {
		return out, ErrInvalidArgument
	}
	if req.FollowUpAt != nil {
		t := req.FollowUpAt.UTC().Truncate(time.Microsecond)
		if t.Year() < 1 || t.Year() > 9999 {
			return out, ErrInvalidArgument
		}
		req.FollowUpAt = &t
	}
	prepared, err := prepareReadinessCompletion(ctx, store, account, tenant, app, scope, id, sequence, req, operator)
	if err != nil {
		return out, err
	}
	if req.ReviewResponse != nil {
		prepared.PreparedReviewResponse, err = prepareReviewResponse(ctx, store, account, tenant, app, scope, id, sequence, req, operator)
		if err != nil {
			return out, err
		}
	}
	prepared.PreparedEscalationActionReview, err = prepareEscalationActionReview(ctx, store, account, tenant, app, scope, id, sequence, req, operator)
	if err != nil {
		return out, err
	}
	prepared.PreparedTargetBinding, err = prepareObligationTarget(ctx, store, account, tenant, app, scope, id, sequence, req, operator)
	if err != nil {
		return out, err
	}
	prepared.PreparedFulfillment, err = prepareObligationFulfillment(ctx, store, account, tenant, app, scope, id, sequence, req, operator)
	if err != nil {
		return out, err
	}
	prepared.PreparedReopening, err = prepareObligationReopening(ctx, store, account, tenant, app, scope, id, sequence, req, operator)
	if err != nil {
		return out, err
	}
	prepared.PreparedReaffirmation, err = prepareObligationReaffirmation(ctx, store, account, tenant, app, scope, id, sequence, req, operator)
	if err != nil {
		return out, err
	}
	prepared.PreparedReviewCoordination, err = prepareFulfillmentReviewCoordination(ctx, store, account, tenant, app, scope, uuid.MustParse(id).String(), sequence, req, operator)
	if err != nil {
		return out, err
	}
	prepared.TargetSubscriptionID, prepared.TargetScope = uuid.MustParse(id).String(), scope
	if req.TargetBinding != nil || req.Fulfillment != nil || req.Reopening != nil || req.Reaffirmation != nil {
		source, e := store.readReadinessInbox(ctx, account, tenant, app, scope, id, sequence-1, 1, operator)
		if e != nil {
			return out, e
		}
		prepared.TargetSourceTenant = tenant
		if operator {
			prepared.TargetSourceTenant = source.Subscription.TenantID
		}
	}
	return store.updateReadinessNotification(ctx, account, tenant, app, scope, uuid.MustParse(id).String(), sequence, actor, prepared, operator)
}
func applyReadinessCoordination(ctx context.Context, n api.OperationWorkflowReadinessNotification, actor string, req readinessCoordinationUpdate) (api.OperationWorkflowReadinessNotification, error) {
	revision := int64(0)
	if n.Coordination != nil {
		revision = n.Coordination.Revision
	}
	if revision != req.Revision {
		return n, ErrConflict
	}
	if n.Coordination != nil && n.Coordination.Completion != nil && (req.Completion != nil || req.FollowUpAt != nil) {
		return n, ErrConflict
	}
	if req.Completion != nil && (req.PreparedCompletion == nil || n.Coordination == nil || n.Coordination.FollowUpAt == nil) {
		return n, ErrConflict
	}
	if n.ReviewResponse != nil && n.ReviewResponse.Status == "active" && req.ReviewResponse == nil && (req.Completion != nil || req.Owner != n.ReviewResponse.Owner || n.ReviewResponse.CheckInAt == nil || req.FollowUpAt == nil || !req.FollowUpAt.Equal(*n.ReviewResponse.CheckInAt)) {
		return n, ErrConflict
	}
	if req.PreparedReviewResponse != nil && req.ReviewResponse == nil {
		return n, ErrConflict
	}
	if req.ReviewResponse != nil && req.ReviewResponse.Decision != nil && (req.PreparedReviewResponse == nil || req.PreparedReviewResponse.Decision == nil || n.ReviewResponse == nil || n.ReviewResponse.Status != "active") {
		return n, ErrConflict
	}
	if req.ReviewResponse != nil && req.PreparedReviewResponse == nil {
		return n, ErrConflict
	}
	if req.ReviewResponse != nil && req.ReviewResponse.HandoffAcceptance != nil {
		if err := validateReviewHandoffAcceptance(n, *req.ReviewResponse); err != nil {
			return n, err
		}
	}
	if req.ReviewResponse != nil && req.ReviewResponse.HandoffDecline != nil {
		if err := validateReviewHandoffDecline(n, *req.ReviewResponse); err != nil {
			return n, err
		}
	}
	if req.ReviewResponse != nil && req.ReviewResponse.HandoffReassignment != nil {
		if err := validateReviewHandoffReassignment(n, *req.ReviewResponse); err != nil {
			return n, err
		}
	}
	if req.ReviewResponse != nil {
		if err := validateScheduledReviewReschedule(n, *req.ReviewResponse); err != nil {
			return n, err
		}
		if err := validateScheduledReviewCancellation(n, *req.ReviewResponse); err != nil {
			return n, err
		}
		if err := validateEscalationActionReview(n, *req.ReviewResponse); err != nil {
			return n, err
		}
		if err := validateReviewEscalationCompletion(n, *req.ReviewResponse); err != nil {
			return n, err
		}
		if err := validateReviewEscalationAcknowledgment(n, *req.ReviewResponse); err != nil {
			return n, err
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if req.PreparedReviewResponse != nil {
		response := *req.PreparedReviewResponse
		response.CreatedAt, response.CreatedBy = now, actor
		if n.ReviewResponse != nil {
			if req.ReviewResponse.Decision != nil && (n.ReviewResponse.Health == nil || n.ReviewResponse.Health.Fingerprint != req.ReviewResponse.Decision.HealthFingerprint) {
				return n, ErrConflict
			}
			if req.ReviewResponse.Decision == nil {
				response.Decision = n.ReviewResponse.Decision
				response.DecisionReview = pinnedDecisionReview(n.ReviewResponse)
				if response.DecisionReview != nil {
					response.DecisionReview.OwnerChanged = response.DecisionReview.OwnerChanged || n.ReviewResponse.Owner != response.Owner
					if response.DecisionReview.Decline != nil {
						response.DecisionReview.ReassignmentRequired = response.Status == "active" && response.Owner == response.DecisionReview.Decline.Owner
					}
				}
			}
			response.Health = n.ReviewResponse.Health
			response.CreatedAt = n.ReviewResponse.CreatedAt
			response.CreatedBy = n.ReviewResponse.CreatedBy
			response.History = slices.Clone(n.ReviewResponse.History)
			response.HistoryTruncated = n.ReviewResponse.HistoryTruncated
		}
		if response.Status == "active" && (n.ReviewResponse == nil || n.ReviewResponse.Status != "active" || n.ReviewResponse.CheckInAt == nil || !n.ReviewResponse.CheckInAt.Equal(*response.CheckInAt)) {
			response.CheckInInitialOwner = response.Owner
		} else if n.ReviewResponse != nil {
			response.CheckInInitialOwner = n.ReviewResponse.CheckInInitialOwner
			response.CheckInOwnerChanged = n.ReviewResponse.CheckInOwnerChanged || n.ReviewResponse.Owner != response.Owner
			response.CheckInNotifiedAt = n.ReviewResponse.CheckInNotifiedAt
		}
		if req.ReviewResponse.Decision != nil {
			decision := *response.Decision
			decision.At = now
			decision.Actor = actor
			response.Decision = &decision
		}
		response.UpdatedAt, response.UpdatedBy = now, actor
		var acceptanceForHistory *api.OperationWorkflowReviewResponseHandoffAcceptance
		if req.ReviewResponse.HandoffAcceptance != nil {
			if response.CheckInAt == nil || !response.CheckInAt.After(now) {
				return n, ErrConflict
			}
			acceptanceForHistory = &api.OperationWorkflowReviewResponseHandoffAcceptance{Owner: response.Owner, Actor: actor, At: now, Note: req.ReviewResponse.HandoffAcceptance.Note, CheckInAt: *response.CheckInAt}
			response.DecisionReview.Acceptance = acceptanceForHistory
		}
		var declineForHistory *api.OperationWorkflowReviewResponseHandoffDecline
		if req.ReviewResponse.HandoffDecline != nil {
			review := response.DecisionReview
			declineForHistory = &api.OperationWorkflowReviewResponseHandoffDecline{DecisionAt: review.DecisionAt, RequiredAt: review.RequiredAt, Owner: response.Owner, PreviousOwner: review.PreviousOwner, Actor: actor, At: now, Reason: req.ReviewResponse.HandoffDecline.Reason}
			review.Decline = declineForHistory
			review.ReassignmentRequired = true
		}
		var reassignmentForHistory *api.OperationWorkflowReviewResponseHandoffReassignment
		if req.ReviewResponse.HandoffReassignment != nil {
			review := response.DecisionReview
			reassignmentForHistory = &api.OperationWorkflowReviewResponseHandoffReassignment{DecisionAt: review.DecisionAt, RequiredAt: review.RequiredAt, Owner: response.Owner, PreviousOwner: n.ReviewResponse.Owner, Actor: actor, At: now, Reason: req.ReviewResponse.HandoffReassignment.Reason, Decline: *review.Decline}
			review.Reassignment = reassignmentForHistory
			review.PreviousOwner = n.ReviewResponse.Owner
			review.Owner = response.Owner
			review.HandedOffAt = &now
			review.AcceptanceEscalationOwner = response.HandoffAcceptanceEscalationOwner
			review.AcceptanceEscalatedAt = nil
			review.EscalationAcknowledgment = nil
			review.OwnerChanged = true
			review.Decline = nil
			review.ReassignmentRequired = false
			review.Acceptance = nil
			review.AcceptanceDeadlineNotifiedAt = nil
			due := now.Add(time.Duration(effectiveHandoffAcceptanceWindow(&response)) * time.Second)
			review.AcceptanceDueAt = &due
		}
		var acknowledgmentForHistory *api.OperationWorkflowReviewResponseEscalationAcknowledgment
		if acknowledgment := req.ReviewResponse.EscalationAcknowledgment; acknowledgment != nil {
			due := acknowledgment.DueAt.UTC().Truncate(time.Microsecond)
			if !due.After(now) {
				return n, ErrConflict
			}
			acknowledgmentForHistory = &api.OperationWorkflowReviewResponseEscalationAcknowledgment{Coordinator: acknowledgment.Coordinator, Plan: acknowledgment.Plan, DueAt: due, At: now, Actor: actor}
			response.DecisionReview.EscalationAcknowledgment = acknowledgmentForHistory
		}
		var completionForHistory *api.OperationWorkflowReviewResponseEscalationCompletion
		if completion := req.ReviewResponse.EscalationCompletion; completion != nil {
			evidence := make([]api.OperationWorkflowReviewResponseLink, 0, len(completion.ReviewIndices))
			for _, index := range completion.ReviewIndices {
				evidence = append(evidence, n.ReviewResponse.Reviews[index])
			}
			completionForHistory = &api.OperationWorkflowReviewResponseEscalationCompletion{AcknowledgedAt: completion.AcknowledgedAt, Summary: completion.Summary, Evidence: evidence, At: now, Actor: actor}
			if completion.ReviewAfterSeconds != nil {
				due := now.Add(time.Duration(*completion.ReviewAfterSeconds) * time.Second)
				completionForHistory.ReviewDueAt = &due
				completionForHistory.OriginalReviewDueAt = &due
			}
			ack := *response.DecisionReview.EscalationAcknowledgment
			ack.Completion = completionForHistory
			response.DecisionReview.EscalationAcknowledgment = &ack
			for i, change := range response.History {
				if previous := change.EscalationAcknowledgment; previous != nil && previous.At.Equal(ack.At) && previous.Actor == ack.Actor {
					response.History[i].EscalationAcknowledgment = &ack
				}
			}
		}
		var actionReviewForHistory *api.OperationWorkflowReviewResponseEscalationActionReview
		if req.ReviewResponse.EscalationActionReview != nil || req.ReviewResponse.EscalationRevision != nil {
			if req.PreparedEscalationActionReview == nil {
				return n, ErrConflict
			}
			captured := *req.PreparedEscalationActionReview
			captured.At, captured.Actor = now, actor
			actionReviewForHistory = &captured
			ack := *response.DecisionReview.EscalationAcknowledgment
			completion := *ack.Completion
			if !captured.CompletedAt.Equal(completion.At) {
				return n, ErrConflict
			}
			completion.Review = actionReviewForHistory
			ack.Completion = &completion
			for i, change := range response.History {
				if previous := change.EscalationCompletion; previous != nil && previous.At.Equal(completion.At) {
					response.History[i].EscalationCompletion = &completion
				}
				if previous := change.EscalationAcknowledgment; previous != nil && previous.At.Equal(ack.At) {
					response.History[i].EscalationAcknowledgment = &ack
				}
			}
			response.DecisionReview.EscalationAcknowledgment = &ack
			if revision := req.ReviewResponse.EscalationRevision; revision != nil {
				if captured.Health.Status == "clear" {
					return n, ErrConflict
				}
				due := revision.DueAt.UTC().Truncate(time.Microsecond)
				if !due.After(now) {
					return n, ErrConflict
				}
				completedAt := completion.At
				acknowledgmentForHistory = &api.OperationWorkflowReviewResponseEscalationAcknowledgment{PreviousCompletedAt: &completedAt, Basis: actionReviewForHistory, Coordinator: revision.Coordinator, Plan: revision.Plan, DueAt: due, At: now, Actor: actor}
				response.DecisionReview.EscalationAcknowledgment = acknowledgmentForHistory
			}
		}
		var cancellationForHistory *api.OperationWorkflowReviewResponseScheduledReviewCancellation
		if cancellation := req.ReviewResponse.ScheduledReviewCancellation; cancellation != nil {
			ack := *response.DecisionReview.EscalationAcknowledgment
			completion := *ack.Completion
			cancellationForHistory = &api.OperationWorkflowReviewResponseScheduledReviewCancellation{CompletedAt: completion.At, DueAt: *completion.ReviewDueAt, Reason: cancellation.Reason, Actor: actor, At: now}
			completion.ReviewCancellation = cancellationForHistory
			ack.Completion = &completion
			response.DecisionReview.EscalationAcknowledgment = &ack
			for i, change := range response.History {
				if previous := change.EscalationCompletion; previous != nil && previous.At.Equal(completion.At) {
					response.History[i].EscalationCompletion = &completion
				}
				if previous := change.EscalationAcknowledgment; previous != nil && previous.At.Equal(ack.At) {
					response.History[i].EscalationAcknowledgment = &ack
				}
			}
		}
		var rescheduleForHistory *api.OperationWorkflowReviewResponseScheduledReviewReschedule
		if reschedule := req.ReviewResponse.ScheduledReviewReschedule; reschedule != nil {
			ack := *response.DecisionReview.EscalationAcknowledgment
			completion := *ack.Completion
			due := reschedule.NewDueAt.UTC().Truncate(time.Microsecond)
			if !due.After(now) {
				return n, ErrConflict
			}
			rescheduleForHistory = &api.OperationWorkflowReviewResponseScheduledReviewReschedule{CompletedAt: completion.At, PreviousDueAt: *completion.ReviewDueAt, DueAt: due, Reason: reschedule.Reason, Actor: actor, At: now}
			if completion.OriginalReviewDueAt == nil {
				original := *completion.ReviewDueAt
				completion.OriginalReviewDueAt = &original
			}
			completion.ReviewDueAt = &due
			completion.ReviewReschedule = rescheduleForHistory
			ack.Completion = &completion
			response.DecisionReview.EscalationAcknowledgment = &ack
			for i, change := range response.History {
				if previous := change.EscalationCompletion; previous != nil && previous.At.Equal(completion.At) {
					response.History[i].EscalationCompletion = &completion
				}
				if previous := change.EscalationAcknowledgment; previous != nil && previous.At.Equal(ack.At) {
					response.History[i].EscalationAcknowledgment = &ack
				}
			}
		}
		changeReason := "human_update"
		if rescheduleForHistory != nil {
			changeReason = "scheduled_review_rescheduled"
		}
		if cancellationForHistory != nil {
			changeReason = "scheduled_review_canceled"
		}
		if actionReviewForHistory != nil {
			changeReason = "escalation_action_reviewed"
		}
		if completionForHistory != nil {
			changeReason = "escalation_commitment_completed"
		}
		if acknowledgmentForHistory != nil {
			changeReason = "escalation_acknowledged"
			if req.ReviewResponse.EscalationRevision != nil {
				changeReason = "escalation_commitment_revised"
			}
		}
		if reassignmentForHistory != nil {
			changeReason = "handoff_reassigned"
		}
		if declineForHistory != nil {
			changeReason = "handoff_declined"
		}
		if acceptanceForHistory != nil {
			changeReason = "handoff_accepted"
		}
		var decisionForHistory *api.OperationWorkflowReviewResponseDecision
		if req.ReviewResponse.Decision != nil {
			decisionForHistory = response.Decision
		}
		response.History = append(response.History, api.OperationWorkflowReviewResponseChange{ScheduledReviewReschedule: rescheduleForHistory, ScheduledReviewCancellation: cancellationForHistory, EscalationActionReview: actionReviewForHistory, EscalationCompletion: completionForHistory, EscalationAcknowledgment: acknowledgmentForHistory, DecisionReviewFallbackOwner: response.DecisionReviewFallbackOwner, DecisionReviewAfterSeconds: response.DecisionReviewAfterSeconds, Decision: decisionForHistory, HandoffAcceptanceEscalationOwner: response.HandoffAcceptanceEscalationOwner, HandoffAcceptAfterSeconds: response.HandoffAcceptAfterSeconds, HandoffReassignment: reassignmentForHistory, HandoffDecline: declineForHistory, HandoffAcceptance: acceptanceForHistory, Reason: changeReason, FallbackOwner: response.FallbackOwner, Owner: response.Owner, Plan: response.Plan, Status: response.Status, CheckInAt: response.CheckInAt, At: now, Actor: actor})
		if len(response.History) > 16 {
			response.History = response.History[len(response.History)-16:]
			response.HistoryTruncated = true
		}
		n.ReviewResponse = &response
		req.Owner = response.Owner
		req.Status = "acknowledged"
		req.FollowUpAt = response.CheckInAt
	}
	c := &api.OperationWorkflowReadinessCoordination{Revision: revision + 1, Status: req.Status, Owner: req.Owner, Note: req.Note, FollowUpAt: req.FollowUpAt, UpdatedAt: now, UpdatedBy: actor}
	if n.Coordination != nil {
		c.TargetBindings = slices.Clone(n.Coordination.TargetBindings)
		c.Fulfillments = slices.Clone(n.Coordination.Fulfillments)
		c.Reopenings = slices.Clone(n.Coordination.Reopenings)
		c.FulfillmentReviews = slices.Clone(n.Coordination.FulfillmentReviews)
		c.Reaffirmations = slices.Clone(n.Coordination.Reaffirmations)
		c.ReviewCoordinationChanges = slices.Clone(n.Coordination.ReviewCoordinationChanges)
		c.FulfillmentReviewReminders = slices.Clone(n.Coordination.FulfillmentReviewReminders)
		c.FulfillmentReviewEscalations = slices.Clone(n.Coordination.FulfillmentReviewEscalations)
		c.Completion = n.Coordination.Completion
	}
	if req.PreparedCompletion != nil {
		completion := *req.PreparedCompletion
		completion.CompletedAt, completion.CompletedBy = now, actor
		if req.PreparedReview != nil {
			review := *req.PreparedReview
			review.ExpiresAt = now.Add(time.Duration(completion.PlaybookReviewAfterSeconds) * time.Second)
			n.PlaybookReview = &review
		}
		c.Completion = &completion
		c.FollowUpAt = nil
		if req.PreparedOutcomeWatch != nil {
			w := *req.PreparedOutcomeWatch
			if w.DeadlineAt != nil && !w.DeadlineAt.After(now) {
				return n, ErrConflict
			}
			w.StartedAt = now
			n.BusinessOutcomeWatch = &w
		}
		if req.PreparedWatch != nil {
			w := *req.PreparedWatch
			w.StartedAt = now
			n.VerificationWatch = &w
		}
	}
	if req.Status == "acknowledged" {
		c.AcknowledgedAt = &now
		c.AcknowledgedBy = actor
		if n.Coordination != nil && n.Coordination.Status == "acknowledged" {
			c.AcknowledgedAt = n.Coordination.AcknowledgedAt
			c.AcknowledgedBy = n.Coordination.AcknowledgedBy
		}
	}
	if n.Reminder != nil && (c.FollowUpAt == nil || !n.Reminder.DueAt.Equal(*c.FollowUpAt)) {
		n.Reminder = nil
	}
	if n.PlaybookStep != nil && n.Coordination != nil && n.Coordination.Owner != c.Owner {
		step := *n.PlaybookStep
		step.ManualOwner = true
		appendPlaybookStepAssignmentHistory(&step, api.OperationWorkflowReadinessAssignmentChange{FromOwner: n.Coordination.Owner, ToOwner: c.Owner, At: now, Actor: actor, Reason: "manual_assignment"})
		n.PlaybookStep = &step
	}
	n = readinessEscalationCoordination(n, c, actor, now)
	n.Coordination = c
	n, err := applyObligationTarget(ctx, n, req, actor, now)
	if err != nil {
		return n, err
	}
	n, err = applyObligationFulfillment(ctx, n, req, actor, now)
	if err != nil {
		return n, err
	}
	n, err = applyObligationReopening(ctx, n, req, actor, now)
	if err != nil {
		return n, err
	}
	n, err = applyObligationReaffirmation(n, req, actor, now)
	if err != nil {
		return n, err
	}
	return applyFulfillmentReviewCoordination(n, req, actor, now)
}
func checkReadinessNotification(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, sequence int64, operator bool) (out api.OperationWorkflowReadinessNotificationCheck, err error) {
	defer func() {
		if err == nil {
			out.Handoff = readinessHandoff(out)
		}
	}()
	out = api.OperationWorkflowReadinessNotificationCheck{Status: "unavailable", CheckedAt: time.Now().UTC()}
	if err := validateReadinessNotificationIdentity(account, tenant, app, scope, id, sequence, operator); err != nil {
		return out, err
	}
	page, err := store.readReadinessInbox(ctx, account, tenant, app, scope, uuid.MustParse(id).String(), sequence-1, 1, operator)
	if err != nil {
		return out, err
	}
	if len(page.Items) != 1 || page.Items[0].Sequence != sequence {
		return out, ErrNotFound
	}
	out.Notification = page.Items[0]
	if c := out.Notification.Coordination; c != nil && c.Completion != nil && c.Completion.ExpectedOutcomeCode != "" {
		out.BusinessOutcome, err = readReadinessBusinessOutcome(ctx, store, account, tenant, app, scope, out.Notification, c.Completion.ExpectedOutcomeCode, operator)
		if err != nil {
			return out, err
		}
	}
	if out.Notification.NextActionDigest != nil {
		workflow := ""
		if policy := page.Subscription.NextActionDigest; policy != nil {
			workflow = policy.Workflow
		}
		out.NextActionDigest, err = checkNextActionDigest(ctx, store, account, tenant, app, scope, page.Subscription.TenantID, workflow, operator, out.Notification.NextActionDigest)
		if err != nil {
			return out, err
		}
		out.Status = "digest_checked"
		out.CheckedAt = time.Now().UTC()
		return out, nil
	}
	saved := readinessNotificationBaseline(out.Notification)
	if saved == nil {
		return out, nil
	}
	app = uuid.MustParse(app).String()
	opts := api.OperationWorkflowAttentionOptions{AppID: app, Scope: scope, TenantID: page.Subscription.TenantID}
	baseline := newReadinessSnapshot(account, tenant, opts, operator)
	baseline.CapturedAt = out.Notification.CreatedAt
	baseline.Items = append(baseline.Items, *saved)
	req := api.OperationWorkflowReadinessComparisonRequest{Scope: scope, Baseline: baseline, Limit: 1}
	if operator {
		req.TenantID = page.Subscription.TenantID
	} else {
		req.AppID = app
	}
	comparison, err := compareWorkflowReadiness(ctx, store, account, tenant, app, req, operator)
	if err != nil {
		return out, err
	}
	out.CheckedAt = comparison.EvaluatedAt
	if len(comparison.Items) > 0 {
		c := comparison.Items[0]
		out.Change = &c
		out.Status = c.Status
		if c.Current != nil && (c.Status == "unchanged" || c.Status == "changed" || c.Status == "newly_ready" || c.Status == "newly_blocked") {
			out.Status = "blocked"
			if c.Current.Ready {
				out.Status = "ready"
			}
		}
	}
	return out, nil
}

// Verification deliveries carry the source workflow even without a discovery change.
func readinessNotificationBaseline(n api.OperationWorkflowReadinessNotification) *api.OperationWorkflowReadinessSnapshotEntry {
	if n.Change != nil {
		return &n.Change.Baseline
	}
	if n.Verification != nil {
		return &n.Verification.Source
	}
	if n.BusinessOutcome != nil {
		return &n.BusinessOutcome.Source
	}
	if n.EscalationResponse != nil {
		return n.EscalationResponse.Source
	}
	return nil
}
