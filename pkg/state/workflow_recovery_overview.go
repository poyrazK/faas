package state

import (
	"context"
	"net/url"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type WorkflowRecoveryHealthOverviewStore interface {
	GetWorkflowRecoveryPolicyHealth(context.Context, string, string, int) (api.WorkflowRecoveryPolicyHealth, error)
}

type recoveryOverviewManual struct {
	RetryAwaiting   int                                 `json:"retry_awaiting"`
	RetryUnresolved int                                 `json:"retry_unresolved"`
	RetryIssues     []api.WorkflowRecoveryOverviewIssue `json:"retry_issues"`
	Awaiting        int                                 `json:"awaiting"`
	Unresolved      int                                 `json:"unresolved"`
	PollingFailures int                                 `json:"polling_failures"`
	Attention       int                                 `json:"attention"`
	Reviews         []api.WorkflowInitialRecoveryReview `json:"reviews"`
}

func recoveryOverview(h api.WorkflowRecoveryPolicyHealth, manual recoveryOverviewManual, alert *api.WorkflowRecoveryOverviewAlert, slug string) api.WorkflowRecoveryPolicyHealth {
	q := h.Queue
	o := &api.WorkflowRecoveryOverview{Status: "healthy", MonitoringStatus: "no_observed_failure", MonitoringOnly: true, ObservedAt: q.ObservedAt, ManualAwaitingApproval: manual.Awaiting, ManualUnresolved: manual.Unresolved, ManualPollingFailures: manual.PollingFailures, ManualAttention: manual.Attention, Issues: []api.WorkflowRecoveryOverviewIssue{}, LastAlert: alert}
	policyPath := "/v1/apps/" + url.PathEscape(slug) + "/recovery-policy"
	if h.Status == "blocked" || h.Status == "stalled" || h.Status == "paused" {
		o.Issues = append(o.Issues, api.WorkflowRecoveryOverviewIssue{Scope: "automatic_queue", Reason: h.Reason, PolicyVersion: q.Policy.Version, InspectionURL: policyPath + "/health"})
	}
	if x := q.Policy.AutomaticExecution; x != nil && x.PollingFailure != nil {
		f := x.PollingFailure
		o.MonitoringStatus = "failing"
		o.Issues = append(o.Issues, api.WorkflowRecoveryOverviewIssue{Scope: "automatic_policy", Reason: f.Code, PolicyVersion: q.Policy.Version, RetryAt: &f.RetryAt, InspectionURL: policyPath})
	} else if n := q.Policy.PollingNotification; n != nil && n.Payload.State == "failed" || alert != nil && alert.Notification.Payload.State == "failed" {
		o.MonitoringStatus = "unknown"
	}
	for _, r := range manual.Reviews {
		reason := "awaiting_approval"
		if r.SubmittedAt != nil {
			reason = "awaiting_result"
		}
		var retry *time.Time
		if r.Attention != nil && r.Attention.BlockReason != "" {
			reason = r.Attention.BlockReason
		}
		if r.ObservationFailure != nil {
			reason = r.ObservationFailure.Code
			retry = &r.ObservationFailure.RetryAt
		} else if r.PollingNotification != nil && r.PollingNotification.Payload.State == "failed" {
			o.MonitoringStatus = "unknown"
		}
		o.Issues = append(o.Issues, api.WorkflowRecoveryOverviewIssue{Scope: "manual_initial", Reason: reason, PolicyVersion: r.PolicyVersion, ReviewID: r.ID, JobID: r.JobID, RetryAt: retry, InspectionURL: "/v1/apps/" + url.PathEscape(slug) + "/initial-recovery-reviews/" + url.PathEscape(r.ID)})
	}
	if manual.PollingFailures > 0 {
		o.MonitoringStatus = "failing"
	}
	o.ManualRetryAwaitingReview, o.ManualRetryUnresolved = manual.RetryAwaiting, manual.RetryUnresolved
	o.Issues = append(o.Issues, manual.RetryIssues...)
	o.Truncated = q.Truncated || manual.Awaiting+manual.Unresolved > len(manual.Reviews)
	o.Truncated = o.Truncated || manual.RetryAwaiting+manual.RetryUnresolved > len(manual.RetryIssues)
	if len(o.Issues) > api.WorkflowRecoveryOverviewItemsMax {
		o.Issues = o.Issues[:api.WorkflowRecoveryOverviewItemsMax]
		o.Truncated = true
	}
	wait := h.Reason == "outstanding_retry" || h.Reason == "outstanding_recovery" || h.Reason == "hourly_budget" || h.Reason == "cooldown" || h.Reason == "execution_wait" || h.Reason == "admission_wait" || h.Reason == "execution_quota"
	switch {
	case o.MonitoringStatus == "failing" || manual.Attention > 0 || h.Status == "stalled" || h.Status == "blocked" && !wait || alert != nil && alert.DeliveryStatus == "failed":
		o.Status = "needs_attention"
	case h.Status == "paused":
		o.Status = "paused"
	case o.Truncated || o.MonitoringStatus == "unknown" || alert != nil && alert.DeliveryStatus == "unknown":
		o.Status = "unknown"
	case wait || q.Pending+q.Outstanding+manual.Awaiting+manual.Unresolved+manual.RetryAwaiting+manual.RetryUnresolved > 0:
		o.Status = "waiting"
	case h.Status == "inactive" && !q.Policy.Policy.Enabled:
		o.Status = "inactive"
	}
	h.Overview = o
	return h
}

func recoveryOverviewDelivery(a *api.WorkflowRecoveryOverviewAlert) {
	a.Expected = len(a.Notification.ReceiverWebhookIDs)
	switch {
	case a.Expected == 0:
		a.DeliveryStatus = "no_receivers"
	case a.Dead+a.Failed > 0:
		a.DeliveryStatus = "failed"
	case a.Missing > 0 && !a.RelayPending:
		a.DeliveryStatus = "unknown"
	case a.Missing > 0:
		a.DeliveryStatus = "relay_pending"
	case a.Succeeded == a.Expected:
		a.DeliveryStatus = "delivered"
	default:
		a.DeliveryStatus = "pending"
	}
}

func (m *MemStore) GetWorkflowRecoveryPolicyHealth(ctx context.Context, account, app string, seconds int) (api.WorkflowRecoveryPolicyHealth, error) {
	if err := eventRecoveryIDs(account, app); err != nil {
		return api.WorkflowRecoveryPolicyHealth{}, err
	}
	if err := api.ValidateRecoveryHealthThreshold(seconds); err != nil {
		return api.WorkflowRecoveryPolicyHealth{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.WorkflowRecoveryPolicyHealth{}, err
	}
	if !m.eventRecoveryAppLocked(account, app) {
		return api.WorkflowRecoveryPolicyHealth{}, ErrNotFound
	}
	scope, _ := m.eventSubscriptionAppLocked(app)
	now := time.Now().UTC()
	q, _ := m.workflowRecoveryAutomaticQueueLocked(app, now, false)
	if err := m.recoveryQueueEvidenceLocked(ctx, account, app, &q); err != nil {
		return api.WorkflowRecoveryPolicyHealth{}, err
	}
	manual := recoveryOverviewManual{Reviews: []api.WorkflowInitialRecoveryReview{}}
	for _, r := range m.initialRecoveryReviews {
		if !sameMemUUID(r.AppID, app) || r.Policy.Mode != "preview" {
			continue
		}
		unresolved := initialReviewUnresolved(r)
		awaiting := r.Status == "prepared" && r.SubmittedAt == nil && r.ExpiresAt.After(now) && q.Policy.Policy.Enabled && r.PolicyVersion == q.Policy.Version
		if !unresolved && !awaiting {
			continue
		}
		if unresolved {
			manual.Unresolved++
		} else {
			manual.Awaiting++
		}
		if r.ObservationFailure != nil {
			manual.PollingFailures++
		}
		if r.Attention != nil && r.Attention.BlockReason != "" {
			manual.Attention++
		}
		manual.Reviews = append(manual.Reviews, r)
		sort.Slice(manual.Reviews, func(i, j int) bool { return recoveryOverviewReviewLess(manual.Reviews[i], manual.Reviews[j]) })
		if len(manual.Reviews) > api.WorkflowRecoveryOverviewItemsMax {
			manual.Reviews = manual.Reviews[:api.WorkflowRecoveryOverviewItemsMax]
		}
	}
	var latest *api.WorkflowRecoveryPollingNotification
	var sequence int64
	if p := m.recoveryPolicies[canonicalMemUUID(app)]; p != nil {
		if err := m.recoveryOverviewRetryLocked(ctx, p, q, &manual, scope); err != nil {
			return api.WorkflowRecoveryPolicyHealth{}, err
		}
		for _, e := range p.History {
			n := e.Entry.PollingNotification
			if n == nil || !e.RecordedAt.After(now.Add(-api.WorkflowRecoveryHistoryRetention)) || e.RecordedAt.After(now) || e.Entry.OccurredAt.After(now) {
				continue
			}
			if latest == nil || n.Payload.ObservedAt.After(latest.Payload.ObservedAt) || n.Payload.ObservedAt.Equal(latest.Payload.ObservedAt) && e.Sequence > sequence {
				latest = n
				sequence = e.Sequence
			}
		}
	}
	var alert *api.WorkflowRecoveryOverviewAlert
	if latest != nil {
		alert = &api.WorkflowRecoveryOverviewAlert{Notification: *latest}
		for _, row := range m.appWebhookEventOutbox {
			if sameMemUUID(row.ID, latest.Payload.EventID) && sameMemUUID(row.AccountID, account) && sameMemUUID(row.AppID, app) && row.Event == AppWebhookEventRecoveryPollingStatus {
				alert.RelayPending = true
			}
		}
		for _, receiver := range latest.ReceiverWebhookIDs {
			found := false
			for id, d := range m.appWebhookDeliveries {
				if !sameMemUUID(m.appWebhookDeliverySources[id], latest.Payload.EventID) || !sameMemUUID(d.AccountID, account) || !sameMemUUID(d.AppID, app) || !sameMemUUID(d.WebhookID, receiver) || d.Event != AppWebhookEventRecoveryPollingStatus {
					continue
				}
				found = true
				switch d.Status {
				case AppWebhookDeliveryPending:
					alert.Pending++
				case AppWebhookDeliveryInFlight:
					alert.InFlight++
				case AppWebhookDeliverySucceeded:
					alert.Succeeded++
				case AppWebhookDeliveryFailed:
					alert.Failed++
				case AppWebhookDeliveryDead:
					alert.Dead++
				}
			}
			if !found {
				alert.Missing++
			}
		}
		recoveryOverviewDelivery(alert)
	}
	return recoveryPolicyClone(recoveryOverview(api.RecoveryPolicyHealth(q, seconds), manual, alert, scope.Slug)), nil
}

func recoveryOverviewReviewLess(a, b api.WorkflowInitialRecoveryReview) bool {
	if (a.ObservationFailure != nil) != (b.ObservationFailure != nil) {
		return a.ObservationFailure != nil
	}
	if (a.Attention != nil) != (b.Attention != nil) {
		return a.Attention != nil
	}
	return a.ID < b.ID
}
