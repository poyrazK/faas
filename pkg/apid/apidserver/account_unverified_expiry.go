package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Accounts nobody ever verified are restricted after 30 days and, if they
// still hold nothing (no app ever created, no billing customer), scheduled
// for deletion after api.UnverifiedAccountDeleteAfter (ADR 20261110120000).
// Deletion goes through scheduleDeletion like a customer's own request: the
// address is mailed, the 30-day restore window applies and erasure is the
// usual pipeline. It keeps squatted and abandoned signups from holding an
// address forever.

const unverifiedExpiryInterval = time.Hour

// runUnverifiedAccountExpiry sweeps hourly until ctx ends.
func (s *server) runUnverifiedAccountExpiry(ctx context.Context) {
	t := time.NewTicker(unverifiedExpiryInterval)
	defer t.Stop()
	for {
		s.expireUnverifiedAccounts(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// expireUnverifiedAccounts schedules one batch for deletion and returns how
// many it scheduled. Several apids may run it at once: scheduleDeletion
// only moves an active account, so a second one is refused.
func (s *server) expireUnverifiedAccounts(ctx context.Context) int {
	st, ok := s.store.(state.UnverifiedAccountExpiryStore)
	if !ok {
		return 0
	}
	cutoff := s.verificationNow().Add(-api.UnverifiedAccountDeleteAfter)
	ids, err := st.ListExpiredUnverifiedAccounts(ctx, api.EmailVerificationEnforcedFrom, cutoff, api.UnverifiedAccountExpiryBatch)
	if err != nil {
		s.log.Warn("unverified_expiry.list", "err", err)
		return 0
	}
	scheduled := 0
	for _, id := range ids {
		acct, err := s.store.AccountByID(ctx, id)
		if err != nil || acct.EmailVerified() || acct.Status != state.AccountActive {
			continue
		}
		if _, prob := s.scheduleDeletion(ctx, acct, "unverified_expiry", false); prob != nil {
			s.log.Warn("unverified_expiry.schedule", "account_id", id, "status", prob.Status, "code", prob.Code)
			continue
		}
		scheduled++
	}
	if scheduled > 0 {
		s.log.Info("unverified_expiry.scheduled", "count", scheduled)
	}
	return scheduled
}
