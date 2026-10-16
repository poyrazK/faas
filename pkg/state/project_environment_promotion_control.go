package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type ProjectPromotionControlStore interface {
	RequestProjectPromotionControl(context.Context, string, string, string, api.ProjectPromotionControlRequest) (*api.ProjectPromotionControl, error)
}

func clonePromotionControl(c *api.ProjectPromotionControl) *api.ProjectPromotionControl {
	if c == nil {
		return nil
	}
	out := *c
	if c.ResolvedAt != nil {
		at := *c.ResolvedAt
		out.ResolvedAt = &at
	}
	return &out
}

func requestPromotionControl(p ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth, active, action string, req api.ProjectPromotionControlRequest, now time.Time) (*api.ProjectPromotionHealth, error) {
	if !req.Valid() || action != "abort" && action != "recover" {
		return nil, ErrInvalidArgument
	}
	if h == nil || h.Generation != req.ExpectedGeneration || h.Policy.Migration != nil && h.Phase != "queued" || h.Control != nil && h.Control.Status == "pending" {
		return nil, ErrConflict
	}
	allowed := api.ProjectPromotionControlActions(h)
	if len(allowed) != 1 || allowed[0] != action || action == "abort" && p.Status != "running" {
		return nil, ErrConflict
	}
	expected := p.PreviousTargetReleaseSetID
	if p.TargetReleaseSetID != "" {
		expected = p.TargetReleaseSetID
	}
	if h.Phase != "queued" && (active == "" || active != expected || p.RestoredTargetReleaseSetID != "" || p.RollbackReleaseSetID != "") {
		return nil, ErrConflict
	}
	next := clonePromotionHealth(h)
	next.Control = &api.ProjectPromotionControl{Request: req, Action: action, Status: "pending", OperatorAccountID: p.AccountID, RequestedAt: now, Deadline: now.Add(api.ProjectReleaseVerificationTimeout)}
	next.Generation++
	next.HealthyRounds, next.RegressedRounds = 0, 0
	next.Evidence, next.CheckedAt = nil, nil
	next.Verdict = "unknown"
	if p.TargetReleaseSetID == "" {
		if action != "abort" {
			return nil, ErrConflict
		}
		next.Phase, next.Message = "aborted", "Operator aborted before cutover; the destination release remains selected."
		next.Control.Status, next.Control.ResolvedAt = "aborted", &now
		if h.Phase == "queued" {
			next.Admission.Reason = "aborted"
			next.Message = "Operator cancelled the queued promotion; no workloads were started."
		}
	} else {
		next.Phase, next.Message = "restoring", "Operator requested restoration; checking the retained baseline before switching destination intent."
		next.Deadline = next.Control.Deadline
	}
	return next, nil
}

// Baseline-only evidence never substitutes candidate health or client verdicts.
func decidePromotionRestoration(h *api.ProjectPromotionHealth, p ProjectEnvironmentPromotion, o PromotionHealthObservation, now time.Time) (*api.ProjectPromotionHealth, error) {
	next := clonePromotionHealth(h)
	if h.Control == nil || h.Control.Status != "pending" {
		return nil, ErrConflict
	}
	switch {
	case o.Action == "conflict":
		next.Phase, next.Message = "conflicted", "Destination intent changed; restoration will not overwrite it."
	case o.Action == "rolled_back":
		if h.Phase != "rolling_back" || p.RollbackStatus != "rolled_back" {
			return nil, ErrConflict
		}
		next.Phase, next.Message = "failed", "Operator requested restoration completed."
		next.Control.Status, next.Control.ResolvedAt = "restored", &now
	case o.Action == "rollback_failed" || o.Action == "fail" || !now.Before(h.Control.Deadline):
		next.Phase, next.Message = "rollback_failed", "Restoration could not be qualified within its deadline; inspect the destination and recover with a new request."
	case o.Action != "":
		return nil, ErrInvalidArgument
	case h.Phase != "restoring":
		return h, nil
	case h.CheckedAt != nil && now.Sub(*h.CheckedAt) < api.ProjectReleaseVerificationInterval:
		return h, nil
	default:
		next.CheckedAt = &now
		next.Evidence = append([]api.ProjectReleaseProbeEvidence(nil), o.Evidence...)
		next.Verdict = "unknown"
		if promotionHealthBaselineHealthy(next, now) {
			next.Verdict = "healthy"
			next.HealthyRounds++
		} else {
			next.HealthyRounds = 0
		}
		if next.HealthyRounds >= api.ProjectReleaseVerificationRounds {
			next.Phase = "rolling_back"
		}
	}
	if next.Phase == "rollback_failed" || next.Phase == "conflicted" {
		next.Control.Status, next.Control.ResolvedAt, next.Control.Reason = "blocked", &now, next.Phase
	}
	next.Generation++
	return next, nil
}

func guardPromotionControlRollback(h *api.ProjectPromotionHealth, now time.Time) error {
	if h == nil || h.Control == nil {
		return nil
	}
	if h.Control.Status != "pending" || h.Phase != "rolling_back" || !now.Before(h.Control.Deadline) || h.HealthyRounds < api.ProjectReleaseVerificationRounds || !promotionHealthBaselineHealthy(h, now) {
		return ErrConflict
	}
	return nil
}
