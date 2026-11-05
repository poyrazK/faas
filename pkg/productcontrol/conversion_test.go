package productcontrol

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261022180921 — a plan requiring a payment method offers a trial and
// names another plan as its fallback, which costs nothing and requires
// nothing; a Paddle signup cannot offer it.
func TestValidateTrialConversion(t *testing.T) {
	plans := func(pro api.ProductPlan, free api.ProductPlan) map[string]api.ProductPlan {
		return map[string]api.ProductPlan{"pro": pro, "free": free}
	}
	pro := api.ProductPlan{Label: "Pro", TrialDays: 14, MonthlyFeeMillicents: 2900000, RequiresPaymentMethod: true, FallbackPlan: "free"}
	free := api.ProductPlan{Label: "Free"}
	for _, tc := range []struct {
		name  string
		spec  api.ProductCatalogSpec
		valid bool
	}{
		{"no requirement", api.ProductCatalogSpec{Plans: plans(api.ProductPlan{Label: "Pro"}, free)}, true},
		{"requirement with fallback", api.ProductCatalogSpec{Plans: plans(pro, free)}, true},
		{"stripe signup", api.ProductCatalogSpec{AuthApp: "auth", Plans: plans(pro, free), Signup: &api.ProductSignupPolicy{Plans: []string{"pro"}, Provider: "stripe"}}, true},
		{"paddle signup without the plan", api.ProductCatalogSpec{AuthApp: "auth", Plans: plans(pro, free), Signup: &api.ProductSignupPolicy{Plans: []string{"free"}, Provider: "paddle"}}, true},
		{"paddle signup offering it", api.ProductCatalogSpec{AuthApp: "auth", Plans: plans(pro, free), Signup: &api.ProductSignupPolicy{Plans: []string{"pro"}, Provider: "paddle"}}, false},
		{"no trial", api.ProductCatalogSpec{Plans: plans(api.ProductPlan{Label: "Pro", RequiresPaymentMethod: true, FallbackPlan: "free"}, free)}, false},
		{"no fallback", api.ProductCatalogSpec{Plans: plans(api.ProductPlan{Label: "Pro", TrialDays: 14, RequiresPaymentMethod: true}, free)}, false},
		{"unknown fallback", api.ProductCatalogSpec{Plans: plans(api.ProductPlan{Label: "Pro", TrialDays: 14, RequiresPaymentMethod: true, FallbackPlan: "basic"}, free)}, false},
		{"fallback to itself", api.ProductCatalogSpec{Plans: plans(api.ProductPlan{Label: "Pro", TrialDays: 14, RequiresPaymentMethod: true, FallbackPlan: "pro"}, free)}, false},
		{"fallback with a fee", api.ProductCatalogSpec{Plans: plans(pro, api.ProductPlan{Label: "Free", MonthlyFeeMillicents: 100})}, false},
		{"fallback with a trial", api.ProductCatalogSpec{Plans: plans(pro, api.ProductPlan{Label: "Free", TrialDays: 7})}, false},
		{"fallback without requirement", api.ProductCatalogSpec{Plans: plans(api.ProductPlan{Label: "Pro", FallbackPlan: "free"}, free)}, false},
	} {
		if err := ValidateTrialConversion(tc.spec); (err == nil) != tc.valid {
			t.Errorf("%s: err = %v, want valid %v", tc.name, err, tc.valid)
		}
	}
	if !UsesPlanPricing(api.ProductCatalogSpec{Plans: plans(pro, free)}) {
		t.Fatal("a payment method requirement is not gated as plan pricing")
	}
}
