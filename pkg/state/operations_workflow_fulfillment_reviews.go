package state

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func fulfillmentReviewKey(binding api.OperationWorkflowObligationTargetBinding, at time.Time) string {
	return readinessDigest([]any{binding.ObligationID, binding.Case, at})
}
func activeFulfillments(coord *api.OperationWorkflowReadinessCoordination) []api.OperationWorkflowObligationFulfillment {
	out := []api.OperationWorkflowObligationFulfillment{}
	if coord == nil {
		return out
	}
	seen := map[string]bool{}
	for i := len(coord.Fulfillments) - 1; i >= 0; i-- {
		f := coord.Fulfillments[i]
		key := readinessDigest([]any{f.Binding.ObligationID, f.Binding.Case})
		if seen[key] {
			continue
		}
		seen[key] = true
		if !fulfillmentReopened(*coord, f) {
			out = append(out, f)
		}
	}
	return out
}
func activeFulfillment(coord *api.OperationWorkflowReadinessCoordination, binding api.OperationWorkflowObligationTargetBinding, at time.Time) *api.OperationWorkflowObligationFulfillment {
	for _, f := range activeFulfillments(coord) {
		if f.CompletedAt.Equal(at) && readinessDigest(f.Binding) == readinessDigest(binding) {
			value := f
			return &value
		}
	}
	return nil
}
func fulfillmentReviewFor(coord *api.OperationWorkflowReadinessCoordination, binding api.OperationWorkflowObligationTargetBinding, at time.Time) *api.OperationWorkflowObligationFulfillmentReview {
	if coord == nil {
		return nil
	}
	key := fulfillmentReviewKey(binding, at)
	for _, review := range coord.FulfillmentReviews {
		if fulfillmentReviewKey(review.Binding, review.FulfilledAt) == key {
			value := review
			return &value
		}
	}
	return nil
}
func fulfillmentBasis(coord *api.OperationWorkflowReadinessCoordination, f api.OperationWorkflowObligationFulfillment) (api.OperationWorkflowStateHistoryEntry, time.Time) {
	evidence, at := f.Evidence, f.CompletedAt
	if coord != nil {
		for _, r := range coord.Reaffirmations {
			if r.FulfilledAt.Equal(f.CompletedAt) && readinessDigest(r.Binding) == readinessDigest(f.Binding) && r.ReaffirmedAt.After(at) {
				evidence, at = r.Evidence, r.ReaffirmedAt
			}
		}
	}
	return evidence, at
}

