package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type OperationWorkflowCoordinationTimelineStore interface {
	WorkflowCoordinationTimeline(context.Context, string, string, api.OperationWorkflowCoordinationTimelineOptions, bool) (api.OperationWorkflowCoordinationTimeline, error)
}

func (m *MemStore) WorkflowCoordinationTimeline(ctx context.Context, account, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, operator bool) (api.OperationWorkflowCoordinationTimeline, error) {
	return workflowCoordinationTimeline(ctx, m, account, tenant, opts, operator)
}
func (s *PgStore) WorkflowCoordinationTimeline(ctx context.Context, account, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, operator bool) (api.OperationWorkflowCoordinationTimeline, error) {
	return workflowCoordinationTimeline(ctx, s, account, tenant, opts, operator)
}

func workflowCoordinationTimeline(ctx context.Context, store readinessSubscriptionBackend, account, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, operator bool) (api.OperationWorkflowCoordinationTimeline, error) {
	out := api.OperationWorkflowCoordinationTimeline{Items: []api.OperationWorkflowCoordinationTimelineEvent{}, Sources: []api.OperationWorkflowCoordinationTimelineSource{}, RetentionLimited: true, Subject: api.OperationSubject{Type: opts.SubjectType, ID: opts.SubjectID}, Workflow: opts.Workflow, InstanceID: opts.InstanceID, Scope: opts.Scope, Limitations: []string{
		"Only retained subscription inboxes are projected; deleted subscriptions, pruned notifications and unattributed failures are not workflow history.",
		"Coordination snapshots retain the latest edit, not every past schedule, owner or note. Assignment histories are bounded and first acknowledgment actors may be unavailable.",
		"Business outcome and watch entries use observation clocks; application occurrence times and causation are not inferred. This does not authorize execution.",
		"Sources are read independently. Offset pages can move during edits, polling, deletion or retention pruning; refresh to start a new view.",
	}}
	if !operator && opts.TenantID != "" {
		return out, ErrInvalidArgument
	}
	if operator {
		tenant = opts.TenantID
	}
	normalized, _, err := prepareOperationHistoryQuery(account, tenant, api.OperationListOptions{AppID: opts.AppID, Scope: opts.Scope, Limit: opts.Limit}, operator)
	if err != nil {
		return out, err
	}
	if api.ValidateOperationSubject(out.Subject) != nil || api.ValidateOperationWorkflowName(opts.Workflow) != nil || api.ValidateOperationWorkflowInstanceID(opts.InstanceID) != nil || opts.Offset < 0 || opts.Offset > api.OperationWorkflowCoordinationTimelineEventsMax {
		return out, ErrInvalidArgument
	}
	opts.AppID, opts.Limit = normalized.AppID, normalized.Limit
	tenant = canonicalReadinessTenant(tenant)
	if operator {
		out.PlatformTenantID = tenant
	}
	inboxTenant := tenant
	if operator {
		inboxTenant = ""
	} // Include broad and customer-specific operator subscriptions; filter exact notification identity below.
	subscriptions, err := store.listReadinessSubscriptions(ctx, account, inboxTenant, opts.AppID, opts.Scope, operator)
	if err != nil {
		return out, err
	}
	sort.Slice(subscriptions, func(i, j int) bool { return subscriptions[i].ID < subscriptions[j].ID })
	if len(subscriptions) > api.OperationWorkflowReadinessSubscriptionsPerAppMax {
		subscriptions = subscriptions[:api.OperationWorkflowReadinessSubscriptionsPerAppMax]
		out.SourcesTruncated = true
	}
	for _, subscription := range subscriptions {
		page, err := store.readReadinessInbox(ctx, account, inboxTenant, opts.AppID, opts.Scope, subscription.ID, 0, api.OperationWorkflowReadinessInboxMax, operator)
		source := api.OperationWorkflowCoordinationTimelineSource{SubscriptionID: subscription.ID, Name: subscription.Name, LatestSequence: subscription.LatestSequence}
		if errors.Is(err, ErrNotFound) {
			source.MissingDuringRead = true
			out.Sources = append(out.Sources, source)
			continue
		}
		if err != nil {
			return out, err
		}
		source.LatestSequence, source.RetainedCount = page.LatestSequence, len(page.Items)
		if len(page.Items) > 0 {
			source.EarliestSequence = page.Items[0].Sequence
		}
		source.GapBefore = page.ResyncRequired || source.EarliestSequence > 1 || len(page.Items) == 0 && page.LatestSequence > 0
		out.Sources = append(out.Sources, source)
		for _, n := range page.Items {
			if err = ctx.Err(); err != nil {
				return out, err
			}
			b := readinessNotificationBaseline(n)
			if b == nil || b.Subject != out.Subject || b.Workflow != opts.Workflow || b.InstanceID != opts.InstanceID {
				continue
			}
			evidenceTenant := b.PlatformTenantID
			if operator && evidenceTenant == "" {
				evidenceTenant = subscription.TenantID
			}
			if operator && evidenceTenant != tenant && !sameOperationHistoryIdentity(evidenceTenant, tenant) || !operator && evidenceTenant != "" && !sameOperationHistoryIdentity(evidenceTenant, tenant) {
				continue
			}
			entries, truncated := readinessCoordinationTimelineEvents(subscription, n)
			if !operator {
				for i := range entries {
					entries[i].SubscriptionTenantID = ""
				}
			}
			out.HistoryTruncated = out.HistoryTruncated || truncated
			out.Items = append(out.Items, entries...)
		}
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return a.ID < b.ID
	})
	out.EvaluatedAt = time.Now().UTC().Truncate(time.Microsecond)
	out.TotalCount = len(out.Items)
	start := min(opts.Offset, len(out.Items))
	end := min(start+opts.Limit, len(out.Items))
	out.Items = out.Items[start:end]
	out.NextOffset = end
	out.HasMore = end < out.TotalCount
	return out, nil
}
