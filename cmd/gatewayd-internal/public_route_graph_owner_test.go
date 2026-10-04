// adr: 531
package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPublicRouteGraphPostgresForeignOverloadCannotBlockClaimedOwner(t *testing.T) {
	for _, bound := range []string{"rows", "bytes"} {
		t.Run(bound, func(t *testing.T) {
			f := newPublicRoutingPGFixture(t)
			f.deployment(t, "production", "sha256:owner-overload")
			host := f.app.Slug + ".apps.gregale.dev"
			f.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: f.app.Slug}})
			account, err := f.store.CreateAccount(t.Context(), "overloaded-foreign@test.local", api.PlanScale)
			if err != nil {
				t.Fatal(err)
			}
			foreign := f.sourceApp(t, "overloaded-foreign", account.ID)
			rows, padding := api.TrafficPolicyMaxHostRules+1, 0
			if bound == "bytes" {
				rows, padding = 130, api.TrafficPolicyMaxContractBytes
			}
			// Raw fixtures represent already-persisted foreign policy overload.
			if _, err := f.pool.Exec(t.Context(), `INSERT INTO edge_rules
				(account_id,app_id,match_host,match_path,kind,action)
				SELECT $1,$2,$3,'/*','route',jsonb_build_object('kind','route',
				'route',jsonb_build_object('target_app_slug',$4::text),'padding',repeat('x',$6::integer))
				FROM generate_series(1,$5::integer)`, account.ID, foreign.ID, host, foreign.Slug, rows, padding); err != nil {
				t.Fatal(err)
			}
			verdict, err := sqlc.New().ReadPublicHostEdgeRules(t.Context(), f.pool, sqlc.ReadPublicHostEdgeRulesParams{
				Host: host, RouteOnly: true, MaxRows: api.TrafficPolicyMaxHostRules, MaxBytes: api.TrafficPolicyMaxHostBytes})
			if err != nil || !verdict.Oversized || len(verdict.Data) != 0 {
				t.Fatalf("global overload fixture: oversized=%v bytes=%d err=%v", verdict.Oversized, len(verdict.Data), err)
			}
			g := newGatewaydEdgeRules(f.store, nil, nil, nil)
			ctx, app := f.compiledApp(t, g, host)
			if got := g.MatchRoute(ctx, host, "/", http.MethodGet); got == nil || got.AccountID != f.app.AccountID {
				t.Fatalf("claimed owner route unavailable: %+v", got)
			}
			if _, err := newPublicRoutingPinner(f.store)(ctx, app, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err != nil {
				t.Fatalf("foreign overload blocked owner dispatch: %v", err)
			}
		})
	}
}

