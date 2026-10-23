package productcontrol

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261022100735 — a signup policy needs an Auth app, offers 1 to
// ProductControlMaxPlans distinct plans the catalog has, and names stripe,
// paddle or no provider; each request's organization slug is stable.
func TestProductSignupPolicy(t *testing.T) {
	plans := map[string]api.ProductPlan{"starter": {Label: "Starter"}, "pro": {Label: "Pro"}}
	many := make([]string, api.ProductControlMaxPlans+1)
	for _, tc := range []struct {
		name    string
		authApp string
		policy  *api.ProductSignupPolicy
		valid   bool
	}{
		{"no policy", "", nil, true},
		{"offered plans", "auth", &api.ProductSignupPolicy{Plans: []string{"starter", "pro"}}, true},
		{"stripe", "auth", &api.ProductSignupPolicy{Plans: []string{"starter"}, Provider: "stripe"}, true},
		{"paddle", "auth", &api.ProductSignupPolicy{Plans: []string{"pro"}, Provider: "paddle"}, true},
		{"no auth app", "", &api.ProductSignupPolicy{Plans: []string{"starter"}}, false},
		{"no plans", "auth", &api.ProductSignupPolicy{}, false},
		{"too many plans", "auth", &api.ProductSignupPolicy{Plans: many}, false},
		{"unknown plan", "auth", &api.ProductSignupPolicy{Plans: []string{"enterprise"}}, false},
		{"repeated plan", "auth", &api.ProductSignupPolicy{Plans: []string{"starter", "starter"}}, false},
		{"unknown provider", "auth", &api.ProductSignupPolicy{Plans: []string{"starter"}, Provider: "lago"}, false},
	} {
		err := ValidateSignupPolicy(api.ProductCatalogSpec{AuthApp: tc.authApp, Plans: plans, Signup: tc.policy})
		if (err == nil) != tc.valid {
			t.Errorf("%s: err = %v, want valid %v", tc.name, err, tc.valid)
		}
	}
	policy := &api.ProductSignupPolicy{Plans: []string{"starter"}}
	if !SignupOffers(policy, "starter") || SignupOffers(policy, "pro") || SignupOffers(nil, "starter") {
		t.Fatal("SignupOffers offered the wrong plans")
	}
	slug := SignupOrganizationSlug("app-1", "req-1")
	if slug != SignupOrganizationSlug("app-1", "req-1") || slug == SignupOrganizationSlug("app-2", "req-1") || slug == SignupOrganizationSlug("app-1", "req-2") ||
		!strings.HasPrefix(slug, "ws-") || len(slug) != 23 {
		t.Fatalf("organization slug = %q", slug)
	}
}
