// adr: 570
package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type publicHostAfterAppStore struct {
	*state.PgStore
	after func()
}

func (s publicHostAfterAppStore) WithPublicHostPolicySnapshot(ctx context.Context, read func(state.PublicHostPolicyReader) error) error {
	return s.PgStore.WithPublicHostPolicySnapshot(ctx, func(reader state.PublicHostPolicyReader) error {
		return read(publicHostAfterAppReader{PublicHostPolicyReader: reader, after: s.after})
	})
}

type publicHostAfterAppReader struct {
	state.PublicHostPolicyReader
	after func()
}

func (s publicHostAfterAppReader) AppBySlug(ctx context.Context, slug string) (state.App, error) {
	app, err := s.PublicHostPolicyReader.AppBySlug(ctx, slug)
	if err == nil {
		s.after()
	}
	return app, err
}

func (f publicRoutingPGFixture) ingress(t *testing.T, deployment string, port int) {
	t.Helper()
	sidecars, err := json.Marshal([]deploymentCompanionRoute{{Name: "proxy", Type: api.SidecarTypeSidecar, Port: port, PrimaryIngress: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE deployments SET sidecars=$2 WHERE id=$1`, deployment, sidecars); err != nil {
		t.Fatal(err)
	}
}

func TestPublicHostSnapshotPostgresDoesNotMixAppAccountAndIngress(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	deployment := f.deployment(t, "production", "sha256:host")
	f.ingress(t, deployment.ID, 8081)
	host := f.app.Slug + ".apps.gregale.dev"
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
	before, found, err := router.ResolveHost(t.Context(), host)
	if err != nil || !found {
		t.Fatalf("initial: found=%v err=%v", found, err)
	}
	var changed atomic.Bool
	duringRouter := router
	duringRouter.store = publicHostAfterAppStore{PgStore: f.store, after: func() {
		if changed.Swap(true) {
			return
		}
		if _, err := f.pool.Exec(t.Context(), `UPDATE apps SET require_authn=true WHERE id=$1`, f.app.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(t.Context(), `UPDATE accounts SET plan='scale' WHERE id=$1`, f.app.AccountID); err != nil {
			t.Fatal(err)
		}
		f.ingress(t, deployment.ID, 8082)
	}}
	during, found, err := duringRouter.ResolveHost(t.Context(), host)
	if err != nil || !found || during.RequireAuthn || during.Plan != api.PlanPro || during.PrimaryIngressPort != 8081 ||
		during.PublicPolicySource.Revision != before.PublicPolicySource.Revision {
		t.Fatalf("host projection mixed committed generations: %+v found=%v err=%v", during, found, err)
	}
	inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "production"}
	if snapshot, err := newPublicRoutingPinner(f.store)(t.Context(), during, inputs); err == nil || len(snapshot.Weights) != 0 {
		t.Fatalf("old app settings combined with new dispatch policy: %+v %v", snapshot, err)
	}
	fresh, found, err := router.ResolveHost(t.Context(), host)
	if err != nil || !found || !fresh.RequireAuthn || fresh.Plan != api.PlanScale || fresh.PrimaryIngressPort != 8082 ||
		fresh.PublicPolicySource.Revision == before.PublicPolicySource.Revision {
		t.Fatalf("new host projection missing updates: %+v found=%v err=%v", fresh, found, err)
	}
	if snapshot, err := newPublicRoutingPinner(f.store)(t.Context(), fresh, inputs); err != nil || len(snapshot.Weights) != 1 || snapshot.Weights[0].ID != deployment.ID {
		t.Fatalf("consistent fresh policy refused: %+v %v", snapshot, err)
	}
}

func TestPublicHostSnapshotPostgresScopesIngressAndRefreshesAliasWithoutNotify(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	production := f.deployment(t, "production", "sha256:prod")
	staging := f.deployment(t, "staging", "sha256:stage")
	f.ingress(t, production.ID, 8081)
	f.ingress(t, staging.ID, 8082)
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev",
		tenantSurfacesEnabled: func() bool { return false }}
	app, found, err := router.ResolveHost(t.Context(), f.app.Slug+".apps.gregale.dev")
	if err != nil || !found || app.PrimaryIngressPort != 8081 {
		t.Fatalf("staging changed production ingress: %+v %v %v", app, found, err)
	}
	preview := gateway.BuildDeploymentPreviewURL(router.deploySuffix, staging.Revision, f.app.Slug)
	app, found, err = router.ResolveHost(t.Context(), preview)
	if err != nil || !found || app.PrimaryIngressPort != 8082 || app.PinnedDeploymentID != staging.ID || app.PublicPolicySource.CanSubstitute {
		t.Fatalf("immutable URL did not project exact ingress: %+v %v %v", app, found, err)
	}
	if _, err := f.store.SetDeploymentAlias(t.Context(), f.app.ID, "qa", staging.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE deployments SET traffic_percent=0 WHERE id=$1`, staging.ID); err != nil {
		t.Fatal(err)
	}
	label, _ := api.DeploymentAliasHostLabel(f.app.ID, "qa")
	host := label + router.appsSuffix
	backend := gateway.NewPGBackend(router, gateway.NewFakeScheduler(""), nil)
	old, found, err := backend.LookupHostPolicy(t.Context(), host)
	if err != nil || !found || old.PrimaryIngressPort != 8082 || old.PinnedDeploymentID != staging.ID || old.PublicPolicySource.CanSubstitute {
		t.Fatalf("zero-weight alias lost its own ingress: %+v %v %v", old, found, err)
	}
	next := f.deployment(t, "staging", "sha256:stage-next")
	f.ingress(t, next.ID, 8083)
	if _, err := f.store.SetDeploymentAlias(t.Context(), f.app.ID, "qa", next.ID); err != nil {
		t.Fatal(err)
	}
	fresh, found, err := backend.LookupHostPolicy(t.Context(), host)
	if err != nil || !found || fresh.PinnedDeploymentID != next.ID || fresh.PrimaryIngressPort != 8083 {
		t.Fatalf("missed notification served stale alias: %+v %v %v", fresh, found, err)
	}
	inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "staging", HostDeploymentID: staging.ID, HostScope: "staging"}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), old, inputs); err == nil {
		t.Fatal("alias retarget between resolution and dispatch was accepted")
	}
	inputs.HostDeploymentID = next.ID
	if snapshot, err := newPublicRoutingPinner(f.store)(t.Context(), fresh, inputs); err != nil || !snapshot.HostAllowed {
		t.Fatalf("fresh alias refused: %+v %v", snapshot, err)
	}
	if err := f.store.DeleteDeploymentAlias(t.Context(), f.app.ID, "qa"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := backend.LookupHostPolicy(t.Context(), host); err != nil || found {
		t.Fatalf("deleted alias remained cached: found=%v err=%v", found, err)
	}
}

