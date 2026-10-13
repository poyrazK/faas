package state

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func normalizeNextActionDigestPolicy(policy api.OperationWorkflowNextActionDigestPolicy) (api.OperationWorkflowNextActionDigestPolicy, error) {
	if policy.CadenceSeconds == 0 {
		policy.CadenceSeconds = 3600
	}
	if policy.SoonAfterSeconds == 0 {
		policy.SoonAfterSeconds = 86400
	}
	if policy.Owner == "" || strings.TrimSpace(policy.Owner) != policy.Owner || len(policy.Owner) > 256 || !utf8.ValidString(policy.Owner) || strings.IndexFunc(policy.Owner, unicode.IsControl) >= 0 || policy.Workflow != "" && api.ValidateOperationWorkflowName(policy.Workflow) != nil || policy.ActionKind != "" && !slices.Contains(workflowNextActionKinds, policy.ActionKind) || !slices.Contains([]string{"", "human", "automatic", "suggested"}, policy.ActionTrigger) || policy.CadenceSeconds < 60 || policy.CadenceSeconds > 604800 || policy.SoonAfterSeconds < 60 || policy.SoonAfterSeconds > 604800 {
		return policy, ErrInvalidArgument
	}
	return policy, nil
}

// Only filter snapshots are delivered: work and authorization remain source-owned.
// Known item fingerprints and the notice are persisted together under the existing subscription lease.
func evaluateNextActionDigest(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord) ([]api.OperationWorkflowReadinessNotification, error) {
	if r.Subscription.NextActionDigest == nil {
		return nil, nil
	}
	policy, err := normalizeNextActionDigestPolicy(*r.Subscription.NextActionDigest)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if r.Subscription.DigestNextAt != nil && now.Before(*r.Subscription.DigestNextAt) {
		return nil, nil
	}
	rows, err := store.readReadinessFollowUps(ctx, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, r.Operator)
	if err != nil {
		return nil, err
	}
	opts := api.OperationWorkflowReadinessFollowUpOptions{AppID: r.Subscription.AppID, TenantID: r.Subscription.TenantID, Scope: r.Subscription.Scope, View: "next_actions", Owner: policy.Owner, Workflow: policy.Workflow, ActionKind: policy.ActionKind, ActionTrigger: policy.ActionTrigger}
	page, err := nextActionWorkQueue(ctx, rows, r.Subscription.TenantID, opts, r.Operator, readinessQueueOffsetMax("next_actions"), api.OperationWorkflowReadinessFollowUpsResponse{Items: []api.OperationWorkflowReadinessFollowUp{}, EvaluatedAt: now, RetentionLimited: true})
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	soon := now.Add(time.Duration(policy.SoonAfterSeconds) * time.Second)
	live := map[string]bool{}
	known := map[string]string{}
	for key, value := range r.DigestKnown {
		known[key] = value
	}
	delivered := map[string]api.OperationWorkflowNextActionQueueItem{}
	for key, item := range r.DigestDeliveredItems {
		delivered[key] = item
	}
	explanations := []api.OperationWorkflowNextActionDigestExplanation{}
	items := []api.OperationWorkflowNextActionQueueItem{}
	matching, changed := 0, 0
	for _, item := range page.NextActions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		action := item.Action
		if action.Kind != "handoff_acceptance" && (action.Kind != "fulfillment_review" || action.DueAt != nil) && (action.DueAt == nil || action.DueAt.After(soon)) {
			continue
		}
		matching++
		key := action.SubscriptionID + "/" + strconv.FormatInt(action.Sequence, 10) + "/" + action.Kind
		if action.Obligation != nil {
			key += "/" + action.Obligation.ID
		}
		live[key] = true
		fingerprint := nextActionDigestFingerprint(item)
		if known[key] == fingerprint {
			continue
		}
		changed++
		if len(items) < 20 {
			previous, available := delivered[key]
			explanations = append(explanations, explainNextActionDigest(item, previous, available, known[key] != "", now))
			delivered[key] = item
			items = append(items, item)
			known[key] = fingerprint
		}
	}
	for key := range known {
		if !live[key] {
			delete(known, key)
		}
	}
	for key := range delivered {
		if !live[key] {
			delete(delivered, key)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	next := now.Add(time.Duration(policy.CadenceSeconds) * time.Second)
	r.DigestKnown = known
	r.DigestDeliveredItems = delivered
	r.Subscription.DigestEvaluatedAt = &now
	r.Subscription.DigestNextAt = &next
	if len(items) == 0 {
		return nil, nil
	}
	notice := readinessSubscriptionNotify(r, "next_action_digest", "New or changed team work needs attention. These are retained queue observations; inspect current source controls before acting. Digest acknowledgment changes no ownership, commitment or application obligation.", nil)
	notice.NextActionDigest = &api.OperationWorkflowNextActionDigest{Explanations: explanations, Items: items, Owner: policy.Owner, Scope: r.Subscription.Scope, EvaluatedAt: now, SoonUntil: soon, MatchingCount: matching, ChangedCount: changed, RemainingChangedCount: changed - len(items), RetentionLimited: true}
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: policy.Owner, UpdatedAt: now, UpdatedBy: "system:next-action-digest"}
	r.Subscription.DigestDeliveredAt = &now
	return []api.OperationWorkflowReadinessNotification{notice}, nil
}

