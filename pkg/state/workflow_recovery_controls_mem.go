package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
	"time"
)

type memRecoveryPolicyControl struct {
	ID           string
	Request      api.ControlWorkflowRecoveryPolicyRequest
	ActorKind    string
	ActorID      string
	ControlledAt time.Time
}

func (m *MemStore) ControlWorkflowRecoveryPolicy(ctx context.Context, account, app string, r api.ControlWorkflowRecoveryPolicyRequest) (api.WorkflowRecoveryPolicyResponse, error) {
	if err := eventRecoveryIDs(account, app); err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, err
	}
	if err := r.Validate(); err != nil {
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
	p := m.recoveryPolicies[canonicalMemUUID(app)]
	if p == nil || !recoveryControlMatches(p.Response, r) {
		return api.WorkflowRecoveryPolicyResponse{}, ErrWorkflowRecoveryPolicyConflict
	}
	if !p.Response.Policy.Enabled || !api.WorkflowRecoveryPolicyAutomaticMode(p.Response.Policy.Mode) {
		return api.WorkflowRecoveryPolicyResponse{}, ErrEventRecoveryQuery
	}
	now := time.Now().UTC()
	actor := recoveryAuditActor(ctx, "policy_"+r.Operation)
	p.Controls = append(p.Controls, memRecoveryPolicyControl{ID: newID(), Request: recoveryPolicyClone(r), ActorKind: actor.Kind, ActorID: actor.ID, ControlledAt: now})
	p.Response.UpdatedAt = now
	p.Response.ActorKind = actor.Kind
	p.Response.ActorID = actor.ID
	a := p.Response.AutomaticExecution
	if r.Operation == "pause" {
		a.PausedAt = &now
		a.PauseReason = "operator: " + r.Reason
		p.Response.Status = "paused"
	} else {
		a.PausedAt = nil
		a.PauseReason = ""
		a.ConsecutiveFailures = 0
		a.PollingFailure = nil
		p.Response.Status = "monitoring"
	}
	p.NextExecute = now
	p.Next = now
	return recoveryPolicyClone(p.Response), nil
}
func (m *MemStore) GetWorkflowRecoveryAutomaticQueue(ctx context.Context, account, app string) (api.WorkflowRecoveryAutomaticQueue, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	out := api.WorkflowRecoveryAutomaticQueue{ObservedAt: time.Now().UTC(), Entries: []api.WorkflowRecoveryAutomaticQueueEntry{}}
	if err := eventRecoveryIDs(account, app); err != nil {
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
	queue, _ := m.workflowRecoveryAutomaticQueueLocked(app, out.ObservedAt, false)
	if err := m.recoveryQueueEvidenceLocked(ctx, account, app, &queue); err != nil {
		return out, err
	}
	return recoveryPolicyClone(queue), nil
}
func (m *MemStore) workflowRecoveryAutomaticQueueLocked(app string, now time.Time, allPending bool) (api.WorkflowRecoveryAutomaticQueue, *time.Time) {
	out := api.WorkflowRecoveryAutomaticQueue{ObservedAt: now, Entries: []api.WorkflowRecoveryAutomaticQueueEntry{}}
	p := m.recoveryPolicies[canonicalMemUUID(app)]
	if p == nil {
		out.Policy = defaultWorkflowRecoveryPolicy(canonicalMemUUID(app))
		return out, nil
	}
	out.Policy = recoveryPolicyClone(p.Response)
	out.HealthIncident = recoveryPolicyClone(p.HealthIncident)
	var last *time.Time
	charges := m.initialAutomaticChargesLocked(app)
	for _, at := range p.Charges {
		charges = append(charges, at)
	}
	for _, at := range charges {
		if last == nil || at.After(*last) {
			v := at
			last = &v
		}
		if at.After(out.ObservedAt.Add(-time.Hour)) {
			out.HourlyExecutions++
		}
	}
	observed := map[string]time.Time{}
	for id, e := range p.Executions {
		r, ok := recoveryAutomaticMemReview(p, id)
		if !ok {
			continue
		}
		if e.Status == "pending" && (allPending || r.PolicyVersion == p.Response.Version) {
			out.Pending++
		} else if e.Status == "applied" && e.Result == nil {
			out.Outstanding++
		} else {
			continue
		}
		out.Entries = append(out.Entries, api.WorkflowRecoveryAutomaticQueueEntry{QueuedAt: r.ObservedAt, ReviewID: r.ID, PolicyVersion: r.PolicyVersion, Action: recoveryPolicyClone(*e)})
		observed[r.ID] = r.ObservedAt
	}
	for _, r := range m.initialRecoveryReviews {
		if !sameMemUUID(r.AppID, app) || r.Policy.Mode != "automatic_initial" {
			continue
		}
		if r.Status == "prepared" && (allPending || r.PolicyVersion == p.Response.Version) {
			out.Pending++
		} else if initialReviewUnresolved(r) {
			out.Outstanding++
		} else {
			continue
		}
		out.Entries = append(out.Entries, initialAutomaticQueueEntry(r))
		observed[r.ID] = r.PreparedAt
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if !observed[a.ReviewID].Equal(observed[b.ReviewID]) {
			return observed[a.ReviewID].Before(observed[b.ReviewID])
		}
		if a.ReviewID != b.ReviewID {
			return a.ReviewID < b.ReviewID
		}
		return a.Action.Position < b.Action.Position
	})
	if len(out.Entries) > api.EventRecoveryHistoryPageMax {
		out.Entries = out.Entries[:api.EventRecoveryHistoryPageMax]
	}
	recoveryQueueFinish(&out, last)
	return out, last
}