func TestPublicRouteGraphPostgresClaimChangeRequiresFreshRequest(t *testing.T) {
	for _, change := range []string{"settings", "new-app", "reservation", "namespace", "owner"} {
		t.Run(change, func(t *testing.T) {
			f := newPublicRoutingPGFixture(t)
			f.deployment(t, "production", "sha256:root-claim")
			host := f.app.Slug + ".apps.gregale.dev"
			if change == "new-app" || change == "reservation" {
				host = "new-root.apps.gregale.dev"
			}
			if change == "reservation" {
				host = "new-root.example.test"
			}
			if change == "owner" {
				host = "owned-root.example.test"
				if _, err := f.store.CreateCustomDomain(t.Context(), host, f.app.ID, "verified"); err != nil {
					t.Fatal(err)
				}
				if err := f.store.MarkDomainVerified(t.Context(), host); err != nil {
					t.Fatal(err)
				}
			}
			var tenants atomic.Bool
			router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", tenantSurfacesEnabled: tenants.Load}
			g := newGatewaydEdgeRules(f.store, nil, nil, nil).withPublicHostRouter(router)
			ctx, err := g.PinHostPolicy(t.Context(), host)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "settings":
				_, err = f.pool.Exec(t.Context(), `UPDATE apps SET maintenance_mode=true WHERE id=$1`, f.app.ID)
			case "new-app":
				f.sourceApp(t, "new-root", f.app.AccountID)
			case "reservation":
				_, err = f.store.CreateCustomDomain(t.Context(), host, f.app.ID, "unverified")
			case "namespace":
				tenants.Store(true)
			case "owner":
				account, createErr := f.store.CreateAccount(t.Context(), "changed-owner@test.local", api.PlanPro)
				if createErr != nil {
					t.Fatal(createErr)
				}
				foreign := f.sourceApp(t, "changed-owner", account.ID)
				_, err = f.pool.Exec(t.Context(), `UPDATE custom_domains SET app_id=$2 WHERE domain=$1`, host, foreign.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := router.ResolveHost(ctx, host); err == nil {
				t.Fatal("changed root claim reused the pinned route graph")
			}
			fresh, err := g.PinHostPolicy(t.Context(), host)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := router.ResolveHost(fresh, host); err != nil {
				t.Fatalf("fresh request did not recover: %v", err)
			}
		})
	}
}

func TestPublicRouteGraphPostgresReservedAndImmutableSkipUnusedRead(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:immutable-root")
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
	g := newGatewaydEdgeRules(f.store, nil, nil, nil).withPublicHostRouter(router)
	lock, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(t.Context(), `LOCK TABLE edge_rules IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	alias, _ := api.DeploymentAliasHostLabel(f.app.ID, "missing")
	for _, host := range []string{alias + ".apps.gregale.dev", gateway.BuildDeploymentPreviewURL(".gregale.dev", 1, f.app.Slug)} {
		ctx, err := g.PinHostPolicy(t.Context(), host)
		if err != nil {
			t.Fatalf("unused route table blocked %s: %v", host, err)
		}
		claim := ctx.Value(publicRouteGraphsKey{}).(publicRouteGraphs).claims[host]
		if claim.Source.CanSubstitute || g.MatchRoute(ctx, host, "/", http.MethodGet) != nil {
			t.Fatalf("reserved/immutable host acquired a route: %+v", claim)
		}
	}
	if _, err := g.PinHostPolicy(t.Context(), f.app.Slug+".apps.gregale.dev"); err == nil {
		t.Fatal("claimed substitutable host skipped its required route read")
	}
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := g.PinHostPolicy(t.Context(), f.app.Slug+".apps.gregale.dev"); err != nil {
		t.Fatalf("required route read did not recover: %v", err)
	}
}

func TestPublicRouteGraphPostgresMissingOwnershipResolverRefusesHTTP(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev"}
	handler := gateway.NewHandlerWith(gateway.NewPGBackend(router, gateway.NewFakeScheduler("node"), nil), nil, nil)
	handler.WithEdgeRules(newGatewaydEdgeRules(f.store, nil, nil, nil), nil, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+f.app.Slug+".apps.gregale.dev/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured production resolver status=%d body=%s", rec.Code, rec.Body.String())
	}
}

type publicRouteGraphAfterReadStore struct {
	*state.PgStore
	after func()
}

func (s publicRouteGraphAfterReadStore) WithPublicHostPolicySnapshot(ctx context.Context, read func(state.PublicHostPolicyReader) error) error {
	if err := s.PgStore.WithPublicHostPolicySnapshot(ctx, read); err != nil {
		return err
	}
	s.after()
	return nil
}

func TestPublicRouteGraphPostgresHTTPRefusesCutoverBeforePureEdgeResponse(t *testing.T) {
	for _, protocol := range []string{"HTTP/1", "HTTP/2"} {
		t.Run(protocol, func(t *testing.T) {
			f := newPublicRoutingPGFixture(t)
			f.deployment(t, "production", "sha256:root-http")
			host := f.app.Slug + ".apps.gregale.dev"
			f.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindRedirect,
				Redirect: &state.EdgeRuleRedirectAction{StatusCode: http.StatusTemporaryRedirect, To: "/accepted"}})
			var changed atomic.Bool
			store := publicRouteGraphAfterReadStore{PgStore: f.store, after: func() {
				if !changed.Swap(true) {
					if _, err := f.pool.Exec(t.Context(), `UPDATE accounts SET plan='scale' WHERE id=$1`, f.app.AccountID); err != nil {
						t.Error(err)
					}
				}
			}}
			router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
			handler := gateway.NewHandlerWith(gateway.NewPGBackend(router, gateway.NewFakeScheduler("node"), nil), nil, nil)
			handler.WithEdgeRules(newGatewaydEdgeRules(store, nil, nil, nil).withPublicHostRouter(router), nil, nil)
			var forwarded atomic.Bool
			handler.WithForwarding(func(gateway.Target) http.Handler {
				return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Store(true) })
			})
			server := httptest.NewUnstartedServer(handler)
			server.EnableHTTP2 = protocol == "HTTP/2"
			if server.EnableHTTP2 {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			client := server.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			for _, want := range []int{http.StatusServiceUnavailable, http.StatusTemporaryRedirect} {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Host = host
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode != want || forwarded.Load() || protocol == "HTTP/2" && resp.ProtoMajor != 2 {
					t.Fatalf("root cutover response=%d want=%d protocol=%s forwarded=%v body=%s", resp.StatusCode, want, resp.Proto, forwarded.Load(), body)
				}
				if want == http.StatusTemporaryRedirect && (resp.Header.Get("Location") != "/accepted" || resp.Header.Get(gateway.TrafficPolicyRevisionHeader) == "") {
					t.Fatal("fresh edge response omitted its action or proof")
				}
			}
		})
	}
}
