package sales

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) CheckoutState(ctx context.Context, acct state.Account) (referrals.CheckoutState, error) {
	if !s.Enabled() || acct.ResourceOrgID != "" || !acct.EmailVerified() {
		return referrals.CheckoutState{Reason: "unavailable"}, nil
	}
	store, ok := s.Store.(referrals.CheckoutStore)
	if !ok {
		return referrals.CheckoutState{}, errors.New("checkout referral store unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, api.ReferralReadTimeout)
	defer cancel()
	return store.CheckoutState(ctx, acct.ID)
}

func (s *Service) CheckoutStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.scope(w, r, acct, false); !ok {
		return
	}
	status, err := s.CheckoutState(r.Context(), acct)
	if err != nil {
		unavailable(w)
		return
	}
	writeJSON(w, status)
}

// PrepareCheckout stores an explicit first-purchase choice, without changing
// billing entitlements or creating a payment. An empty code leaves billing alone.
func (s *Service) PrepareCheckout(ctx context.Context, acct state.Account, plan api.Plan, code string) *api.Problem {
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
	if acct.ResourceOrgID != "" || !plan.IsPaid() {
		return api.NewProblem(400, "referral_checkout_unsupported", "First paid account purchase required", "Salesperson codes apply to a personal account's first paid plan purchase.")
	}
	if !referrals.ValidCode(code) {
		return checkoutProblem(referrals.ErrInvalidCheckoutCode)
	}
	store, ok := s.Store.(referrals.CheckoutStore)
	if !ok {
		return checkoutProblem(nil)
	}
	members := make([]string, 0, len(s.Config.Members))
	for email := range s.Config.Members {
		members = append(members, email)
	}
	ctx, cancel := context.WithTimeout(ctx, api.ReferralReadTimeout)
	defer cancel()
	created, err := store.AttributeCheckout(ctx, acct.ID, referrals.Attribution{Code: code, Policy: s.Config.Policy}, members)
	if err != nil {
		return checkoutProblem(err)
	}
	if created {
		s.emit(ctx, "sales.checkout_referral_selected", acct.ID, map[string]any{"kind": "sales"})
	}
	return nil
}

func checkoutProblem(err error) *api.Problem {
	switch {
	case errors.Is(err, referrals.ErrInvalidCheckoutCode):
		return api.NewProblem(400, "referral_code_invalid", "Salesperson code unavailable", "Enter an active six-character salesperson code, or clear it to continue.")
	case errors.Is(err, referrals.ErrAlreadyAttributed):
		return api.NewProblem(409, "referral_already_attributed", "Referral already saved", "This account already has a referral. Clear the new code to keep that referral and continue.")
	case errors.Is(err, referrals.ErrCheckoutIneligible):
		return api.NewProblem(409, "referral_first_purchase_required", "First paid purchase required", "A salesperson code can only be added before this account's first paid purchase. Clear it to continue.")
	default:
		return api.NewProblem(503, "referral_checkout_unavailable", "Could not verify salesperson code", "Try again, or clear the optional code to continue without a new referral.")
	}
}
