package productcontrol

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductTeamPolicyRequiresExplicitPermissionAndDelegation(t *testing.T) {
	for _, role := range []string{"owner", "admin", "member", "unknown"} {
		if EffectiveAccess(nil, role).ManageTeam || TeamRoleAllowed(nil, role, "member") {
			t.Fatal("team authority was enabled by default")
		}
	}
	access := &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"manager": {api.ProductPermissionManageTeam}, "member": {}, "operator": {api.ProductPermissionRunWork}}}
	if got := EffectiveAccess(access, "manager"); got != (api.ProductPermissions{ManageTeam: true}) {
		t.Fatalf("Manage team implied unrelated access: %+v", got)
	}
	tooMany := map[string][]string{}
	for i := range api.ProductControlAccessMaxRoles + 1 {
		tooMany[fmt.Sprintf("role-%d", i)] = []string{}
	}
	for name, delegations := range map[string]map[string][]string{
		"null": nil, "null targets": {"manager": nil}, "unknown source": {"foreign": {"member"}}, "unknown target": {"manager": {"foreign"}}, "duplicate": {"manager": {"member", "member"}}, "invalid": {"manager": {"*"}}, "uppercase": {"Manager": {"member"}}, "too many": tooMany,
	} {
		t.Run(name, func(t *testing.T) {
			if ValidateTeamPolicy(&api.ProductTeamPolicy{Delegations: delegations}, access) == nil {
				t.Fatal("invalid delegation accepted")
			}
		})
	}
	for _, p := range []*api.ProductTeamPolicy{nil, {Delegations: map[string][]string{}}, {Delegations: map[string][]string{"manager": {"member", "operator"}}}} {
		if err := ValidateTeamPolicy(p, access); err != nil {
			t.Fatal(err)
		}
	}
	p := &api.ProductTeamPolicy{Delegations: map[string][]string{"manager": {"operator"}}}
	if !TeamRoleAllowed(p, "manager", "operator") || TeamRoleAllowed(p, "manager", "member") || TeamRoleAllowed(p, "member", "operator") {
		t.Fatal("delegation crossed a role boundary")
	}
}

func TestProductTeamPolicyReviewPreservesCatalogAndCopies(t *testing.T) {
	spec := api.ProductCatalogSpec{AuthApp: "auth", Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}, Access: &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"manager": {api.ProductPermissionManageTeam}, "member": {}}}, SelfService: &api.ProductPlanPolicy{Transitions: map[string][]string{"starter": {}}}}
	base, err := Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	p := api.ProductTeamPolicy{Delegations: map[string][]string{"manager": {"member"}}}
	review, err := PreviewTeamPolicy(base, p)
	if err != nil {
		t.Fatal(err)
	}
	p.Delegations["manager"][0] = "manager"
	if base.Spec.Team != nil || review.Change.Policy.Delegations["manager"][0] != "member" || review.Before.Revision != base.Revision || review.Change.ExpectedHead != base.Revision || review.After.Revision == base.Revision {
		t.Fatal("review did not preserve its immutable base or private copy")
	}
	spec.Team = &review.Change.Policy
	after, err := Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	without := after.Spec
	without.Team = nil
	if !reflect.DeepEqual(without, base.Spec) {
		t.Fatal("delegation changed other product terms")
	}
	config := TeamPolicyConfiguration(after)
	config.Policy.Delegations["manager"][0] = "manager"
	if after.Spec.Team.Delegations["manager"][0] != "member" {
		t.Fatal("configuration aliases the catalog")
	}
}
