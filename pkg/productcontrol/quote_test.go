package productcontrol

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductQuoteBindsCustomerTermsAndMonth(t *testing.T) {
	spec := api.ProductCatalogSpec{Currency: "EUR", Plans: map[string]api.ProductPlan{"starter": {Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	catalog := api.ProductCatalog{Revision: "revision", Spec: spec}
	customer := api.ProductCustomer{Plan: "starter"}
	first := Quote("account", "app", "tenant", "export", catalog, customer, "2026-10", 0)
	for _, other := range []string{
		Quote("foreign", "app", "tenant", "export", catalog, customer, "2026-10", 0),
		Quote("account", "foreign", "tenant", "export", catalog, customer, "2026-10", 0),
		Quote("account", "app", "foreign", "export", catalog, customer, "2026-10", 0),
		Quote("account", "app", "tenant", "foreign", catalog, customer, "2026-10", 0),
		Quote("account", "app", "tenant", "export", catalog, customer, "2026-11", 0),
	} {
		if first == other {
			t.Fatal("quote did not bind consent to identity and terms")
		}
	}
}

// adr: 20261021154511 — a grant with included starts binds the next start's
// price: quotes agree while the price stays and differ when the next start
// crosses into overage. A grant without included starts quotes as before,
// whatever the month's starts.
func TestProductQuoteBindsIncludedStartPrice(t *testing.T) {
	plain := api.ProductFeatureGrant{MonthlyStarts: 10, UnitPriceMillicents: 25000}
	included := plain
	included.IncludedStarts = 2
	quote := func(grant api.ProductFeatureGrant, starts int64) string {
		spec := api.ProductCatalogSpec{Currency: "EUR", Plans: map[string]api.ProductPlan{"starter": {Features: map[string]api.ProductFeatureGrant{"export": grant}}}}
		return Quote("account", "app", "tenant", "export", api.ProductCatalog{Revision: "revision", Spec: spec}, api.ProductCustomer{Plan: "starter"}, "2026-10", starts)
	}
	if quote(plain, 0) != quote(plain, 7) {
		t.Fatal("a grant without included starts quoted by the month's starts")
	}
	if quote(included, 0) != quote(included, 1) || quote(included, 1) == quote(included, 2) || quote(included, 2) != quote(included, 9) {
		t.Fatal("included-start quotes do not follow the next start's price")
	}
	if quote(included, 0) == quote(plain, 0) {
		t.Fatal("included starts did not change the quoted terms")
	}
	if raw, _ := json.Marshal(plain); strings.Contains(string(raw), "included_starts") {
		t.Fatalf("a grant without included starts encodes differently: %s", raw)
	}
}
