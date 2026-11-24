package claim

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Session is POST /v1/anonymous/sessions (unauthenticated).
func (s *Service) Session(w http.ResponseWriter, r *http.Request) {
	store, ok := s.unclaimedStore()
	if !ok || !s.Host.Offered() {
		httpkit.NoSuchRoute(w)
		return
	}
	var req api.AnonymousSessionRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil || !req.AcceptTerms {
		api.WriteProblem(w, api.ErrValidation("body must be {\"accept_terms\": true}: deploying without an account accepts the Terms of Service"))
		return
	}
	acct, raw, expiresAt, err := s.createAccount(r)
	if prob := s.refusal(r.Context(), err); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	defer clear(raw)
	if links := s.Host.RecordSignupFacts(r.Context(), r, acct.ID, acct.Email); links > 0 {
		// Kept (scheduled for deletion) rather than removed, so the link
		// stays reviewable; the sweep retries a deletion that fails now.
		if !s.expire(r.Context(), store, acct, "unclaimed_held_link") {
			s.Host.Log().Warn("anonymous_session.held_link_expire", "account_id", acct.ID)
		}
		s.Host.Observe(EventRefusedHeldLink)
		api.WriteProblem(w, api.ErrAnonymousDeploysUnavailable(0))
		return
	}
	key, err := s.mintKey(r, acct, expiresAt)
	if err != nil {
		s.Host.Log().Error("anonymous_session.key", "err", err, "account_id", acct.ID)
		// The account is unusable without its key: remove it, so it holds
		// no place under the caps.
		if derr := s.Host.Store().DeleteAccount(context.WithoutCancel(r.Context()), acct.ID); derr != nil {
			s.Host.Log().Error("anonymous_session.cleanup", "err", derr, "account_id", acct.ID)
		}
		api.WriteProblem(w, api.ErrCapacity("could not create the anonymous session; try again"))
		return
	}
	// Counted only once the session is granted.
	s.Host.RecordSignup(r.Context(), acct.ID)
	s.Host.Observe(EventCreated)
	s.Host.Audit(r.Context(), "account.unclaimed_created", &acct.ID, map[string]any{
		"expires_at": expiresAt.UTC().Format(time.RFC3339), "client": truncateClient(req.Client),
	})
	httpkit.WriteJSON(w, http.StatusCreated, api.AnonymousSessionResponse{
		AccountID: acct.ID, APIKey: key, ClaimURL: s.claimURL(r, raw),
		ExpiresAt: expiresAt.UTC(), Limits: Limits(),
	})
}

// refusal maps a failed account creation to its answer: a cap the store
// enforced is a 429 with a retry hint, anything else a 503.
func (s *Service) refusal(ctx context.Context, err error) *api.Problem {
	if err == nil {
		return nil
	}
	var limit *state.UnclaimedLimitError
	if !errors.As(err, &limit) {
		s.Host.Log().Error("anonymous_session.create", "err", err)
		return api.ErrCapacity("could not create the anonymous session; try again")
	}
	retry := int((24 * time.Hour).Seconds())
	event := EventRefusedNetwork
	if limit.Reason == state.UnclaimedLimitLive {
		retry, event = int(time.Hour.Seconds()), EventRefusedLiveCap
	}
	s.Host.Observe(event)
	s.Host.Audit(ctx, "account.unclaimed_refused", nil, map[string]any{
		"reason": limit.Reason, "limit": limit.Limit, "observed": limit.Observed,
	})
	return api.ErrAnonymousDeploysUnavailable(retry).WithLimit(int64(limit.Limit), int64(limit.Observed))
}

// createAccount creates the Free account and its unclaimed row in one
// transaction, which also enforces the caps. It returns the claim token.
func (s *Service) createAccount(r *http.Request) (state.Account, []byte, time.Time, error) {
	raw, err := newToken(TokenBytes)
	if err != nil {
		return state.Account{}, nil, time.Time{}, err
	}
	local, err := newToken(10)
	if err != nil {
		return state.Account{}, nil, time.Time{}, err
	}
	set := s.Host.Settings()
	expiresAt := s.now().Add(set.TTL)
	res, err := s.Host.Store().CreateAccountWithPersonalOrg(r.Context(), state.CreateAccountWithPersonalOrgParams{
		Email: "anon-" + hex.EncodeToString(local) + "@" + api.UnclaimedEmailDomain,
		Plan:  api.PlanFree,
		// The placeholder is never mailed; the account is trusted to deploy
		// within the unclaimed limits, and a claim proves a real address.
		RequireEmailVerification: false,
		TermsAcceptance:          s.Host.TermsAcceptance(),
		Unclaimed: &state.UnclaimedAccountParams{
			ClaimTokenSHA256: api.HashToken(raw), ExpiresAt: expiresAt,
			CreatedIP: s.Host.ClientIP(r), UserAgent: r.UserAgent(),
			LiveMax: set.LiveMax, PerAddress: set.PerAddress, PerNetwork: set.PerNetwork,
		},
	})
	if err != nil {
		clear(raw)
		return state.Account{}, nil, time.Time{}, err
	}
	return res.Account, raw, expiresAt, nil
}

// mintKey creates the session's deploy key, expiring with the account.
func (s *Service) mintKey(r *http.Request, acct state.Account, expiresAt time.Time) (string, error) {
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		return "", err
	}
	org, err := s.Host.Store().OrgByPersonalAccount(r.Context(), acct.ID)
	if err != nil {
		return "", fmt.Errorf("personal org: %w", err)
	}
	k, err := s.Host.Store().CreateOrgAPIKeyWithProvenance(r.Context(), org.ID, acct.ID, hash, KeyLabel,
		api.ScopesAdminOnly, &expiresAt, s.Host.ClientIP(r), r.UserAgent(), nil)
	if err != nil {
		return "", fmt.Errorf("create key: %w", err)
	}
	s.Host.RecordKeyDisplayPrefix(r.Context(), k.ID, plaintext)
	return plaintext, nil
}

// Link is POST /v1/account/claim-link: an unclaimed account's key gets a
// fresh claim link. The previous link, and any confirmation already mailed
// through it, stop working.
func (s *Service) Link(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.unclaimedStore()
	if !ok || !IsUnclaimed(acct) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Account already claimed", "only an account created by an anonymous deploy has a claim link"))
		return
	}
	row, err := store.UnclaimedAccountByID(r.Context(), acct.ID)
	if err != nil || !row.Live(s.now()) {
		api.WriteProblem(w, api.NewProblem(http.StatusGone, api.CodeConflict,
			"Claim window ended", "this anonymous deploy expired; run gregale login and deploy again"))
		return
	}
	raw, err := newToken(TokenBytes)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create a claim link"))
		return
	}
	defer clear(raw)
	if err := store.RotateUnclaimedClaimToken(r.Context(), acct.ID, api.HashToken(raw)); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create a claim link"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ClaimLinkResponse{ClaimURL: s.claimURL(r, raw), ExpiresAt: row.ExpiresAt.UTC()})
}
