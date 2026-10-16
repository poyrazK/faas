package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type WorkflowRecoveryAutomaticPolicyStore interface {
	ProcessNextWorkflowRecoveryAutomaticPolicy(context.Context, time.Time) (bool, error)
}

func recoveryPolicyPauseID(app string, version int64) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("gregale:recovery-policy:%s:%d:paused", app, version))).String()
}
func recoveryPolicyPausedPayload(p api.WorkflowRecoveryPolicyResponse, slug string) api.WorkflowRecoveryPolicyPausedWebhookPayload {
	a := p.AutomaticExecution
	eventID := recoveryPolicyPauseID(p.AppID, p.Version)
	if p.Policy.Mode == "automatic_initial" {
		eventID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(eventID+":"+a.PausedAt.UTC().Format(time.RFC3339Nano))).String()
	}
	return api.WorkflowRecoveryPolicyPausedWebhookPayload{EventID: eventID, AppID: p.AppID, App: slug, PolicyVersion: p.Version, PausedAt: *a.PausedAt, ConsecutiveFailures: a.ConsecutiveFailures, Reason: a.PauseReason, PolicyURL: "/v1/apps/" + url.PathEscape(slug) + "/recovery-policy"}
}
func recoveryAutomaticAction(r api.WorkflowRecoveryPolicyReview, e api.WorkflowRecoveryAutomaticAction, p api.WorkflowRecoveryPolicyResponse) (api.WorkflowRecoveryResponseAction, error) {
	if r.Plan == nil || r.PolicyVersion != p.Version || r.Policy.Mode != "automatic_retry" || !reflect.DeepEqual(r.Policy, p.Policy) || r.Plan.AppID != p.AppID || r.Plan.JobID != r.JobID {
		return api.WorkflowRecoveryResponseAction{}, fmt.Errorf("%w: review does not match current policy", errAutomaticPollingData)
	}
	for _, a := range r.Plan.Actions {
		if a.Position != e.Position || api.WorkflowRecoveryResponseActionID(*r.Plan, a) != e.ActionID {
			continue
		}
		if a.Kind != "retry" || a.Retry == nil || a.Cancellation != nil || a.Retry.RequestID != e.ActionID || a.Retry.ParentJobID != r.JobID || a.Retry.WorkflowRuns == nil || len(a.Retry.WorkflowRuns.Runs) != 1 || !recoveryPolicyHas(p.Policy.WorkflowNames, a.Retry.WorkflowRuns.Name) {
			break
		}
		target := a.Retry.WorkflowRuns.Runs[0]
		if target.RunID != a.RunID || target.ParentPosition != a.Position || target.ExpectedResumeCount >= p.Policy.MaxResumeCount {
			break
		}
		for _, id := range []string{r.ID, r.AppID, r.JobID, a.RunID, e.ActionID} {
			v, err := uuid.Parse(id)
			if err != nil || v == uuid.Nil {
				return api.WorkflowRecoveryResponseAction{}, errManualObservationIdentity
			}
		}
		return a, nil
	}
	return api.WorkflowRecoveryResponseAction{}, fmt.Errorf("%w: invalid retry action", errAutomaticPollingData)
}
func recoveryAutomaticEligibility(a api.WorkflowRecoveryResponseAction, candidates []recoveryPolicyCandidate) (bool, string) {
	for _, c := range candidates {
		if c.Decision.Position != a.Position {
			continue
		}
		if c.Decision.RunID != a.RunID || c.Decision.ResumeCount != a.Retry.WorkflowRuns.Runs[0].ExpectedResumeCount || c.Decision.Action != "retry" {
			return false, "changed"
		}
		return c.Decision.Outcome == "eligible", c.Decision.Outcome
	}
	return false, "target_unavailable"
}
func recoveryAutomaticEvaluationResult(ctx context.Context, s WorkflowRecoveryReviewInspector, account, app, review string, e api.WorkflowRecoveryAutomaticAction, now time.Time) (*api.WorkflowRecoveryReviewActionStatus, string, error) {
	status, err := InspectWorkflowRecoveryReview(ctx, s, account, app, review, now)
	if err != nil {
		return nil, "inspection_error", fmt.Errorf("inspect automatic retry result: %w", err)
	}
	for _, a := range status.Actions {
		if a.Position == e.Position && a.Kind == "retry" && a.ChildJobID == e.ChildJobID {
			if a.Complete {
				return &a, "result_recorded", nil
			}
			reason := "evidence_unknown"
			if a.Outcome == "running" {
				reason = "retry_running"
			}
			if a.Outcome == "pending" {
				reason = "execution_wait"
			}
			if a.OutcomeReason == "admission_pending" {
				reason = "admission_wait"
			}
			return nil, reason, nil
		}
	}
	return nil, "evidence_unknown", fmt.Errorf("%w: retry execution missing from retained review", errAutomaticPollingData)
}
func recoveryAutomaticRecordFailure(p *api.WorkflowRecoveryPolicyResponse, result api.WorkflowRecoveryReviewActionStatus, now time.Time) bool {
	state := p.AutomaticExecution
	if result.Outcome == "failed" {
		state.ConsecutiveFailures++
	} else {
		state.ConsecutiveFailures = 0
	}
	if state.ConsecutiveFailures >= p.Policy.PauseAfterFailures {
		state.PausedAt = &now
		state.PauseReason = "consecutive_failures"
		p.Status = "paused"
		return true
	}
	return false
}
func automaticRecoveryDeferrable(err error) bool { return errors.Is(err, ErrEventRecoveryQuota) }
func automaticRecoveryChanged(err error) bool {
	return errors.Is(err, ErrEventRecoveryChanged) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrEventRecoveryRetryParent)
}
func automaticRecoveryActor(ctx context.Context, p api.WorkflowRecoveryPolicyResponse) context.Context {
	return WithEventRecoveryActor(ctx, "system", fmt.Sprintf("recovery_policy:%s:%d", p.AppID, p.Version))
}
func automaticRecoveryRawResult(result *api.WorkflowRecoveryReviewActionStatus) ([]byte, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode automatic retry result: %w", err)
	}
	return raw, nil
}
