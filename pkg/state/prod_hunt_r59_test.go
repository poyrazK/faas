package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Prod hunt r59: `domain add bh62.example.invalid` was accepted and printed
// DNS records that no public zone can hold. Claims below special-use names
// are refused; .test and .example stay claimable for test suites.
func TestCustomDomainClaimRefusesSpecialUseNames(t *testing.T) {
	for _, domain := range []string{"bh62.example.invalid", "app.localhost", "printer.local", "x.onion", "a.b.alt", "svc.corp.internal", "nas.home.arpa", "*.lab.internal", "App.LOCAL"} {
		if err := state.ValidateCustomDomainClaim(domain); err == nil {
			t.Errorf("ValidateCustomDomainClaim(%q) = nil, want a special-use refusal", domain)
		}
	}
	for _, domain := range []string{"shop.example.test", "app.example.com", "internal.example.com", "local.dev", "my-alt.io", "*.invalidation.app"} {
		if err := state.ValidateCustomDomainClaim(domain); err != nil {
			t.Errorf("ValidateCustomDomainClaim(%q) = %v, want nil", domain, err)
		}
	}
}
