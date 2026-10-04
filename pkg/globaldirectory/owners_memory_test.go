package globaldirectory

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGlobalOrganizationMemoryAdmission(t *testing.T) {
	authority := state.NewMemStore()
	actor, err := authority.CreateAccount(t.Context(), "org-actor@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := authority.CreateOrg(t.Context(), state.Org{Slug: "global-team", Name: "Global Team", Plan: api.PlanHobby})
	if err != nil {
		t.Fatal(err)
	}
	if err = authority.AddOrgMember(t.Context(), org.ID, actor.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	s := NewMemoryStoreWithOrgAuthority(authority)
	owner, err := s.ResolveOrgOwner(t.Context(), org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if owner.ID == actor.ID || owner.ID == org.ID || !owner.Shared() {
		t.Fatalf("owner is not an independent tenant: %+v", owner)
	}
	if retry, err := s.ResolveOrgOwner(t.Context(), org.ID); err != nil || retry != owner {
		t.Fatalf("owner retry: %+v %v", retry, err)
	}
	app, err := s.CreateOrgApp(t.Context(), owner.ID, actor.ID, org.Plan, "org-global", 1)
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := s.CreateOrgApp(t.Context(), owner.ID, actor.ID, org.Plan, app.Slug, 1); err != nil || retry.ID != app.ID {
		t.Fatalf("app retry: %+v %v", retry, err)
	}
	if _, err = s.CreateOrgApp(t.Context(), owner.ID, actor.ID, org.Plan, "org-overflow", 1); !errors.Is(err, ErrLimit) {
		t.Fatalf("quota: %v", err)
	}
	if _, err = s.CreateOrgApp(t.Context(), owner.ID, actor.ID, api.PlanPro, "stale-plan", 3); !errors.Is(err, ErrOwnerPlanChanged) {
		t.Fatalf("stale plan: %v", err)
	}
	developer, err := authority.CreateAccount(t.Context(), "developer@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = authority.AddOrgMember(t.Context(), org.ID, developer.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	if err = authority.UpdateOrgMemberRole(t.Context(), org.ID, developer.ID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateOrgApp(t.Context(), owner.ID, developer.ID, org.Plan, "role-bypass", 3); !errors.Is(err, ErrOwnerForbidden) {
		t.Fatalf("role: %v", err)
	}
	if err = authority.UpdateOrgStatus(t.Context(), org.ID, state.OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateOrgApp(t.Context(), owner.ID, actor.ID, org.Plan, "inactive-bypass", 3); !errors.Is(err, ErrOwnerInactive) {
		t.Fatalf("inactive org: %v", err)
	}
}
