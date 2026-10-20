package apidserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// claimPendingError is the 409 for a name another account holds unverified:
// it names the TXT record that would let the caller take the claim over
// (ADR-20261020090000).
type claimPendingError struct {
	name, record, token string
}

func (e *claimPendingError) Error() string {
	return fmt.Sprintf("another account has an unverified claim on %s; to show you control it, publish a TXT record %s with the value %s, then add it again", e.name, e.record, e.token)
}

func (e *claimPendingError) Unwrap() error { return state.ErrConflict }

func (e *claimPendingError) hint() string {
	return fmt.Sprintf("publish TXT %s = %s, then retry", e.record, e.token)
}

// takeOverDomainClaim answers a create that conflicted with an existing row.
// When another account holds the domain unverified and the caller's takeover
// token is published at its challenge name, the claim moves to app. Otherwise
// it returns conflict unchanged, or a *claimPendingError naming the record to
// publish.
func (s *server) takeOverDomainClaim(ctx context.Context, acct state.Account, app state.App, domain, environmentID string, perApp, perAccount int, conflict error) (state.CustomDomain, error) {
	takeover, ok := s.store.(state.CustomDomainClaimTakeover)
	if !ok {
		return state.CustomDomain{}, conflict
	}
	holder, err := s.store.DomainByName(ctx, domain)
	if err != nil || holder.Verified() {
		return state.CustomDomain{}, conflict
	}
	holderApp, err := s.store.AppByID(ctx, holder.AppID)
	if err != nil || holderApp.AccountID == acct.ID {
		return state.CustomDomain{}, conflict
	}
	token := state.ClaimTakeoverToken(state.ClaimKindCustomDomain, acct.ID, domain)
	if !checkTXT(ctx, domain, token) {
		return state.CustomDomain{}, &claimPendingError{name: domain, record: state.CustomDomainChallengeName(domain), token: token}
	}
	d, err := takeover.TakeOverCustomDomainClaim(ctx, domain, app.ID, environmentID, token, perApp, perAccount)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			return state.CustomDomain{}, conflict
		}
		return state.CustomDomain{}, err
	}
	s.audit.Emit(ctx, "domain.claim_taken_over", &acct.ID, map[string]any{"domain": d.Domain, "app_id": app.ID})
	s.audit.Emit(ctx, "domain.claim_released", &holderApp.AccountID, map[string]any{
		"domain": d.Domain, "app_id": holder.AppID, "reason": "dns_proof_by_another_account",
	})
	return d, nil
}
