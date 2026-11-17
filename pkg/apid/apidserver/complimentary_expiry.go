package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Complimentary accounts with an end date return to Free when it passes
// (ADR-20261111140000), the same way an ended subscription does: the plan
// moves to Free, the $0 spend cap is cleared and the row is removed. An
// account that subscribed meanwhile keeps its plan; only the row goes.

const (
	complimentaryExpiryInterval = time.Hour
	complimentaryExpiryBatch    = 100
)

// runComplimentaryExpiry sweeps hourly until ctx ends.
func (s *server) runComplimentaryExpiry(ctx context.Context) {
	t := time.NewTicker(complimentaryExpiryInterval)
	defer t.Stop()
	for {
		s.expireComplimentaryAccounts(ctx, time.Now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// expireComplimentaryAccounts ends one batch of complimentary accounts whose
// end date is at or before now and returns how many it ended. Several apids
// may run it at once: ending an account twice leaves it on Free.
func (s *server) expireComplimentaryAccounts(ctx context.Context, now time.Time) int {
	store, ok := s.store.(state.ComplimentaryAccountStore)
	if !ok {
		return 0
	}
	rows, err := store.ExpiredComplimentaryAccounts(ctx, now, complimentaryExpiryBatch)
	if err != nil {
		s.log.Warn("complimentary_expiry.list", "err", err)
		return 0
	}
	ended := 0
	for _, row := range rows {
		acct, err := s.store.AccountByID(ctx, row.AccountID)
		if err != nil {
			s.log.Warn("complimentary_expiry.account", "account_id", row.AccountID, "err", err)
			continue
		}
		if err := s.endComplimentary(ctx, store, acct); err != nil {
			s.log.Warn("complimentary_expiry.end", "account_id", row.AccountID, "err", err)
			continue
		}
		if s.audit != nil {
			subject := acct.ID
			s.audit.Emit(ctx, "account.complimentary_expired", &subject, map[string]any{
				"from_plan": string(row.Plan), "expires_at": row.ExpiresAt, "reason": row.Reason,
			})
		}
		ended++
	}
	if ended > 0 {
		s.log.Info("complimentary_expiry.ended", "count", ended)
	}
	return ended
}
