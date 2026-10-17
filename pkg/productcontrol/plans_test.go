// ADR-1599: self-service transitions are explicit and separate from permissions.
package productcontrol

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductPlanPolicyValidation(t *testing.T) {
	plans := map[string]api.ProductPlan{"starter": {}, "pro": {}}
	tooMany := map[string][]string{}
	for i := range api.ProductControlPlanPolicyMaxSources + 1 {
		tooMany[fmt.Sprintf("plan-%d", i)] = []string{"pro"}
	}
	for name, transitions := range map[string]map[string][]string{
		"null map": nil, "null targets": {"starter": nil}, "invalid source": {"Starter": {"pro"}}, "missing target": {"starter": {"missing"}}, "duplicate targets": {"starter": {"pro", "pro"}}, "invalid target": {"starter": {"*"}}, "too many sources": tooMany,
	} {
		t.Run(name, func(t *testing.T) {
			if ValidatePlanPolicy(&api.ProductPlanPolicy{Transitions: transitions}, plans) == nil {
				t.Fatal("invalid choices accepted")
			}
		})
	}
	for _, policy := range []*api.ProductPlanPolicy{nil, {Transitions: map[string][]string{}}, {Transitions: map[string][]string{"retired": {"pro"}, "starter": {"starter", "pro"}}}} {
		if err := ValidatePlanPolicy(policy, plans); err != nil {
			t.Fatal("valid transition rejected", err)
		}
	}
	if PlanOffered(nil, "starter", "pro") || PlanOffered(&api.ProductPlanPolicy{Transitions: map[string][]string{}}, "starter", "pro") {
		t.Fatal("self service opened by default")
	}
	for _, role := range []string{"owner", "admin", "member"} {
		if EffectiveAccess(nil, role).ManagePlan {
			t.Fatal("Manage plan granted by default")
		}
	}
	access := &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"manager": {api.ProductPermissionManagePlan}}}
	if got := EffectiveAccess(access, "manager"); got != (api.ProductPermissions{ManagePlan: true}) {
		t.Fatalf("Manage plan implied other permissions: %+v", got)
	}
}

func TestProductPlanPolicyReviewPreservesTermsAndPrivateCopies(t *testing.T) {
	spec := api.ProductCatalogSpec{AuthApp: "auth", Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}, "pro": {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 100, UnitPriceMillicents: 10000}}}}, Access: &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"manager": {api.ProductPermissionManagePlan}}}}
	base, err := Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	policy := api.ProductPlanPolicy{Transitions: map[string][]string{"starter": {"pro"}}}
	p, err := PreviewPlanPolicy(base, policy)
	if err != nil {
		t.Fatal(err)
	}
	if p.Before.Revision != base.Revision || p.Change.ExpectedHead != base.Revision || p.After.Revision == base.Revision {
		t.Fatal("review was not bound to the immutable base")
	}
	policy.Transitions["starter"][0] = "starter"
	if p.Change.Policy.Transitions["starter"][0] != "pro" || base.Spec.SelfService != nil {
		t.Fatal("preview aliases caller or rewrites base")
	}
	updated := base.Spec
	updated.SelfService = &p.Change.Policy
	compiled, err := Compile(updated)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(compiled.Spec.Plans, base.Spec.Plans) || !reflect.DeepEqual(compiled.Spec.Features, base.Spec.Features) || !reflect.DeepEqual(compiled.Spec.Access, base.Spec.Access) || compiled.Spec.AuthApp != base.Spec.AuthApp {
		t.Fatal("policy changed other product terms")
	}
	config := PlanPolicyConfiguration(compiled)
	config.Policy.Transitions["starter"][0] = "starter"
	if compiled.Spec.SelfService.Transitions["starter"][0] != "pro" {
		t.Fatal("configuration aliases catalog")
	}
}