// Observe only retained facts and explicitly named resolution evidence. Ordinary
// workflow progress does not invalidate a completed human obligation.
func observeFulfillmentReview(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, n api.OperationWorkflowReadinessNotification, f api.OperationWorkflowObligationFulfillment, operator bool) (api.OperationWorkflowObligationFulfillmentReview, error) {
	proof, basisAt := fulfillmentBasis(n.Coordination, f)
	out := api.OperationWorkflowObligationFulfillmentReview{ActionKind: f.ActionKind, Binding: f.Binding, FulfilledAt: f.CompletedAt, BasisAt: basisAt, SourceSequence: n.Sequence, Owner: f.Owner, Reasons: []string{}, CurrentReasons: []string{}, CheckedAt: time.Now().UTC().Truncate(time.Microsecond)}
	c := f.Binding.Case
	evidenceTenant := tenant
	if operator {
		evidenceTenant = c.PlatformTenantID
	}
	if n.Coordination != nil {
		for _, r := range n.Coordination.Reaffirmations {
			if r.ReaffirmedAt.Equal(basisAt) && r.FulfilledAt.Equal(f.CompletedAt) && readinessDigest(r.Binding) == readinessDigest(f.Binding) {
				out.Owner = r.Owner
			}
		}
	}
	raw := n
	if raw.Coordination != nil {
		copy := *raw.Coordination
		copy.Fulfillments = nil
		raw.Coordination = &copy
	}
	for _, item := range sourceObligationActions(ctx, id, scope, raw, tenant) {
		if item.Action.Kind != f.ActionKind || item.Action.Obligation == nil || item.Action.Obligation.ID != f.Binding.ObligationID {
			continue
		}
		for _, ref := range item.Cases {
			if ref.Subject == c.Subject && ref.Workflow == c.Workflow && ref.InstanceID == c.InstanceID && (c.PlatformTenantID == "" || sameOperationHistoryIdentity(ref.PlatformTenantID, c.PlatformTenantID)) {
				out.Owner = item.Action.Owner
			}
		}
	}
	add := func(reason string) {
		if !slices.Contains(out.CurrentReasons, reason) {
			out.CurrentReasons = append(out.CurrentReasons, reason)
		}
	}
	op, err := store.OperationByID(ctx, account, evidenceTenant, proof.OperationID)
	if errors.Is(err, ErrNotFound) {
		add("supporting_report_unavailable")
	} else if err != nil {
		return out, err
	} else if !sameOperationHistoryIdentity(op.AppID, app) || op.Scope != scope || !sameOperationHistoryIdentity(op.PlatformTenantID, evidenceTenant) || op.Subject == nil || *op.Subject != c.Subject {
		add("supporting_report_unavailable")
	}
	opts := api.OperationMilestoneListOptions{AppID: app, Scope: scope, SubjectType: c.Subject.Type, SubjectID: c.Subject.ID, Workflow: c.Workflow, WorkflowInstanceID: c.InstanceID, Limit: api.OperationHistoryPageMax}
	if operator {
		opts.TenantID = evidenceTenant
	}
	retained := false
	var instance *api.OperationWorkflowInstanceSnapshot
	for scanned := 0; scanned < 1000; {
		var page api.OperationMilestonesResponse
		if operator {
			page, err = store.ListAccountOperationMilestones(ctx, account, opts)
		} else {
			page, err = store.ListPlatformTenantOperationMilestones(ctx, account, evidenceTenant, opts)
		}
		if errors.Is(err, ErrNotFound) {
			add("current_evidence_unavailable")
			break
		}
		if err != nil {
			return out, err
		}
		if scanned == 0 {
			instance = page.WorkflowInstance
		}
		for _, report := range page.WorkflowStateHistory {
			scanned++
			if report.ID == proof.ID && report.OperationID == proof.OperationID && report.Workflow == c.Workflow && report.InstanceID == c.InstanceID && report.FromState == proof.FromState && report.State == proof.State && report.ContractVersion == proof.ContractVersion {
				retained = true
				break
			}
		}
		if retained || page.NextWorkflowStateCursor == "" || len(page.WorkflowStateHistory) == 0 {
			break
		}
		opts.WorkflowStateCursor = page.NextWorkflowStateCursor
	}
	if !retained {
		add("supporting_report_unavailable")
	}
	if instance == nil || instance.State == nil {
		add("current_evidence_unavailable")
	} else {
		state := instance.State
		out.CurrentContractVersion = effectiveWorkflowContractVersion(state.ContractVersion)
		out.CurrentStateRevision = state.Revision
		out.CurrentOperationID, out.CurrentReportID = state.OperationID, state.ReportID
		if state.Stale || operationWorkflowStateIsStale(out.CheckedAt, *state) || state.Revision < proof.Revision {
			add("current_evidence_unavailable")
		}
		if out.CurrentContractVersion != effectiveWorkflowContractVersion(proof.ContractVersion) {
			add("contract_changed")
		} else if !slices.Contains(out.CurrentReasons, "current_evidence_unavailable") {
			if state.Revision > proof.Revision {
				for _, resolution := range proof.BlockerResolutions {
					if !readinessVerificationBlockerActive(proof.Blockers, api.OperationWorkflowResolutionVerification{Resolution: resolution}) && readinessVerificationBlockerActive(state.Blockers, api.OperationWorkflowResolutionVerification{Resolution: resolution}) {
						add("resolution_invalidated")
					}
				}
			}
			for _, verification := range proof.ResolutionVerifications {
				prior := api.OperationWorkflowReadinessVerificationWatchItem{BlockerActive: readinessVerificationBlockerActive(proof.Blockers, verification), Key: readinessDigest(resolutionVerificationKey(verification.Resolution, verification.ResolutionOperationID)), Status: verification.Status, Proof: verification}
				current := readinessVerificationItem(prior, api.OperationWorkflowReadinessVerificationWatch{ContractVersion: effectiveWorkflowContractVersion(proof.ContractVersion), StateRevision: proof.Revision}, instance, out.CheckedAt)
				if current.Status == "invalidated" {
					add("resolution_invalidated")
				} else if current.Status == "unavailable" && verification.Status == "verified" {
					add("verification_unavailable")
				}
			}
		}
	}
	slices.Sort(out.CurrentReasons)
	return out, nil
}

