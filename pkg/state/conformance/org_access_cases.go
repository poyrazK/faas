package conformance

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testOrgAccessAndOrgProjects pins #4882's organization access reads. A
// member's snapshot names their role, a non-member has none, a fence at the
// current revision passes and a stale one is revoked; projects resolve by
// their persisted organization, never by another organization's slug. Both
// stores must agree because these reads authorize every company request.
func testOrgAccessAndOrgProjects(t *testing.T, fx *Fixture) {
	t.Helper()
	// An explicit team organization: MemStore keeps lightweight account-only
	// fixtures without a personal org, so the personal path differs by design.
	org, err := fx.Store.CreateOrg(fx.Ctx, state.Org{Slug: "org-access-" + uuid.NewString()[:8], Name: "Org access", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	if err := fx.Store.AddOrgMember(fx.Ctx, org.ID, fx.Account.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatalf("AddOrgMember: %v", err)
	}
	project, err := fx.Store.CreateProject(fx.Ctx, state.Project{AccountID: fx.Account.ID, OrgID: org.ID, Slug: "org-access-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if project.OrgID != org.ID {
		t.Fatalf("CreateProject persisted org %q, want %q", project.OrgID, org.ID)
	}
	orgID, stranger := org.ID, uuid.NewString()

	snapshot, err := fx.Store.OrgAccessSnapshot(fx.Ctx, orgID, fx.Account.ID)
	if err != nil || snapshot.Role != state.OrgRoleOwner {
		t.Fatalf("OrgAccessSnapshot(owner) = %+v, %v; want owner role", snapshot, err)
	}
	if _, err := fx.Store.OrgAccessSnapshot(fx.Ctx, orgID, stranger); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("OrgAccessSnapshot(non-member) err = %v, want ErrNotFound", err)
	}

	if err := fx.Store.CheckOrgAccess(fx.Ctx); err != nil {
		t.Fatalf("CheckOrgAccess without a fence = %v, want nil", err)
	}
	current, cancel := state.WithOrgAccessFence(fx.Ctx, state.OrgAccessFence{OrgID: orgID, AccountID: fx.Account.ID, Revision: snapshot.Revision})
	defer cancel(nil)
	if err := fx.Store.CheckOrgAccess(current); err != nil {
		t.Fatalf("CheckOrgAccess at the current revision = %v, want nil", err)
	}
	stale, cancelStale := state.WithOrgAccessFence(fx.Ctx, state.OrgAccessFence{OrgID: orgID, AccountID: fx.Account.ID, Revision: snapshot.Revision + 1})
	defer cancelStale(nil)
	if err := fx.Store.CheckOrgAccess(stale); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Fatalf("CheckOrgAccess at a stale revision = %v, want ErrOrgAccessRevoked", err)
	}

	got, err := fx.Store.ProjectByOrgSlug(fx.Ctx, orgID, project.Slug)
	if err != nil || got.ID != project.ID {
		t.Fatalf("ProjectByOrgSlug(owner org) = %s, %v; want %s", got.ID, err, project.ID)
	}
	if _, err := fx.Store.ProjectByOrgSlug(fx.Ctx, stranger, project.Slug); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ProjectByOrgSlug(other org) err = %v, want ErrNotFound", err)
	}
	listed, err := fx.Store.ListProjectsForOrg(fx.Ctx, orgID)
	if err != nil {
		t.Fatalf("ListProjectsForOrg: %v", err)
	}
	found := false
	for _, p := range listed {
		if p.OrgID != orgID {
			t.Fatalf("ListProjectsForOrg(%s) returned project %s of org %s", orgID, p.ID, p.OrgID)
		}
		found = found || p.ID == project.ID
	}
	if !found {
		t.Fatalf("ListProjectsForOrg(%s) omitted project %s", orgID, project.ID)
	}
	if other, err := fx.Store.ListProjectsForOrg(fx.Ctx, stranger); err != nil || len(other) != 0 {
		t.Fatalf("ListProjectsForOrg(other org) = %d projects, %v; want none", len(other), err)
	}
}
