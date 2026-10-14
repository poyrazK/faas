package state

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
	"time"
)

type memWorkflowRecoveryPolicy struct {
	History              []memRecoveryHistoryEvent
	HistorySequence      int64
	HealthIncident       *api.WorkflowRecoveryHealthIncident
	NextHealth           time.Time
	BlockedNext          map[string]time.Time
	Controls             []memRecoveryPolicyControl
	Charges              map[string]time.Time
	NextExecute          time.Time
	Executions           map[string]*api.WorkflowRecoveryAutomaticAction
	Response             api.WorkflowRecoveryPolicyResponse
	Next                 time.Time
	Cursor               string
	InitialFailureCursor string
	Reviews              map[string][]byte
	Notifications        map[string]*memRecoveryReviewNotification
}

func (m *MemStore) GetWorkflowRecoveryPolicy(ctx context.Context, account, app string) (api.WorkflowRecoveryPolicyResponse, error) {
	if err := eventRecoveryIDs(account, app); err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, err
	}
	if !m.eventRecoveryAppLocked(account, app) {
		return api.WorkflowRecoveryPolicyResponse{}, ErrNotFound
	}
	p := m.recoveryPolicies[canonicalMemUUID(app)]
	if p == nil {
		return defaultWorkflowRecoveryPolicy(canonicalMemUUID(app)), nil
	}
	return recoveryPolicyClone(p.Response), nil
}
func (m *MemStore) SetWorkflowRecoveryPolicy(ctx context.Context, account, app string, req api.SetWorkflowRecoveryPolicyRequest) (api.WorkflowRecoveryPolicyResponse, error) {
	if err := eventRecoveryIDs(account, app); err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, err
	}
	if err := req.Validate(); err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, ErrEventRecoveryQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, err
	}
	if !m.eventRecoveryAppLocked(account, app) {
		return api.WorkflowRecoveryPolicyResponse{}, ErrNotFound
	}
	app = canonicalMemUUID(app)
	p := m.recoveryPolicies[app]
	version := int64(0)
	if p != nil {
		version = p.Response.Version
	}
	if version != req.ExpectedVersion {
		return api.WorkflowRecoveryPolicyResponse{}, ErrWorkflowRecoveryPolicyConflict
	}
	if p == nil {
		p = &memWorkflowRecoveryPolicy{Reviews: map[string][]byte{}}
	}
	now := time.Now().UTC()
	actor := recoveryAuditActor(ctx, "policy_updated")
	lastEvaluation := p.Response.LastEvaluationAt
	notification := p.Response.PollingNotification
	p.Response = api.WorkflowRecoveryPolicyResponse{AppID: app, Version: version + 1, Policy: recoveryPolicyClone(req.Policy), UpdatedAt: now, ActorKind: actor.Kind, ActorID: actor.ID, Status: "disabled", LastEvaluationAt: lastEvaluation, PollingNotification: notification}
	if req.Policy.Enabled {
		p.Response.Status = "monitoring"
	}
	if api.WorkflowRecoveryPolicyAutomaticMode(req.Policy.Mode) {
		p.Response.AutomaticExecution = &api.WorkflowRecoveryPolicyAutomaticExecution{}
	}
	p.NextExecute = now
	p.NextHealth = now
	for _, e := range p.Executions {
		if e.Status == "pending" {
			e.Status = "suppressed"
			e.Reason = "policy_changed"
		}
	}
	p.Next = now
	p.Cursor = ""
	p.InitialFailureCursor = ""
	if m.recoveryPolicies == nil {
		m.recoveryPolicies = map[string]*memWorkflowRecoveryPolicy{}
	}
	p.appendHistoryEvent(recoveryPolicyHistoryEvent(p.Response), now)
	m.recoveryPolicies[app] = p
	return recoveryPolicyClone(p.Response), nil
}
func (m *MemStore) ListWorkflowRecoveryPolicyReviews(ctx context.Context, account, app, after string, limit int) (api.WorkflowRecoveryPolicyReviews, error) {
	out := api.WorkflowRecoveryPolicyReviews{Reviews: []api.WorkflowRecoveryPolicyReview{}}
	if err := validRecoveryPolicyReviewQuery(account, app, after, limit); err != nil {
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
		return out, nil
	}
	ids := []string{}
	for id, raw := range p.Reviews {
		var review api.WorkflowRecoveryPolicyReview
		_ = json.Unmarshal(raw, &review)
		if m.eventRecoveryJobs[review.JobID] != nil && id > canonicalMemUUID(after) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for i, id := range ids {
		if i == limit {
			out.NextAfter = ids[i-1]
			break
		}
		var review api.WorkflowRecoveryPolicyReview
		_ = json.Unmarshal(p.Reviews[id], &review)
		m.decorateRecoveryReviewNotificationLocked(p, &review)
		out.Reviews = append(out.Reviews, review)
	}
	return out, nil
}
func (m *MemStore) GetWorkflowRecoveryPolicyReview(ctx context.Context, account, app, id string) (api.WorkflowRecoveryPolicyReview, error) {
	var out api.WorkflowRecoveryPolicyReview
	if err := validRecoveryPolicyReviewQuery(account, app, id, 1); err != nil {
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
	raw, ok := p.Reviews[canonicalMemUUID(id)]
	if !ok {
		return out, ErrNotFound
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	if m.eventRecoveryJobs[out.JobID] == nil {
		return api.WorkflowRecoveryPolicyReview{}, ErrNotFound
	}
	m.decorateRecoveryReviewNotificationLocked(p, &out)
	return out, nil
}
func (m *MemStore) ProcessNextWorkflowRecoveryPolicy(ctx context.Context, now time.Time) (worked bool, retErr error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	m.mu.Lock()
	var failures []string
	var account, appID string
	var version int64
	defer func() {
		m.mu.Unlock()
		if retErr == nil && worked {
			retErr = prepareInitialPolicyReviews(ctx, m, account, appID, version, failures)
		}
	}()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	policies := []*memWorkflowRecoveryPolicy{}
	for app, p := range m.recoveryPolicies {
		a, ok := m.eventSubscriptionAppLocked(app)
		if ok && a.Status != AppDeleted && p.Response.Policy.Enabled && (p.Response.AutomaticExecution == nil || p.Response.AutomaticExecution.PausedAt == nil) && !p.Next.After(now) {
			policies = append(policies, p)
		}
	}
	sort.Slice(policies, func(i, j int) bool {
		if policies[i].Next.Equal(policies[j].Next) {
			return policies[i].Response.AppID < policies[j].Response.AppID
		}
		return policies[i].Next.Before(policies[j].Next)
	})
	if len(policies) == 0 {
		return false, nil
	}
	p := policies[0]
	app, _ := m.eventSubscriptionAppLocked(p.Response.AppID)
	budget := recoveryPolicyBudget{Existing: map[string]bool{}}
	for id, raw := range p.Reviews {
		var review api.WorkflowRecoveryPolicyReview
		_ = json.Unmarshal(raw, &review)
		if m.eventRecoveryJobs[review.JobID] == nil {
			forgetRecoveryPolicyReview(p, id)
			continue
		}
		if review.Plan == nil {
			continue
		}
		if review.ObservedAt.After(budget.Last) {
			budget.Last = review.ObservedAt
		}
		for _, d := range review.Decisions {
			if d.Outcome == "prepared" {
				budget.Existing[recoveryPolicyTargetKey(d.RunID, d.ResumeCount, d.Action)] = true
				if review.ObservedAt.After(now.Add(-time.Hour)) {
					budget.Hourly++
				}
			}
		}
	}
	for _, r := range m.initialRecoveryReviews {
		if !sameMemUUID(r.AppID, app.ID) {
			continue
		}
		if r.PreparedAt.After(budget.Last) {
			budget.Last = r.PreparedAt
		}
		if r.PreparedAt.After(now.Add(-time.Hour)) {
			budget.Hourly++
		}
	}
	jobs := []*memEventRecoveryJob{}
	for _, job := range m.eventRecoveryJobs {
		if sameMemUUID(job.Job.AppID, app.ID) && job.Job.Selection.WorkflowRuns != nil && job.Job.ID > p.Cursor {
			jobs = append(jobs, job)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Job.ID < jobs[j].Job.ID })
	if len(jobs) > api.WorkflowRecoveryPolicyJobsPerPoll {
		jobs = jobs[:api.WorkflowRecoveryPolicyJobsPerPoll]
	}
	// Stage reviews before mutation so failed diagnostics never leave partial drafts.
	staged := map[string][]byte{}
	cursor := ""
	status := "monitoring"
	for _, job := range jobs {
		cursor = job.Job.ID
		if !recoveryPolicyHas(p.Response.Policy.WorkflowNames, job.Job.Selection.WorkflowRuns.Name) {
			continue
		}
		attention, err := m.workflowRecoveryAttentionLocked(ctx, job, now)
		if err != nil {
			return false, err
		}
		attentionByPosition := map[int64]string{}
		for _, a := range attention.Targets {
			attentionByPosition[a.Position] = a.Status
		}
		candidates := []recoveryPolicyCandidate{}
		for _, item := range job.Items {
			row := api.EventRecoveryPreflightItem{}
			if workflowRecoveryRetryParentTerminal(job.Job.State) {
				row, err = m.workflowRecoveryPreflightItemLocked(ctx, job, item, now, true)
				if err != nil {
					return false, err
				}
			}
			var run *WorkflowRun
			if r, ok := m.workflowRuns[item.WorkflowRunID]; ok {
				run = &r
			}
			_, operation := m.operationForWorkflowLocked(item.WorkflowRunID)
			c, ok := recoveryPolicyCandidateForItem(p.Response.Policy, job.Job, item.EventRecoveryItem, row, attentionByPosition[item.Position], run, m.workflowRecoveryExecutionLocked(job, item, now), operation)
			if ok {
				candidates = append(candidates, c)
			}
		}
		if len(candidates) == 0 {
			continue
		}
		review, _ := buildRecoveryPolicyReview(p.Response, app.Slug, job.Job, candidates, budget, now)
		if _, ok := p.Reviews[review.ID]; ok {
			continue
		}
		if _, ok := staged[review.ID]; ok {
			continue
		}
		if len(p.Reviews)+len(staged) >= api.WorkflowRecoveryPolicyReviewsMax {
			status = "review_limit"
			break
		}
		encoded, err := json.Marshal(review)
		if err != nil {
			return false, err
		}
		staged[review.ID] = encoded
		if review.Plan != nil {
			budget.Last = now
			for _, d := range review.Decisions {
				if d.Outcome == "prepared" {
					budget.Hourly++
					budget.Existing[recoveryPolicyTargetKey(d.RunID, d.ResumeCount, d.Action)] = true
				}
			}
		}
	}
	for id, raw := range staged {
		p.Reviews[id] = raw
		var r api.WorkflowRecoveryPolicyReview
		_ = json.Unmarshal(raw, &r)
		workflow := ""
		if job := m.eventRecoveryJobs[canonicalMemUUID(r.JobID)]; job != nil && job.Job.Selection.WorkflowRuns != nil {
			workflow = job.Job.Selection.WorkflowRuns.Name
		}
		p.appendHistoryEvent(recoveryCandidateHistoryEvent(r, workflow), time.Now().UTC())
		if r.Policy.Mode == "automatic_retry" && r.Plan != nil {
			if p.Executions == nil {
				p.Executions = map[string]*api.WorkflowRecoveryAutomaticAction{}
			}
			for _, a := range r.Plan.Actions {
				if a.Kind != "retry" {
					continue
				}
				id := api.WorkflowRecoveryResponseActionID(*r.Plan, a)
				p.Executions[id] = &api.WorkflowRecoveryAutomaticAction{ActionID: id, Position: a.Position, Status: "pending", Reason: "awaiting_execution"}
			}
		}
	}
	if len(jobs) < api.WorkflowRecoveryPolicyJobsPerPoll {
		cursor = ""
	}
	p.Cursor = cursor
	p.Next = now.Add(api.WorkflowRecoveryPolicyPollInterval)
	p.Response.LastEvaluationAt = &now
	p.Response.Status = status
	account, appID, version = app.AccountID, app.ID, p.Response.Version
	if initialPolicyPreparationEnabled(p.Response.Policy) {
		for _, e := range m.workflowFailureEvidence[canonicalMemUUID(app.ID)] {
			if e.ResumeCount == 0 && e.ID > p.InitialFailureCursor && !e.ObservedAt.After(now) && e.ObservedAt.After(now.Add(-api.WorkflowRecoveryFailureRetention)) && recoveryPolicyHas(p.Response.Policy.WorkflowNames, e.Workflow) && recoveryPolicyHas(p.Response.Policy.FailureStatuses, e.FailureStatus) {
				if _, exists := m.initialRecoveryReviews[initialReviewID(app.ID, e.ID, p.Response.Version)]; !exists {
					failures = append(failures, e.ID)
				}
			}
		}
		sort.Strings(failures)
		if len(failures) > api.WorkflowRecoveryPolicyJobsPerPoll {
			failures = failures[:api.WorkflowRecoveryPolicyJobsPerPoll]
		}
		p.InitialFailureCursor = initialPolicyNextCursor(failures)
	}
	return true, nil
}