func TestPublicHostSnapshotPostgresProjectionIsMinimalAndTransactionEnds(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	deployment := f.deployment(t, "production", "sha256:projection")
	f.ingress(t, deployment.ID, 8081)
	manifest := state.AppManifest{Env: map[string]string{"PRIVATE": "never-load-this"}, RequestTimeoutS: 12,
		SessionAffinity: true, VersionAffinityCookie: "revision", RevisionPinTTLSeconds: 1800,
		Healthz: "/health", RobotsTxt: "test", Favicon: []byte("icon"), HeadWakes: true}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE apps SET manifest=$2, public_auth_basic=$3,
		cors_default_enabled=true, cors_default_origins=ARRAY['https://example.test'],
		public_auth_ip_allowlist=ARRAY['203.0.113.0/24']::cidr[], request_rate_limit_rps=7,
		request_rate_limit_burst=9, only_declared_routes=true,
		declared_routes='[{"path":"/health","methods":["GET"]}]' WHERE id=$1`, f.app.ID, encoded, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	var escaped state.PublicHostPolicyReader
	err = f.store.WithPublicHostPolicySnapshot(t.Context(), func(reader state.PublicHostPolicyReader) error {
		escaped = reader
		if _, writable := reader.(state.Store); writable {
			t.Fatal("snapshot exposed a writable Store")
		}
		app, err := reader.AppByID(t.Context(), f.app.ID)
		if err != nil || len(app.Manifest.Env) != 0 {
			t.Fatalf("public projection loaded env: %v %v", app.Manifest.Env, err)
		}
		account, err := reader.AccountByID(t.Context(), f.app.AccountID)
		if err != nil || account.Email != "" || len(account.MFASecretEncrypted) != 0 {
			t.Fatal("public projection loaded unrelated account credentials")
		}
		actual, found, err := (pgRouter{store: reader}).toApp(t.Context(), app)
		if err != nil || !found {
			return err
		}
		full, err := f.store.AppByID(t.Context(), f.app.ID)
		if err != nil {
			return err
		}
		want, found, err := (pgRouter{store: f.store}).toApp(t.Context(), full)
		if err != nil || !found || !reflect.DeepEqual(actual, want) {
			left, right := reflect.ValueOf(actual), reflect.ValueOf(want)
			for i := range left.NumField() {
				if !reflect.DeepEqual(left.Field(i).Interface(), right.Field(i).Interface()) {
					t.Errorf("public projection %s: actual=%#v want=%#v", left.Type().Field(i).Name, left.Field(i).Interface(), right.Field(i).Interface())
				}
			}
			t.Fatalf("public projection differs: found=%v err=%v", found, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := escaped.AppByID(t.Context(), f.app.ID); err == nil {
		t.Fatal("host transaction stayed open after callback")
	}
	sentinel := errors.New("refuse snapshot")
	if err := f.store.WithPublicHostPolicySnapshot(t.Context(), func(reader state.PublicHostPolicyReader) error {
		escaped = reader
		_, err := reader.AppByID(t.Context(), f.app.ID)
		if err != nil {
			return err
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("snapshot failure lost: %v", err)
	}
	if _, err := escaped.AppByID(t.Context(), f.app.ID); err == nil {
		t.Fatal("refused host transaction stayed open")
	}
}

func TestPublicHostSnapshotPostgresEnvironmentAndDomainBindingsRemainVerified(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	production := f.deployment(t, "production", "sha256:prod-binding")
	f.ingress(t, production.ID, 8081)
	environment, err := f.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{
		AccountID: f.app.AccountID, ProjectID: f.project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	staging := f.deployment(t, "staging", "sha256:stage-binding")
	f.ingress(t, staging.ID, 8082)
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev",
		tenantSurfacesEnabled: func() bool { return false }}
	named := gateway.BuildEnvironmentHost(router.deploySuffix, environment.ID, f.app.ID)
	old, found, err := router.ResolveHost(t.Context(), named)
	if err != nil || !found || old.PinnedDeploymentID != staging.ID || old.PrimaryIngressPort != 8082 || old.PublicPolicySource.CanSubstitute {
		t.Fatalf("named environment projection: %+v %v %v", old, found, err)
	}
	inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "staging", HostDeploymentID: staging.ID, HostScope: "staging"}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), old, inputs); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PutProjectEnvironmentEdgePolicy(t.Context(), state.ProjectEnvironmentEdgePolicy{
		AccountID: f.app.AccountID, ProjectID: f.project.ID, AppID: f.app.ID, EnvironmentSlug: "staging"}); err != nil {
		t.Fatal(err)
	}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), old, inputs); err == nil {
		t.Fatal("environment policy update between views was missed")
	}
	const domain = "binding.example.test"
	if _, err := f.store.CreateCustomDomain(t.Context(), domain, f.app.ID, "token"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDomainVerified(t.Context(), domain); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE custom_domains SET environment_id=$2 WHERE domain=$1`, domain, environment.ID); err != nil {
		t.Fatal(err)
	}
	backend := gateway.NewPGBackend(router, gateway.NewFakeScheduler(""), nil)
	old, found, err = backend.LookupHostPolicy(t.Context(), domain)
	if err != nil || !found || old.PinnedDeploymentID != staging.ID || old.PrimaryIngressPort != 8082 || !old.CustomDomainRoute {
		t.Fatalf("environment domain projection: %+v %v %v", old, found, err)
	}
	prodEnvironment, err := f.store.ProjectEnvironmentBySlug(t.Context(), f.app.AccountID, f.project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE custom_domains SET environment_id=$2 WHERE domain=$1`, domain, prodEnvironment.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), old, inputs); err == nil {
		t.Fatal("domain retarget combined old environment settings with a new binding")
	}
	fresh, found, err := backend.LookupHostPolicy(t.Context(), domain)
	if err != nil || !found || fresh.PinnedDeploymentID != production.ID || fresh.PrimaryIngressPort != 8081 {
		t.Fatalf("missed domain notification retained staging: %+v %v %v", fresh, found, err)
	}
	// A large revision must not wrap through the sqlc int32 parameter to v1.
	large := int(int64(staging.Revision) + (int64(1) << 32))
	if _, found, err := router.ResolveHost(t.Context(), gateway.BuildDeploymentPreviewURL(router.deploySuffix, large, f.app.Slug)); err != nil || found {
		t.Fatalf("large immutable revision resolved a different revision: %v %v", found, err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE deployments SET deleted_at=now() WHERE id=$1`, staging.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := router.ResolveHost(t.Context(), named); err != nil || found {
		t.Fatalf("soft-deleted environment target routed: %v %v", found, err)
	}
	if _, found, err := router.ResolveHost(t.Context(), gateway.BuildDeploymentPreviewURL(router.deploySuffix, staging.Revision, f.app.Slug)); err != nil || found {
		t.Fatalf("soft-deleted immutable target routed: %v %v", found, err)
	}
}

