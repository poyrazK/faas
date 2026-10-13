package state

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func prepareReviewResponse(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, sequence int64, req api.OperationWorkflowReadinessCoordinationRequest, operator bool) (*api.OperationWorkflowReviewResponse, error) {
	input := req.ReviewResponse
	text := func(value string, max int) bool {
		return value != "" && strings.TrimSpace(value) == value && len(value) <= max && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
	}
	if req.Completion != nil || !text(input.Owner, api.OperationWorkflowReadinessFollowUpOwnerMaxBytes) || (strings.TrimSpace(input.Plan) == "" || len(input.Plan) > api.OperationWorkflowReadinessFollowUpNoteMaxBytes || !utf8.ValidString(input.Plan) || strings.IndexFunc(input.Plan, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0) || (input.Status != "active" && input.Status != "resolved") || len(input.Reviews) > 16 {
		return nil, ErrInvalidArgument
	}
	if input.DecisionReviewFallbackOwner != nil && *input.DecisionReviewFallbackOwner != "" && (!text(*input.DecisionReviewFallbackOwner, api.OperationWorkflowReadinessFollowUpOwnerMaxBytes) || *input.DecisionReviewFallbackOwner == input.Owner) {
		return nil, ErrInvalidArgument
	}
	if input.FallbackOwner != nil && *input.FallbackOwner != "" && (!text(*input.FallbackOwner, api.OperationWorkflowReadinessFollowUpOwnerMaxBytes) || *input.FallbackOwner == input.Owner) {
		return nil, ErrInvalidArgument
	}
	if input.HandoffAcceptanceEscalationOwner != nil && *input.HandoffAcceptanceEscalationOwner != "" && (!text(*input.HandoffAcceptanceEscalationOwner, api.OperationWorkflowReadinessFollowUpOwnerMaxBytes) || *input.HandoffAcceptanceEscalationOwner == input.Owner) {
		return nil, ErrInvalidArgument
	}
	if input.HandoffAcceptAfterSeconds != nil && (*input.HandoffAcceptAfterSeconds < 60 || *input.HandoffAcceptAfterSeconds > 604800) {
		return nil, ErrInvalidArgument
	}
	if input.DecisionReviewAfterSeconds != nil && (*input.DecisionReviewAfterSeconds < 60 || *input.DecisionReviewAfterSeconds > 604800) {
		return nil, ErrInvalidArgument
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if input.CheckInAt != nil {
		normalized := input.CheckInAt.UTC().Truncate(time.Microsecond)
		inputCopy := *input
		inputCopy.CheckInAt = &normalized
		input = &inputCopy
	}
	if input.Status == "active" && (input.CheckInAt == nil || (!input.CheckInAt.After(now) && input.HandoffDecline == nil && input.HandoffReassignment == nil && input.EscalationAcknowledgment == nil && input.EscalationCompletion == nil && input.EscalationActionReview == nil && input.EscalationRevision == nil && input.ScheduledReviewCancellation == nil && input.ScheduledReviewReschedule == nil) || input.CheckInAt.Year() > 9999) || input.Status == "resolved" && input.CheckInAt != nil {
		return nil, ErrInvalidArgument
	}
	page, err := store.readReadinessInbox(ctx, account, tenant, app, scope, uuid.MustParse(id).String(), sequence-1, 1, operator)
	if err != nil {
		return nil, err
	}
	if len(page.Items) != 1 || page.Items[0].Sequence != sequence {
		return nil, ErrNotFound
	}
	alert := page.Items[0]
	if alert.Kind != "business_impact_changed" || alert.BusinessImpact == nil || alert.BusinessImpact.Current == nil {
		return nil, ErrInvalidArgument
	}
	if alert.Coordination == nil || alert.Coordination.Revision != req.Revision || alert.Coordination.Completion != nil {
		return nil, ErrConflict
	}
	if err := validateScheduledReviewReschedule(alert, *input); err != nil {
		return nil, err
	}
	if err := validateScheduledReviewCancellation(alert, *input); err != nil {
		return nil, err
	}
	if err := validateEscalationActionReview(alert, *input); err != nil {
		return nil, err
	}
	if err := validateReviewEscalationCompletion(alert, *input); err != nil {
		return nil, err
	}
	if err := validateReviewEscalationAcknowledgment(alert, *input); err != nil {
		return nil, err
	}
	if input.HandoffReassignment != nil {
		if err := validateReviewHandoffReassignment(alert, *input); err != nil {
			return nil, err
		}
	}
	if input.HandoffDecline != nil {
		if err := validateReviewHandoffDecline(alert, *input); err != nil {
			return nil, err
		}
	}
	if input.HandoffAcceptance != nil {
		if err := validateReviewHandoffAcceptance(alert, *input); err != nil {
			return nil, err
		}
	}
	response := &api.OperationWorkflowReviewResponse{HandoffAcceptAfterSeconds: api.OperationWorkflowReviewResponseHandoffAcceptDefaultSeconds, DecisionReviewAfterSeconds: api.OperationWorkflowReviewResponseDecisionReviewDefaultSeconds, Owner: input.Owner, Plan: input.Plan, Status: input.Status, Reviews: []api.OperationWorkflowReviewResponseLink{}, History: []api.OperationWorkflowReviewResponseChange{}}
	if alert.ReviewResponse != nil {
		response.HandoffAcceptanceEscalationOwner = alert.ReviewResponse.HandoffAcceptanceEscalationOwner
		response.HandoffAcceptAfterSeconds = effectiveHandoffAcceptanceWindow(alert.ReviewResponse)
		response.DecisionReviewFallbackOwner = alert.ReviewResponse.DecisionReviewFallbackOwner
		response.DecisionReviewAfterSeconds = effectiveDecisionReviewWindow(alert.ReviewResponse)
	}
	if input.HandoffAcceptanceEscalationOwner != nil {
		response.HandoffAcceptanceEscalationOwner = *input.HandoffAcceptanceEscalationOwner
	}
	if input.HandoffAcceptAfterSeconds != nil {
		response.HandoffAcceptAfterSeconds = *input.HandoffAcceptAfterSeconds
	}
	if input.DecisionReviewFallbackOwner != nil {
		response.DecisionReviewFallbackOwner = *input.DecisionReviewFallbackOwner
	}
	if input.DecisionReviewAfterSeconds != nil {
		response.DecisionReviewAfterSeconds = *input.DecisionReviewAfterSeconds
	}
	if input.CheckInAt != nil {
		check := input.CheckInAt.UTC().Truncate(time.Microsecond)
		response.CheckInAt = &check
	}
	if alert.ReviewResponse != nil {
		response.FallbackOwner = alert.ReviewResponse.FallbackOwner
	}
	if input.FallbackOwner != nil {
		response.FallbackOwner = *input.FallbackOwner
	}
	if alert.ReviewResponse != nil {
		response.Reviews = slices.Clone(alert.ReviewResponse.Reviews)
	}
	if alert.ReviewResponse == nil && len(input.Reviews) == 0 {
		return nil, ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, link := range input.Reviews {
		parsed, e := uuid.Parse(link.SubscriptionID)
		if e != nil || parsed == uuid.Nil || link.Sequence < 1 || link.Sequence > 9007199254740991 {
			return nil, ErrInvalidArgument
		}
		key := parsed.String() + ":" + strconv.FormatInt(link.Sequence, 10)
		if seen[key] {
			return nil, ErrInvalidArgument
		}
		seen[key] = true
		if alert.ReviewResponse != nil {
			if !slices.ContainsFunc(response.Reviews, func(old api.OperationWorkflowReviewResponseLink) bool {
				return old.SubscriptionID == parsed.String() && old.Sequence == link.Sequence
			}) {
				return nil, ErrConflict
			}
			continue
		}
		sourceTenant := tenant
		if operator {
			sourceTenant = ""
		}
		sourcePage, e := store.readReadinessInbox(ctx, account, sourceTenant, app, scope, parsed.String(), link.Sequence-1, 1, operator)
		if e != nil {
			return nil, e
		}
		if len(sourcePage.Items) != 1 || sourcePage.Items[0].Sequence != link.Sequence {
			return nil, ErrNotFound
		}
		source := sourcePage.Items[0]
		if source.PlaybookStep == nil || source.PlaybookStep.DependencyReview == nil || source.PlaybookEvidence == nil || source.PlaybookStep.ReviewImpact == nil {
			return nil, ErrInvalidArgument
		}
		impact := source.PlaybookStep.ReviewImpact
		if source.PlaybookEvidence.Workflow != alert.BusinessImpact.Workflow || impact.Kind != alert.BusinessImpact.Kind || impact.Code != alert.BusinessImpact.Code {
			return nil, ErrInvalidArgument
		}
		sourceTenantID := source.PlaybookEvidence.PlatformTenantID
		if operator && page.Subscription.TenantID != "" && !sameOperationHistoryIdentity(sourceTenantID, page.Subscription.TenantID) {
			return nil, ErrInvalidArgument
		}
		if !operator {
			sourceTenantID = ""
		}
		step := source.PlaybookStep
		response.Reviews = append(response.Reviews, api.OperationWorkflowReviewResponseLink{SubscriptionID: parsed.String(), Sequence: link.Sequence, NotificationID: source.ID, ProgressID: step.ProgressID, OutcomeCode: step.OutcomeCode, ItemIndex: step.ItemIndex, DependencyReviewRevision: step.DependencyReview.DependencyReviewRevision, PlatformTenantID: sourceTenantID, Subject: source.PlaybookEvidence.Subject, Workflow: source.PlaybookEvidence.Workflow, InstanceID: source.PlaybookEvidence.InstanceID})
	}
	if alert.ReviewResponse != nil && input.Reviews != nil && len(input.Reviews) != len(response.Reviews) {
		return nil, ErrConflict
	}
	slices.SortFunc(response.Reviews, func(a, b api.OperationWorkflowReviewResponseLink) int {
		if a.SubscriptionID != b.SubscriptionID {
			return strings.Compare(a.SubscriptionID, b.SubscriptionID)
		}
		if a.Sequence < b.Sequence {
			return -1
		}
		if a.Sequence > b.Sequence {
			return 1
		}
		return 0
	})
	if input.Decision != nil {
		decision := input.Decision
		if (decision.Kind != "continue" && decision.Kind != "revise" && decision.Kind != "pause") || strings.TrimSpace(decision.Reason) == "" || len(decision.Reason) > 1024 || !utf8.ValidString(decision.Reason) || strings.IndexFunc(decision.Reason, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 || len(decision.HealthFingerprint) != 64 || input.Status != "active" {
			return nil, ErrInvalidArgument
		}
		if alert.ReviewResponse == nil || alert.ReviewResponse.Status != "active" || alert.ReviewResponse.Health == nil || alert.ReviewResponse.Health.Fingerprint == "" || alert.ReviewResponse.Health.Fingerprint != decision.HealthFingerprint {
			return nil, ErrConflict
		}
		// Re-read the scoped sources, then fence the stored sampled condition again at publication.
		record := readinessSubscriptionRecord{Account: account, Tenant: tenant, Operator: operator, Subscription: page.Subscription, ReviewBlockages: map[string][]string{alert.ID: slices.Clone(alert.ReviewResponse.Health.RemainingBlockers)}}
		updates, _ := evaluateReviewResponseBlockages(ctx, store, &record, []api.OperationWorkflowReadinessNotification{alert})
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(updates) != 1 {
			return nil, ErrConflict
		}
		checked, _, accepted := applyReviewResponseBlockage(&record, alert, updates[0])
		if !accepted || checked.ReviewResponse.Health.Fingerprint != decision.HealthFingerprint {
			return nil, ErrConflict
		}
		// Snapshot the condition the operator reviewed, including its original freshness timestamps.
		encoded, err := json.Marshal(alert.ReviewResponse.Health)
		if err != nil {
			return nil, err
		}
		snapshot := api.OperationWorkflowReviewResponseHealth{}
		if err = json.Unmarshal(encoded, &snapshot); err != nil {
			return nil, err
		}
		response.Decision = &api.OperationWorkflowReviewResponseDecision{Kind: decision.Kind, Reason: decision.Reason, Owner: response.Owner, Health: snapshot}
	}
	if input.Status == "resolved" {
		cache := map[string][]api.OperationWorkflowPlaybookProgress{}
		for _, link := range response.Reviews {
			r := readinessSubscriptionRecord{Account: account, Tenant: tenant, Operator: operator, Subscription: page.Subscription}
			identity := playbookEvidenceWatchIdentity(r, api.OperationWorkflowPlaybookEvidenceWatch{PlatformTenantID: link.PlatformTenantID, Subject: link.Subject, Workflow: link.Workflow, InstanceID: link.InstanceID})
			rows, exists := cache[identity.key()]
			if !exists {
				rows, err = store.readPlaybookProgress(ctx, identity)
				if err != nil {
					return nil, err
				}
				cache[identity.key()] = rows
			}
			resolved := false
			for _, progress := range rows {
				if progress.ID == link.ProgressID && progress.Revision >= link.DependencyReviewRevision {
					for _, follow := range progress.OutcomeFollowUps {
						if follow.OutcomeCode == link.OutcomeCode && link.ItemIndex >= 0 && link.ItemIndex < len(follow.Items) {
							resolved = follow.Items[link.ItemIndex].DependencyReviewRevision == 0
						}
					}
				}
			}
			if !resolved {
				return nil, ErrConflict
			}
		}
		checked := time.Now().UTC().Truncate(time.Microsecond)
		response.ResolutionCheckedAt = &checked
	}
	return response, nil
}