func mergeFulfillmentReview(coord *api.OperationWorkflowReadinessCoordination, current api.OperationWorkflowObligationFulfillmentReview) api.OperationWorkflowObligationFulfillmentReview {
	current.Reasons = slices.Clone(current.CurrentReasons)
	previous := fulfillmentReviewFor(coord, current.Binding, current.FulfilledAt)
	if previous != nil && previous.BasisAt.Equal(current.BasisAt) && previous.ReviewRequired {
		current.RequiredAt = previous.RequiredAt
		current.DueAt = previous.DueAt
		current.ManualOwner = previous.ManualOwner
		current.EscalationOwner = previous.EscalationOwner
		current.EscalateAfterSeconds = previous.EscalateAfterSeconds
		if previous.ManualOwner {
			current.Owner = previous.Owner
		}
		for _, reason := range previous.Reasons {
			if !slices.Contains(current.Reasons, reason) {
				current.Reasons = append(current.Reasons, reason)
			}
		}
	}
	current.ReviewRequired = len(current.Reasons) > 0
	if current.ReviewRequired && current.RequiredAt == nil {
		at := current.CheckedAt
		current.RequiredAt = &at
	}
	slices.Sort(current.Reasons)
	current.Fingerprint = fulfillmentReviewFingerprint(current)
	return current
}

// Rotate individual fulfilled cases so a large shared plan cannot starve later sources.
func evaluateFulfillmentReviews(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord) ([]readinessWatchUpdate, error) {
	page, err := store.readReadinessInbox(ctx, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, r.Subscription.ID, 0, api.OperationWorkflowReadinessInboxMax, r.Operator)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		key string
		n   api.OperationWorkflowReadinessNotification
		f   api.OperationWorkflowObligationFulfillment
	}
	candidates := []candidate{}
	for _, n := range page.Items {
		for _, f := range activeFulfillments(n.Coordination) {
			candidates = append(candidates, candidate{n.ID + "/" + fulfillmentReviewKey(f.Binding, f.CompletedAt), n, f})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].key > r.FulfillmentReviewCursor, candidates[j].key > r.FulfillmentReviewCursor
		if a != b {
			return a
		}
		return candidates[i].key < candidates[j].key
	})
	if len(candidates) > 5 {
		candidates = candidates[:5]
	}
	updates := []readinessWatchUpdate{}
	var firstErr error
	for _, candidate := range candidates {
		r.FulfillmentReviewCursor = candidate.key
		review, err := observeFulfillmentReview(ctx, store, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, r.Subscription.ID, candidate.n, candidate.f, r.Operator)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		updates = append(updates, readinessWatchUpdate{SourceID: candidate.n.ID, Sequence: candidate.n.Sequence, Revision: candidate.n.Coordination.Revision, FulfillmentReview: &review})
	}
	return updates, firstErr
}

func applyFulfillmentReview(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	observed := u.FulfillmentReview
	if observed == nil || n.ID != u.SourceID || n.Coordination == nil || n.Coordination.Revision != u.Revision {
		return n, nil, false
	}
	f := activeFulfillment(n.Coordination, observed.Binding, observed.FulfilledAt)
	if f == nil {
		return n, nil, false
	}
	_, basis := fulfillmentBasis(n.Coordination, *f)
	if !basis.Equal(observed.BasisAt) {
		return n, nil, false
	}
	current := mergeFulfillmentReview(n.Coordination, *observed)
	previous := fulfillmentReviewFor(n.Coordination, current.Binding, current.FulfilledAt)
	coord := *n.Coordination
	reviews := []api.OperationWorkflowObligationFulfillmentReview{}
	for _, review := range coord.FulfillmentReviews {
		if activeFulfillment(&coord, review.Binding, review.FulfilledAt) != nil && fulfillmentReviewKey(review.Binding, review.FulfilledAt) != fulfillmentReviewKey(current.Binding, current.FulfilledAt) {
			reviews = append(reviews, review)
		}
	}
	if len(reviews) >= 128 {
		return n, nil, false
	}
	coord.FulfillmentReviews = append(reviews, current)
	changed := previous == nil || previous.Fingerprint != current.Fingerprint
	if changed {
		coord.Revision++
		coord.UpdatedAt = current.CheckedAt
		coord.UpdatedBy = "system:fulfillment-review"
	}
	n.Coordination = &coord
	if !current.ReviewRequired || !changed {
		return n, nil, true
	}
	notice := readinessSubscriptionNotify(r, "fulfillment_review_required", "Fulfillment evidence needs human review: "+strings.Join(current.Reasons, ", ")+". The obligation remains fulfilled until an explicit reaffirmation or reopening.", nil)
	notice.FulfillmentReview = &current
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Status: "unread", Owner: current.Owner, Revision: 1, UpdatedAt: notice.CreatedAt, UpdatedBy: "system:fulfillment-review"}
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}

