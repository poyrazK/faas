package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type readinessWatchUpdate struct {
	FulfillmentReviewEscalation *fulfillmentReviewEscalationUpdate
	FulfillmentReviewReminder   *fulfillmentReviewReminderUpdate
	FulfillmentReview           *api.OperationWorkflowObligationFulfillmentReview
	Reopening                   *api.OperationWorkflowObligationReopening
	TargetAssessment            *api.OperationWorkflowObligationAssessment
	ScheduledActionReview       *scheduledActionReviewUpdate
	EscalationCommitment        *reviewEscalationCommitmentUpdate
	HandoffReassignment         *reviewHandoffReassignmentUpdate
	HandoffDecline              *reviewHandoffDeclineUpdate
	HandoffDeadline             *reviewHandoffDeadlineUpdate
	DecisionDeadline            *reviewDecisionDeadlineUpdate
	ReviewBlockage              *reviewResponseBlockageUpdate
	ReviewCheckInAt             *time.Time
	CompletionReview            *api.OperationWorkflowPlaybookCompletionReview
	Playbook                    *playbookEvidenceWatchUpdate
	ResponseWatchID             string
	OutcomeWatch                *api.OperationWorkflowReadinessBusinessOutcomeWatch
	OutcomeEvents               []api.OperationWorkflowReadinessBusinessOutcomeEvent
	SourceID                    string
	Sequence                    int64
	WatchID                     string
	Revision                    int64
	Watch                       api.OperationWorkflowReadinessVerificationWatch
	Events                      []api.OperationWorkflowReadinessVerificationEvent
	Kinds                       []string
}

// Rotate through retained source notifications independently of readiness discovery filters.
func evaluateReadinessVerificationWatches(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord) ([]readinessWatchUpdate, error) {
	page, err := store.readReadinessInbox(ctx, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, r.Subscription.ID, 0, api.OperationWorkflowReadinessInboxMax, r.Operator)
	if err != nil {
		return nil, err
	}
	candidates := []api.OperationWorkflowReadinessNotification{}
	for _, n := range page.Items {
		if n.VerificationWatch != nil && readinessNotificationBaseline(n) != nil {
			candidates = append(candidates, n)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].Sequence > r.VerificationCursor, candidates[j].Sequence > r.VerificationCursor
		if a != b {
			return a
		}
		return candidates[i].Sequence < candidates[j].Sequence
	})
	if len(candidates) > api.OperationWorkflowReadinessVerificationWatchesPerPassMax {
		candidates = candidates[:api.OperationWorkflowReadinessVerificationWatchesPerPassMax]
	}
	updates := []readinessWatchUpdate{}
	cursor := r.VerificationCursor
	for _, n := range candidates {
		before := *readinessNotificationBaseline(n)
		opts := api.OperationMilestoneListOptions{ReadinessOnly: true, AppID: r.Subscription.AppID, Scope: r.Subscription.Scope, SubjectType: before.Subject.Type, SubjectID: before.Subject.ID, Workflow: before.Workflow, WorkflowInstanceID: before.InstanceID, Limit: 1}
		var projection api.OperationMilestonesResponse
		if r.Operator {
			opts.TenantID = before.PlatformTenantID
			projection, err = store.ListAccountOperationMilestones(ctx, r.Account, opts)
		} else {
			projection, err = store.ListPlatformTenantOperationMilestones(ctx, r.Account, r.Tenant, opts)
		}
		if err != nil {
			return nil, err
		}
		updates = append(updates, readinessVerificationObservation(n, projection.WorkflowInstance, time.Now().UTC().Truncate(time.Microsecond)))
		cursor = n.Sequence
	}
	r.VerificationCursor = cursor
	return updates, nil
}

func readinessVerificationObservation(n api.OperationWorkflowReadinessNotification, instance *api.OperationWorkflowInstanceSnapshot, at time.Time) readinessWatchUpdate {
	w := *n.VerificationWatch
	w.Items = append([]api.OperationWorkflowReadinessVerificationWatchItem{}, w.Items...)
	out := readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, WatchID: w.ID, Revision: w.Revision}
	changes := map[string][]api.OperationWorkflowReadinessVerificationChange{}
	for i, prior := range w.Items {
		current := readinessVerificationItem(prior, w, instance, at)
		w.Items[i] = current
		if current.Status == prior.Status || current.Status == "awaiting_verification" {
			continue
		}
		changes[current.Status] = append(changes[current.Status], api.OperationWorkflowReadinessVerificationChange{BeforeStatus: prior.Status, After: current})
	}
	w.EvaluatedAt = at
	w.Revision++
	out.Watch = w
	for _, status := range []string{"verified", "invalidated", "unavailable"} {
		if len(changes[status]) == 0 {
			continue
		}
		out.Kinds = append(out.Kinds, "verification_"+status)
		out.Events = append(out.Events, api.OperationWorkflowReadinessVerificationEvent{WatchID: w.ID, SourceNotificationID: n.ID, SourceSequence: n.Sequence, CheckedAt: at, Source: *readinessNotificationBaseline(n), Changes: changes[status]})
	}
	return out
}

