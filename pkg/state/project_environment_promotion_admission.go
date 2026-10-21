package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type ProjectPromotionAdmissionStore interface {
	// Dispatch requires the current health lease and generation. It performs no
	// external work; capacity and original-intent checks commit with activation.
	DispatchProjectEnvironmentPromotion(context.Context, string, string, string, int64) (*api.ProjectPromotionHealth, error)
}

type PromotionAdmissionFullError struct {
	Scope           string
	Limit, Observed int64
}

func (e *PromotionAdmissionFullError) Error() string {
	return fmt.Sprintf("promotion %s waiting queue is full (%d/%d)", e.Scope, e.Observed, e.Limit)
}

type PromotionApprovalIntent struct{ PlanTokenHash, ApprovalTokenHash string }
type PromotionApprovalError struct{}

func (*PromotionApprovalError) Error() string { return "promotion approval is expired or unavailable" }

type promotionAdmissionCounts struct{ fleetActive, accountActive, fleetQueued, accountQueued, older int64 }

func (c promotionAdmissionCounts) waitingReason() string {
	if c.accountActive >= api.ProjectPromotionAdmissionAccountActive {
		return "account_capacity"
	}
	if c.fleetActive >= api.ProjectPromotionAdmissionFleetActive {
		return "fleet_capacity"
	}
	if c.older > 0 {
		return "earlier_account_request"
	}
	return ""
}
func admitPromotionHealth(h *api.ProjectPromotionHealth, c promotionAdmissionCounts, now time.Time) error {
	if h == nil {
		return nil
	}
	// New callers cannot jump ahead of accepted requests for the same account.
	c.older = c.accountQueued
	reason := c.waitingReason()
	if reason == "" {
		return nil
	}
	if c.accountQueued >= api.ProjectPromotionAdmissionAccountQueued {
		return &PromotionAdmissionFullError{"account", api.ProjectPromotionAdmissionAccountQueued, c.accountQueued}
	}
	if c.fleetQueued >= api.ProjectPromotionAdmissionFleetQueued {
		return &PromotionAdmissionFullError{"fleet", api.ProjectPromotionAdmissionFleetQueued, c.fleetQueued}
	}
	h.Phase, h.StartedAt, h.Deadline = "queued", now, now.Add(api.ProjectPromotionAdmissionWaitTimeout)
	h.Admission = &api.ProjectPromotionAdmission{QueuedAt: now, WaitDeadline: h.Deadline, Reason: reason}
	h.Message = "Waiting for promotion capacity; workload preparation and health verification have not started."
	return nil
}
func queuedPromotionDecision(h *api.ProjectPromotionHealth, action string, now time.Time) *api.ProjectPromotionHealth {
	next := clonePromotionHealth(h)
	switch {
	case action == "conflict":
		next.Phase, next.Message = "conflicted", "Queued promotion inputs changed; preview and approve the current destination intent again."
		next.Admission.Reason = "intent_changed"
	case action == "fail":
		next.Phase, next.Message = "failed", "Queued promotion could not be validated; no workloads were started."
		next.Admission.Reason = "validation_failed"
	case !now.Before(h.Admission.WaitDeadline):
		next.Phase, next.Message = "failed", "Promotion expired while waiting for capacity; no workloads were started."
		next.Admission.Reason = "queue_timeout"
	default:
		return h
	}
	next.Generation++
	return next
}
func startQueuedPromotion(h *api.ProjectPromotionHealth, now time.Time) *api.ProjectPromotionHealth {
	next := clonePromotionHealth(h)
	next.Admission.StartedAt = &now
	next.Admission.Reason = "started"
	next.Phase, next.StartedAt, next.Deadline = "preflight", now, now.Add(api.ProjectReleaseVerificationTimeout)
	next.Message = "Promotion capacity acquired; starting fresh preflight verification."
	if next.Policy.Migration != nil {
		next.Phase = "migrating"
		next.Deadline = now.Add(api.ProjectPromotionMigrationTimeout)
	}
	next.Generation++
	return next
}
