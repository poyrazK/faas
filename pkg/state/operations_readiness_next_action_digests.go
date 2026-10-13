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
	items := []api.OperationWorkflowNextActionQueueItem{}
	matching, changed := 0, 0
	for _, item := range page.NextActions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		action := item.Action
		if action.Kind != "handoff_acceptance" && (action.DueAt == nil || action.DueAt.After(soon)) {
			continue
		}
		matching++
		key := action.SubscriptionID + "/" + strconv.FormatInt(action.Sequence, 10) + "/" + action.Kind
		live[key] = true
		fingerprint := readinessDigest(item)
		if known[key] == fingerprint {
			continue
		}
		changed++
		if len(items) < 20 {
			items = append(items, item)
			known[key] = fingerprint
		}
	}
	for key := range known {
		if !live[key] {
			delete(known, key)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	next := now.Add(time.Duration(policy.CadenceSeconds) * time.Second)
	r.DigestKnown = known
	r.Subscription.DigestEvaluatedAt = &now
	r.Subscription.DigestNextAt = &next
	if len(items) == 0 {
		return nil, nil
	}
	notice := readinessSubscriptionNotify(r, "next_action_digest", "New or changed team work needs attention. These are retained queue observations; inspect current source controls before acting. Digest acknowledgment changes no ownership, commitment or application obligation.", nil)
	notice.NextActionDigest = &api.OperationWorkflowNextActionDigest{Items: items, Owner: policy.Owner, Scope: r.Subscription.Scope, EvaluatedAt: now, SoonUntil: soon, MatchingCount: matching, ChangedCount: changed, RemainingChangedCount: changed - len(items), RetentionLimited: true}
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: policy.Owner, UpdatedAt: now, UpdatedBy: "system:next-action-digest"}
	r.Subscription.DigestDeliveredAt = &now
	return []api.OperationWorkflowReadinessNotification{notice}, nil
}