func TestPublicHostSnapshotPostgresTenantBindingCannotFallThroughAfterSuspension(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:tenant")
	limits := api.MustLimitsFor(api.PlanPro)
	surface, err := f.store.CreateTenantSurfaceIfUnderQuota(t.Context(), state.CreateTenantSurfaceParams{
		AccountID: f.app.AccountID, AppID: f.app.ID, Name: "customer", CertKind: state.CertKindPerHostSAN}, limits)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := f.store.CreatePlatformTenant(t.Context(), f.app.AccountID, "customer", "Customer", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.LinkPlatformTenantSurface(t.Context(), f.app.AccountID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	const host = "tenant.example.test"
	if _, err := f.store.CreateTenantHostnameIfUnderQuota(t.Context(), state.CreateTenantHostnameParams{
		SurfaceID: surface.ID, Hostname: host, ChallengeToken: "token"}, limits); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkTenantHostnameVerified(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE tenant_surfaces SET status='active' WHERE id=$1`, surface.ID); err != nil {
		t.Fatal(err)
	}
	// A legacy domain with the same name must not bypass the claimed tenant.
	if _, err := f.store.CreateCustomDomain(t.Context(), host, f.app.ID, "token"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDomainVerified(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", tenantSurfacesEnabled: func() bool { return true }}
	if binding, found, err := router.ResolvePlatformTenantHost(t.Context(), host); err != nil || !found || binding.TenantID != tenant.ID {
		t.Fatalf("fresh-policy router hid its tenant guard: %+v %v %v", binding, found, err)
	}
	backend := gateway.NewPGBackend(router, gateway.NewFakeScheduler(""), nil)
	old, found, err := backend.LookupHostPolicy(t.Context(), host)
	if err != nil || !found || old.PlatformTenantID != tenant.ID || old.RoutedSurfaceID != surface.ID {
		t.Fatalf("tenant snapshot binding: %+v %v %v", old, found, err)
	}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), old, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE platform_tenants SET status='suspended' WHERE id=$1`, tenant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := newPublicRoutingPinner(f.store)(t.Context(), old, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err == nil {
		t.Fatal("tenant suspension between policy views was missed")
	}
	if _, found, err := backend.LookupHostPolicy(t.Context(), host); err != nil || found {
		t.Fatalf("suspended tenant used a stale or legacy domain route: %v %v", found, err)
	}
}