func prepareObligationReaffirmation(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, sequence int64, req api.OperationWorkflowReadinessCoordinationRequest, operator bool) (*api.OperationWorkflowObligationReaffirmation, error) {
	input := req.Reaffirmation
	if input == nil {
		return nil, nil
	}
	if req.ReviewCoordination != nil || req.Reopening != nil || req.Fulfillment != nil || req.TargetBinding != nil || req.Completion != nil || req.ReviewResponse != nil || input.Reason == "" || len(input.Reason) > 2048 || strings.TrimSpace(input.Reason) != input.Reason || !utf8.ValidString(input.Reason) || strings.IndexFunc(input.Reason, unicode.IsControl) >= 0 || input.ReviewFingerprint == "" || len(input.ReviewFingerprint) > 128 || input.EvidenceReportID == "" || len(input.EvidenceReportID) > 128 {
		return nil, ErrInvalidArgument
	}
	if !operator && input.Binding.Case.PlatformTenantID != "" || operator && input.Binding.Case.PlatformTenantID == "" {
		return nil, ErrInvalidArgument
	}
	opID, err := uuid.Parse(input.EvidenceOperationID)
	if err != nil || opID == uuid.Nil {
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
	f := activeFulfillment(n.Coordination, input.Binding, input.FulfilledAt)
	review := fulfillmentReviewFor(n.Coordination, input.Binding, input.FulfilledAt)
	if f == nil || review == nil || !review.ReviewRequired || review.RequiredAt == nil || review.Fingerprint != input.ReviewFingerprint || len(n.Coordination.Reaffirmations) >= 128 {
		return nil, ErrConflict
	}
	fresh, err := observeFulfillmentReview(ctx, store, account, tenant, app, scope, id, n, *f, operator)
	if err != nil {
		return nil, err
	}
	if fresh.CurrentContractVersion < 1 || slices.Contains(fresh.CurrentReasons, "current_evidence_unavailable") {
		return nil, ErrConflict
	}
	proof, err := readReaffirmationEvidence(ctx, store, account, tenant, app, scope, input.Binding, opID.String(), input.EvidenceReportID, fresh.CurrentContractVersion, *review.RequiredAt, operator)
	if err != nil {
		return nil, err
	}
	// Observe the new proof before accepting an explicit decision. Contract changes
	// and named invalidations during these independent reads cannot produce acceptance.
	temporary := *f
	temporary.Evidence = proof
	copy := *n.Coordination
	copy.Reaffirmations = nil
	probe := n
	probe.Coordination = &copy
	checked, err := observeFulfillmentReview(ctx, store, account, tenant, app, scope, id, probe, temporary, operator)
	if err != nil {
		return nil, err
	}
	if len(checked.CurrentReasons) > 0 {
		return nil, ErrConflict
	}
	return &api.OperationWorkflowObligationReaffirmation{ActionKind: f.ActionKind, Binding: f.Binding, FulfilledAt: f.CompletedAt, ReviewFingerprint: review.Fingerprint, Reason: input.Reason, SourceSequence: sequence, Owner: review.Owner, Evidence: proof, EvidenceCheckedAt: checked.CheckedAt}, nil
}

func readReaffirmationEvidence(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope string, binding api.OperationWorkflowObligationTargetBinding, operation, reportID string, version int, after time.Time, operator bool) (api.OperationWorkflowStateHistoryEntry, error) {
	c := binding.Case
	if operator {
		tenant = c.PlatformTenantID
	}
	op, err := store.OperationByID(ctx, account, tenant, operation)
	if err != nil {
		return api.OperationWorkflowStateHistoryEntry{}, err
	}
	if !sameOperationHistoryIdentity(op.AppID, app) || op.Scope != scope || !sameOperationHistoryIdentity(op.PlatformTenantID, tenant) || op.Subject == nil || *op.Subject != c.Subject {
		return api.OperationWorkflowStateHistoryEntry{}, ErrNotFound
	}
	opts := api.OperationMilestoneListOptions{AppID: app, Scope: scope, SubjectType: c.Subject.Type, SubjectID: c.Subject.ID, Workflow: c.Workflow, WorkflowInstanceID: c.InstanceID, Limit: api.OperationHistoryPageMax}
	if operator {
		opts.TenantID = tenant
	}
	for scanned := 0; scanned < 1000; {
		var page api.OperationMilestonesResponse
		if operator {
			page, err = store.ListAccountOperationMilestones(ctx, account, opts)
		} else {
			page, err = store.ListPlatformTenantOperationMilestones(ctx, account, tenant, opts)
		}
		if err != nil {
			return api.OperationWorkflowStateHistoryEntry{}, err
		}
		for _, report := range page.WorkflowStateHistory {
			scanned++
			if report.ID != reportID || report.OperationID != operation {
				continue
			}
			if report.Workflow != c.Workflow || report.InstanceID != c.InstanceID || report.ContractVersion != version || report.PublishedAt.Before(after) || report.BlockersOnly || report.DeadlineOnly || report.OutcomeOnly || report.DependenciesOnly {
				return api.OperationWorkflowStateHistoryEntry{}, ErrInvalidArgument
			}
			report.PlatformTenantID = c.PlatformTenantID
			return report, nil
		}
		if page.NextWorkflowStateCursor == "" || len(page.WorkflowStateHistory) == 0 {
			break
		}
		opts.WorkflowStateCursor = page.NextWorkflowStateCursor
	}
	return api.OperationWorkflowStateHistoryEntry{}, ErrNotFound
}

func applyObligationReaffirmation(n api.OperationWorkflowReadinessNotification, req readinessCoordinationUpdate, actor string, now time.Time) (api.OperationWorkflowReadinessNotification, error) {
	if req.Reaffirmation == nil {
		return n, nil
	}
	prepared := req.PreparedReaffirmation
	if prepared == nil || n.Coordination == nil || len(n.Coordination.Reaffirmations) >= 128 || activeFulfillment(n.Coordination, prepared.Binding, prepared.FulfilledAt) == nil {
		return n, ErrConflict
	}
	review := fulfillmentReviewFor(n.Coordination, prepared.Binding, prepared.FulfilledAt)
	if review == nil || !review.ReviewRequired || review.Fingerprint != prepared.ReviewFingerprint {
		return n, ErrConflict
	}
	value := *prepared
	value.ReaffirmedAt, value.ReaffirmedBy = now, actor
	coord := *n.Coordination
	coord.Reaffirmations = append(slices.Clone(coord.Reaffirmations), value)
	coord.FulfillmentReviews = slices.DeleteFunc(slices.Clone(coord.FulfillmentReviews), func(r api.OperationWorkflowObligationFulfillmentReview) bool {
		return fulfillmentReviewKey(r.Binding, r.FulfilledAt) == fulfillmentReviewKey(value.Binding, value.FulfilledAt)
	})
	n.Coordination = &coord
	return n, nil
}

func fulfillmentReviewEvent(review api.OperationWorkflowObligationFulfillmentReview, kind string, at time.Time, actor string) api.OperationWorkflowObligationEvent {
	value := review
	return api.OperationWorkflowObligationEvent{ActionKind: review.ActionKind, FulfillmentReview: &value, ObligationID: review.Binding.ObligationID, Cases: []api.OperationWorkflowNextActionCase{review.Binding.Case}, Kind: kind, At: at, Actor: actor, Explanation: "Fulfillment review required: " + strings.Join(review.Reasons, ", ")}
}
func reaffirmationEvent(r api.OperationWorkflowObligationReaffirmation) api.OperationWorkflowObligationEvent {
	value := r
	return api.OperationWorkflowObligationEvent{ActionKind: r.ActionKind, Reaffirmation: &value, ObligationID: r.Binding.ObligationID, Cases: []api.OperationWorkflowNextActionCase{r.Binding.Case}, Kind: "fulfillment_reaffirmed", At: r.ReaffirmedAt, Actor: r.ReaffirmedBy, Explanation: "Fulfillment explicitly reaffirmed with fresh evidence: " + r.Reason}
}
