// adr: 531
package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func scopedPublicHeaders(value string) state.EdgeRuleAction {
	return state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
		ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Environment", Action: "set", Value: value}}}}
}

func TestPublicScopedDomainPostgresPolicyAndFreshAdmission(t *testing.T) {
	for _, mode := range []string{"exact", "wildcard", "escaped-boundary", "configured-environment-host"} {
		t.Run(mode, func(t *testing.T) {
			f := newPublicRoutingPGFixture(t)
			deployment := f.deployment(t, "staging", "sha256:scoped-domain-policy")
			environment, err := f.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: f.app.AccountID, ProjectID: f.project.ID, Slug: "staging"})
			if err != nil {
				t.Fatal(err)
			}
			router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".custom.gregale.test", tenantSurfacesEnabled: func() bool { return false }}
			host, domain := "staging.example.test", "staging.example.test"
			if mode == "wildcard" {
				host, domain = "nested.api.example.test", "*.example.test"
			} else if mode == "escaped-boundary" {
				host, domain = strings.Repeat("&", api.TrafficPolicyMaxHostnameBytes-len(".example.test"))+".example.test", "*.example.test"
			} else if mode == "configured-environment-host" {
				host, domain = gateway.BuildEnvironmentHost(router.deploySuffix, environment.ID, f.app.ID), ""
			}
			if domain != "" {
				if _, err := f.store.CreateCustomDomainInEnvironmentIfUnderQuota(t.Context(), domain, f.app.ID, environment.ID, "token", 100, 500); err != nil {
					t.Fatal(err)
				}
				if err := f.store.MarkDomainVerified(t.Context(), domain); err != nil {
					t.Fatal(err)
				}
			}
			f.edgeRule(t, host, scopedPublicHeaders("fallback"))
			g := newGatewaydEdgeRules(f.store, nil, nil, nil)
			old, admitted := f.compiledAppWithRouter(t, g, router, host)
			if admitted.PublicEnvironmentID != environment.ID || admitted.PinnedDeploymentID != deployment.ID {
				t.Fatalf("scoped policy identity: %+v", admitted)
			}
			if got := g.MatchHeaders(old, host, "/", "GET"); got == nil || got.ResponseHeaders[0].Value != "fallback" {
				t.Fatalf("missing scoped policy lost fallback: %+v", got)
			}
			policy := state.ProjectEnvironmentEdgePolicy{AccountID: f.app.AccountID, ProjectID: f.project.ID, AppID: f.app.ID, EnvironmentSlug: "staging",
				Rules: []state.ProjectEnvironmentEdgeRule{{Kind: state.EdgeRuleKindHeaders, MatchPath: "/*", Enabled: true, Action: scopedPublicHeaders("staging")}}}
			if _, err := f.store.PutProjectEnvironmentEdgePolicy(t.Context(), policy); err != nil {
				t.Fatal(err)
			}
			inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "staging", HostDeploymentID: deployment.ID, HostScope: "staging"}
			if _, err := newPublicRoutingPinner(f.store)(old, admitted, inputs); err == nil {
				t.Fatal("overlay update authorized an old dispatch")
			}
			fresh, _ := f.compiledAppWithRouter(t, g, router, host)
			if got := g.MatchHeaders(fresh, host, "/", "GET"); got == nil || got.ResponseHeaders[0].Value != "staging" {
				t.Fatalf("missed notification retained fallback: %+v", got)
			}
			if got := g.MatchHeaders(old, host, "/", "GET"); got == nil || got.ResponseHeaders[0].Value != "fallback" {
				t.Fatalf("admitted policy mutated: %+v", got)
			}
			policy.Rules = []state.ProjectEnvironmentEdgeRule{}
			if _, err := f.store.PutProjectEnvironmentEdgePolicy(t.Context(), policy); err != nil {
				t.Fatal(err)
			}
			empty, _ := f.compiledAppWithRouter(t, g, router, host)
			if got := g.MatchHeaders(empty, host, "/", "GET"); got != nil {
				t.Fatalf("empty scoped policy inherited fallback: %+v", got)
			}
			if _, err := f.pool.Exec(t.Context(), `UPDATE project_environment_edge_policies SET rules=jsonb_build_array(jsonb_build_object('padding',repeat('x',$2::integer))) WHERE app_id=$1 AND environment_slug='staging'`, f.app.ID, api.TrafficPolicyMaxContractBytes+1); err != nil {
				t.Fatal(err)
			}
			if _, err := g.PinHostPolicy(t.Context(), host); err == nil {
				t.Fatal("unavailable scoped overlay admitted a public policy")
			}
			if _, err := f.store.PutProjectEnvironmentEdgePolicy(t.Context(), policy); err != nil {
				t.Fatal(err)
			}
			f.compiledAppWithRouter(t, g, router, host)
		})
	}
}

