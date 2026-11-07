// adr: 20261105104500
package projectsetup

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestSiblingAddressesKeepOnlyRewrittenAddresses(t *testing.T) {
	billing := "http://billing:8080/v1"
	level := "debug"
	env := reposcan.ComposeEnv{Import: true, Services: map[string]map[string]*string{
		"web": {"BILLING_URL": &billing, "LOG_LEVEL": &level},
	}}
	got := SiblingAddresses(env, "web", []string{"web", "billing"}, api.MustLimitsFor(api.PlanPro), true)
	if len(got) != 1 || got["BILLING_URL"] != "http://billing.svc.gregale:10081/v1" {
		t.Fatalf("addresses = %v, want only BILLING_URL even with the opt-in", got)
	}
}

func TestValidSiblingAddress(t *testing.T) {
	for value, want := range map[string]bool{
		"http://billing.svc.gregale:10081/v1":      true,
		"ws://chat.svc.gregale:10081":              true,
		"https://orders.internal/rpc":              true,
		"http://billing.svc.gregale:8080":          false,
		"https://orders.internal:8443":             false,
		"http://user:pw@billing.svc.gregale:10081": false,
		"http://attacker.example.com":              false,
		"redis://cache.svc.gregale:10081":          false,
		"not a url":                                false,
	} {
		if got := ValidSiblingAddress(value); got != want {
			t.Errorf("ValidSiblingAddress(%q) = %v, want %v", value, got, want)
		}
	}
}
