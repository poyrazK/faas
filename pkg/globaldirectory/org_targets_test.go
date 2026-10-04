package globaldirectory

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func exerciseOrgTargets(t *testing.T, s Store, authority state.Store, owner Owner, org state.Org, actor state.Account) {
	t.Helper()
	shared := s.(OrgTargetStore)
	creator, err := authority.CreateAccount(t.Context(), "binding-creator@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err = authority.AddOrgMember(t.Context(), org.ID, creator.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	app, err := s.(OwnerStore).CreateOrgApp(t.Context(), owner.ID, creator.ID, org.Plan, "binding-global", 3)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.(OwnerStore).CreateOrgApp(t.Context(), owner.ID, actor.ID, org.Plan, "binding-other", 3)
	if err != nil {
		t.Fatal(err)
	}
	cell, err := s.PutCell(t.Context(), Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: "https://fsn.example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{CellID: cell.ID, Region: cell.Region, CellGeneration: cell.Generation, RegionalAccountID: uuid.NewString(), RegionalOrgID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "regional-team"}
	first, err := shared.AttachOrgTarget(t.Context(), owner.ID, actor.ID, app.Slug, b)
	if err != nil || first.Binding != b {
		t.Fatalf("shared attachment: %+v %v", first, err)
	}
	if err = authority.RemoveOrgMember(t.Context(), org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if err = authority.MarkAccountDeletionPending(t.Context(), creator.ID); err != nil {
		t.Fatal(err)
	}
	if err = authority.DeleteAccount(t.Context(), creator.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(t.Context(), s, owner.ID, app.Slug, b.Region); err != nil || got.Binding != b {
		t.Fatalf("creator deletion lost tenant binding: %+v %v", got, err)
	}
	// Different app creators in one regional organization use the same mapping.
	b2 := b
	b2.RegionalAccountID = uuid.NewString()
	b2.RegionalAppID = uuid.NewString()
	b2.RegionalAppSlug = "regional-other"
	if _, err = shared.AttachOrgTarget(t.Context(), owner.ID, actor.ID, other.Slug, b2); err != nil {
		t.Fatalf("creator-scoped mapping: %v", err)
	}
	if _, err = s.PlanRelease(t.Context(), owner.ID, app.Slug, uuid.NewString(), releaseTestImage); !errors.Is(err, ErrOrgTargetsUnavailable) {
		t.Fatalf("shared runtime bypass: %v", err)
	}
	if _, err = s.AttachTarget(t.Context(), owner.ID, app.Slug, b); !errors.Is(err, ErrOrgTargetsUnavailable) {
		t.Fatalf("actor-free shared mutation: %v", err)
	}
	changed := b
	changed.RegionalOrgID = uuid.NewString()
	if _, err = shared.AttachOrgTarget(t.Context(), owner.ID, actor.ID, app.Slug, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("org replaced: %v", err)
	}
	admin, err := authority.CreateAccount(t.Context(), "binding-admin@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err = authority.AddOrgMember(t.Context(), org.ID, admin.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	if retry, err := shared.AttachOrgTarget(t.Context(), owner.ID, admin.ID, app.Slug, b); err != nil || retry.Binding != first.Binding {
		t.Fatalf("admin retry: %+v %v", retry, err)
	}
	if err = authority.UpdateOrgMemberRole(t.Context(), org.ID, admin.ID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	if err = shared.DetachOrgTarget(t.Context(), owner.ID, admin.ID, app.Slug, b.Region); !errors.Is(err, ErrOwnerForbidden) {
		t.Fatalf("demoted detach: %v", err)
	}
	if err = shared.DetachOrgTarget(t.Context(), owner.ID, actor.ID, app.Slug, b.Region); err != nil {
		t.Fatal(err)
	}
	if _, err = shared.AttachOrgTarget(t.Context(), owner.ID, actor.ID, app.Slug, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("detach released tenant anchor: %v", err)
	}
	if _, err = shared.AttachOrgTarget(t.Context(), owner.ID, actor.ID, app.Slug, b); err != nil {
		t.Fatal(err)
	}
	newOrg, err := authority.CreateOrg(t.Context(), state.Org{Slug: "another-binding-team", Name: "Other Team", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err = authority.AddOrgMember(t.Context(), newOrg.ID, actor.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	newOwner, err := s.(OwnerStore).ResolveOrgOwner(t.Context(), newOrg.ID)
	if err != nil {
		t.Fatal(err)
	}
	newApp, err := s.(OwnerStore).CreateOrgApp(t.Context(), newOwner.ID, actor.ID, newOrg.Plan, "another-global", 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = shared.AttachOrgTarget(t.Context(), newOwner.ID, actor.ID, newApp.Slug, b2); !errors.Is(err, ErrConflict) {
		t.Fatalf("regional organization claimed twice: %v", err)
	}
	if err = authority.UpdateOrgStatus(t.Context(), org.ID, state.OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err = shared.AttachOrgTarget(t.Context(), owner.ID, actor.ID, app.Slug, b); !errors.Is(err, ErrOwnerInactive) {
		t.Fatalf("inactive tenant: %v", err)
	}
}

func TestGlobalOrganizationTargetMemoryContract(t *testing.T) {
	a := state.NewMemStore()
	actor, err := a.CreateAccount(t.Context(), "binding-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	o, err := a.CreateOrg(t.Context(), state.Org{Slug: "binding-team", Name: "Binding Team", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.AddOrgMember(t.Context(), o.ID, actor.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	s := NewMemoryStoreWithOrgAuthority(a)
	root, err := s.ResolveOrgOwner(t.Context(), o.ID)
	if err != nil {
		t.Fatal(err)
	}
	exerciseOrgTargets(t, s, a, root, o, actor)
}
