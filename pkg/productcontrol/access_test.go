// ADR-1598: live Auth roles grant bounded product permissions.
package productcontrol

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductAccessDefaultsAndExplicitPolicy(t *testing.T) {
	for _, tc := range []struct {
		role string
		want api.ProductPermissions
	}{
		{"owner", api.ProductPermissions{ViewWork: true, RunWork: true, ViewBilling: true}},
		{"admin", api.ProductPermissions{ViewWork: true, RunWork: true, ViewBilling: true}},
		{"member", api.ProductPermissions{ViewWork: true}},
		{"operator", api.ProductPermissions{}},
		{"", api.ProductPermissions{}},
	} {
		t.Run(tc.role, func(t *testing.T) {
			if got := EffectiveAccess(nil, tc.role); got != tc.want {
				t.Fatalf("default %s: %+v", tc.role, got)
			}
		})
	}
	p := api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"operator": {api.ProductPermissionRunWork}, "billing": {api.ProductPermissionViewBilling}, "blocked": {}}}
	if got := EffectiveAccess(&p, "operator"); !got.RunWork || !got.ViewWork || got.ViewBilling {
		t.Fatalf("operator: %+v", got)
	}
	if got := EffectiveAccess(&p, "billing"); !got.ViewBilling || got.ViewWork || got.RunWork {
		t.Fatalf("billing: %+v", got)
	}
	for _, role := range []string{"owner", "admin", "member", "blocked", "unknown"} {
		if got := EffectiveAccess(&p, role); got != (api.ProductPermissions{}) {
			t.Fatalf("explicit policy implicitly grants %s: %+v", role, got)
		}
	}
	defaults := DefaultAccess()
	defaults.Roles["member"][0] = api.ProductPermissionRunWork
	if EffectiveAccess(nil, "member").RunWork {
		t.Fatal("default policy aliases caller memory")
	}
}

func TestProductAccessRejectsInvalidPolicy(t *testing.T) {
	tooMany := map[string][]api.ProductPermission{}
	for i := range api.ProductControlAccessMaxRoles + 1 {
		tooMany[fmt.Sprintf("role-%d", i)] = []api.ProductPermission{}
	}
	for name, roles := range map[string]map[string][]api.ProductPermission{
		"null roles":           nil,
		"missing role":         {"": {}},
		"uppercase":            {"Admin": {}},
		"long role":            {strings.Repeat("a", api.ProductControlAccessRoleMaxBytes+1): {}},
		"wildcard role":        {"*": {api.ProductPermissionRunWork}},
		"unknown permission":   {"member": {"product:admin"}},
		"duplicate permission": {"member": {api.ProductPermissionRunWork, api.ProductPermissionRunWork}},
		"null permissions":     {"member": nil},
		"too many roles":       tooMany,
	} {
		t.Run(name, func(t *testing.T) {
			p := &api.ProductAccessPolicy{Roles: roles}
			if ValidateAccess(p) == nil {
				t.Fatal("invalid access policy accepted")
			}
			if EffectiveAccess(p, "member") != (api.ProductPermissions{}) {
				t.Fatal("invalid policy granted access")
			}
		})
	}
	if err := ValidateAccess(&api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{}}); err != nil {
		t.Fatal("explicit deny-all rejected", err)
	}
}

func TestProductAccessPublicationKeepsCommercialTermsAndPrivateCopies(t *testing.T) {
	spec := api.ProductCatalogSpec{AuthApp: "auth", Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	before, err := Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultAccess()
	policy.Roles["operator"] = []api.ProductPermission{api.ProductPermissionRunWork}
	preview, err := PreviewAccess(before, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Before.Default || preview.After.Default || preview.Change.ExpectedHead != before.Revision || preview.After.Revision == before.Revision {
		t.Fatalf("invalid preview: %+v", preview)
	}
	if before.Spec.Access != nil || before.Spec.Plans["starter"].Features["export"].UnitPriceMillicents != 25000 {
		t.Fatal("preview changed base catalog")
	}
	policy.Roles["operator"][0] = api.ProductPermissionViewBilling
	if !EffectiveAccess(&preview.Change.Policy, "operator").RunWork {
		t.Fatal("preview aliases caller permissions")
	}
	withPolicy := before.Spec
	withPolicy.Access = &preview.Change.Policy
	published, err := Compile(withPolicy)
	if err != nil {
		t.Fatal(err)
	}
	configuration := AccessConfiguration(published)
	configuration.Policy.Roles["operator"][0] = api.ProductPermissionViewBilling
	if !EffectiveAccess(published.Spec.Access, "operator").RunWork {
		t.Fatal("access projection aliases catalog permissions")
	}
	copy, err := Compile(before.Spec)
	if err != nil || copy.Revision != before.Revision {
		t.Fatal("legacy omitted access changed its content revision", err)
	}
}