type countedPublicHostSnapshotStore struct {
	state.Store
	snapshots state.PublicHostPolicySnapshotStore
	calls     int
}

func (s *countedPublicHostSnapshotStore) WithPublicHostPolicySnapshot(ctx context.Context, visit func(state.PublicHostPolicyReader) error) error {
	s.calls++
	return s.snapshots.WithPublicHostPolicySnapshot(ctx, visit)
}

func TestPublicScopedDomainHostnameBoundPrecedesReads(t *testing.T) {
	store := &countedPublicHostSnapshotStore{Store: state.NewMemStore()}
	g := newGatewaydEdgeRules(store, nil, nil, nil).withPublicHostRouter(pgRouter{})
	if _, err := g.PinHostPolicy(t.Context(), strings.Repeat("x", api.TrafficPolicyMaxHostnameBytes+1)); err == nil || store.calls != 0 {
		t.Fatalf("overlong hostname reached snapshot: calls=%d err=%v", store.calls, err)
	}
}

func TestPublicScopedDomainPostgresAsteriskHostsCannotRoute(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:wildcard-host")
	if _, err := f.store.CreateCustomDomain(t.Context(), "*.example.test", f.app.ID, "token"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDomainVerified(t.Context(), "*.example.test"); err != nil {
		t.Fatal(err)
	}
	if domain, err := f.store.DomainByName(t.Context(), "*.example.test"); err != nil || !domain.Verified() {
		t.Fatal("wildcard claim became unavailable to domain management")
	}
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
	for _, host := range []string{"*.example.test", "api*.example.test", "nested.*.example.test"} {
		if _, found, err := router.ResolveHost(t.Context(), host); err != nil || found {
			t.Fatalf("asterisk host routed: host=%q found=%v err=%v", host, found, err)
		}
		if active, err := router.CachedCustomDomainRouteActive(t.Context(), host, f.app.ID); err != nil || active {
			t.Fatalf("asterisk host kept a cached route: %q %v/%v", host, active, err)
		}
	}
}

func TestPublicScopedDomainPostgresActualBindingWins(t *testing.T) {
	for _, mode := range []string{"exact-ordinary", "alias", "tenant"} {
		t.Run(mode, func(t *testing.T) {
			f := newPublicRoutingPGFixture(t)
			production := f.deployment(t, "production", "sha256:shadow-production")
			f.deployment(t, "staging", "sha256:shadow-staging")
			environment, err := f.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: f.app.AccountID, ProjectID: f.project.ID, Slug: "staging"})
			if err != nil {
				t.Fatal(err)
			}
			host, scoped := "shadow.example.test", "*.example.test"
			router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev", tenantSurfacesEnabled: func() bool { return mode == "tenant" }}
			if mode == "alias" {
				if _, err := f.store.SetDeploymentAlias(t.Context(), f.app.ID, "shadow", production.ID); err != nil {
					t.Fatal(err)
				}
				host = "tag-shadow-" + strings.ReplaceAll(f.app.ID, "-", "") + router.appsSuffix
				scoped = host
			} else if mode == "tenant" {
				limits := api.MustLimitsFor(api.PlanPro)
				surface, err := f.store.CreateTenantSurfaceIfUnderQuota(t.Context(), state.CreateTenantSurfaceParams{AccountID: f.app.AccountID, AppID: f.app.ID, Name: "shadow", CertKind: state.CertKindPerHostSAN}, limits)
				if err != nil {
					t.Fatal(err)
				}
				tenant, _, err := f.store.CreatePlatformTenant(t.Context(), f.app.AccountID, "shadow", "Shadow", 1)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.LinkPlatformTenantSurface(t.Context(), f.app.AccountID, tenant.ID, surface.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.CreateTenantHostnameIfUnderQuota(t.Context(), state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: host, ChallengeToken: "token"}, limits); err != nil {
					t.Fatal(err)
				}
				if err := f.store.MarkTenantHostnameVerified(t.Context(), host); err != nil {
					t.Fatal(err)
				}
				if _, err := f.pool.Exec(t.Context(), `UPDATE tenant_surfaces SET status='active' WHERE id=$1`, surface.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.store.CreateCustomDomainInEnvironmentIfUnderQuota(t.Context(), scoped, f.app.ID, environment.ID, "scoped-token", 100, 500); err != nil {
				t.Fatal(err)
			}
			if err := f.store.MarkDomainVerified(t.Context(), scoped); err != nil {
				t.Fatal(err)
			}
			if mode == "exact-ordinary" {
				if _, err := f.store.CreateCustomDomain(t.Context(), host, f.app.ID, "ordinary-token"); err != nil {
					t.Fatal(err)
				}
				if err := f.store.MarkDomainVerified(t.Context(), host); err != nil {
					t.Fatal(err)
				}
			}
			f.edgeRule(t, host, scopedPublicHeaders("ordinary"))
			if _, err := f.store.PutProjectEnvironmentEdgePolicy(t.Context(), state.ProjectEnvironmentEdgePolicy{AccountID: f.app.AccountID, ProjectID: f.project.ID, AppID: f.app.ID, EnvironmentSlug: "staging",
				Rules: []state.ProjectEnvironmentEdgeRule{{Kind: state.EdgeRuleKindHeaders, MatchPath: "/*", Enabled: true, Action: scopedPublicHeaders("shadowed")}}}); err != nil {
				t.Fatal(err)
			}
			g := newGatewaydEdgeRules(f.store, nil, nil, nil)
			ctx, admitted := f.compiledAppWithRouter(t, g, router, host)
			if admitted.PublicEnvironmentID != "" {
				t.Fatalf("shadowed domain supplied environment: %+v", admitted)
			}
			if got := g.MatchHeaders(ctx, host, "/", "GET"); got == nil || got.ResponseHeaders[0].Value != "ordinary" {
				t.Fatalf("shadowed domain changed selected policy: %+v", got)
			}
		})
	}
}

func TestPublicScopedDomainPostgresHTTPPeersRepairWithoutNotify(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	deployment := f.deployment(t, "staging", "sha256:scoped-domain-http")
	environment, err := f.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: f.app.AccountID, ProjectID: f.project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	const host = "http-scoped.example.test"
	if _, err := f.store.CreateCustomDomainInEnvironmentIfUnderQuota(t.Context(), host, f.app.ID, environment.ID, "token", 100, 500); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDomainVerified(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	f.edgeRule(t, host, scopedPublicHeaders("fallback"))
	peers := make([]*httptest.Server, 0, 2)
	forwards := &atomic.Int32{}
	for _, h2 := range []bool{false, true} {
		router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
		scheduler := gateway.NewFakeScheduler("node").WithInstanceID(uuid.NewString()).WithDeploymentID(deployment.ID)
		handler := gateway.NewHandlerWith(gateway.NewPGBackend(router, scheduler, nil), nil, nil)
		handler.SetWakeGateHook()
		handler.WithPublicRoutingPolicy(newPublicRoutingPinner(f.store)).WithEdgeRules(newGatewaydEdgeRules(f.store, nil, nil, nil).withPublicHostRouter(router), nil, nil)
		handler.WithForwarding(func(gateway.Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forwards.Add(1); w.WriteHeader(http.StatusNoContent) })
		})
		server := httptest.NewUnstartedServer(handler)
		server.EnableHTTP2 = h2
		if h2 {
			server.StartTLS()
		} else {
			server.Start()
		}
		t.Cleanup(server.Close)
		server.Client().Timeout = 2 * time.Second
		peers = append(peers, server)
	}
	request := func(server *httptest.Server, status int, value string) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/", nil)
		req.Host = host
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != status || resp.Header.Get("X-Environment") != value {
			t.Fatalf("scoped HTTP status=%d headers=%v body=%s", resp.StatusCode, resp.Header, body)
		}
		proof := resp.Header.Get(gateway.TrafficPolicyRevisionHeader)
		if status == http.StatusNoContent && proof == "" {
			t.Fatal("scoped response omitted policy revision")
		}
		return proof
	}
	prior := make([]string, len(peers))
	for i, server := range peers {
		prior[i] = request(server, http.StatusNoContent, "fallback")
	}
	policy := state.ProjectEnvironmentEdgePolicy{AccountID: f.app.AccountID, ProjectID: f.project.ID, AppID: f.app.ID, EnvironmentSlug: "staging",
		Rules: []state.ProjectEnvironmentEdgeRule{{Kind: state.EdgeRuleKindHeaders, MatchPath: "/*", Enabled: true, Action: scopedPublicHeaders("staging")}}}
	if _, err := f.store.PutProjectEnvironmentEdgePolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	for i, server := range peers {
		if proof := request(server, http.StatusNoContent, "staging"); proof == prior[i] {
			t.Fatal("HTTP peer retained old policy revision")
		}
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE project_environment_edge_policies SET rules=jsonb_build_array(jsonb_build_object('padding',repeat('x',$2::integer))) WHERE app_id=$1 AND environment_slug='staging'`, f.app.ID, api.TrafficPolicyMaxContractBytes+1); err != nil {
		t.Fatal(err)
	}
	for _, server := range peers {
		request(server, http.StatusServiceUnavailable, "")
	}
	if forwards.Load() != 4 {
		t.Fatal("unavailable scoped policy reached an upstream")
	}
	policy.Rules = []state.ProjectEnvironmentEdgeRule{}
	if _, err := f.store.PutProjectEnvironmentEdgePolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	for _, server := range peers {
		request(server, http.StatusNoContent, "")
	}
	if forwards.Load() != 6 {
		t.Fatal("scoped policy repair did not restore both HTTP peers")
	}
}
