// adr: 167
package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainWildcardRoutingFollowsCurrentOwnerPlan(t *testing.T) {
	for _, company := range []bool{false, true} {
		for _, environmentBound := range []bool{false, true} {
			name := map[bool]string{false: "personal", true: "company"}[company] + map[bool]string{false: "/app", true: "/environment"}[environmentBound]
			t.Run(name, func(t *testing.T) {
				ctx := t.Context()
				store := state.NewMemStore()
				app := seedApp(t, store, "wildcard-plan", api.PlanPro)
				var org state.Org
				if company {
					var err error
					org, err = store.CreateOrg(ctx, state.Org{Slug: "wildcard-company", Name: "Company", Plan: api.PlanFree})
					if err != nil {
						t.Fatal(err)
					}
					if err = store.UpdateOrgPlan(ctx, org.ID, api.PlanPro); err != nil {
						t.Fatal(err)
					}
					app, err = store.CreateApp(ctx, state.App{AccountID: app.AccountID, OrgID: org.ID, Slug: "company-wildcard", Type: state.AppTypeApp, Status: state.AppActive})
					if err != nil {
						t.Fatal(err)
					}
					// The namespace creator's plan must never authorize a company wildcard.
					if err = store.UpdateAccountPlan(ctx, app.AccountID, api.PlanFree); err != nil {
						t.Fatal(err)
					}
				}
				var environmentID string
				if environmentBound {
					project, err := store.CreateProject(ctx, state.Project{AccountID: app.AccountID, OrgID: app.OrgID, Slug: "wildcard-project", ScanSource: state.ProjectScanSourceCompose})
					if err != nil {
						t.Fatal(err)
					}
					app, err = store.CreateApp(ctx, state.App{AccountID: app.AccountID, OrgID: app.OrgID, ProjectID: project.ID, Slug: "environment-wildcard", Type: state.AppTypeApp, Status: state.AppActive})
					if err != nil {
						t.Fatal(err)
					}
					environment, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: app.AccountID, ProjectID: project.ID, Slug: "staging"})
					if err != nil {
						t.Fatal(err)
					}
					environmentID = environment.ID
					if _, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Status: state.DeployLive}); err != nil {
						t.Fatal(err)
					}
				}
				const wildcard = "*.plan.example.com"
				const host = "shop.plan.example.com"
				const exact = "exact.example.com"
				for _, domain := range []string{wildcard, exact} {
					var err error
					if environmentBound {
						_, err = store.CreateCustomDomainInEnvironmentIfUnderQuota(ctx, domain, app.ID, environmentID, "proof", 100, 500)
					} else {
						_, err = store.CreateCustomDomainIfUnderQuota(ctx, domain, app.ID, "proof", 100, 500)
					}
					if err != nil {
						t.Fatal(err)
					}
					if err = store.MarkDomainVerified(ctx, domain); err != nil {
						t.Fatal(err)
					}
				}
				router := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}
				policy := gateway.NewOnDemandTLSPolicy(store, "gregale.dev", nil, nil)
				for _, plan := range []api.Plan{api.PlanPro, api.PlanFree, api.PlanPro} {
					var err error
					if company {
						err = store.UpdateOrgPlan(ctx, org.ID, plan)
					} else {
						err = store.UpdateAccountPlan(ctx, app.AccountID, plan)
					}
					if err != nil {
						t.Fatal(err)
					}
					for _, candidate := range []string{host, exact} {
						want := candidate == exact || plan.WildcardDomainsAllowed()
						resolved, found, err := router.ResolveHost(ctx, candidate)
						if err != nil || found != want || found && (resolved.ID != app.ID || resolved.Plan != plan) {
							t.Fatalf("cold %s plan=%s: app=%+v found=%v err=%v", candidate, plan, resolved, found, err)
						}
						warm, err := router.CachedCustomDomainRouteActive(ctx, candidate, app.ID)
						wantWarm := want && !environmentBound // environment routes are always resolved afresh
						if err != nil || warm != wantWarm {
							t.Fatalf("warm %s plan=%s: active=%v err=%v", candidate, plan, warm, err)
						}
					}
					if !environmentBound {
						allowed, decision := policy.Decide(ctx, host)
						if allowed != plan.WildcardDomainsAllowed() {
							t.Fatalf("TLS disagrees with ingress for plan %s: %v %s", plan, allowed, decision)
						}
					}
				}
			})
		}
	}
}

func TestCustomDomainWildcardPostgresSnapshotAndCachedRouting(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	ctx := t.Context()
	f.deployment(t, "production", "sha256:wildcard-plan")
	for _, domain := range []string{"*.plan.example.com", "exact.example.com"} {
		if _, err := f.store.CreateCustomDomain(ctx, domain, f.app.ID, "proof"); err != nil {
			t.Fatal(err)
		}
		if err := f.store.MarkDomainVerified(ctx, domain); err != nil {
			t.Fatal(err)
		}
	}
	for _, plan := range []api.Plan{api.PlanPro, api.PlanFree, api.PlanPro} {
		if err := f.store.UpdateAccountPlan(ctx, f.app.AccountID, plan); err != nil {
			t.Fatal(err)
		}
		for _, cached := range []bool{false, true} {
			router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", cachedHostPolicy: cached,
				tenantSurfacesEnabled: func() bool { return false }}
			for _, host := range []string{"shop.plan.example.com", "exact.example.com"} {
				want := host == "exact.example.com" || plan.WildcardDomainsAllowed()
				app, found, err := router.ResolveHost(ctx, host)
				if err != nil || found != want || found && (app.ID != f.app.ID || app.Plan != plan) {
					t.Fatalf("Postgres cached=%v %s plan=%s: app=%s found=%v err=%v", cached, host, plan, app.ID, found, err)
				}
				active, err := router.CachedCustomDomainRouteActive(ctx, host, f.app.ID)
				if err != nil || active != want {
					t.Fatalf("Postgres warm %s plan=%s: active=%v err=%v", host, plan, active, err)
				}
			}
		}
	}
}
