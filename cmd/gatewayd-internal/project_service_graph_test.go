package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type frozenGraphTestStore struct {
	MemStore *state.MemStore
	route    state.EnvironmentGitOpsServiceBindingRoute
}

func (s frozenGraphTestStore) ResolveProjectServiceBinding(context.Context, string, string, string, ...string) (state.EnvironmentGitOpsServiceBindingRoute, bool, bool, error) {
	return s.route, true, true, nil
}

func TestFrozenProjectBindingUsesCapturedProtocolAndTenantBoundary(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "frozen@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "frozen"})
	if err != nil {
		t.Fatal(err)
	}
	caller, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "web", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "api", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	frozen := frozenGraphTestStore{MemStore: store, route: state.EnvironmentGitOpsServiceBindingRoute{AccountID: account.ID,
		TargetAppID: target.ID, TargetDeploymentID: "target", ReleaseSetID: "graph", AppProtocol: "http2", WebSocketEnabled: true}}
	binding, managed, found, err := newServiceProxyEnvironmentBindingResolver(statetest.NewMem(frozen))(ctx, caller.ID, "caller", "api")
	if err != nil || !managed || !found || !binding.FrozenPolicy || binding.Target.AppProtocol != "http2" || !binding.Target.WebSocketEnabled {
		t.Fatalf("frozen service settings lost: %+v %v", binding, err)
	}
	identity, err := newServiceProxyBoundAuthorizer(store)(ctx, caller.ID, target.ID)
	if err != nil || identity.AppID != caller.ID || identity.AccountID != account.ID {
		t.Fatal("tenant identity rejected", identity, err)
	}
	other, err := store.CreateAccount(ctx, "other-frozen@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "outsider", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = newServiceProxyBoundAuthorizer(store)(ctx, outsider.ID, target.ID); !errors.Is(err, gateway.ErrServiceProxyDenied) {
		t.Fatal("frozen route widened tenant boundary", err)
	}
}
