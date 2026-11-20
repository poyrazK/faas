package sales

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

// CheckoutState reports whether the billing subject (a personal account or a
// company organization's billing subject) can still take a salesperson code.
// actor is the signed-in member; for a personal account it is the subject.
func (s *Service) CheckoutState(ctx context.Context, actor, subject state.Account) (referrals.CheckoutState, error) {
	if !s.Enabled() || !actor.EmailVerified() {
		return referrals.CheckoutState{Reason: "unavailable"}, nil
	}
	store, ok := s.Store.(referrals.CheckoutStore)
	if !ok {
		return referrals.CheckoutState{}, errors.New("checkout referral store unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, api.ReferralReadTimeout)
	defer cancel()
	if subject.ResourceOrgID != "" {
		return store.OrgCheckoutState(ctx, subject.ResourceOrgID)
	}
	if subject.ID != actor.ID {
		return referrals.CheckoutState{Reason: "unavailable"}, nil
	}
	return store.CheckoutState(ctx, subject.ID)
}

func (s *Service) CheckoutStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.scope(w, r, acct, false); !ok {
		return
	}
	status, err := s.CheckoutState(r.Context(), acct, acct)
	if err != nil {
		unavailable(w)
		return
	}
	writeJSON(w, status)
}

// OrgCheckoutStatus serves an organization's salesperson-code eligibility to
// a member who may manage its billing; subject is its billing subject.
func (s *Service) OrgCheckoutStatus(w http.ResponseWriter, r *http.Request, actor, subject state.Account) {
	if !s.Enabled() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	status, err := s.CheckoutState(r.Context(), actor, subject)
	if err != nil {
		unavailable(w)
		return
	}
	writeJSON(w, status)
}

// PrepareCheckout stores an explicit first-purchase choice, without changing
// billing entitlements or creating a payment. An empty code leaves billing alone.
// subject is the billing subject: the actor's own account, or a company
// organization's billing subject when the actor buys for that organization.
func (s *Service) PrepareCheckout(ctx context.Context, actor, subject state.Account, plan api.Plan, code string) *api.Problem {
	acct := actor
	code = referrals.NormalizeCode(code)
	if code == "" {
		return nil
	}
	if !s.Enabled() {
		return api.NewProblem(400, "referral_program_disabled", "Referral codes unavailable", "Clear the salesperson code to continue, or try again when the referral program is available.")
	}
	if !acct.EmailVerified() {
		return api.ErrEmailVerificationRequired()
	}
	org := subject.ResourceOrgID != ""
	if !plan.IsPaid() || (!org && subject.ID != actor.ID) {
		return api.NewProblem(400, "referral_checkout_unsupported", "First paid purchase required", "Salesperson codes apply to the first paid plan purchase of your account or organization.")
	}
	if !referrals.ValidCode(code) {
		return checkoutProblem(referrals.ErrInvalidCheckoutCode)
	}
	store, ok := s.Store.(referrals.CheckoutStore)
	if !ok {
		return checkoutProblem(nil)
	}
	ctx, cancel := context.WithTimeout(ctx, api.ReferralReadTimeout)
	defer cancel()
	team, err := s.roster(ctx)
	if err != nil {
		return checkoutProblem(err)
	}
	members := team.emails()
	input := referrals.Attribution{Code: code, Policy: s.Config.Policy, SubjectKind: referrals.SubjectAccount}
	target := acct.ID
	if org {
		input.SubjectKind, input.SelectedBy, target = referrals.SubjectOrg, actor.ID, subject.ResourceOrgID
	}
	created, err := store.AttributeCheckout(ctx, target, input, members)
	if err != nil {
		return checkoutProblem(err)
	}
	if created {
		s.emit(ctx, "sales.checkout_referral_selected", acct.ID, map[string]any{"kind": "sales", "subject_kind": input.SubjectKind, "subject_id": target})
	}
	return nil
}

func checkoutProblem(err error) *api.Problem {
	switch {
	case errors.Is(err, referrals.ErrInvalidCheckoutCode):
		return api.NewProblem(400, "referral_code_invalid", "Salesperson code unavailable", "Enter an active six-character salesperson code, or clear it to continue.")
	case errors.Is(err, referrals.ErrAlreadyAttributed):
		return api.NewProblem(409, "referral_already_attributed", "Referral already saved", "This account or organization already has a referral. Clear the new code to keep that referral and continue.")
	case errors.Is(err, referrals.ErrCheckoutIneligible):
		return api.NewProblem(409, "referral_first_purchase_required", "First paid purchase required", "A salesperson code can only be added before the first paid purchase. Clear it to continue.")
	default:
		return api.NewProblem(503, "referral_checkout_unavailable", "Could not verify salesperson code", "Try again, or clear the optional code to continue without a new referral.")
	}
}
