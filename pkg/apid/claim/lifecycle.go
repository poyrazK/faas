package claim

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// FinishClaim runs once the claimer's address is on the account: every key
// on it (only the anonymous session could mint them) is revoked first, and
// only then is the claim recorded. A failure leaves the row open, so the
// sweep finishes the claim later (FinishPending); until then the middleware
// refuses the anonymous key on a claimed account, and the key expires with
// the claim window anyway.
func (s *Service) FinishClaim(ctx context.Context, accountID string) error {
	store, ok := s.unclaimedStore()
	if !ok {
		return nil
	}
	keys, err := s.Host.Store().ListAPIKeys(ctx, accountID)
	if err != nil {
		return fmt.Errorf("claim: list keys: %w", err)
	}
	for _, k := range keys {
		if err := s.Host.Store().DeleteAPIKey(ctx, accountID, k.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("claim: revoke key %s: %w", k.ID, err)
		}
	}
	claimed, err := store.MarkUnclaimedAccountClaimed(ctx, accountID, s.now())
	if err != nil {
		return fmt.Errorf("claim: record: %w", err)
	}
	if claimed {
		s.Host.Observe(EventClaimed)
		s.Host.Audit(ctx, "account.claimed", &accountID, nil)
	}
	return nil
}

// EndedAnonymousKey reports whether key is an anonymous deploy's key on an
// account that has been claimed: the claim revokes those keys, and this
// refuses one whose revocation has not landed yet.
func (s *Service) EndedAnonymousKey(r *http.Request, actor state.Account, key *state.APIKey) bool {
	if key == nil || key.Label != KeyLabel || IsUnclaimed(actor) {
		return false
	}
	store, ok := s.unclaimedStore()
	if !ok {
		return false
	}
	_, err := store.UnclaimedAccountByID(r.Context(), actor.ID)
	return err == nil
}

// expire ends an unclaimed account: its deletion is scheduled first, and the
// row is marked expired only once that is done or cannot be done, so a
// transient failure is retried by the next sweep. Marking the row also drops
// pending claim confirmations.
func (s *Service) expire(ctx context.Context, store state.UnclaimedAccountStore, acct state.Account, via string) bool {
	log := s.Host.Log()
	switch acct.Status {
	case state.AccountActive:
		if prob := s.Host.ScheduleDeletion(ctx, acct, via); prob != nil {
			if prob.Status >= http.StatusInternalServerError {
				log.Warn("unclaimed_expiry.schedule_retry", "account_id", acct.ID, "status", prob.Status, "code", prob.Code)
				return false
			}
			// A refusal that will not change (a hold, a deletion already
			// recorded by another apid): the claim window still ends.
			log.Warn("unclaimed_expiry.schedule_refused", "account_id", acct.ID, "status", prob.Status, "code", prob.Code)
		}
	case state.AccountDeletedPending:
		// Already scheduled.
	default:
		// Suspended or held for abuse: the hold owns the account and its
		// apps are stopped; the claim window still ends.
		log.Info("unclaimed_expiry.not_active", "account_id", acct.ID, "status", acct.Status)
	}
	marked, err := store.MarkUnclaimedAccountExpired(ctx, acct.ID, s.now())
	if err != nil {
		log.Warn("unclaimed_expiry.mark", "account_id", acct.ID, "err", err)
		return false
	}
	if !marked {
		return false
	}
	s.Host.Audit(ctx, "account.unclaimed_expired", &acct.ID, map[string]any{"via": via, "status": string(acct.Status)})
	return true
}

// RunExpiry sweeps every few minutes until ctx ends.
func (s *Service) RunExpiry(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		s.ExpireOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ExpireOnce ends one batch of unclaimed accounts past their expiry and
// returns how many it ended. A row whose account already carries a real
// address was claimed but not recorded: the sweep finishes that claim
// instead. Several apids may run it at once; marking the row is the claim on
// the work.
func (s *Service) ExpireOnce(ctx context.Context) int {
	store, ok := s.unclaimedStore()
	if !ok {
		return 0
	}
	log := s.Host.Log()
	ids, err := store.ListExpiredUnclaimedAccounts(ctx, s.now(), api.UnclaimedAccountExpiryBatch)
	if err != nil {
		log.Warn("unclaimed_expiry.list", "err", err)
		return 0
	}
	ended := 0
	for _, id := range ids {
		acct, err := s.Host.Store().AccountByID(ctx, id)
		if err != nil {
			log.Warn("unclaimed_expiry.account", "account_id", id, "err", err)
			continue
		}
		if !IsUnclaimed(acct) {
			if err := s.FinishClaim(ctx, id); err != nil {
				log.Error("unclaimed_expiry.finish_claim", "account_id", id, "err", err)
			}
			continue
		}
		if s.expire(ctx, store, acct, "unclaimed_expiry") {
			s.Host.Observe(EventExpired)
			ended++
		}
	}
	if ended > 0 {
		log.Info("unclaimed_expiry.ended", "count", ended)
	}
	return ended
}
