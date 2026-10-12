package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

var ErrWorkflowRecoveryPolicyConflict = errors.New("recovery policy version changed; reload before updating")

type WorkflowRecoveryPolicyStore interface {
	DecideWorkflowRecoveryPolicyReview(context.Context, string, string, string, api.WorkflowRecoveryReviewDecisionRequest) (api.WorkflowRecoveryPolicyReview, error)
	GetWorkflowRecoveryPolicy(context.Context, string, string) (api.WorkflowRecoveryPolicyResponse, error)
	SetWorkflowRecoveryPolicy(context.Context, string, string, api.SetWorkflowRecoveryPolicyRequest) (api.WorkflowRecoveryPolicyResponse, error)
	ListWorkflowRecoveryPolicyReviews(context.Context, string, string, string, int) (api.WorkflowRecoveryPolicyReviews, error)
	GetWorkflowRecoveryPolicyReview(context.Context, string, string, string) (api.WorkflowRecoveryPolicyReview, error)
	ProcessNextWorkflowRecoveryPolicy(context.Context, time.Time) (bool, error)
}
type recoveryPolicyCandidate struct {
	Decision api.WorkflowRecoveryPolicyDecision
	Action   api.WorkflowRecoveryResponseAction
}
type recoveryPolicyBudget struct {
	Hourly   int
	Last     time.Time
	Existing map[string]bool
}

func recoveryPolicyTargetKey(run string, count int, kind string) string {
	return fmt.Sprintf("%s:%d:%s", run, count, kind)
}
func recoveryPolicyHas(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
func defaultWorkflowRecoveryPolicy(app string) api.WorkflowRecoveryPolicyResponse {
	return api.WorkflowRecoveryPolicyResponse{AppID: app, Policy: api.WorkflowRecoveryPolicy{Mode: "preview", WorkflowNames: []string{}, Triggers: []string{"saved_failure"}, FailureStatuses: []string{}, MaxResumeCount: 3, CooldownSeconds: 300, HourlyActionBudget: 10, RatePerSecond: 1, Reason: "policy-reviewed recovery"}, Status: "disabled"}
}
func buildRecoveryPolicyReview(p api.WorkflowRecoveryPolicyResponse, slug string, job api.EventRecoveryJob, candidates []recoveryPolicyCandidate, b recoveryPolicyBudget, now time.Time) (api.WorkflowRecoveryPolicyReview, string) {
	out := api.WorkflowRecoveryPolicyReview{AppID: p.AppID, JobID: job.ID, PolicyVersion: p.Version, Policy: p.Policy, ObservedAt: now, Decisions: []api.WorkflowRecoveryPolicyDecision{}}
	actions := []api.WorkflowRecoveryResponseAction{}
	for _, c := range candidates {
		d := c.Decision
		if d.Outcome == "eligible" {
			switch {
			case b.Existing[recoveryPolicyTargetKey(d.RunID, d.ResumeCount, d.Action)]:
				d.Outcome = "already_prepared"
			case d.Action == "retry" && d.ResumeCount >= p.Policy.MaxResumeCount:
				d.Outcome = "generation_limit"
			case !b.Last.IsZero() && b.Last.Add(time.Duration(p.Policy.CooldownSeconds)*time.Second).After(now):
				d.Outcome = "cooldown"
			case b.Hourly+len(actions) >= p.Policy.HourlyActionBudget:
				d.Outcome = "hourly_budget"
			default:
				d.Outcome = "prepared"
				actions = append(actions, c.Action)
			}
		}
		out.Decisions = append(out.Decisions, d)
	}
	encoded, _ := json.Marshal(struct {
		Version   int64
		Decisions []api.WorkflowRecoveryPolicyDecision
	}{p.Version, out.Decisions})
	hash := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(hash[:])
	out.ID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("recovery-policy:"+p.AppID+":"+job.ID+":"+fingerprint)).String()
	if len(actions) > 0 {
		plan := api.WorkflowRecoveryResponsePlan{Version: 1, App: slug, AppID: p.AppID, JobID: job.ID, Key: "policy-" + out.ID, CreatedAt: now, Actions: actions}
		for i := range plan.Actions {
			a := &plan.Actions[i]
			id := api.WorkflowRecoveryResponseActionID(plan, *a)
			if a.Cancellation != nil {
				a.Cancellation.RequestID = id
			} else {
				a.Retry.RequestID = id
			}
		}
		out.Plan = &plan
	}
	return out, fingerprint
}
func recoveryPolicyCandidateForItem(p api.WorkflowRecoveryPolicy, job api.EventRecoveryJob, item api.EventRecoveryItem, row api.EventRecoveryPreflightItem, attention string, run *WorkflowRun, execution api.EventRecoveryExecution, operation bool) (recoveryPolicyCandidate, bool) {
	var c recoveryPolicyCandidate
	if item.ExpectedResumeCount == nil || item.WorkflowRunID == "" {
		return c, false
	}
	count := *item.ExpectedResumeCount + 1
	kind, trigger, outcome := "", "", ""
	if recoveryPolicyHas(p.Triggers, "saved_failure") && row.SavedOutcome == "failed" && (len(p.FailureStatuses) == 0 || run != nil && recoveryPolicyHas(p.FailureStatuses, run.Status)) {
		kind, trigger, outcome = "retry", "saved_failure", row.Reason
		if row.Reason == "capacity" {
			outcome = "eligible"
		}
		target := api.WorkflowRunRecoveryTarget{RunID: item.WorkflowRunID, ExpectedResumeCount: count, ParentPosition: item.Position}
		req := api.EventRecoveryRequest{Mode: "workflow", ParentJobID: job.ID, RatePerSecond: p.RatePerSecond, Reason: p.Reason, WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: job.Selection.WorkflowRuns.Name, Runs: []api.WorkflowRunRecoveryTarget{target}}}
		c.Action = api.WorkflowRecoveryResponseAction{Kind: kind, Position: item.Position, RunID: item.WorkflowRunID, Retry: &req}
	} else if recoveryPolicyHas(p.Triggers, attention) && (attention == "stalled" || attention == "deadline_exceeded") {
		kind, trigger = "cancel", attention
		req := api.WorkflowRecoveryCancellationRequest{Position: item.Position, ExpectedResumeCount: &count, Reason: p.Reason}
		outcome = recoveryCancellationClassify(job, item, run, execution, operation, req, true).Outcome
		c.Action = api.WorkflowRecoveryResponseAction{Kind: kind, Position: item.Position, RunID: item.WorkflowRunID, Cancellation: &req}
	} else {
		return c, false
	}
	c.Decision = api.WorkflowRecoveryPolicyDecision{Position: item.Position, RunID: item.WorkflowRunID, ResumeCount: count, Action: kind, Trigger: trigger, Outcome: outcome}
	c.Decision.PreflightOutcome = outcome
	c.Decision.PreflightBlockers = append([]string(nil), row.Blockers...)
	if run != nil {
		c.Decision.FailureStatus = run.Status
	}
	return c, true
}
func validRecoveryPolicyReviewQuery(account, app, after string, limit int) error {
	if err := eventRecoveryIDs(account, app); err != nil {
		return err
	}
	if after != "" {
		if err := eventRecoveryIDs(account, after); err != nil {
			return err
		}
	}
	if limit < 1 || limit > api.EventRecoveryHistoryPageMax {
		return ErrEventRecoveryQuery
	}
	return nil
}
func recoveryPolicyClone[T any](v T) T {
	data, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(data, &out)
	return out
}
