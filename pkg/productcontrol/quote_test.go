package productcontrol

import (
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
)

func TestProductQuoteBindsCustomerTermsAndMonth(t *testing.T) {
	spec := api.ProductCatalogSpec{Currency: "EUR", Plans: map[string]api.ProductPlan{"starter": {Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	catalog := api.ProductCatalog{Revision: "revision", Spec: spec}
	customer := api.ProductCustomer{Plan: "starter"}
	first := Quote("account", "app", "tenant", "export", catalog, customer, "2026-10")
	for _, other := range []string{
		Quote("foreign", "app", "tenant", "export", catalog, customer, "2026-10"),
		Quote("account", "foreign", "tenant", "export", catalog, customer, "2026-10"),
		Quote("account", "app", "foreign", "export", catalog, customer, "2026-10"),
		Quote("account", "app", "tenant", "foreign", catalog, customer, "2026-10"),
		Quote("account", "app", "tenant", "export", catalog, customer, "2026-11"),
	} {
		if first == other {
			t.Fatal("quote did not bind consent to identity and terms")
		}
	}
}
