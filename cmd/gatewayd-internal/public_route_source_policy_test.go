// adr: 375
package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func (f publicRoutingPGFixture) sourceApp(t *testing.T, slug, account string) state.App {
	t.Helper()
	app, err := f.store.CreateApp(t.Context(), state.App{AccountID: account, Slug: slug,
		Type: state.AppTypeApp, RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func (f publicRoutingPGFixture) routeTarget(t *testing.T, host string) gateway.App {
	t.Helper()
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
	source, found, err := router.ResolveHost(t.Context(), host)
	if err != nil || source.PublicPolicySource == nil || source.PublicPolicySource.Revision == "" {
		t.Fatalf("source claim lacks authoritative evidence: found=%v err=%v", found, err)
	}
	target, ready, err := router.resolvePublicAppSlug(t.Context(), f.app.Slug)
	if err != nil || !ready {
		t.Fatalf("target projection: ready=%v err=%v", ready, err)
	}
	target.PublicRouteSource = &gateway.PublicRouteSourcePolicy{Source: source.PublicPolicySource,
		AppID: source.ID, AccountID: source.AccountID, Found: found}
	return target
}

func TestPublicRouteSourcePostgresRequiresStableClaimAndTarget(t *testing.T) {
	for _, kind := range []string{"settings", "new-claim", "deleted-claim", "foreign-retarget"} {
		t.Run(kind, func(t *testing.T) {
			f := newPublicRoutingPGFixture(t)
			deployment := f.deployment(t, "production", "sha256:route-source")
			const host = "source.apps.gregale.dev"
			sourceHost := host
			var source state.App
			if kind != "new-claim" {
				source = f.sourceApp(t, "source", f.app.AccountID)
			}
			if kind == "foreign-retarget" {
				sourceHost = "source.example.test"
				if _, err := f.store.CreateCustomDomain(t.Context(), sourceHost, source.ID, "token"); err != nil {
					t.Fatal(err)
				}
				if err := f.store.MarkDomainVerified(t.Context(), sourceHost); err != nil {
					t.Fatal(err)
				}
			}
			target := f.routeTarget(t, sourceHost)
			if target.PublicRouteSource.Found != (kind != "new-claim") {
				t.Fatal("source claim presence was guessed")
			}
			pin := newPublicRoutingPinner(f.store)
			inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "production"}
			if snapshot, err := pin(t.Context(), target, inputs); err != nil || len(snapshot.Weights) != 1 || snapshot.Weights[0].ID != deployment.ID {
				t.Fatalf("unchanged source/target refused: %+v %v", snapshot, err)
			}
			switch kind {
			case "settings":
				if _, err := f.pool.Exec(t.Context(), `UPDATE apps SET maintenance_mode=true WHERE id=$1`, source.ID); err != nil {
					t.Fatal(err)
				}
			case "new-claim":
				f.sourceApp(t, "source", f.app.AccountID)
			case "deleted-claim":
				if err := f.store.DeleteApp(t.Context(), source.ID); err != nil {
					t.Fatal(err)
				}
			case "foreign-retarget":
				account, err := f.store.CreateAccount(t.Context(), "foreign-source@test.local", api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				foreign := f.sourceApp(t, "foreign-source", account.ID)
				if _, err := f.pool.Exec(t.Context(), `UPDATE custom_domains SET app_id=$2 WHERE domain=$1`, sourceHost, foreign.ID); err != nil {
					t.Fatal(err)
				}
			}
			if snapshot, err := pin(t.Context(), target, inputs); err == nil || len(snapshot.Weights) != 0 {
				t.Fatalf("changed source combined with old target projection: %+v %v", snapshot, err)
			}
			fresh := f.routeTarget(t, sourceHost)
			_, err := pin(t.Context(), fresh, inputs)
			if kind == "foreign-retarget" || kind == "deleted-claim" {
				if err == nil {
					t.Fatal("fresh foreign or deleted source authorized a target")
				}
			} else if err != nil {
				t.Fatalf("fresh matching ownership did not recover: %v", err)
			}
		})
	}
}

func TestPublicRouteSourcePostgresUsesSameViewAsTargetAndWeights(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	deployment := f.deployment(t, "production", "sha256:source-view")
	const host = "new-source.apps.gregale.dev"
	target := f.routeTarget(t, host)
	if target.PublicRouteSource.Found {
		t.Fatal("initial hostname was claimed")
	}
	inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "production"}
	// The owner read starts the transaction view. Committing a source claim
	// before the later source read must not mix that new claim into this view.
	during, err := newPublicRoutingPinner(publicSnapshotAfterOwner{PublicRoutingSnapshotStore: f.store, after: func() {
		f.sourceApp(t, "new-source", f.app.AccountID)
	}})(t.Context(), target, inputs)
	if err != nil || len(during.Weights) != 1 || during.Weights[0].ID != deployment.ID {
		t.Fatalf("source, target and routing did not share one view: %+v %v", during, err)
	}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), target, inputs); err == nil {
		t.Fatal("fresh dispatch ignored a newly claimed synthetic hostname")
	}
}

