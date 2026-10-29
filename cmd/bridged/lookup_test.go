package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/state"
)

// A bridge into a company app routes the company's environment traffic to
// its creator's laptop and lets it call the company's dependencies. The relay
// checks the session on every request and every second of an open socket, so
// the bridge must end as soon as the creator leaves the company or loses the
// permission that created it, not when the session expires an hour later.
func TestRelayLookupFollowsTheCreatorsOrganizationAccess(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	creator, err := store.CreateAccount(ctx, "creator@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateAccount(ctx, "owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	bridge := func(orgID, slug string) string {
		t.Helper()
		project, err := store.CreateProject(ctx, state.Project{AccountID: creator.ID, OrgID: orgID, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		env, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: creator.ID, ProjectID: project.ID, Slug: "development"})
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: orgID, ProjectID: project.ID, Slug: slug,
			Type: state.AppTypeApp, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		session, _, err := devbridge.NewSession(devbridge.Scope{AccountID: creator.ID, DeveloperID: "alice", ProjectID: project.ID,
			EnvironmentID: env.ID, TargetAppID: app.ID}, now, now.Add(api.DevBridgeSessionTTL))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CreateDevBridge(ctx, session); err != nil {
			t.Fatal(err)
		}
		return session.ID
	}
	company := bridge(org.ID, "acme-api")
	own := bridge("", "own-api")
	lookup := relayLookup(store)
	opens := func(id string) bool {
		t.Helper()
		_, err := lookup(context.Background(), creator.ID, id)
		if err != nil && !errors.Is(err, devbridge.ErrUnauthorized) {
			t.Fatalf("lookup %s: %v", id, err)
		}
		return err == nil
	}

	if opens(company) {
		t.Fatal("a bridge into a company app opened for a creator who is not a member")
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	if !opens(company) || !opens(own) {
		t.Fatal("an admin creator's bridges do not open")
	}
	// Creating a bridge needs the owner/admin permission; a demotion ends it.
	if err := store.UpdateOrgMemberRole(ctx, org.ID, creator.ID, state.OrgRoleDeveloper); err != nil {
		t.Fatal(err)
	}
	if opens(company) {
		t.Error("a bridge kept working after its creator was demoted to developer")
	}
	if err := store.UpdateOrgMemberRole(ctx, org.ID, creator.ID, state.OrgRoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOrgStatus(ctx, org.ID, state.OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	if opens(company) {
		t.Error("a bridge kept working while its organization is suspended")
	}
	if err := store.UpdateOrgStatus(ctx, org.ID, state.OrgStatusActive); err != nil {
		t.Fatal(err)
	}
	if !opens(company) {
		t.Fatal("the bridge does not reopen for a restored admin in an active organization")
	}
	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if opens(company) {
		t.Error("a bridge kept working after its creator left the company")
	}
	if !opens(own) {
		t.Error("leaving a company ended the creator's bridge into their own app")
	}
}
