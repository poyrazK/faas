package orgbilling_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/orgbilling"
)

// An organization's billing email is one bare address: the billing provider
// keys the customer and the portal sign-in on it.
// adr: 20261103131742
func TestNormalizeBillingEmail(t *testing.T) {
	cases := []struct {
		raw, want string
		ok        bool
	}{
		{" Billing@Example.COM ", "billing@example.com", true},
		{"finance+gregale@example.co.uk", "finance+gregale@example.co.uk", true},
		{"", "", false},
		{"not-an-email", "", false},
		{"Finance <finance@example.com>", "", false},
		{"a@example.com, b@example.com", "", false},
		{"root@localhost", "", false},
		{strings.Repeat("a", 250) + "@example.com", "", false},
	}
	for _, tc := range cases {
		got, ok := orgbilling.NormalizeBillingEmail(tc.raw)
		if got != tc.want || ok != tc.ok {
			t.Errorf("NormalizeBillingEmail(%q) = %q, %v; want %q, %v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

// The member whose email the organization bills to is its billing contact,
// whatever the case of either address.
// adr: 20261103131742
func TestIsBillingContact(t *testing.T) {
	if !orgbilling.IsBillingContact("Finance@Example.com", "finance@example.com ") {
		t.Fatal("same address in another case is the billing contact")
	}
	if orgbilling.IsBillingContact("", "") || orgbilling.IsBillingContact("finance@example.com", "dev@example.com") {
		t.Fatal("an empty billing email or another address is not the billing contact")
	}
}