func TestPublicRouteSourcePostgresRefusesMissingOrInconsistentEvidence(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:source-evidence")
	f.sourceApp(t, "source", f.app.AccountID)
	target := f.routeTarget(t, "source.apps.gregale.dev")
	for _, kind := range []string{"missing-claim", "missing-reader", "empty-revision", "slug-source", "wrong-account", "inconsistent-miss", "substitution-disabled", "ordinary-with-source"} {
		t.Run(kind, func(t *testing.T) {
			data, err := json.Marshal(target)
			if err != nil {
				t.Fatal(err)
			}
			var malformed gateway.App
			if err := json.Unmarshal(data, &malformed); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing-claim":
				malformed.PublicRouteSource = nil
			case "missing-reader":
				malformed.PublicRouteSource.Source = nil
			case "empty-revision":
				malformed.PublicRouteSource.Source.Revision = ""
			case "slug-source":
				malformed.PublicRouteSource.Source.Slug = "source"
			case "wrong-account":
				malformed.PublicRouteSource.AccountID = "foreign"
			case "inconsistent-miss":
				malformed.PublicRouteSource.Found = false
			case "substitution-disabled":
				malformed.PublicRouteSource.Source.CanSubstitute = false
			case "ordinary-with-source":
				malformed.PublicPolicySource = f.routingApp().PublicPolicySource
			}
			if snapshot, err := newPublicRoutingPinner(f.store)(t.Context(), malformed, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err == nil || len(snapshot.Weights) != 0 {
				t.Fatalf("malformed source evidence accepted: %+v %v", snapshot, err)
			}
		})
	}
}

func TestPublicRouteSourcePostgresReservationsCannotBecomeSyntheticHosts(t *testing.T) {
	for _, kind := range []string{"internal-app", "deleted-app", "unverified-domain", "unverified-wildcard", "wildcard-apex",
		"failed-alias", "deleted-alias-target", "deleted-alias", "missing-alias", "unverified-tenant", "suspended-tenant", "missing-revision", "missing-environment", "unclaimed"} {
		t.Run(kind, func(t *testing.T) {
			f := newPublicRoutingPGFixture(t)
			deployment := f.deployment(t, "production", "sha256:reserved-source")
			host := "reserved.example.test"
			allowed := kind == "unclaimed" || kind == "wildcard-apex"
			switch kind {
			case "internal-app", "deleted-app":
				app := f.sourceApp(t, "reserved-source", f.app.AccountID)
				host = app.Slug + ".apps.gregale.dev"
				if kind == "internal-app" {
					if _, err := f.pool.Exec(t.Context(), `UPDATE apps SET visibility='internal' WHERE id=$1`, app.ID); err != nil {
						t.Fatal(err)
					}
				} else if err := f.store.DeleteApp(t.Context(), app.ID); err != nil {
					t.Fatal(err)
				}
			case "unverified-domain", "unverified-wildcard", "wildcard-apex":
				domain := host
				if kind != "unverified-domain" {
					domain = "*." + host
					if kind == "unverified-wildcard" {
						host = "child." + host
					}
				}
				if _, err := f.store.CreateCustomDomain(t.Context(), domain, f.app.ID, "token"); err != nil {
					t.Fatal(err)
				}
			case "failed-alias", "deleted-alias-target", "deleted-alias":
				if _, err := f.store.SetDeploymentAlias(t.Context(), f.app.ID, "reserved", deployment.ID); err != nil {
					t.Fatal(err)
				}
				label, _ := api.DeploymentAliasHostLabel(f.app.ID, "reserved")
				host = label + ".apps.gregale.dev"
				if kind == "deleted-alias" {
					if err := f.store.DeleteDeploymentAlias(t.Context(), f.app.ID, "reserved"); err != nil {
						t.Fatal(err)
					}
					break
				}
				query := `UPDATE deployments SET status='failed' WHERE id=$1`
				if kind == "deleted-alias-target" {
					query = `UPDATE deployments SET deleted_at=now() WHERE id=$1`
				}
				if _, err := f.pool.Exec(t.Context(), query, deployment.ID); err != nil {
					t.Fatal(err)
				}
			case "missing-alias":
				label, _ := api.DeploymentAliasHostLabel(f.app.ID, "missing")
				host = label + ".apps.gregale.dev"
			case "unverified-tenant", "suspended-tenant":
				f.reserveTenantHostname(t, host, kind == "suspended-tenant")
			case "missing-revision":
				host = gateway.BuildDeploymentPreviewURL(".gregale.dev", 999, f.app.Slug)
			case "missing-environment":
				host = gateway.BuildEnvironmentHost(".gregale.dev", uuid.NewString(), f.app.ID)
			}
			router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev",
				tenantSurfacesEnabled: func() bool { return true }}
			source, found, err := router.ResolveHost(t.Context(), host)
			if err != nil || found || source.PublicPolicySource == nil || source.PublicPolicySource.CanSubstitute != allowed {
				t.Fatalf("reservation projection: found=%v allowed=%v want=%v err=%v", found, source.PublicPolicySource, allowed, err)
			}
			if !allowed {
				// Even a forged allow bit with the current content fingerprint
				// cannot turn a reservation into an unclaimed hostname.
				target, ready, err := router.resolvePublicAppSlug(t.Context(), f.app.Slug)
				if err != nil || !ready {
					t.Fatalf("target projection: %v %v", ready, err)
				}
				source.PublicPolicySource.CanSubstitute = true
				target.PublicRouteSource = &gateway.PublicRouteSourcePolicy{Source: source.PublicPolicySource}
				if _, err := newPublicRoutingPinner(f.store)(t.Context(), target, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err == nil {
					t.Fatal("reserved hostname authorized a synthetic target")
				}
			}
		})
	}
}

