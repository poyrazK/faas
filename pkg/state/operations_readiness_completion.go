package state

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type readinessCoordinationUpdate struct {
	PreparedReviewCoordination     *api.OperationWorkflowFulfillmentReviewCoordinationChange
	PreparedReaffirmation          *api.OperationWorkflowObligationReaffirmation
	PreparedReopening              *api.OperationWorkflowObligationReopening
	PreparedFulfillment            *api.OperationWorkflowObligationFulfillment
	PreparedTargetBinding          *api.OperationWorkflowObligationTargetBinding
	TargetSubscriptionID           string
	TargetScope                    string
	TargetSourceTenant             string
	PreparedEscalationActionReview *api.OperationWorkflowReviewResponseEscalationActionReview
	PreparedReviewResponse         *api.OperationWorkflowReviewResponse
	api.OperationWorkflowReadinessCoordinationRequest
	PreparedReview       *api.OperationWorkflowPlaybookCompletionReview
	PreparedCompletion   *api.OperationWorkflowReadinessCompletion
	PreparedWatch        *api.OperationWorkflowReadinessVerificationWatch
	PreparedOutcomeWatch *api.OperationWorkflowReadinessBusinessOutcomeWatch
}

func prepareReadinessCompletion(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, sequence int64, req api.OperationWorkflowReadinessCoordinationRequest, operator bool) (readinessCoordinationUpdate, error) {
	out := readinessCoordinationUpdate{OperationWorkflowReadinessCoordinationRequest: req}
	if req.Completion == nil {
		return out, nil
	}
	r := req.Completion
	businessCompletion := r.PlaybookResolution != ""
	if r.PlaybookReviewAfterSeconds < 0 || r.PlaybookReviewAfterSeconds > api.OperationWorkflowReadinessOutcomeEscalationDelayMaxSeconds || (r.PlaybookReviewAfterSeconds > 0 && !businessCompletion) {
		return out, ErrInvalidArgument
	}
	if businessCompletion {
		expectedOutcome := map[string]string{"expected_outcome_achieved": "resolved", "alternative_accepted": "resolved", "follow_up_canceled": "no_longer_applicable"}[r.PlaybookResolution]
		if expectedOutcome == "" || r.Outcome != expectedOutcome || r.WatchOutcome || r.WatchVerification || r.ExpectedOutcomeCode != "" || r.OutcomeDeadlineAt != nil || r.EscalationPolicy != nil {
			return out, ErrInvalidArgument
		}
	}
	if r.EscalationPolicy != nil {
		policy := *r.EscalationPolicy
		if policy.AcknowledgeAfterSeconds < 0 || policy.AcknowledgeAfterSeconds > api.OperationWorkflowReadinessOutcomeEscalationDelayMaxSeconds || (policy.FallbackOwner != "" && (policy.AcknowledgeAfterSeconds == 0 || policy.FallbackOwner == policy.Owner || strings.TrimSpace(policy.FallbackOwner) != policy.FallbackOwner || len(policy.FallbackOwner) > api.OperationWorkflowReadinessFollowUpOwnerMaxBytes || !utf8.ValidString(policy.FallbackOwner) || strings.IndexFunc(policy.FallbackOwner, unicode.IsControl) >= 0)) {
			return out, ErrInvalidArgument
		}
		if !r.WatchOutcome || r.OutcomeDeadlineAt == nil || policy.Owner == "" || strings.TrimSpace(policy.Owner) != policy.Owner || len(policy.Owner) > api.OperationWorkflowReadinessFollowUpOwnerMaxBytes || !utf8.ValidString(policy.Owner) || strings.IndexFunc(policy.Owner, unicode.IsControl) >= 0 || len(policy.Note) > api.OperationWorkflowReadinessFollowUpNoteMaxBytes || !utf8.ValidString(policy.Note) || policy.FollowUpAfterSeconds < 1 || policy.FollowUpAfterSeconds > api.OperationWorkflowReadinessOutcomeEscalationDelayMaxSeconds {
			return out, ErrInvalidArgument
		}
		r.EscalationPolicy = &policy
	}
	if (r.WatchOutcome && r.ExpectedOutcomeCode == "") || (r.OutcomeDeadlineAt != nil && !r.WatchOutcome) {
		return out, ErrInvalidArgument
	}
	if r.OutcomeDeadlineAt != nil {
		deadline := r.OutcomeDeadlineAt.UTC().Truncate(time.Microsecond)
		if deadline.Year() < 1 || deadline.Year() > 9999 || !deadline.After(time.Now().UTC()) {
			return out, ErrInvalidArgument
		}
		r.OutcomeDeadlineAt = &deadline
	}
	if r.ExpectedOutcomeCode != "" && api.ValidateOperationWorkflowStateName(r.ExpectedOutcomeCode) != nil {
		return out, ErrInvalidArgument
	}
	if req.FollowUpAt != nil || (r.Outcome != "resolved" && r.Outcome != "no_longer_applicable" && r.Outcome != "handed_off") || (!businessCompletion && strings.TrimSpace(r.Note) == "") || len(r.Note) > api.OperationWorkflowReadinessFollowUpNoteMaxBytes || !utf8.ValidString(r.Note) || r.EvidenceOperationIDs == nil || len(r.EvidenceOperationIDs) > api.OperationWorkflowReadinessCompletionEvidenceMax || (!businessCompletion && r.Outcome == "resolved" && len(r.EvidenceOperationIDs) == 0) {
		return out, ErrInvalidArgument
	}
	ids := make([]string, 0, len(r.EvidenceOperationIDs))
	seen := map[string]bool{}
	for _, value := range r.EvidenceOperationIDs {
		parsed, err := uuid.Parse(value)
		if err != nil || seen[parsed.String()] {
			return out, ErrInvalidArgument
		}
		seen[parsed.String()] = true
		ids = append(ids, parsed.String())
	}
	check, err := checkReadinessNotification(ctx, store, account, tenant, app, scope, id, sequence, operator)
	if err != nil {
		return out, err
	}
	c := check.Notification.Coordination
	if c == nil || c.Revision != req.Revision || c.FollowUpAt == nil || c.Completion != nil {
		return out, ErrConflict
	}
	saved := readinessNotificationBaseline(check.Notification)
	evidenceTenant := canonicalReadinessTenant(tenant)
	if operator && saved != nil {
		evidenceTenant = saved.PlatformTenantID
	} else if operator && check.Notification.PlaybookEvidence != nil {
		evidenceTenant = check.Notification.PlaybookEvidence.PlatformTenantID
	}
	for _, evidenceID := range ids {
		op, err := store.OperationByID(ctx, uuid.MustParse(account).String(), evidenceTenant, evidenceID)
		if err != nil {
			return out, err
		}
		if event := check.Notification.PlaybookEvidence; event != nil && (!sameOperationHistoryIdentity(op.PlatformTenantID, evidenceTenant) || op.Subject == nil || *op.Subject != event.Subject) {
			return out, ErrNotFound
		}
		if !sameOperationHistoryIdentity(op.AppID, app) || op.Scope != scope || (saved != nil && (!sameOperationHistoryIdentity(op.PlatformTenantID, evidenceTenant) || op.Subject == nil || *op.Subject != saved.Subject)) {
			return out, ErrNotFound
		}
	}
	e := api.OperationWorkflowReadinessCompletionEvidence{CheckedAt: check.CheckedAt, Status: check.Status, Explanation: check.Handoff.Summary, RemainingReasons: []string{}, Warnings: append([]string{}, check.Handoff.Limitations...)}
	if check.Change != nil {
		e.ReportingOperationID = check.Change.CurrentOperationID
		if check.Handoff.Comparable && check.Change.Current != nil {
			current := check.Change.Current
			e.StateRevision = current.StateRevision
			e.EvidenceComplete = check.Change.EvidenceComparisonComplete
			e.RemainingReasons = append(e.RemainingReasons, current.Reasons...)
			e.PendingVerificationCount = current.PendingVerificationCount
			e.Warnings = append(e.Warnings, current.Advisories...)
		}
	}
	if check.Status != "ready" {
		e.Warnings = append(e.Warnings, "Follow-up completion does not establish workflow readiness: "+check.Status+".")
	}
	out.PreparedCompletion = &api.OperationWorkflowReadinessCompletion{EscalationPolicy: r.EscalationPolicy, WatchOutcome: r.WatchOutcome, OutcomeDeadlineAt: r.OutcomeDeadlineAt, ExpectedOutcomeCode: r.ExpectedOutcomeCode, WatchVerification: r.WatchVerification, Outcome: r.Outcome, Note: r.Note, EvidenceOperationIDs: ids, Evidence: e}
	if businessCompletion {
		if check.Notification.PlaybookEscalation == nil || check.Notification.PlaybookEvidence == nil {
			return out, ErrInvalidArgument
		}
		out.PreparedCompletion.PlaybookOutcome, err = readPlaybookCompletionOutcome(ctx, store, account, tenant, app, scope, check.Notification, r.PlaybookResolution, operator)
		if err != nil {
			return out, err
		}
		out.PreparedCompletion.PlaybookReviewAfterSeconds = r.PlaybookReviewAfterSeconds
		if r.PlaybookReviewAfterSeconds > 0 {
			current := out.PreparedCompletion.PlaybookOutcome.Observation
			revision := check.Notification.PlaybookEscalation.Current.Evidence.StateRevision
			if current.Evidence.ContractVersion == check.Notification.PlaybookEvidence.ContractVersion && current.Evidence.StateRevision > revision {
				revision = current.Evidence.StateRevision
			}
			out.PreparedReview = &api.OperationWorkflowPlaybookCompletionReview{ID: uuid.NewString(), Revision: 1, Owner: req.Owner, Status: "active", StateRevision: revision, Current: current}
		}
	}
	if r.ExpectedOutcomeCode != "" {
		out.PreparedCompletion.BusinessOutcome, err = readReadinessBusinessOutcome(ctx, store, account, tenant, app, scope, check.Notification, r.ExpectedOutcomeCode, operator)
		if err != nil {
			return out, err
		}
	}
	if r.WatchOutcome {
		if saved == nil {
			return out, ErrConflict
		}
		observed := out.PreparedCompletion.BusinessOutcome
		if observed == nil || observed.Status == "achieved" || observed.Status == "different_outcome" {
			return out, ErrConflict
		}
		out.PreparedOutcomeWatch = &api.OperationWorkflowReadinessBusinessOutcomeWatch{EscalationPolicy: r.EscalationPolicy, ID: uuid.NewString(), Revision: 1, StateRevision: saved.StateRevision, DeadlineAt: r.OutcomeDeadlineAt, Current: *observed}
		if observed.ContractVersion == effectiveWorkflowContractVersion(saved.ContractVersion) && observed.StateRevision > saved.StateRevision {
			out.PreparedOutcomeWatch.StateRevision = observed.StateRevision
		}
	}
	if r.WatchVerification {
		if !check.Handoff.Comparable || check.Change == nil || check.Change.Current == nil || len(check.Change.Current.PendingVerifications) == 0 {
			return out, ErrConflict
		}
		current := check.Change.Current
		w := &api.OperationWorkflowReadinessVerificationWatch{ID: uuid.NewString(), Revision: 1, EvaluatedAt: check.CheckedAt, StateRevision: current.StateRevision, ContractVersion: current.ContractVersion, PendingCountAtStart: current.PendingVerificationCount, CoverageIncomplete: current.PendingVerificationsTruncated, Items: []api.OperationWorkflowReadinessVerificationWatchItem{}}
		for _, proof := range current.PendingVerifications {
			if len(w.Items) >= api.OperationWorkflowReadinessVerificationWatchItemsMax {
				w.CoverageIncomplete = true
				break
			}
			w.Items = append(w.Items, api.OperationWorkflowReadinessVerificationWatchItem{Key: readinessDigest(resolutionVerificationKey(proof.Resolution, proof.ResolutionOperationID)), BlockerActive: readinessVerificationBlockerActive(current.Blockers, proof), Status: "awaiting_verification", Explanation: "Application-reported resolution is awaiting its named proof.", Proof: proof})
		}
		out.PreparedWatch = w
	}
	return out, nil
}
