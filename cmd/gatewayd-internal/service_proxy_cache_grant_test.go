// adr: 20261124174500 — binding a managed cache grants the call.
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// The 2026-11-24 voting-app eval: a Compose project's workers were given
// REDIS_URL for a managed cache, and every connection was refused, because a
// cache is never a project member and the project policy denies the rest.
func TestServiceProxyAuthorizerManagedCacheGrant(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "cache-grant-authz@local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "voting"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(app state.App) state.App {
		t.Helper()
		app.AccountID, app.Type, app.RAMMB, app.Status = account.ID, state.AppTypeApp, 128, state.AppActive
		created, createErr := store.CreateApp(ctx, app)
		if createErr != nil {
			t.Fatalf("CreateApp(%s): %v", app.Slug, createErr)
		}
		return created
	}
	worker := create(state.App{ProjectID: project.ID, Slug: "voting-worker", WorkloadName: "worker",
		Manifest: state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyProject}})
	declared := create(state.App{ProjectID: project.ID, Slug: "voting-vote", WorkloadName: "vote",
		Manifest: state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyDeclared}})
	cache := create(state.App{Slug: "voting-redis"})
	plain := create(state.App{Slug: "voting-plain"})
	if _, err := store.CreateManagedValkeyCache(ctx, state.ManagedValkeyCache{AppID: cache.ID, AccountID: account.ID,
		MemoryMB: 128, EvictionPolicy: "allkeys-lru"}); err != nil {
		t.Fatal(err)
	}
	authorize := newServiceProxyAuthorizer(store)
	pin := newServicePolicyPinner(serviceSnapshotStoreFixture{reader: store})
	check := func(name string, caller, target state.App, want error) gateway.ServicePolicySnapshot {
		t.Helper()
		if _, err := authorize(ctx, caller.ID, target.ID); want == nil && err != nil || want != nil && !errors.Is(err, want) {
			t.Errorf("%s: authorize(%s -> %s) = %v, want %v", name, caller.Slug, target.Slug, err, want)
		}
		snapshot, err := pin(ctx, caller.ID, target.Slug, false)
		if err != nil || !snapshot.Found {
			t.Fatalf("%s: pin(%s -> %s) = %+v, %v; want a found target", name, caller.Slug, target.Slug, snapshot, err)
		}
		if got := snapshot.AuthorizationError; want == nil && got != nil || want != nil && !errors.Is(got, want) {
			t.Errorf("%s: pinned authorize(%s -> %s) = %v, want %v", name, caller.Slug, target.Slug, got, want)
		}
		return snapshot
	}

	before := check("no grant", worker, cache, gateway.ErrServiceProxyBindingDenied)
	check("declared caller without a grant", declared, cache, gateway.ErrServiceProxyBindingDenied)
	for _, app := range []state.App{worker, declared} {
		if err := store.GrantManagedValkeyCache(ctx, cache.ID, app.ID, account.ID); err != nil {
			t.Fatal(err)
		}
	}
	after := check("granted", worker, cache, nil)
	check("granted declared caller", declared, cache, nil)
	if before.InputRevision == after.InputRevision {
		t.Errorf("granting the cache left the policy revision at %s; a pinned call must see the grant change", before.InputRevision)
	}
	// A grant names one cache; it opens nothing else outside the project.
	check("grant does not reach another app", worker, plain, gateway.ErrServiceProxyBindingDenied)

	if revoked, err := store.RevokeManagedValkeyCacheGrant(ctx, cache.ID, worker.ID); err != nil || !revoked {
		t.Fatalf("RevokeManagedValkeyCacheGrant = %v, %v; want true", revoked, err)
	}
	check("revoked", worker, cache, gateway.ErrServiceProxyBindingDenied)
	check("other grant kept", declared, cache, nil)

	// A grant counts only while its target is still a cache.
	if err := store.DeleteManagedValkeyCache(ctx, cache.ID); err != nil {
		t.Fatal(err)
	}
	check("no longer a cache", declared, cache, gateway.ErrServiceProxyBindingDenied)
}
