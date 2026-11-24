package claim

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// FinishClaim runs once the claimer's address is on the account: the
// anonymous session's keys (KeyLabel, which no customer key may take) are
// revoked first, and only then is the claim recorded. Keys the owner created
// after the claim are never touched, even when the sweep finishes a claim
// late. A failure leaves the row open, so the
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
		if !isAnonymousKey(k) {
			continue
		}
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
	if key == nil || !isAnonymousKey(*key) || IsUnclaimed(actor) {
		return false
	}
	store, ok := s.unclaimedStore()
	if !ok {
		return false
	}
	_, err := store.UnclaimedAccountByID(r.Context(), actor.ID)
	return err == nil
}

// isAnonymousKey reports an anonymous session's key. apid reserves the
// label, so no customer key carries it.
func isAnonymousKey(k state.APIKey) bool { return strings.TrimSpace(k.Label) == KeyLabel }

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
	t := time.NewTicker(api.UnclaimedAccountSweepInterval)
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

// ExpireOnce ends the unclaimed accounts past their expiry and returns how
// many it ended. It pages through every due row, so rows it cannot finish
// (a failing deletion, a store error) never hold back the ones after them;
// they are retried on the next run. A row whose account already carries a
// real address was claimed but not recorded: the sweep finishes that claim
// instead. Several apids may run it at once; marking the row is the claim
// on the work.
func (s *Service) ExpireOnce(ctx context.Context) int {
	store, ok := s.unclaimedStore()
	if !ok {
		return 0
	}
	log := s.Host.Log()
	now := s.now()
	ended := 0
	var cursor state.UnclaimedCursor
	for range api.UnclaimedAccountSweepMaxPages {
		rows, err := store.ListExpiredUnclaimedAccounts(ctx, now, cursor, api.UnclaimedAccountExpiryBatch)
		if err != nil {
			log.Warn("unclaimed_expiry.list", "err", err)
			break
		}
		for _, row := range rows {
			ended += s.sweepOne(ctx, store, row.AccountID)
		}
		if len(rows) < api.UnclaimedAccountExpiryBatch || ctx.Err() != nil {
			break
		}
		cursor = rows[len(rows)-1]
	}
	if ended > 0 {
		log.Info("unclaimed_expiry.ended", "count", ended)
	}
	return ended
}

// sweepOne ends or finishes one due account; 1 when it ended it.
func (s *Service) sweepOne(ctx context.Context, store state.UnclaimedAccountStore, id string) int {
	log := s.Host.Log()
	acct, err := s.Host.Store().AccountByID(ctx, id)
	if err != nil {
		log.Warn("unclaimed_expiry.account", "account_id", id, "err", err)
		return 0
	}
	if !IsUnclaimed(acct) {
		if err := s.FinishClaim(ctx, id); err != nil {
			log.Error("unclaimed_expiry.finish_claim", "account_id", id, "err", err)
		}
		return 0
	}
	if !s.expire(ctx, store, acct, "unclaimed_expiry") {
		return 0
	}
	s.Host.Observe(EventExpired)
	return 1
}
