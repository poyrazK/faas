package stripe

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestStripeCustomerParamsIncludesBillingIdentity(t *testing.T) {
	params := stripeCustomerParams(state.Account{
		ID:             "acct-1",
		Email:          "owner@example.com",
		BusinessName:   "Acme GmbH",
		BillingAddress: "Hauptstrasse 1, Berlin",
		TaxID:          "DE123456789",
	})
	if params.Name == nil || *params.Name != "Acme GmbH" {
		t.Fatalf("customer name = %v", params.Name)
	}
	if params.Address == nil || params.Address.Line1 == nil || *params.Address.Line1 != "Hauptstrasse 1, Berlin" {
		t.Fatalf("customer address = %+v", params.Address)
	}
	if len(params.TaxIDData) != 1 || params.TaxIDData[0].Type == nil || *params.TaxIDData[0].Type != "eu_vat" || params.TaxIDData[0].Value == nil || *params.TaxIDData[0].Value != "DE123456789" {
		t.Fatalf("customer tax ids = %+v", params.TaxIDData)
	}
}
