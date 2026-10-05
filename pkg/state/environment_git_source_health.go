package state

import (
	"context"
	"time"
)

// EnvironmentGitSourceHealth contains only fleet aggregates. Conditions overlap:
// an unavailable source can also have stale verification or an unapproved
// candidate. Suspended sources contribute only to Suspended.
type EnvironmentGitSourceHealth struct {
	Active                       int64
	Suspended                    int64
	Unchecked                    int64
	Unverified                   int64
	PollStale                    int64
	VerificationStale            int64
	Unavailable                  int64
	CandidatePendingApproval     int64
	ApprovedPendingApply         int64
	OldestCheckAgeSeconds        float64
	OldestVerificationAgeSeconds float64
}

// One consistent observation is sufficient for health reporting; it neither
// claims a poll nor changes approval, ownership, or reconcile work.
type EnvironmentGitSourceHealthStore interface {
	EnvironmentGitSourceHealth(context.Context, time.Time, time.Duration) (EnvironmentGitSourceHealth, error)
}

func validateEnvironmentGitSourceHealth(now time.Time, staleAfter time.Duration) error {
	if now.IsZero() || staleAfter <= 0 {
		return ErrInvalidArgument
	}
	return nil
}
