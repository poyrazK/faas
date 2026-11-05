package productcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// SignupProviders are the billing providers a signup can create a customer
// in (ADR-20261022100735).
var SignupProviders = map[string]bool{"stripe": true, "paddle": true}

// ValidateSignupPolicy checks a catalog's signup policy: an Auth app to sign
// up with, distinct offered plans the catalog has, and a known provider.
func ValidateSignupPolicy(spec api.ProductCatalogSpec) error {
	p := spec.Signup
	if p == nil {
		return nil
	}
	if spec.AuthApp == "" || len(p.Plans) == 0 || len(p.Plans) > api.ProductControlMaxPlans || p.Provider != "" && !SignupProviders[p.Provider] {
		return fmt.Errorf("signup requires auth_app, 1 to %d offered plans and provider stripe, paddle or none", api.ProductControlMaxPlans)
	}
	seen := map[string]bool{}
	for _, plan := range p.Plans {
		if _, ok := spec.Plans[plan]; !ok || seen[plan] {
			return fmt.Errorf("signup offers unknown or repeated plan %q", plan)
		}
		seen[plan] = true
	}
	return nil
}

// SignupOffers reports whether the policy offers plan at signup.
func SignupOffers(p *api.ProductSignupPolicy, plan string) bool {
	if p == nil {
		return false
	}
	for _, offered := range p.Plans {
		if offered == plan {
			return true
		}
	}
	return false
}

// SignupOrganizationSlug is the Auth organization slug of one signup request,
// derived from the app and request ID so a retry finds the organization its
// first attempt created instead of creating another.
func SignupOrganizationSlug(appID, requestID string) string {
	digest := sha256.Sum256([]byte(appID + "/" + requestID))
	return "ws-" + hex.EncodeToString(digest[:10])
}
