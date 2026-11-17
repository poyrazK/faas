package sales

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

type checkoutFake struct {
	*fakeStore
	state   referrals.CheckoutState
	err     error
	input   referrals.Attribution
	account string
	members []string
	calls   int
}

func (f *checkoutFake) CheckoutState(context.Context, string) (referrals.CheckoutState, error) {
	return f.state, f.err
}
func (f *checkoutFake) AttributeCheckout(_ context.Context, account string, input referrals.Attribution, members []string) (bool, error) {
	f.calls++
	f.account = account
	f.input = input
	f.members = members
	return f.err == nil, f.err
}

func TestCheckoutCodeNormalizationAndConfiguredTerms(t *testing.T) {
	s, base, acct := serviceFixture()
	f := &checkoutFake{fakeStore: base}
	s.Store = f
	if p := s.PrepareCheckout(t.Context(), acct, api.PlanPro, " poju43 "); p != nil {
		t.Fatal(p)
	}
	if f.calls != 1 || f.account != acct.ID || f.input.Code != "POJU43" || f.input.Policy != s.Config.Policy || len(f.members) != 1 || f.members[0] != acct.Email {
		t.Fatal(f)
	}
}

func TestCheckoutCodeFailuresPrecedeBilling(t *testing.T) {
	for _, tt := range []struct {
		name, code string
		modify     func(*Service, *state.Account, *checkoutFake)
		want       string
	}{
		{"disabled", "POJU43", func(s *Service, _ *state.Account, _ *checkoutFake) { s.Config.Enabled = false }, "referral_program_disabled"},
		{"unverified", "POJU43", func(_ *Service, a *state.Account, _ *checkoutFake) { a.EmailVerifiedAt = nil }, "email_verification_required"},
		{"organization", "POJU43", func(_ *Service, a *state.Account, _ *checkoutFake) { a.ResourceOrgID = a.ID }, "referral_checkout_unsupported"},
		{"malformed", "too-long-code", func(*Service, *state.Account, *checkoutFake) {}, "referral_code_invalid"},
		{"existing referral", "POJU43", func(_ *Service, _ *state.Account, f *checkoutFake) { f.err = referrals.ErrAlreadyAttributed }, "referral_already_attributed"},
		{"previous purchase", "POJU43", func(_ *Service, _ *state.Account, f *checkoutFake) { f.err = referrals.ErrCheckoutIneligible }, "referral_first_purchase_required"},
		{"wrong kind or inactive", "POJU43", func(_ *Service, _ *state.Account, f *checkoutFake) { f.err = referrals.ErrInvalidCheckoutCode }, "referral_code_invalid"},
		{"storage failure", "POJU43", func(_ *Service, _ *state.Account, f *checkoutFake) { f.err = errors.New("database failure") }, "referral_checkout_unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, base, acct := serviceFixture()
			f := &checkoutFake{fakeStore: base}
			s.Store = f
			tt.modify(s, &acct, f)
			p := s.PrepareCheckout(t.Context(), acct, api.PlanPro, tt.code)
			if p == nil || p.Code != tt.want {
				t.Fatal(p)
			}
		})
	}
}

func TestCheckoutCodeEmptyAndFreePlan(t *testing.T) {
	s, base, acct := serviceFixture()
	f := &checkoutFake{fakeStore: base}
	s.Store = f
	s.Config.Enabled = false
	if p := s.PrepareCheckout(t.Context(), acct, api.PlanPro, " "); p != nil || f.calls != 0 {
		t.Fatal(p, f.calls)
	}
	s.Config.Enabled = true
	if p := s.PrepareCheckout(t.Context(), acct, api.PlanFree, "POJU43"); p == nil || p.Code != "referral_checkout_unsupported" || f.calls != 0 {
		t.Fatal(p, f.calls)
	}
}

func TestCheckoutStatusIsPrivateAndNeverCreatesAttribution(t *testing.T) {
	s, base, acct := serviceFixture()
	f := &checkoutFake{fakeStore: base, state: referrals.CheckoutState{Eligible: false, Reason: "already_attributed", Code: "POJU43"}}
	s.Store = f
	w := httptest.NewRecorder()
	s.CheckoutStatus(w, httptest.NewRequest("GET", "/v1/referrals/checkout", nil), acct)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || f.calls != 0 || !strings.Contains(w.Body.String(), "POJU43") || strings.Contains(w.Body.String(), acct.Email) {
		t.Fatal(w.Code, w.Body.String(), f.calls)
	}
}
