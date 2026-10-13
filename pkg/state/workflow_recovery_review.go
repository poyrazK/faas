package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrWorkflowRecoveryReviewConflict = errors.New("review decision already recorded; reload the original decision")

func recoveryReviewDecisionMatches(d *api.WorkflowRecoveryReviewDecision, r api.WorkflowRecoveryReviewDecisionRequest) bool {
	return d != nil && d.Decision == r.Decision && d.Reason == r.Reason
}
func newRecoveryReviewDecision(ctx context.Context, r api.WorkflowRecoveryReviewDecisionRequest) *api.WorkflowRecoveryReviewDecision {
	actor := recoveryAuditActor(ctx, "review_decided")
	return &api.WorkflowRecoveryReviewDecision{Decision: r.Decision, Reason: r.Reason, DecidedAt: time.Now().UTC(), ActorKind: actor.Kind, ActorID: actor.ID}
}
func (m *MemStore) DecideWorkflowRecoveryPolicyReview(ctx context.Context, account, app, id string, req api.WorkflowRecoveryReviewDecisionRequest) (api.WorkflowRecoveryPolicyReview, error) {
	var out api.WorkflowRecoveryPolicyReview
	if err := validRecoveryPolicyReviewQuery(account, app, id, 1); err != nil {
		return out, err
	}
	if err := req.Validate(); err != nil {
		return out, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
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
	if out.Plan == nil {
		return out, ErrEventRecoveryQuery
	}
	if out.ReviewDecision != nil {
		if !recoveryReviewDecisionMatches(out.ReviewDecision, req) {
			return out, ErrWorkflowRecoveryReviewConflict
		}
		m.decorateRecoveryReviewNotificationLocked(p, &out)
		return out, nil
	}
	out.ReviewDecision = newRecoveryReviewDecision(ctx, req)
	encoded, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	p.Reviews[out.ID] = encoded
	m.decorateRecoveryReviewNotificationLocked(p, &out)
	return out, nil
}
func (s *PgStore) DecideWorkflowRecoveryPolicyReview(ctx context.Context, account, app, id string, req api.WorkflowRecoveryReviewDecisionRequest) (api.WorkflowRecoveryPolicyReview, error) {
	var out api.WorkflowRecoveryPolicyReview
	if err := validRecoveryPolicyReviewQuery(account, app, id, 1); err != nil {
		return out, err
	}
	if err := req.Validate(); err != nil {
		return out, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	raw, _ := json.Marshal(newRecoveryReviewDecision(ctx, req))
	result, err := sqlc.New().WorkflowRecoveryPolicyReviewDecide(ctx, s.pool, sqlc.WorkflowRecoveryPolicyReviewDecideParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ID: mustPgUUID(id), Decision: raw})
	if errors.Is(err, pgx.ErrNoRows) {
		review, e := s.GetWorkflowRecoveryPolicyReview(ctx, account, app, id)
		if e != nil {
			return out, e
		}
		return review, ErrEventRecoveryQuery
	}
	if err != nil {
		return out, fmt.Errorf("decide recovery review: %w", err)
	}
	if err = json.Unmarshal(result, &out); err != nil {
		return out, fmt.Errorf("decode recovery review: %w", err)
	}
	if !recoveryReviewDecisionMatches(out.ReviewDecision, req) {
		return out, ErrWorkflowRecoveryReviewConflict
	}
	return out, nil
}

type WorkflowRecoveryReviewInspector interface {
	WorkflowRecoveryPolicyStore
	WorkflowRecoveryActionReceiptStore
	WorkflowRecoveryRetryPreflightStore
	ListEventRecoveryItems(context.Context, string, string, int64, int) (api.EventRecoveryItems, error)
	WorkflowRecoveryCancellation(context.Context, string, string, api.WorkflowRecoveryCancellationRequest, bool) (api.WorkflowRecoveryCancellationResponse, error)
}

// InspectWorkflowRecoveryReview is advisory. Applying still rechecks exact targets.
// Receipts take precedence over live generations so applied actions never become stale.
func InspectWorkflowRecoveryReview(ctx context.Context, s WorkflowRecoveryReviewInspector, account, app, id string, now time.Time) (api.WorkflowRecoveryReviewStatus, error) {
	if now.IsZero() {
		return api.WorkflowRecoveryReviewStatus{}, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	out := api.WorkflowRecoveryReviewStatus{ReviewID: id, ObservedAt: now, Status: "no_plan", Outcome: "no_plan", Actions: []api.WorkflowRecoveryReviewActionStatus{}}
	review, err := s.GetWorkflowRecoveryPolicyReview(ctx, account, app, id)
	if err != nil {
		return out, err
	}
	if review.CompletionNotification != nil {
		out = recoveryPolicyClone(review.CompletionNotification.Result)
		out.ObservedAt = now
		return out, nil
	}
	if review.Plan == nil {
		return out, nil
	}
	out.Status = "ready"
	for _, a := range review.Plan.Actions {
		row, err := inspectRecoveryReviewAction(ctx, s, account, *review.Plan, a, now)
		if err != nil {
			return out, err
		}
		out.Actions = append(out.Actions, row)
	}
	applied := 0
	for _, a := range out.Actions {
		switch a.Status {
		case "stale":
			out.Status = "stale"
		case "unknown":
			if out.Status != "stale" {
				out.Status = "unknown"
			}
		case "applied":
			applied++
		}
	}
	if applied == len(out.Actions) {
		out.Status = "applied"
	} else if applied > 0 && out.Status == "ready" {
		out.Status = "partially_applied"
	}
	finishRecoveryReviewOutcome(&out)
	return out, nil
}
func inspectRecoveryReviewAction(ctx context.Context, s WorkflowRecoveryReviewInspector, account string, p api.WorkflowRecoveryResponsePlan, a api.WorkflowRecoveryResponseAction, now time.Time) (api.WorkflowRecoveryReviewActionStatus, error) {
	out := api.WorkflowRecoveryReviewActionStatus{Position: a.Position, Kind: a.Kind, Status: "unknown", Reason: "evidence_unavailable", Outcome: "unknown", RunID: a.RunID, RunURL: "/v1/workflows/runs/" + a.RunID}
	if a.Cancellation != nil && a.Cancellation.ExpectedResumeCount != nil {
		out.ResumeCount = *a.Cancellation.ExpectedResumeCount
	}
	if a.Retry != nil && a.Retry.WorkflowRuns != nil && len(a.Retry.WorkflowRuns.Runs) == 1 {
		out.ResumeCount = a.Retry.WorkflowRuns.Runs[0].ExpectedResumeCount + 1
	}
	id := api.WorkflowRecoveryResponseActionID(p, a)
	if a.Kind == "cancel" && a.Cancellation != nil {
		receipt, err := s.GetWorkflowRecoveryActionReceipt(ctx, account, p.JobID, id)
		if err == nil {
			if !reflect.DeepEqual(receipt.Request, *a.Cancellation) || receipt.Response.JobID != p.JobID || receipt.Response.Position != a.Position || receipt.Response.Preview || receipt.Response.ResumeCount != out.ResumeCount || receipt.CapturedAt.IsZero() || receipt.Response.RunID != "" && receipt.Response.RunID != a.RunID {
				return out, fmt.Errorf("cancellation receipt does not match review")
			}
			out.Reason = receipt.Response.Outcome
			if out.Reason == "cancelled" || out.Reason == "already_cancelled" {
				out.Status = "applied"
				out.Outcome = "cancelled"
				out.Complete = true
				out.ResumeCount = receipt.Response.ResumeCount
				out.EvidenceSource = "cancellation_receipt"
				out.RecordedAt = cloneEventReceiptTime(&receipt.CapturedAt)
			} else {
				out.Status = "stale"
			}
			return out, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return out, err
		}
		preview, err := s.WorkflowRecoveryCancellation(ctx, account, p.JobID, *a.Cancellation, true)
		if err != nil {
			return out, err
		}
		out.Reason = preview.Outcome
		if preview.Outcome == "eligible" && preview.RunID == a.RunID {
			out.Status = "ready"
			out.Outcome = "pending"
			out.ResumeCount = *a.Cancellation.ExpectedResumeCount
		} else if preview.Outcome != "unknown" {
			out.Status = "stale"
		}
		return out, nil
	}
	if a.Kind == "retry" && a.Retry != nil {
		child, err := s.GetEventRecoveryByRequest(ctx, account, p.AppID, id)
		if err == nil {
			selection := child.Selection
			selection.Reason = ""
			req := *a.Retry
			req.Reason = ""
			if !reflect.DeepEqual(selection, req) || child.AppID != p.AppID {
				return out, fmt.Errorf("retry receipt does not match review")
			}
			out.Status = "applied"
			out.Reason = "child_created"
			if err := inspectRecoveryReviewRetryOutcome(ctx, s, account, p, a, child, &out); err != nil {
				return out, err
			}
			return out, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return out, err
		}
		preflight, err := s.GetWorkflowRecoveryRetryPreflight(ctx, account, p.JobID, now)
		if err != nil {
			return out, err
		}
		for _, row := range preflight.Sample {
			if row.Position != a.Position {
				continue
			}
			out.Reason = row.Reason
			if a.Retry.WorkflowRuns == nil || len(a.Retry.WorkflowRuns.Runs) != 1 {
				return out, fmt.Errorf("invalid retry review target")
			}
			count := a.Retry.WorkflowRuns.Runs[0].ExpectedResumeCount
			if row.WorkflowRunID != a.RunID || row.ExpectedResumeCount == nil || *row.ExpectedResumeCount != count {
				out.Status = "stale"
				out.Reason = "changed"
			} else if row.Reason == "eligible" || row.Reason == "capacity" {
				out.Status = "ready"
				out.Outcome = "pending"
				out.ResumeCount = count + 1
			} else if row.Reason != "unknown" {
				out.Status = "stale"
			}
			return out, nil
		}
	}
	return out, nil
}
