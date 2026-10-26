package main

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// runningForks is a forkReader over one running fork per app.
type runningForks struct {
	forks map[string]state.AppFork
	apps  map[string]state.App
}

func (r runningForks) AppForkForApp(_ context.Context, appID, forkID string) (state.AppFork, error) {
	if fork, ok := r.forks[appID]; ok && fork.ID == forkID {
		return fork, nil
	}
	return state.AppFork{}, state.ErrNotFound
}

func (r runningForks) InstanceByID(_ context.Context, id string) (state.Instance, error) {
	for _, fork := range r.forks {
		if *fork.InstanceID == id {
			return state.Instance{ID: id, AppID: fork.AppID, DeploymentID: fork.DeploymentID, NodeID: "node-1",
				Mode: string(state.InstanceModeFork), State: string(state.StateRunning)}, nil
		}
	}
	return state.Instance{}, state.ErrNotFound
}

func (r runningForks) DeploymentByID(_ context.Context, id string) (state.Deployment, error) {
	return state.Deployment{ID: id, Status: state.DeployLive}, nil
}

func (r runningForks) AppByID(_ context.Context, id string) (state.App, error) {
	if app, ok := r.apps[id]; ok {
		return app, nil
	}
	return state.App{}, state.ErrNotFound
}

// A production fork holds a copy of a running production instance and is
// reached with a bearer token for up to its TTL. A company cannot cancel a
// fork its creator made, so the token must stop working as soon as the
// creator leaves the company or loses the permission that created it.
func TestForkTargetLoaderFollowsTheCreatorsOrganizationAccess(t *testing.T) {
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
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	const token = "gfk_secret-token"
	forks := runningForks{forks: map[string]state.AppFork{}, apps: map[string]state.App{}}
	newFork := func(orgID, slug string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: orgID, Slug: slug, Type: state.AppTypeApp, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		instance := "instance-" + slug
		forks.apps[app.ID] = app
		forks.forks[app.ID] = state.AppFork{ID: "fork-" + slug, AccountID: creator.ID, AppID: app.ID, DeploymentID: "dep-" + slug,
			RequestedBy: "account:" + creator.ID, Status: state.AppForkRunning, ExpiresAt: time.Now().Add(time.Hour),
			InstanceID: &instance, AccessTokenHash: api.AppForkAccessTokenHash(token)}
		return app
	}
	company := newFork(org.ID, "acme-api")
	own := newFork("", "own-api")
	load := forkTargetLoader(forks, store, time.Now)
	routes := func(app state.App, token string) bool {
		t.Helper()
		target, found, err := load(context.Background(), app.ID, forks.forks[app.ID].ID, token)
		if err != nil {
			t.Fatalf("load fork of %s: %v", app.Slug, err)
		}
		if found && target.InstanceID != *forks.forks[app.ID].InstanceID {
			t.Fatalf("fork of %s routed to %q", app.Slug, target.InstanceID)
		}
		return found
	}

	if !routes(company, token) || !routes(own, token) {
		t.Fatal("an admin creator's forks do not route")
	}
	if routes(company, "gfk_wrong") {
		t.Fatal("a wrong token routed")
	}
	if err := store.UpdateOrgMemberRole(ctx, org.ID, creator.ID, state.OrgRoleDeveloper); err != nil {
		t.Fatal(err)
	}
	if routes(company, token) {
		t.Error("a fork kept routing after its creator was demoted to developer")
	}
	if err := store.UpdateOrgMemberRole(ctx, org.ID, creator.ID, state.OrgRoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if routes(company, token) {
		t.Error("a fork kept routing after its creator left the company")
	}
	if !routes(own, token) {
		t.Error("leaving a company stopped the creator's fork of their own app")
	}
}
