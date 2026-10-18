package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func recoveryAutomaticMemReview(p *memWorkflowRecoveryPolicy, id string) (api.WorkflowRecoveryPolicyReview, bool) {
	for _, raw := range p.Reviews {
		var r api.WorkflowRecoveryPolicyReview
		if json.Unmarshal(raw, &r) != nil || r.Plan == nil {
			continue
		}
		for _, a := range r.Plan.Actions {
			if api.WorkflowRecoveryResponseActionID(*r.Plan, a) == id {
				return r, true
			}
		}
	}
	return api.WorkflowRecoveryPolicyReview{}, false
}
func forgetRecoveryPolicyReview(p *memWorkflowRecoveryPolicy, id string) {
	var r api.WorkflowRecoveryPolicyReview
	if json.Unmarshal(p.Reviews[id], &r) == nil && r.Plan != nil {
		for _, a := range r.Plan.Actions {
			delete(p.Executions, api.WorkflowRecoveryResponseActionID(*r.Plan, a))
			delete(p.BlockedNext, api.WorkflowRecoveryResponseActionID(*r.Plan, a))
		}
	}
	delete(p.Reviews, id)
	delete(p.Notifications, id)
}
func (m *MemStore) ProcessNextWorkflowRecoveryAutomaticPolicy(ctx context.Context, now time.Time) (bool, error) {
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
	policies := []*memWorkflowRecoveryPolicy{}
	for _, p := range m.recoveryPolicies {
		app, ok := m.eventSubscriptionAppLocked(p.Response.AppID)
		if ok && app.Status != AppDeleted && p.Response.Policy.Enabled && api.WorkflowRecoveryPolicyAutomaticMode(p.Response.Policy.Mode) && p.Response.AutomaticExecution != nil && p.Response.AutomaticExecution.PausedAt == nil && !p.NextExecute.After(now) {
			policies = append(policies, p)
		}
	}
	sort.Slice(policies, func(i, j int) bool {
		if policies[i].NextExecute.Equal(policies[j].NextExecute) {
			return policies[i].Response.AppID < policies[j].Response.AppID
		}
		return policies[i].NextExecute.Before(policies[j].NextExecute)
	})
	if len(policies) == 0 {
		m.mu.Unlock()
		return false, nil
	}
	p := policies[0]
	if err := validateAutomaticPollingMemLocked(m, p); err != nil {
		did, e := m.deferAutomaticPollingMem(p, err, now)
		m.mu.Unlock()
		return did, e
	}
	ctx = automaticRecoveryActor(ctx, p.Response)
	if p.Response.Policy.Mode == "automatic_initial" {
		now = now.UTC().Truncate(time.Microsecond)
	}
	app, _ := m.eventSubscriptionAppLocked(p.Response.AppID)
	if r := m.initialAutomaticOutstandingLocked(app.ID); r != nil {
		defer m.mu.Unlock()
		return m.runAutomaticPollingMemLocked(ctx, p, app, now, func() error { return m.processInitialAutomaticResultLocked(ctx, p, app, *r, now) })
	}
	var outstanding *api.WorkflowRecoveryAutomaticAction
	var retained api.WorkflowRecoveryPolicyReview
	for id, e := range p.Executions {
		r, ok := recoveryAutomaticMemReview(p, id)
		if !ok || m.eventRecoveryJobs[r.JobID] == nil {
			delete(p.Executions, id)
			continue
		}
		if e.Status == "pending" && r.PolicyVersion != p.Response.Version {
			e.Status = "suppressed"
			e.Reason = "policy_changed"
		}
		if e.Status == "applied" && e.Result == nil && (outstanding == nil || e.AppliedAt.Before(*outstanding.AppliedAt) || e.AppliedAt.Equal(*outstanding.AppliedAt) && e.ActionID < outstanding.ActionID) {
			outstanding = e
			retained = r
		}
	}
	if outstanding != nil {
		version := p.Response.Version
		updatedAt := p.Response.UpdatedAt
		appID := p.Response.AppID
		e := recoveryPolicyClone(*outstanding)
		m.mu.Unlock()
		result, reason, err := recoveryAutomaticEvaluationResult(ctx, m, app.AccountID, appID, retained.ID, e, now)
		m.mu.Lock()
		defer m.mu.Unlock()
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if p.Response.Version != version || !p.Response.UpdatedAt.Equal(updatedAt) || p.Response.AutomaticExecution.PausedAt != nil || p.Executions[e.ActionID] != outstanding || outstanding.Result != nil {
			return true, nil
		}
		if err != nil {
			return m.deferAutomaticPollingMem(p, err, now)
		}
		return m.runAutomaticPollingMemLocked(ctx, p, app, now, func() error {
			m.recordRecoveryEvaluationLocked(p, outstanding, reason, now, result)
			if result == nil {
				if chosen, _ := recoveryAutomaticPendingMem(p); chosen != nil {
					m.recordRecoveryEvaluationLocked(p, chosen, "outstanding_retry", now, nil)
				}
				return nil
			}
			outstanding.Result = recoveryPolicyClone(result)
			p.NextExecute = now
			if retained.PolicyVersion == version && recoveryAutomaticRecordFailure(&p.Response, *result, now) {
				if err = m.enqueueAutomaticRecoveryPauseLocked(app.AccountID, app.Slug, p.Response); err != nil {
					return err
				}
			}
			return nil
		})
	}
	defer m.mu.Unlock()
	if p.Response.Policy.Mode == "automatic_initial" {
		return m.runAutomaticPollingMemLocked(ctx, p, app, now, func() error { return m.executeInitialAutomaticPendingLocked(ctx, p, app, now) })
	}
	return m.runAutomaticPollingMemLocked(ctx, p, app, now, func() error { return m.executeAutomaticRecoveryPendingLocked(ctx, p, app, now) })
}
func (m *MemStore) executeAutomaticRecoveryPendingLocked(ctx context.Context, p *memWorkflowRecoveryPolicy, app App, now time.Time) (err error) {
	chosen, review := recoveryAutomaticPendingMem(p)
	hourly := 0
	last := time.Time{}
	charges := m.initialAutomaticChargesLocked(app.ID)
	for _, at := range p.Charges {
		charges = append(charges, at)
	}
	for _, at := range charges {
		if at.After(now.Add(-time.Hour)) {
			hourly++
		}
		if at.After(last) {
			last = at
		}
	}
	if chosen == nil {
		return nil
	}
	defer func() {
		if err == nil {
			reason := chosen.Reason
			if chosen.Status == "suppressed" {
				reason = "suppressed"
			}
			m.recordRecoveryEvaluationLocked(p, chosen, reason, now, nil)
		}
	}()
	suppress := func(reason string) error {
		chosen.Status = "suppressed"
		chosen.Reason = reason
		p.NextExecute = now
		return nil
	}
	if review.ReviewDecision != nil && review.ReviewDecision.Decision == "dismissed" {
		return suppress("dismissed")
	}
	action, err := recoveryAutomaticAction(review, *chosen, p.Response)
	if err != nil {
		return err
	}
	for _, job := range m.eventRecoveryJobs {
		if sameMemUUID(job.AccountID, app.AccountID) && job.Job.Selection.RequestID == chosen.ActionID {
			return suppress("already_applied")
		}
	}
	if hourly >= p.Response.Policy.HourlyExecutionBudget {
		chosen.Reason = "hourly_budget"
		return nil
	}
	if !last.IsZero() && last.Add(time.Duration(p.Response.Policy.CooldownSeconds)*time.Second).After(now) {
		chosen.Reason = "cooldown"
		return nil
	}
	parent := m.eventRecoveryJobs[review.JobID]
	if parent == nil {
		return suppress("target_unavailable")
	}
	candidates := []recoveryPolicyCandidate{}
	for _, item := range parent.Items {
		if item.Position != action.Position {
			continue
		}
		row, err := m.workflowRecoveryPreflightItemLocked(ctx, parent, item, now, true)
		if err != nil {
			return fmt.Errorf("recheck automatic retry: %w", err)
		}
		if row.Reason == "unknown" {
			chosen.Reason = "evidence_unknown"
			return nil
		}
		var run *WorkflowRun
		if r, ok := m.workflowRuns[item.WorkflowRunID]; ok {
			run = &r
		} else {
			chosen.Reason = "evidence_unknown"
			return nil
		}
		_, operation := m.operationForWorkflowLocked(item.WorkflowRunID)
		c, ok := recoveryPolicyCandidateForItem(p.Response.Policy, parent.Job, item.EventRecoveryItem, row, "", run, m.workflowRecoveryExecutionLocked(parent, item, now), operation)
		if ok {
			candidates = append(candidates, c)
		}
	}
	allowed, reason := recoveryAutomaticEligibility(action, candidates)
	if !allowed {
		if reason == "unknown" {
			chosen.Reason = "evidence_unknown"
			return nil
		}
		return suppress(reason)
	}
	child, err := m.createEventRecoveryLocked(automaticRecoveryActor(ctx, p.Response), app.AccountID, p.Response.AppID, *action.Retry)
	if automaticRecoveryDeferrable(err) {
		chosen.Reason = "execution_quota"
		return nil
	}
	if automaticRecoveryChanged(err) {
		return suppress("changed")
	}
	if err != nil {
		return fmt.Errorf("apply automatic retry: %w", err)
	}
	chosen.Status = "applied"
	chosen.Reason = "child_created"
	chosen.ChildJobID = child.ID
	chosen.AppliedAt = &now
	if p.Charges == nil {
		p.Charges = map[string]time.Time{}
	}
	p.Charges[chosen.ActionID] = now
	return nil
}
func (m *MemStore) enqueueAutomaticRecoveryPauseLocked(account, slug string, p api.WorkflowRecoveryPolicyResponse) error {
	m.recoveryPolicies[canonicalMemUUID(p.AppID)].appendHistoryEvent(recoveryAutomaticPauseHistoryEvent(p), time.Now().UTC())
	receivers := []string{}
	for _, h := range m.appWebhooks {
		if h.Scope == AppWebhookScopeApp && sameMemUUID(h.AccountID, account) && sameMemUUID(h.AppID, p.AppID) && h.Enabled && appWebhookMatches(h.EventFilter, AppWebhookEventRecoveryPolicyPaused) {
			receivers = append(receivers, h.ID)
		}
	}
	if len(receivers) == 0 {
		return nil
	}
	sort.Strings(receivers)
	payload := recoveryPolicyPausedPayload(p, slug)
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode automatic pause notification: %w", err)
	}
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
	}
	m.appWebhookEventOutbox[payload.EventID] = appWebhookOutboxEvent{ID: payload.EventID, AccountID: account, AppID: p.AppID, Event: AppWebhookEventRecoveryPolicyPaused, SourceID: payload.EventID, Payload: raw, RecipientWebhookIDs: receivers, CreatedAt: payload.PausedAt}
	return nil
}

func recoveryAutomaticPendingMem(p *memWorkflowRecoveryPolicy) (*api.WorkflowRecoveryAutomaticAction, api.WorkflowRecoveryPolicyReview) {
	var chosen *api.WorkflowRecoveryAutomaticAction
	var review api.WorkflowRecoveryPolicyReview
	for _, e := range p.Executions {
		if e.Status != "pending" {
			continue
		}
		r, ok := recoveryAutomaticMemReview(p, e.ActionID)
		if !ok || r.PolicyVersion != p.Response.Version {
			continue
		}
		if chosen == nil || r.ObservedAt.Before(review.ObservedAt) || r.ObservedAt.Equal(review.ObservedAt) && (r.ID < review.ID || r.ID == review.ID && e.Position < chosen.Position) {
			chosen = e
			review = r
		}
	}
	return chosen, review
}
