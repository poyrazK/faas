package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) GetWorkflowRecoveryHistory(ctx context.Context, account, app string, o api.WorkflowRecoveryHistoryOptions) (api.WorkflowRecoveryHistory, error) {
	return m.getWorkflowRecoveryHistory(ctx, account, app, o, 0)
}
func (m *MemStore) getWorkflowRecoveryHistory(ctx context.Context, account, app string, o api.WorkflowRecoveryHistoryOptions, scanLimit int) (api.WorkflowRecoveryHistory, error) {
	var out api.WorkflowRecoveryHistory
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	readAt := time.Now().UTC()
	c, limit, err := recoveryHistoryWindow(account, app, o, readAt)
	if err != nil {
		return out, err
	}
	if scanLimit > 0 {
		limit = scanLimit
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !m.eventRecoveryAppLocked(account, app) {
		return out, ErrNotFound
	}
	entries := []api.WorkflowRecoveryHistoryEntry{}
	add := func(e api.WorkflowRecoveryHistoryEntry) {
		if !recoveryHistoryMatches(e, o, c) {
			return
		}
		entries = append(entries, e)
		if len(entries) > limit+1 {
			sort.Slice(entries, func(i, j int) bool {
				if !entries[i].OccurredAt.Equal(entries[j].OccurredAt) {
					return entries[i].OccurredAt.After(entries[j].OccurredAt)
				}
				return entries[i].ID > entries[j].ID
			})
			entries = entries[:limit+1]
		}
	}
	p := m.recoveryPolicies[canonicalMemUUID(app)]
	if p != nil {
		if !c.HistoryFence {
			c.HistorySequence = p.HistorySequence
			c.HistoryFence = true
		}
		oldestRecordedAt := readAt.Add(-api.WorkflowRecoveryHistoryRetention)
		for _, event := range p.History {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			if event.Sequence <= c.HistorySequence && !event.RecordedAt.After(c.ObservedAt) && event.RecordedAt.After(oldestRecordedAt) {
				add(event.Entry)
			}
		}
		r := p.Response
		add(api.WorkflowRecoveryHistoryEntry{ID: "policy:" + strconv.FormatInt(r.Version, 10), OccurredAt: r.UpdatedAt, Kind: "policy_observation", Outcome: r.Status, Reason: r.Policy.Reason, PolicyVersion: r.Version, ActorKind: r.ActorKind, ActorID: r.ActorID})
		for _, control := range p.Controls {
			add(api.WorkflowRecoveryHistoryEntry{ID: "control:" + canonicalMemUUID(control.ID), OccurredAt: control.ControlledAt, Kind: "policy_control", Outcome: control.Request.Operation, Reason: control.Request.Reason, PolicyVersion: control.Request.ExpectedVersion, ActorKind: control.ActorKind, ActorID: control.ActorID})
		}
		for _, raw := range p.Reviews {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			var r api.WorkflowRecoveryPolicyReview
			if err := json.Unmarshal(raw, &r); err != nil {
				return out, fmt.Errorf("decode retained recovery review: %w", err)
			}

			for _, repairs := range []struct {
				kind     string
				receipts map[string]api.WorkflowInitialEvidenceRepairReceipt
			}{{"parent", r.ParentEvidenceRepairs}, {"child", r.EvidenceRepairs}} {
				for action, receipt := range repairs.receipts {
					execution := p.Executions[action]
					if execution != nil && !recoveryHistoryHasRepair(p, action, repairs.kind, receipt.RequestID) {
						add(retryRepairHistory(r, *execution, repairs.kind, receipt))
					}
				}
			}
			job := m.eventRecoveryJobs[canonicalMemUUID(r.JobID)]
			if job == nil || !sameMemUUID(job.Job.AppID, app) {
				continue
			}
			workflow := ""
			if job.Job.Selection.WorkflowRuns != nil {
				workflow = job.Job.Selection.WorkflowRuns.Name
			}
			for _, d := range r.Decisions {
				base := api.WorkflowRecoveryHistoryEntry{ID: r.ID + ":" + strconv.FormatInt(d.Position, 10), PolicyVersion: r.PolicyVersion, Workflow: workflow, RunID: canonicalMemUUID(d.RunID), ReviewID: r.ID, JobID: r.JobID}
				e := base
				e.ID = "review:" + base.ID
				e.OccurredAt = r.ObservedAt
				e.Kind = "review_outcome"
				e.Outcome = d.Outcome
				e.Reason = d.Trigger
				add(e)
				if resolution := r.ReviewDecision; resolution != nil {
					e = base
					e.ID = "resolution:" + base.ID
					e.OccurredAt = resolution.DecidedAt
					e.Kind = "review_resolution"
					e.Outcome = resolution.Decision
					e.Reason = resolution.Reason
					e.ActorKind = resolution.ActorKind
					e.ActorID = resolution.ActorID
					add(e)
				}
				if r.Plan == nil {
					continue
				}
				for _, action := range r.Plan.Actions {
					if action.Position != d.Position {
						continue
					}
					id := api.WorkflowRecoveryResponseActionID(*r.Plan, action)
					execution := p.Executions[id]
					if execution != nil {
						recoveryHistoryExecution(add, base, *execution)
					}
				}
			}
		}
		for _, r := range m.initialRecoveryReviews {
			if !sameMemUUID(r.AppID, app) || r.EvidenceRepair == nil {
				continue
			}
			recorded := false
			for _, event := range p.History {
				if event.Entry.Kind == "initial_evidence_repaired" && event.Entry.ReviewID == r.ID && event.Entry.RequestID == r.EvidenceRepair.RequestID {
					recorded = true
					break
				}
			}
			if !recorded {
				add(initialRepairHistory(r))
			}
		}
		if i := p.HealthIncident; i != nil {
			add(api.WorkflowRecoveryHistoryEntry{ID: "health:" + i.EpisodeID, OccurredAt: i.ObservedAt, Kind: "health_observation", Outcome: i.Status, Reason: i.Reason})
			for _, n := range []struct {
				kind    string
				receipt *api.WorkflowRecoveryHealthNotification
			}{{"health_alert", i.AlertNotification}, {"health_recovered", i.RecoveryNotification}} {
				if n.receipt != nil {
					add(api.WorkflowRecoveryHistoryEntry{ID: n.kind + ":" + n.receipt.EventID, OccurredAt: n.receipt.CapturedAt, Kind: n.kind, Outcome: n.receipt.Status})
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return recoveryPolicyClone(recoveryHistoryPage(c, limit, entries)), nil
}
func recoveryHistoryExecution(add func(api.WorkflowRecoveryHistoryEntry), base api.WorkflowRecoveryHistoryEntry, a api.WorkflowRecoveryAutomaticAction) {
	base.ActionID = a.ActionID
	base.ChildJobID = a.ChildJobID
	emit := func(kind string, at time.Time, outcome, reason, actorKind, actorID string) {
		e := base
		e.ID = kind + ":" + a.ActionID
		e.OccurredAt = at
		e.Kind = kind
		e.Outcome = outcome
		e.Reason = reason
		e.ActorKind = actorKind
		e.ActorID = actorID
		add(e)
	}
	if a.AppliedAt != nil {
		emit("retry_applied", *a.AppliedAt, "applied", "", "", "")
	}
	if s := a.Suppression; s != nil {
		emit("retry_suppressed", s.SuppressedAt, "suppressed", s.Reason, s.ActorKind, s.ActorID)
	}
	if e := a.LastEvaluation; e != nil {
		emit("retry_evaluation", e.EvaluatedAt, a.Status, e.Reason, "", "")
	}
	if r := a.Result; r != nil && r.RecordedAt != nil {
		emit("retry_result", *r.RecordedAt, r.Outcome, r.OutcomeReason, "", "")
	}
	if n := a.BlockedNotification; n != nil {
		emit("retry_blocked_notification", n.CapturedAt, n.Status, n.Reason, "", "")
	}
}
