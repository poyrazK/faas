package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrWorkflowInitialReviewBlocked = errors.New("initial recovery review is blocked")

type WorkflowInitialRecoveryReviewStore interface {
	ListWorkflowInitialRecoveryReviews(context.Context, string, string, string, int) (api.WorkflowInitialRecoveryReviews, error)
	PrepareWorkflowInitialRecoveryReview(context.Context, string, string, string, api.WorkflowInitialRecoveryReviewRequest) (api.WorkflowInitialRecoveryReview, error)
	GetWorkflowInitialRecoveryReview(context.Context, string, string, string) (api.WorkflowInitialRecoveryReview, error)
	DecideWorkflowInitialRecoveryReview(context.Context, string, string, string, api.WorkflowInitialRecoveryReviewDecisionRequest) (api.WorkflowInitialRecoveryReview, error)
}

func initialReviewID(app, failure string, version int64) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("initial-recovery:%s:%s:%d", canonicalMemUUID(app), canonicalMemUUID(failure), version))).String()
}
func initialReviewTransition(ctx context.Context, r *api.WorkflowInitialRecoveryReview, status, reason string, now time.Time) {
	r.Status, r.Reason, r.ObservedAt = status, reason, now
	actor := recoveryAuditActor(ctx, status)
	r.Events = append(r.Events, api.WorkflowInitialRecoveryReviewEvent{Status: status, Reason: reason, OccurredAt: now, ActorKind: actor.Kind, ActorID: actor.ID})
}
func newInitialReview(ctx context.Context, app string, p api.WorkflowRecoveryPolicyResponse, preview api.WorkflowInitialRecoveryPreview, now time.Time) api.WorkflowInitialRecoveryReview {
	r := api.WorkflowInitialRecoveryReview{ID: initialReviewID(p.AppID, preview.Failure.ID, p.Version), AppID: p.AppID, App: app, Failure: preview.Failure, Target: *preview.Target, PolicyVersion: p.Version, Policy: recoveryPolicyClone(p.Policy), PreparedAt: now, ExpiresAt: now.Add(api.WorkflowInitialRecoveryReviewLifetime), Events: []api.WorkflowInitialRecoveryReviewEvent{}}
	initialReviewTransition(ctx, &r, "prepared", p.Policy.Reason, now)
	return r
}
func initialReviewFreshness(r api.WorkflowInitialRecoveryReview, p api.WorkflowRecoveryPolicyResponse, now time.Time) string {
	if !r.ExpiresAt.After(now) {
		return "expired"
	}
	if p.Version != r.PolicyVersion {
		return "policy_changed"
	}
	if !p.Policy.Enabled {
		return "policy_disabled"
	}
	return ""
}
func initialReviewObserved(r api.WorkflowInitialRecoveryReview, p api.WorkflowRecoveryPolicyResponse, preview api.WorkflowInitialRecoveryPreview, err error, now time.Time) (api.WorkflowInitialRecoveryReview, error) {
	r = recoveryPolicyClone(r)
	r.ObservedAt = now
	if r.Status != "prepared" {
		return r, nil
	}
	reason := initialReviewFreshness(r, p, now)
	if reason == "" {
		if errors.Is(err, ErrNotFound) {
			reason = "failure_evidence_unavailable"
		} else if err != nil {
			return r, err
		} else {
			reason = api.WorkflowInitialFailurePolicyReason(p.Policy, preview)
		}
	}
	switch reason {
	case "eligible", "capacity":
		r.Reason = reason
	default:
		r.Status = "stale"
		if reason == "expired" {
			r.Status = "expired"
		}
		r.Reason = reason
	}
	return r, nil
}
func initialReviewJobRequest(r api.WorkflowInitialRecoveryReview, reason string) api.EventRecoveryRequest {
	return api.EventRecoveryRequest{RequestID: r.ID, RatePerSecond: r.Policy.RatePerSecond, Reason: reason, WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: r.Failure.Workflow, Runs: []api.WorkflowRunRecoveryTarget{r.Target}}}
}
func initialReviewAdmissionReason(r api.WorkflowInitialRecoveryReview, p api.WorkflowRecoveryPolicyResponse, now time.Time) string {
	if p.Policy.Mode == "automatic_initial" && p.AutomaticExecution != nil && p.AutomaticExecution.PausedAt != nil {
		return "automatic_paused"
	}
	if reason := initialReviewFreshness(r, p, now); reason != "" {
		return reason
	}
	preview := api.WorkflowInitialRecoveryPreview{Failure: r.Failure, Target: &r.Target, Reason: "eligible"}
	if reason := api.WorkflowInitialFailurePolicyReason(p.Policy, preview); reason != "eligible" {
		return reason
	}
	return ""
}
func initialReviewBudgetReason(count int, last time.Time, p api.WorkflowRecoveryPolicy, now time.Time) string {
	if count >= p.HourlyActionBudget {
		return "hourly_budget"
	}
	if !last.IsZero() && last.Add(time.Duration(p.CooldownSeconds)*time.Second).After(now) {
		return "cooldown"
	}
	return ""
}
func initialReviewBlocked(reason string) error {
	return &initialReviewBlockError{Reason: reason}
}
func decodeInitialReview(raw []byte) (api.WorkflowInitialRecoveryReview, error) {
	var r api.WorkflowInitialRecoveryReview
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, fmt.Errorf("decode initial recovery review: %w", err)
	}
	return r, nil
}

func initialReviewSkipReason(reason string) string {
	if reason == "expired" {
		return reason
	}
	return "changed"
}

type initialReviewBlockError struct{ Reason string }

func (e *initialReviewBlockError) Error() string {
	return fmt.Sprintf("%s: initial recovery review: %s", ErrWorkflowInitialReviewBlocked, e.Reason)
}
func (e *initialReviewBlockError) Unwrap() error { return ErrWorkflowInitialReviewBlocked }

func initialReviewUnresolved(r api.WorkflowInitialRecoveryReview) bool {
	return r.SubmittedAt != nil && r.AutomaticResult == nil && r.Result == nil
}