func (f publicRoutingPGFixture) reserveTenantHostname(t *testing.T, host string, suspended bool) {
	t.Helper()
	limits := api.Limits{TenantSurfacesAllowed: true, TenantSurfacesPerAccount: 10, TenantHostnamesPerSurface: 10}
	surface, err := f.store.CreateTenantSurfaceIfUnderQuota(t.Context(), state.CreateTenantSurfaceParams{
		AccountID: f.app.AccountID, AppID: f.app.ID, Name: "reserved"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateTenantHostnameIfUnderQuota(t.Context(), state.CreateTenantHostnameParams{
		SurfaceID: surface.ID, Hostname: host, ChallengeToken: "token"}, limits); err != nil {
		t.Fatal(err)
	}
	if !suspended {
		return
	}
	if err := f.store.MarkTenantHostnameVerified(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := f.store.CreatePlatformTenant(t.Context(), f.app.AccountID, "reserved", "Reserved", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.LinkPlatformTenantSurface(t.Context(), f.app.AccountID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SetPlatformTenantStatus(t.Context(), f.app.AccountID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
}

func TestPublicRouteSourcePostgresNewReservationRefusesOldNegativeClaim(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:reservation-change")
	const host = "new-reservation.example.test"
	target := f.routeTarget(t, host)
	inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "production"}
	pin := newPublicRoutingPinner(f.store)
	if _, err := pin(t.Context(), target, inputs); err != nil {
		t.Fatalf("unclaimed hostname refused: %v", err)
	}
	if _, err := f.store.CreateCustomDomain(t.Context(), host, f.app.ID, "unverified"); err != nil {
		t.Fatal(err)
	}
	if _, err := pin(t.Context(), target, inputs); err == nil {
		t.Fatal("old negative claim survived a new unverified reservation")
	}
	if _, err := pin(t.Context(), f.routeTarget(t, host), inputs); err == nil {
		t.Fatal("fresh reservation authorized substitution")
	}
	if err := f.store.DeleteCustomDomain(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	if _, err := pin(t.Context(), f.routeTarget(t, host), inputs); err != nil {
		t.Fatalf("released hostname did not recover: %v", err)
	}
}

func TestPublicRouteSourcePostgresUnavailableClaimRefusesAndReleasesTransaction(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:source-outage")
	source := f.sourceApp(t, "source", f.app.AccountID)
	const host = "blocked-source.example.test"
	if _, err := f.store.CreateCustomDomain(t.Context(), host, source.ID, "token"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDomainVerified(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	target := f.routeTarget(t, host)
	lock, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(t.Context(), `LOCK TABLE custom_domains IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if snapshot, err := newPublicRoutingPinner(f.store)(bounded, target, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err == nil || len(snapshot.Weights) != 0 {
		t.Fatalf("blocked source read granted dispatch: %+v %v", snapshot, err)
	}
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	// pgx closes a cancelled connection asynchronously. Require that cleanup
	// actually finishes, rather than treating its brief pool state as a leak.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for f.pool.Stat().AcquiredConns() != 0 {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("refused source read retained its transaction")
		}
	}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), target, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err != nil {
		t.Fatalf("source read did not recover after store became available: %v", err)
	}
}
