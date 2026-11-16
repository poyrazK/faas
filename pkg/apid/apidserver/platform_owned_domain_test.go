package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Hunt 34 H34-51: the platform's own names were accepted as pending custom
// domain claims, the apex included.
func TestPlatformOwnedDomain(t *testing.T) {
	for domain, want := range map[string]bool{
		"gregale.dev":             true,
		"GREGALE.DEV.":            true,
		"api.gregale.dev":         true,
		"*.gregale.dev":           true,
		"my-app.apps.example":     true,
		"apps.example":            true,
		"example.com":             false,
		"notgregale.dev":          false,
		"gregale.dev.attacker.io": false,
		"shop.customer.io":        false,
	} {
		if got := apidserver.PlatformOwnedDomain("apps.example", "gregale.dev", domain); got != want {
			t.Errorf("%s: platform owned = %t, want %t", domain, got, want)
		}
	}
	if apidserver.PlatformOwnedDomain("", "", "gregale.dev") {
		t.Error("no configured domains reserved a name")
	}
}