func readinessVerificationItem(prior api.OperationWorkflowReadinessVerificationWatchItem, watch api.OperationWorkflowReadinessVerificationWatch, instance *api.OperationWorkflowInstanceSnapshot, at time.Time) api.OperationWorkflowReadinessVerificationWatchItem {
	// An explicit reopened blocker permanently invalidates this saved resolution, not its replacement.
	if prior.Status == "invalidated" {
		return prior
	}
	current := prior
	current.ReportingRevision = 0
	current.Status, current.Explanation, current.ReportingOperationID = "unavailable", "The saved resolution or current application state is no longer retained; missing proof is not verification.", ""
	if instance == nil || instance.State == nil {
		return current
	}
	state := instance.State
	current.ReportingOperationID = state.OperationID
	current.ReportingRevision = state.Revision
	if effectiveWorkflowContractVersion(state.ContractVersion) != watch.ContractVersion || state.Revision < watch.StateRevision {
		current.Explanation = "The application contract changed or retained state precedes the watch; verification cannot be compared."
		return current
	}
	if operationWorkflowStateIsStale(at, *state) {
		current.Explanation = "The current application state report is stale; no verification outcome is inferred."
		return current
	}
	current.BlockerActive = readinessVerificationBlockerActive(state.Blockers, prior.Proof)
	if state.Revision > watch.StateRevision && current.BlockerActive && !prior.BlockerActive {
		current.Status, current.Explanation = "invalidated", "The application explicitly reported the watched blocker active after an observed absence; this saved resolution is invalidated."
		return current
	}

	for _, proof := range instance.ReadinessVerifications {
		if readinessDigest(resolutionVerificationKey(proof.Resolution, proof.ResolutionOperationID)) != prior.Key {
			continue
		}
		current.Status, current.Explanation = "awaiting_verification", "Application-reported resolution is still awaiting its named retained proof."
		if proof.Status == "verified" {
			current.Proof = proof
			current.Status, current.Explanation = "verified", "The exact named verification milestone is retained for the saved application-reported resolution. This does not authorize workflow execution."
		} else if prior.Proof.Status == "verified" {
			current.Status, current.Explanation = "unavailable", "Previously observed verification proof is no longer available; resolution is not inferred."
		} else {
			current.Proof = proof
		}
		return current
	}
	return current
}

// Called only while the source row and subscription are locked. Coordination is preserved.
func applyReadinessWatchUpdate(ctx context.Context, r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	if u.FulfillmentReviewEscalation != nil {
		return applyFulfillmentReviewEscalation(r, n, u)
	}
	if u.FulfillmentReviewReminder != nil {
		return applyFulfillmentReviewReminder(r, n, u)
	}
	if u.FulfillmentReview != nil {
		return applyFulfillmentReview(r, n, u)
	}
	if u.Reopening != nil {
		return applyObligationReopeningNotice(r, n, u)
	}
	if u.TargetAssessment != nil {
		return applyObligationTargetObservation(ctx, r, n, u)
	}
	if u.ScheduledActionReview != nil {
		return applyScheduledActionReview(r, n, u)
	}
	if u.EscalationCommitment != nil {
		return applyReviewEscalationCommitmentDeadline(r, n, u)
	}
	if u.HandoffReassignment != nil {
		return applyReviewHandoffReassignmentNotice(r, n, u)
	}
	if u.HandoffDecline != nil {
		return applyReviewHandoffDeclineNotice(r, n, u)
	}
	if u.HandoffDeadline != nil {
		return applyReviewHandoffDeadline(r, n, u)
	}
	if u.DecisionDeadline != nil {
		return applyReviewDecisionDeadline(r, n, u)
	}
	if u.ReviewBlockage != nil {
		return applyReviewResponseBlockage(r, n, u)
	}
	if u.ReviewCheckInAt != nil {
		return applyReviewResponseCheckIn(r, n, u)
	}
	if u.CompletionReview != nil {
		return applyPlaybookCompletionReview(r, n, u)
	}
	if u.ResponseWatchID != "" {
		return applyReadinessEscalationResponse(r, n, u)
	}
	if u.OutcomeWatch != nil {
		return applyReadinessOutcomeWatchUpdate(r, n, u)
	}
	if n.ID != u.SourceID || n.VerificationWatch == nil || n.VerificationWatch.ID != u.WatchID || n.VerificationWatch.Revision != u.Revision {
		return n, nil, false
	}
	n.VerificationWatch = &u.Watch
	notifications := []api.OperationWorkflowReadinessNotification{}
	for i, event := range u.Events {
		notification := readinessSubscriptionNotify(r, u.Kinds[i], "Watched completion verification changed. Consult the reported evidence; application decisions and execution remain application-owned.", nil)
		notification.Verification = &event
		notifications = append(notifications, notification)
	}
	return n, notifications, true
}

func readinessVerificationBlockerActive(blockers []api.OperationWorkflowBlocker, proof api.OperationWorkflowResolutionVerification) bool {
	for _, blocker := range blockers {
		if blocker.Operation == proof.Resolution.Operation && blocker.Code == proof.Resolution.Code {
			return true
		}
	}
	return false
}