// Compare only the last delivered snapshot, never an undelivered evaluation.
func explainNextActionDigest(item, previous api.OperationWorkflowNextActionQueueItem, available, known bool, now time.Time) api.OperationWorkflowNextActionDigestExplanation {
	a := item.Action
	out := api.OperationWorkflowNextActionDigestExplanation{SubscriptionID: a.SubscriptionID, Sequence: a.Sequence, ActionKind: a.Kind, Reasons: []string{}, Comparison: "first_delivery", ChangedFields: []string{}, NextStep: "Inspect current source controls and review the follow-up with current evidence."}
	if a.Obligation != nil {
		out.ObligationID = a.Obligation.ID
	}
	if a.DueAt != nil {
		if !a.DueAt.After(now) {
			out.Reasons = append(out.Reasons, "overdue")
		} else {
			out.Reasons = append(out.Reasons, "approaching_deadline")
		}
	}
	if a.Kind == "handoff_acceptance" {
		out.Reasons = append(out.Reasons, "waiting_for_acceptance")
	}
	if a.Kind == "fulfillment_review" {
		out.Reasons = append(out.Reasons, "waiting_for_fulfillment_review")
	}
	if available {
		out.Comparison = "previous_delivery"
		pairs := []struct {
			name           string
			current, prior any
		}{
			{"fulfillment_review", fulfillmentActionFingerprint(a), fulfillmentActionFingerprint(previous.Action)}, {"obligation", a.Obligation, previous.Action.Obligation}, {"deadline", a.DueAt, previous.Action.DueAt}, {"overdue", a.Overdue, previous.Action.Overdue},
			{"owner", a.Owner, previous.Action.Owner}, {"summary", a.Summary, previous.Action.Summary},
			{"trigger", a.Trigger, previous.Action.Trigger}, {"evidence", a.Evidence, previous.Action.Evidence},
			{"cases", item.Cases, previous.Cases}, {"shared_plan", a.SharedPlan, previous.Action.SharedPlan},
			{"scope", item.Scope, previous.Scope}, {"subscription_tenant_id", item.SubscriptionTenantID, previous.SubscriptionTenantID},
		}
		for _, pair := range pairs {
			if readinessDigest(pair.current) != readinessDigest(pair.prior) {
				out.ChangedFields = append(out.ChangedFields, pair.name)
			}
		}
	} else if known {
		out.Comparison = "previous_snapshot_unavailable"
	}
	switch a.Kind {
	case "fulfillment_review":
		out.NextStep = "Inspect the exact case evidence, then explicitly reaffirm fulfillment or reopen the obligation."
	case "response_check_in":
		out.NextStep = "Inspect the response plan and record a check-in with current evidence."
	case "decision_revalidation":
		out.NextStep = "Review current conditions and record continue, revise or pause."
	case "handoff_acceptance":
		out.NextStep = "Inspect the handoff and explicitly accept or decline it using the source controls."
	case "handoff_reassignment":
		out.NextStep = "Inspect the declined handoff and route it to a different team with a reason."
	case "escalation_acknowledgment":
		out.NextStep = "Inspect the escalation and name a coordinator, action plan and deadline."
	case "coordinator_completion":
		out.NextStep = "Inspect the coordinator plan and record completion with linked evidence when fulfilled."
	case "scheduled_evidence_check":
		out.NextStep = "The worker will check evidence automatically; inspect the source to reschedule or cancel."
	case "dependency_review":
		out.NextStep = "Review current prerequisite evidence using the source playbook controls."
	}
	return out
}
