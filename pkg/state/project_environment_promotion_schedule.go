package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ProjectPromotionHealthScheduler claims one due job per available execution slot.
// Claims share the health lease used by direct reconciliation and survive restart.
type ProjectPromotionHealthScheduler interface {
	ClaimNextProjectEnvironmentPromotionHealth(context.Context, string, bool) (*PromotionHealthJob, error)
	ProjectEnvironmentPromotionHealthLeaseCurrent(context.Context, string, string, string) (bool, error)
}

func nextPromotionHealthCheck(h *api.ProjectPromotionHealth, r *api.ProjectPromotionMigrationRecovery, now time.Time) time.Time {
	next := now.Add(api.ProjectPromotionHealthPollInterval)
	deadline := h.Deadline
	if r != nil && !promotionRecoveryTerminal(r.Status) {
		deadline = r.Deadline
	} else if h.Phase == "preflight" || h.Phase == "verifying" || h.Phase == "restoring" {
		if h.CheckedAt != nil && h.CheckedAt.Add(api.ProjectReleaseVerificationInterval).After(next) {
			next = h.CheckedAt.Add(api.ProjectReleaseVerificationInterval)
		}
	}
	if h.Control != nil && h.Control.Status == "pending" {
		deadline = h.Control.Deadline
	}
	if deadline.After(now) && deadline.Before(next) {
		next = deadline
	}
	return next
}

type promotionHealthSchedule struct {
	nextCheckAt, lastClaimedAt time.Time
	generation                 int64
	recoveryID, recoveryStatus string
}
