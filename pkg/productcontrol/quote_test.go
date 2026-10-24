package productcontrol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductQuoteBindsCustomerTermsAndMonth(t *testing.T) {
	spec := api.ProductCatalogSpec{Currency: "EUR", Plans: map[string]api.ProductPlan{"starter": {Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	catalog := api.ProductCatalog{Revision: "revision", Spec: spec}
	customer := api.ProductCustomer{Plan: "starter"}
	first := Quote("account", "app", "tenant", "export", catalog, customer, "2026-10", 0, time.Time{})
	for _, other := range []string{
		Quote("foreign", "app", "tenant", "export", catalog, customer, "2026-10", 0, time.Time{}),
		Quote("account", "foreign", "tenant", "export", catalog, customer, "2026-10", 0, time.Time{}),
		Quote("account", "app", "foreign", "export", catalog, customer, "2026-10", 0, time.Time{}),
		Quote("account", "app", "tenant", "foreign", catalog, customer, "2026-10", 0, time.Time{}),
		Quote("account", "app", "tenant", "export", catalog, customer, "2026-11", 0, time.Time{}),
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
		return Quote("account", "app", "tenant", "export", api.ProductCatalog{Revision: "revision", Spec: spec}, api.ProductCustomer{Plan: "starter"}, "2026-10", starts, time.Time{})
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

// adr: 20261021195901 — during a trial a start is free and the quote binds
// that price, so a confirmation from the trial goes stale when it ends;
// after it, included starts count only the starts outside the trial.
func TestProductStartPriceDuringTrial(t *testing.T) {
	grant := api.ProductFeatureGrant{MonthlyStarts: 100, UnitPriceMillicents: 20000, IncludedStarts: 2}
	start, end := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	customer := api.ProductCustomer{Plan: "starter", TrialStartsAt: &start, TrialEndsAt: &end}
	during, after := start.Add(time.Hour), end.Add(time.Hour)
	if price, trial, bind := StartPrice(grant, customer, 50, during); price != 0 || !trial || !bind {
		t.Fatalf("start during the trial = %d %v %v", price, trial, bind)
	}
	if price, trial, _ := StartPrice(grant, customer, 1, after); price != 0 || trial {
		t.Fatalf("included start after the trial = %d %v", price, trial)
	}
	if price, _, _ := StartPrice(grant, customer, 2, after); price != 20000 {
		t.Fatalf("paid start after the trial = %d", price)
	}
	noIncluded := grant
	noIncluded.IncludedStarts = 0
	if _, _, bind := StartPrice(noIncluded, customer, 0, after); !bind {
		t.Fatal("a customer with a trial does not bind the start price")
	}
	if _, _, bind := StartPrice(noIncluded, api.ProductCustomer{Plan: "starter"}, 0, after); bind {
		t.Fatal("a customer without a trial or included starts binds the start price")
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Plans: map[string]api.ProductPlan{"starter": {Features: map[string]api.ProductFeatureGrant{"export": noIncluded}}}}
	quote := func(at time.Time) string {
		return Quote("account", "app", "tenant", "export", api.ProductCatalog{Revision: "revision", Spec: spec}, customer, "2026-11", 0, at)
	}
	if quote(during) == quote(after) || quote(after) != quote(after.Add(time.Hour)) {
		t.Fatal("trial quotes do not follow the trial's end")
	}
}
