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
	return store.updateReadinessNotification(ctx, account, tenant, app, scope, uuid.MustParse(id).String(), sequence, actor, prepared, operator)
}
func applyReadinessCoordination(n api.OperationWorkflowReadinessNotification, actor string, req readinessCoordinationUpdate) (api.OperationWorkflowReadinessNotification, error) {
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
		changeReason := "human_update"
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
		response.History = append(response.History, api.OperationWorkflowReviewResponseChange{DecisionReviewFallbackOwner: response.DecisionReviewFallbackOwner, DecisionReviewAfterSeconds: response.DecisionReviewAfterSeconds, Decision: decisionForHistory, HandoffAcceptAfterSeconds: response.HandoffAcceptAfterSeconds, HandoffDecline: declineForHistory, HandoffAcceptance: acceptanceForHistory, Reason: changeReason, FallbackOwner: response.FallbackOwner, Owner: response.Owner, Plan: response.Plan, Status: response.Status, CheckInAt: response.CheckInAt, At: now, Actor: actor})
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
	return n, nil
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
