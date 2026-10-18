package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) initialReviewPolicyLocked(account, app string) (api.WorkflowRecoveryPolicyResponse, error) {
	if !m.eventRecoveryAppLocked(account, app) {
		return api.WorkflowRecoveryPolicyResponse{}, ErrNotFound
	}
	if p := m.recoveryPolicies[canonicalMemUUID(app)]; p != nil {
		return recoveryPolicyClone(p.Response), nil
	}
	return defaultWorkflowRecoveryPolicy(canonicalMemUUID(app)), nil
}
func (m *MemStore) initialRecoveryPendingLocked(app, run string) bool {
	for _, j := range m.eventRecoveryJobs {
		if !sameMemUUID(j.Job.AppID, app) || !eventRecoveryActive(j.Job.State) {
			continue
		}
		for _, i := range j.Items {
			if i.State == "pending" && sameMemUUID(i.WorkflowRunID, run) && i.ExpectedResumeCount != nil && *i.ExpectedResumeCount == 0 {
				return true
			}
		}
	}
	return false
}
func (m *MemStore) initialReviewBudgetLocked(app string, submit bool, now time.Time) (int, time.Time, int) {
	count, retained := 0, 0
	var last time.Time
	for id, r := range m.initialRecoveryReviews {
		if !sameMemUUID(r.AppID, app) {
			continue
		}
		if !r.PreparedAt.After(now.Add(-api.WorkflowRecoveryHistoryRetention)) && !initialReviewUnresolved(r) {
			delete(m.initialRecoveryReviews, id)
			continue
		}
		retained++
		at := r.PreparedAt
		if submit {
			if r.SubmittedAt == nil {
				continue
			}
			at = *r.SubmittedAt
		}
		if at.After(last) {
			last = at
		}
		if at.After(now.Add(-time.Hour)) {
			count++
		}
	}
	if !submit {
		if p := m.recoveryPolicies[canonicalMemUUID(app)]; p != nil {
			for _, raw := range p.Reviews {
				var r api.WorkflowRecoveryPolicyReview
				_ = json.Unmarshal(raw, &r)
				if r.Plan == nil {
					continue
				}
				if r.ObservedAt.After(last) {
					last = r.ObservedAt
				}
				if r.ObservedAt.After(now.Add(-time.Hour)) {
					count += len(r.Plan.Actions)
				}
			}
		}
	}
	return count, last, retained
}
func (m *MemStore) PrepareWorkflowInitialRecoveryReview(ctx context.Context, account, app, failure string, req api.WorkflowInitialRecoveryReviewRequest) (api.WorkflowInitialRecoveryReview, error) {
	var out api.WorkflowInitialRecoveryReview
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	if err := eventRecoveryIDs(account, failure); err != nil {
		return out, err
	}
	if err := req.Validate(); err != nil {
		return out, ErrEventRecoveryQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	p, err := m.initialReviewPolicyLocked(account, app)
	if err != nil {
		return out, err
	}
	if p.Version != req.ExpectedPolicyVersion {
		return out, ErrWorkflowRecoveryPolicyConflict
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	count, last, retained := m.initialReviewBudgetLocked(app, false, now)
	id := initialReviewID(app, failure, p.Version)
	if existing, ok := m.initialRecoveryReviews[id]; ok {
		preview, err := m.previewWorkflowInitialRecoveryLocked(ctx, account, app, failure, now)
		return initialReviewObserved(existing, p, preview, err, now)
	}
	preview, err := m.previewWorkflowInitialRecoveryLocked(ctx, account, app, failure, now)
	if err != nil {
		return out, err
	}
	if reason := api.WorkflowInitialFailurePolicyReason(p.Policy, preview); reason != "eligible" {
		return out, initialReviewBlocked(reason)
	}
	if m.initialRecoveryPendingLocked(app, preview.Failure.RunID) {
		return out, initialReviewBlocked("pending_recovery")
	}
	if retained >= api.WorkflowRecoveryPolicyReviewsMax {
		return out, initialReviewBlocked("review_limit")
	}
	if reason := initialReviewBudgetReason(count, last, p.Policy, now); reason != "" {
		return out, initialReviewBlocked(reason)
	}
	owner, _ := m.eventSubscriptionAppLocked(app)
	out = newInitialReview(ctx, owner.Slug, p, preview, now)
	m.captureInitialReviewNotificationLocked(account, &out, now)
	if m.initialRecoveryReviews == nil {
		m.initialRecoveryReviews = map[string]api.WorkflowInitialRecoveryReview{}
	}
	m.initialRecoveryReviews[id] = recoveryPolicyClone(out)
	m.recordInitialLifecycleLocked(out)
	return out, nil
}
func (m *MemStore) GetWorkflowInitialRecoveryReview(ctx context.Context, account, app, id string) (api.WorkflowInitialRecoveryReview, error) {
	var out api.WorkflowInitialRecoveryReview
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	p, err := m.initialReviewPolicyLocked(account, app)
	if err != nil {
		return out, err
	}
	r, ok := m.initialRecoveryReviews[canonicalMemUUID(id)]
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !ok || !sameMemUUID(r.AppID, app) || !r.PreparedAt.After(now.Add(-api.WorkflowRecoveryHistoryRetention)) && !initialReviewUnresolved(r) {
		return out, ErrNotFound
	}
	if r.Status != "prepared" {
		return recoveryPolicyClone(r), nil
	}
	preview, err := m.previewWorkflowInitialRecoveryLocked(ctx, account, app, r.Failure.ID, now)
	return initialReviewObserved(r, p, preview, err, now)
}
func (m *MemStore) DecideWorkflowInitialRecoveryReview(ctx context.Context, account, app, id string, req api.WorkflowInitialRecoveryReviewDecisionRequest) (api.WorkflowInitialRecoveryReview, error) {
	var out api.WorkflowInitialRecoveryReview
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	if err := req.Validate(); err != nil {
		return out, ErrEventRecoveryQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	p, err := m.initialReviewPolicyLocked(account, app)
	if err != nil {
		return out, err
	}
	return m.decideInitialReviewLocked(ctx, account, app, id, p, req, time.Now().UTC().Truncate(time.Microsecond))
}
func (m *MemStore) decideInitialReviewLocked(ctx context.Context, account, app, id string, p api.WorkflowRecoveryPolicyResponse, req api.WorkflowInitialRecoveryReviewDecisionRequest, now time.Time) (api.WorkflowInitialRecoveryReview, error) {
	var out api.WorkflowInitialRecoveryReview
	r, ok := m.initialRecoveryReviews[canonicalMemUUID(id)]
	if !ok || !sameMemUUID(r.AppID, app) || !r.PreparedAt.After(now.Add(-api.WorkflowRecoveryHistoryRetention)) && !initialReviewUnresolved(r) {
		return out, ErrNotFound
	}
	if r.Status != "prepared" {
		if req.Decision == "submit" && r.Status == "submitted" || req.Decision == "cancel" && r.Status == "cancelled" {
			return recoveryPolicyClone(r), nil
		}
		return out, initialReviewBlocked(r.Status)
	}
	if req.Decision == "cancel" {
		initialReviewTransition(ctx, &r, "cancelled", req.Reason, now)
		m.initialRecoveryReviews[r.ID] = recoveryPolicyClone(r)
		m.recordInitialLifecycleLocked(r)
		return recoveryPolicyClone(r), nil
	}

	preview, err := m.previewWorkflowInitialRecoveryLocked(ctx, account, app, r.Failure.ID, now)
	observed, err := initialReviewObserved(r, p, preview, err, now)
	if err != nil {
		return out, err
	}
	if observed.Status != "prepared" {
		initialReviewTransition(ctx, &r, observed.Status, observed.Reason, now)
		m.initialRecoveryReviews[r.ID] = r
		m.recordInitialLifecycleLocked(r)
		return recoveryPolicyClone(r), nil
	}
	if req.Decision == "cancel" {
		initialReviewTransition(ctx, &r, "cancelled", req.Reason, now)
	} else {
		if observed.Reason != "eligible" {
			return out, initialReviewBlocked(observed.Reason)
		}
		if m.initialRecoveryPendingLocked(app, r.Failure.RunID) {
			return out, initialReviewBlocked("pending_recovery")
		}
		if p.Policy.Mode == "automatic_initial" {
			if reason := m.initialAutomaticSubmissionReasonLocked(app, p, now); reason != "" {
				return out, initialReviewBlocked(reason)
			}
		}
		if p.Policy.Mode != "automatic_initial" {
			count, last, _ := m.initialReviewBudgetLocked(app, true, now)
			if reason := initialReviewBudgetReason(count, last, p.Policy, now); reason != "" {
				return out, initialReviewBlocked(reason)
			}
		}
		job, err := m.createEventRecoveryLocked(ctx, account, app, initialReviewJobRequest(r, req.Reason))
		if err != nil {
			return out, err
		}
		r.JobID = job.ID
		r.SubmittedAt = &now
		initialReviewTransition(ctx, &r, "submitted", req.Reason, now)
	}
	m.initialRecoveryReviews[r.ID] = recoveryPolicyClone(r)
	m.recordInitialLifecycleLocked(r)
	return recoveryPolicyClone(r), nil
}
func (m *MemStore) captureInitialReviewNotificationLocked(account string, r *api.WorkflowInitialRecoveryReview, now time.Time) {
	receivers := []string{}
	for _, h := range m.appWebhooks {
		if h.Scope == AppWebhookScopeApp && sameMemUUID(h.AccountID, account) && sameMemUUID(h.AppID, r.AppID) && h.Enabled && appWebhookMatches(h.EventFilter, AppWebhookEventRecoveryReviewReady) {
			receivers = append(receivers, h.ID)
		}
	}
	sort.Strings(receivers)
	r.Notification = newRecoveryReviewNotification(r.ID, "ready", true, receivers, now)
	if len(receivers) == 0 {
		return
	}
	payload, _ := json.Marshal(initialReviewReadyPayload(*r, now))
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
	}
	m.appWebhookEventOutbox[r.Notification.EventID] = appWebhookOutboxEvent{ID: r.Notification.EventID, AccountID: account, AppID: r.AppID, Event: AppWebhookEventRecoveryReviewReady, SourceID: r.ID, Payload: payload, RecipientWebhookIDs: receivers, CreatedAt: now}
}
func (m *MemStore) ListWorkflowInitialRecoveryReviews(ctx context.Context, account, app, after string, limit int) (api.WorkflowInitialRecoveryReviews, error) {
	out := api.WorkflowInitialRecoveryReviews{Reviews: []api.WorkflowInitialRecoveryReview{}}
	if err := validRecoveryPolicyReviewQuery(account, app, after, limit); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	p, err := m.initialReviewPolicyLocked(account, app)
	if err != nil {
		return out, err
	}
	ids := []string{}
	now := time.Now().UTC().Truncate(time.Microsecond)
	for id, r := range m.initialRecoveryReviews {
		if sameMemUUID(r.AppID, app) && id > canonicalMemUUID(after) && (r.PreparedAt.After(now.Add(-api.WorkflowRecoveryHistoryRetention)) || initialReviewUnresolved(r)) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		out.NextAfter = ids[limit-1]
		ids = ids[:limit]
	}
	for _, id := range ids {
		r := m.initialRecoveryReviews[id]
		if r.Status == "prepared" {
			preview, err := m.previewWorkflowInitialRecoveryLocked(ctx, account, app, r.Failure.ID, now)
			r, err = initialReviewObserved(r, p, preview, err, now)
			if err != nil {
				return out, err
			}
		}
		out.Reviews = append(out.Reviews, recoveryPolicyClone(r))
	}
	return out, nil
}

func (m *MemStore) initialReviewJobAdmissionLocked(job *memEventRecoveryJob, now time.Time) (string, error) {
	for _, r := range m.initialRecoveryReviews {
		if r.JobID == job.Job.ID {
			p, err := m.initialReviewPolicyLocked(job.AccountID, job.Job.AppID)
			if err != nil {
				return "", err
			}
			return initialReviewAdmissionReason(r, p, now), nil
		}
	}
	return "", nil
}
