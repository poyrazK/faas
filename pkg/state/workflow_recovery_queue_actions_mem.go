package state

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
	"time"
)

func (m *MemStore) SuppressWorkflowRecoveryAutomaticAction(ctx context.Context, account, app, review, action string, r api.SuppressWorkflowRecoveryAutomaticActionRequest) (api.WorkflowRecoveryAutomaticAction, error) {
	var out api.WorkflowRecoveryAutomaticAction
	if err := validateAutomaticSuppression(account, app, review, action, r); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !m.eventRecoveryAppLocked(account, app) {
		return out, ErrNotFound
	}
	p := m.recoveryPolicies[canonicalMemUUID(app)]
	if p == nil {
		return out, ErrNotFound
	}
	if p.Response.Version != r.ExpectedPolicyVersion {
		return out, ErrWorkflowRecoveryPolicyConflict
	}
	if initial, exists := m.initialRecoveryReviews[canonicalMemUUID(review)]; exists && sameMemUUID(review, action) && sameMemUUID(initial.AppID, app) {
		if initial.PolicyVersion != r.ExpectedPolicyVersion {
			return out, ErrWorkflowRecoveryPolicyConflict
		}
		decided, err := m.decideInitialReviewLocked(ctx, account, app, review, p.Response, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "cancel", Reason: r.Reason}, time.Now().UTC())
		if err != nil {
			return out, err
		}
		return initialAutomaticSuppressionAction(decided), nil
	}
	e := p.Executions[canonicalMemUUID(action)]
	if e == nil {
		return out, ErrNotFound
	}
	retained, ok := recoveryAutomaticMemReview(p, e.ActionID)
	if !ok || !sameMemUUID(retained.ID, review) {
		return out, ErrNotFound
	}
	if retained.PolicyVersion != r.ExpectedPolicyVersion {
		return out, ErrWorkflowRecoveryPolicyConflict
	}
	if automaticSuppressionReplay(*e, r) {
		return recoveryPolicyClone(*e), nil
	}
	if e.Status != "pending" {
		return out, ErrEventRecoveryChanged
	}
	actor := recoveryAuditActor(ctx, "automatic_retry_suppressed")
	e.Status = "suppressed"
	e.Reason = "operator_suppressed"
	e.Suppression = &api.WorkflowRecoveryAutomaticSuppression{Reason: r.Reason, ActorKind: actor.Kind, ActorID: actor.ID, SuppressedAt: time.Now().UTC()}
	return recoveryPolicyClone(*e), nil
}
func (m *MemStore) ProcessNextWorkflowRecoveryAutomaticBlockedNotification(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return false, err
	}
	type candidate struct {
		p      *memWorkflowRecoveryPolicy
		e      *api.WorkflowRecoveryAutomaticAction
		review api.WorkflowRecoveryPolicyReview
		app    App
	}
	candidates := []candidate{}
	for _, p := range m.recoveryPolicies {
		app, ok := m.eventSubscriptionAppLocked(p.Response.AppID)
		if !ok || !m.eventRecoveryAppLocked(app.AccountID, app.ID) {
			continue
		}
		for id, e := range p.Executions {
			r, ok := recoveryAutomaticMemReview(p, id)
			if ok && automaticBlockedEligible(p.Response, *e, r.PolicyVersion) && !p.BlockedNext[id].After(now) {
				candidates = append(candidates, candidate{p, e, r, app})
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		x, y := a.p.BlockedNext[a.e.ActionID], b.p.BlockedNext[b.e.ActionID]
		if !x.Equal(y) {
			return x.Before(y)
		}
		if a.p.Response.AppID != b.p.Response.AppID {
			return a.p.Response.AppID < b.p.Response.AppID
		}
		return a.e.ActionID < b.e.ActionID
	})
	if len(candidates) == 0 {
		m.mu.Unlock()
		return false, nil
	}
	c := candidates[0]
	if c.p.BlockedNext == nil {
		c.p.BlockedNext = map[string]time.Time{}
	}
	c.p.BlockedNext[c.e.ActionID] = now.Add(api.WorkflowRecoveryPolicyPollInterval)
	defer m.mu.Unlock()
	action := recoveryPolicyClone(*c.e)
	policy := recoveryPolicyClone(c.p.Response)
	_, retained, current, frame, err := m.retryEvidenceScopeLocked(c.app.AccountID, policy.AppID, c.review.ID, action.ActionID, now)
	if err != nil {
		return false, err
	}
	if retained.ReviewDecision != nil && retained.ReviewDecision.Decision == "dismissed" {
		return true, nil
	}
	var diagnosis api.WorkflowRetryEvidenceDiagnosis
	if action.Status == "pending" {
		diagnosis = m.retryParentEvidenceLocked(retained, *current, frame, now)
		live, exists := m.workflowRuns[frame.Target.RunID]
		if !exists {
			diagnosis.Reason, diagnosis.Repairable = "run_not_retained", false
		} else if live.ResumeCount != frame.Target.ExpectedResumeCount {
			diagnosis.Reason, diagnosis.Repairable = "generation_superseded", false
		}
	} else {
		diagnosis = retryEvidenceDiagnosis(m.retryEvidenceLocked(retained, *current, frame, now), action.ActionID)
		diagnosis.EvidenceKind = "child"
		diagnosis.ItemPosition = 1
	}
	transition := automaticEvidenceTransition(policy.AppID, c.app.Slug, retained.ID, retained.PolicyVersion, action, diagnosis, now)
	if transition == nil {
		return true, nil
	}
	// Recheck scope after inspection before freezing recipients.
	if !m.eventRecoveryAppLocked(c.app.AccountID, policy.AppID) {
		return true, nil
	}
	ids := []string{}
	for _, h := range m.appWebhooks {
		if h.Scope == AppWebhookScopeApp && sameMemUUID(h.AccountID, c.app.AccountID) && sameMemUUID(h.AppID, policy.AppID) && h.Enabled && appWebhookMatches(h.EventFilter, AppWebhookEventRecoveryQueueBlocked) {
			ids = append(ids, h.ID)
		}
	}
	sort.Strings(ids)
	payload := *transition
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("encode blocked retry notification: %w", err)
	}
	receipt := automaticEvidenceReceipt(payload, action.BlockedNotification, ids)
	c.e.BlockedNotification = &receipt
	c.p.appendHistoryEvent(retryAlertHistory(retained, payload), now)
	if len(ids) > 0 {
		if m.appWebhookEventOutbox == nil {
			m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
		}
		m.appWebhookEventOutbox[payload.EventID] = appWebhookOutboxEvent{ID: payload.EventID, AccountID: c.app.AccountID, AppID: policy.AppID, Event: AppWebhookEventRecoveryQueueBlocked, SourceID: payload.EventID, Payload: raw, RecipientWebhookIDs: ids, CreatedAt: now}
	}
	return true, nil
}
