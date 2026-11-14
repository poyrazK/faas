package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReferredSignupAndInvoiceTransaction(t *testing.T) {
	s, _, ctx := pgStoreInvoicesWithPool(t)
	owner, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "referrer@example.com", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	link, err := s.ReferralStore().EnsureLink(ctx, owner.Account.ID, "customer")
	if err != nil {
		t.Fatal(err)
	}
	params := state.CreateAccountWithPersonalOrgParams{Email: "referred@example.com", Plan: api.PlanFree, Referral: &referrals.Attribution{Code: link.Code, Policy: referrals.Policy{Currency: "usd", CustomerRewardCents: 2000, ReferredRewardCents: 1000, ConversionDays: 90}}}
	created, err := s.CreateAccountWithPersonalOrg(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateAccountWithPersonalOrg(ctx, params); err == nil {
		t.Fatal("duplicate account created")
	}
	inv := state.Invoice{AccountID: created.Account.ID, Provider: "stripe", ProviderInvoiceID: "referral_paid_invoice", Status: "paid", Currency: "usd", SubtotalCents: 5000, TotalCents: 5000, AmountPaidCents: 5000, PeriodStart: time.Now(), PeriodEnd: time.Now().AddDate(0, 1, 0)}
	if err = s.UpsertInvoice(ctx, inv); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ReferralStore().List(ctx, referrals.Scope{OwnerID: owner.Account.ID}, "", 25)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if !rows[0].Paid || rows[0].RewardStatus != "eligible" || rows[0].RewardCents != 2000 {
		t.Fatalf("invoice transaction did not record conversion: %+v", rows[0])
	}
	first := *rows[0].EligibleAt
	if err = s.UpsertInvoice(ctx, inv); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ReferralStore().List(ctx, referrals.Scope{OwnerID: owner.Account.ID}, "", 25)
	if err != nil || !rows[0].EligibleAt.Equal(first) {
		t.Fatal("invoice retry reset conversion hold", rows, err)
	}
}
