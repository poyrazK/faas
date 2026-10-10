// adr: 950
package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func bindingsFor(services ...string) []api.AppServiceBinding {
	out := make([]api.AppServiceBinding, 0, len(services))
	for _, s := range services {
		out = append(out, api.AppServiceBinding{Binding: s, Service: s})
	}
	return out
}

func createWakeAheadApp(t *testing.T, store state.Store, accountID, slug string, manifest state.AppManifest) state.App {
	t.Helper()
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: accountID, Slug: slug, Type: state.AppTypeApp, RAMMB: 128,
		Status: state.AppActive, Manifest: manifest,
	})
	if err != nil {
		t.Fatalf("CreateApp %s: %v", slug, err)
	}
	return app
}

func planTargetIDs(plan gateway.ServiceWakeAheadPlan) []string {
	ids := make([]string, 0, len(plan.Targets))
	for _, target := range plan.Targets {
		ids = append(ids, target.ID)
	}
	slices.Sort(ids)
	return ids
}

// The plan must reuse the proxy's own resolver and authorizer: a binding that
// names no app, or an app the caller may not call, is never restored.
func TestServiceWakeAheadPlannerUsesProxyPolicy(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "wake-ahead@local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	auth := createWakeAheadApp(t, store, acct.ID, "auth", state.AppManifest{})
	billing := createWakeAheadApp(t, store, acct.ID, "billing", state.AppManifest{})
	foreign := seedApp(t, store, "foreign", api.PlanPro) // another account
	caller := createWakeAheadApp(t, store, acct.ID, "public-api", state.AppManifest{
		ServiceWakeAhead: api.ServiceWakeAheadDeclared,
		ServiceBindings:  bindingsFor("auth", "billing", "ghost", foreign.Slug, "auth"),
	})

	planner := newServiceWakeAheadPlanner(store, newServiceProxyResolver(store), newServiceProxyAuthorizer(store))
	plan, err := planner(ctx, caller.ID)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	want := []string{auth.ID, billing.ID}
	slices.Sort(want)
	if got := planTargetIDs(plan); !slices.Equal(got, want) {
		t.Fatalf("targets = %v, want %v (duplicate binding must collapse)", got, want)
	}
	if plan.Unresolved != 1 || plan.Denied != 1 || plan.Truncated != 0 {
		t.Fatalf("unresolved/denied/truncated = %d/%d/%d, want 1/1/0", plan.Unresolved, plan.Denied, plan.Truncated)
	}
	for _, target := range plan.Targets {
		if target.Plan != api.PlanPro || target.AccountID != acct.ID {
			t.Fatalf("target %s projected plan=%q account=%q", target.ID, target.Plan, target.AccountID)
		}
	}
}

// ADR-196 refused speculation by default: without the opt-in, and for any
// unknown stored value, the plan is empty and the store is not walked.
func TestServiceWakeAheadPlannerRequiresOptIn(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "wake-ahead-off@local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	createWakeAheadApp(t, store, acct.ID, "auth", state.AppManifest{})
	resolveCalls := 0
	resolve := func(context.Context, string, string) (gateway.ServiceTarget, bool, error) {
		resolveCalls++
		return gateway.ServiceTarget{}, false, nil
	}
	for _, mode := range []api.ServiceWakeAhead{"", api.ServiceWakeAheadOff, "always"} {
		caller := createWakeAheadApp(t, store, acct.ID, "caller-"+string(mode), state.AppManifest{
			ServiceWakeAhead: mode,
			ServiceBindings:  bindingsFor("auth"),
		})
		plan, err := newServiceWakeAheadPlanner(store, resolve, newServiceProxyAuthorizer(store))(ctx, caller.ID)
		if err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if len(plan.Targets) != 0 {
			t.Fatalf("mode %q planned %d targets, want 0", mode, len(plan.Targets))
		}
	}
	if resolveCalls != 0 {
		t.Fatalf("resolver called %d times for callers that did not opt in", resolveCalls)
	}
	plan, err := newServiceWakeAheadPlanner(store, resolve, newServiceProxyAuthorizer(store))(ctx, "not-a-uuid")
	if err != nil || len(plan.Targets) != 0 {
		t.Fatalf("malformed caller id: plan=%+v err=%v", plan, err)
	}
}

// One wake restores at most ServiceWakeAheadMaxTargets dependencies; the rest
// are reported as truncated and still wake on demand.
func TestServiceWakeAheadPlannerCapsTargets(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "wake-ahead-cap@local", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for i := range api.ServiceWakeAheadMaxTargets + 3 {
		name := fmt.Sprintf("dep-%d", i)
		names = append(names, name)
		createWakeAheadApp(t, store, acct.ID, name, state.AppManifest{})
	}
	caller := createWakeAheadApp(t, store, acct.ID, "fanout", state.AppManifest{
		ServiceWakeAhead: api.ServiceWakeAheadDeclared,
		ServiceBindings:  bindingsFor(names...),
	})
	plan, err := newServiceWakeAheadPlanner(store, newServiceProxyResolver(store), newServiceProxyAuthorizer(store))(ctx, caller.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Targets) != api.ServiceWakeAheadMaxTargets || plan.Truncated != 3 {
		t.Fatalf("targets=%d truncated=%d, want %d/3", len(plan.Targets), plan.Truncated, api.ServiceWakeAheadMaxTargets)
	}
}

// A store failure is a platform fault and must surface as plan_failed rather
// than being counted as a customer denial.
func TestServiceWakeAheadPlannerSurfacesPlatformFailures(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "wake-ahead-fail@local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	target := createWakeAheadApp(t, store, acct.ID, "auth", state.AppManifest{})
	caller := createWakeAheadApp(t, store, acct.ID, "api", state.AppManifest{
		ServiceWakeAhead: api.ServiceWakeAheadDeclared,
		ServiceBindings:  bindingsFor("auth"),
	})
	resolve := func(context.Context, string, string) (gateway.ServiceTarget, bool, error) {
		return gateway.ServiceTarget{AppID: target.ID}, true, nil
	}
	boom := errors.New("pg down")
	cases := map[string]struct {
		authorize gateway.ServiceProxyAuthorizer
		wantErr   bool
		denied    int
	}{
		"binding denial is counted": {
			authorize: func(context.Context, string, string) (gateway.ServiceCaller, error) {
				return gateway.ServiceCaller{}, gateway.ErrServiceProxyBindingDenied
			},
			denied: 1,
		},
		"store failure is returned": {
			authorize: func(context.Context, string, string) (gateway.ServiceCaller, error) {
				return gateway.ServiceCaller{}, fmt.Errorf("load caller app: %w", boom)
			},
			wantErr: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			plan, err := newServiceWakeAheadPlanner(store, resolve, tc.authorize)(ctx, caller.ID)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, boom) {
				t.Fatalf("err = %v, want wrapped %v", err, boom)
			}
			if plan.Denied != tc.denied || len(plan.Targets) != 0 {
				t.Fatalf("denied=%d targets=%d, want %d/0", plan.Denied, len(plan.Targets), tc.denied)
			}
		})
	}
}
