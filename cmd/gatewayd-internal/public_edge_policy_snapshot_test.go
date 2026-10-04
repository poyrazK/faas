// adr: 531
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (f publicRoutingPGFixture) edgeRule(t *testing.T, host string, action state.EdgeRuleAction) state.EdgeRule {
	t.Helper()
	rule, err := f.store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{
		AccountID: f.app.AccountID, AppID: f.app.ID, MatchHost: host, MatchPath: "/*",
		Enabled: true, Kind: action.Kind, Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func (f publicRoutingPGFixture) corsPreset(t *testing.T, origins ...string) state.CorsPreset {
	t.Helper()
	limits, _ := api.LimitsFor(api.PlanPro)
	preset, err := f.store.CreateCorsPresetIfUnderQuota(t.Context(), state.CorsPreset{
		AccountID: f.app.AccountID, Name: "public", AllowOrigins: origins, AllowMethods: []string{"GET"}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	return preset
}

func (f publicRoutingPGFixture) compiledApp(t *testing.T, g *gatewaydEdgeRules, host string) (context.Context, gateway.App) {
	t.Helper()
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
	return f.compiledAppWithRouter(t, g, router, host)
}

func (f publicRoutingPGFixture) compiledAppWithRouter(t *testing.T, g *gatewaydEdgeRules, router pgRouter, host string) (context.Context, gateway.App) {
	t.Helper()
	if g.publicHostSource == nil {
		g.withPublicHostRouter(router)
	}
	ctx, err := g.PinHostPolicy(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	app, found, err := router.ResolveHost(ctx, host)
	if err != nil || !found || len(app.PublicCompiledPolicies) != 1 {
		t.Fatalf("compiled app: found=%v policies=%d err=%v", found, len(app.PublicCompiledPolicies), err)
	}
	ctx, err = gateway.WithPinnedHostPolicy(ctx, host, app.PublicCompiledPolicies[host])
	if err != nil {
		t.Fatal(err)
	}
	return gateway.WithEdgeRuleOwner(ctx, app.AccountID), app
}

func TestPublicEdgePolicyPostgresFreshReadsAndImmutableAdmission(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:edge")
	host := f.app.Slug + ".apps.gregale.dev"
	preset := f.corsPreset(t, "https://old.example.test")
	f.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{CorsPresetID: &preset.ID}})
	g := newGatewaydEdgeRules(f.store, nil, nil, nil)
	ctx, admitted := f.compiledApp(t, g, host)
	inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "production"}
	for range 2 {
		if _, err := newPublicRoutingPinner(f.store)(ctx, admitted, inputs); err != nil {
			t.Fatalf("cache changed host metadata: %v", err)
		}
	}
	oldRevision := gateway.PinnedHostPolicyRevision(ctx, host)
	preset.AllowOrigins = []string{"https://new.example.test"}
	if _, err := f.store.UpdateCorsPreset(t.Context(), f.app.AccountID, preset.ID, preset); err != nil {
		t.Fatal(err)
	}
	if _, err := newPublicRoutingPinner(f.store)(ctx, admitted, inputs); err == nil {
		t.Fatal("changed preset authorized old dispatch")
	}
	freshCtx, _ := f.compiledApp(t, g, host) // No invalidation notification or Reset.
	if got := g.MatchCORS(freshCtx, host, "/", "GET"); got == nil || got.AllowOrigins[0] != "https://new.example.test" {
		t.Fatalf("fresh preset: %+v", got)
	}
	if gateway.PinnedHostPolicyRevision(freshCtx, host) == oldRevision {
		t.Fatal("preset update retained policy proof")
	}
	g.Reset()
	if got := g.MatchCORS(ctx, host, "/", "GET"); got == nil || got.AllowOrigins[0] != "https://old.example.test" {
		t.Fatalf("admitted policy changed after reset: %+v", got)
	}
	encoded, err := json.Marshal(admitted)
	if err != nil || strings.Contains(string(encoded), "old.example.test") {
		t.Fatalf("compiled actions escaped private carrier: %v", err)
	}
}

func TestPublicEdgePolicyPostgresAppRulesAndPresetsShareView(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:edge-view")
	host := f.app.Slug + ".apps.gregale.dev"
	preset := f.corsPreset(t, "https://before.example.test")
	rule := f.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{CorsPresetID: &preset.ID}})
	g := newGatewaydEdgeRules(f.store, nil, nil, nil)
	ctx, before := f.compiledApp(t, g, host)
	var changed atomic.Bool
	router := pgRouter{store: publicHostAfterAppStore{PgStore: f.store, after: func() {
		if changed.Swap(true) {
			return
		}
		if _, err := f.pool.Exec(t.Context(), `UPDATE apps SET maintenance_mode=true WHERE id=$1`, f.app.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(t.Context(), `UPDATE edge_rules SET priority=7 WHERE id=$1`, rule.ID); err != nil {
			t.Fatal(err)
		}
		preset.AllowOrigins = []string{"https://after.example.test"}
		if _, err := f.store.UpdateCorsPreset(t.Context(), f.app.AccountID, preset.ID, preset); err != nil {
			t.Fatal(err)
		}
	}}, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
	during, found, err := router.ResolveHost(ctx, host)
	if err != nil || !found || during.PublicPolicySource.Revision != before.PublicPolicySource.Revision || during.MaintenanceMode {
		t.Fatalf("app/rule/preset mixed views: found=%v err=%v", found, err)
	}
	if _, err := newPublicRoutingPinner(f.store)(ctx, during, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err == nil {
		t.Fatal("mixed dispatch authorized after cutover")
	}
	_, after := f.compiledApp(t, g, host)
	entry := after.PublicCompiledPolicies[host]
	if !after.MaintenanceMode || len(entry.CORS) != 1 || entry.CORS[0].AllowOrigins[0] != "https://after.example.test" || entry.CORS[0].Priority != 7 {
		t.Fatalf("fresh compiled view: %+v", entry.CORS)
	}
}

func TestPublicEdgePolicyPostgresForeignPresetDoesNotBlockOwner(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:edge-owner")
	host := f.app.Slug + ".apps.gregale.dev"
	account, err := f.store.CreateAccount(t.Context(), "foreign-edge@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign := f.sourceApp(t, "foreign-edge", account.ID)
	f2 := f
	f2.app = foreign
	preset := f2.corsPreset(t, "https://foreign.example.test")
	f2.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{CorsPresetID: &preset.ID}})
	lock, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(t.Context(), `LOCK TABLE cors_presets IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	g := newGatewaydEdgeRules(f.store, nil, nil, nil)
	ctx, app := f.compiledApp(t, g, host)
	if got := g.MatchCORS(ctx, host, "/", "GET"); got != nil {
		t.Fatalf("foreign CORS entered owner policy: %+v", got)
	}
	if _, err := newPublicRoutingPinner(f.store)(ctx, app, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err != nil {
		t.Fatalf("unused foreign preset lock blocked owner: %v", err)
	}
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE cors_presets SET allow_origins=ARRAY['*'], allow_credentials=true WHERE id=$1`, preset.ID); err != nil {
		t.Fatal(err)
	}
	f.compiledApp(t, g, host)
	// Owner policy must refuse the same failure when the preset is referenced.
	f.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{CorsPresetID: &preset.ID}})
	if _, _, err := (pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev", tenantSurfacesEnabled: func() bool { return false }}).ResolveHost(ctx, host); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-owner preset was accepted: %v", err)
	}
}

func TestPublicEdgePolicyPostgresTwoHTTPGatewaysRepairWithoutNotify(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	deployment := f.deployment(t, "production", "sha256:edge-http")
	host := f.app.Slug + ".apps.gregale.dev"
	preset := f.corsPreset(t, "https://old.example.test")
	f.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{CorsPresetID: &preset.ID}})
	ip := f.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"192.0.2.0/24"}}})
	type peer struct {
		server   *httptest.Server
		forwards *atomic.Int32
		before   string
	}
	peers := make([]peer, 0, 2)
	for _, h2 := range []bool{false, true} {
		router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
		scheduler := gateway.NewFakeScheduler("node").WithInstanceID(uuid.NewString()).WithDeploymentID(deployment.ID)
		handler := gateway.NewHandlerWith(gateway.NewPGBackend(router, scheduler, nil), nil, nil)
		handler.SetWakeGateHook()
		handler.WithPublicRoutingPolicy(newPublicRoutingPinner(f.store)).WithEdgeRules(newGatewaydEdgeRules(f.store, nil, nil, nil).withPublicHostRouter(router), nil, nil).WithEdgeTargetPolicyLoader(
			func(ctx context.Context, slug string) (gateway.App, bool, error) {
				return router.resolvePublicAppSlug(ctx, slug)
			})
		forwards := &atomic.Int32{}
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
		peers = append(peers, peer{server: server, forwards: forwards})
	}
	request := func(p peer, origin string, status int) string {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, p.server.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		req.Header.Set("Origin", origin)
		req.Header.Set("X-Forwarded-For", "198.51.100.7") // The internal listener receives this from the public gateway.
		resp, err := p.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != status {
			t.Fatalf("HTTP edge status=%d want=%d body=%s", resp.StatusCode, status, body)
		}
		if status == http.StatusNoContent && resp.Header.Get("Access-Control-Allow-Origin") != origin {
			t.Fatalf("stale CORS header: %v", resp.Header)
		}
		proof := resp.Header.Get(gateway.TrafficPolicyRevisionHeader)
		if proof == "" {
			t.Fatal("edge response omitted policy proof")
		}
		return proof
	}
	for i := range peers {
		peers[i].before = request(peers[i], "https://old.example.test", http.StatusNoContent)
	}
	if _, err := f.store.UpdateEdgeRule(t.Context(), ip.ID, state.UpdateEdgeRuleParams{Action: &state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"0.0.0.0/0", "::/0"}}}}); err != nil {
		t.Fatal(err)
	}
	for _, p := range peers {
		if proof := request(p, "https://old.example.test", http.StatusForbidden); proof == p.before || p.forwards.Load() != 1 {
			t.Fatal("stale IP policy reached forwarder")
		}
	}
	if _, err := f.store.UpdateEdgeRule(t.Context(), ip.ID, state.UpdateEdgeRuleParams{Action: &state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{}}}); err != nil {
		t.Fatal(err)
	}
	preset.AllowOrigins = []string{"https://new.example.test"}
	if _, err := f.store.UpdateCorsPreset(t.Context(), f.app.AccountID, preset.ID, preset); err != nil {
		t.Fatal(err)
	}
	for _, p := range peers {
		if proof := request(p, "https://new.example.test", http.StatusNoContent); proof == p.before || p.forwards.Load() != 2 {
			t.Fatal("preset repair retained stale policy")
		}
	}
}

func TestPublicEdgePolicyPostgresRouteGraphCannotChangeOwner(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:edge-routes")
	const host = "synthetic.example.test"
	old := f.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: f.app.Slug}})
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
	g := newGatewaydEdgeRules(f.store, nil, nil, nil).withPublicHostRouter(router)
	ctx, err := g.PinHostPolicy(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := router.ResolveHost(ctx, host); err != nil || found {
		t.Fatalf("initial synthetic graph: %v/%v", found, err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE edge_rules SET priority=100 WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	account, err := f.store.CreateAccount(t.Context(), "synthetic-owner@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign := f.sourceApp(t, "synthetic-owner", account.ID)
	f2 := f
	f2.app = foreign
	f2.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: foreign.Slug}})
	if _, _, err := router.ResolveHost(ctx, host); err == nil {
		t.Fatal("new synthetic route reused the old global graph")
	}
	fresh, err := g.PinHostPolicy(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := router.ResolveHost(fresh, host); err != nil || found {
		t.Fatalf("fresh synthetic route did not recover: %v/%v", found, err)
	}
	if got := g.MatchRoute(fresh, host, "/", "GET"); got == nil || got.AccountID != account.ID {
		t.Fatalf("fresh route chose stale owner: %+v", got)
	}
}

func TestPublicEdgePolicyPostgresSourceTargetAgreementBeforePureEdgeResponse(t *testing.T) {
	for _, failure := range []string{"unchanged-claim", "unchanged-negative", "source-cutover", "target-outage", "missing-target"} {
		t.Run(failure, func(t *testing.T) {
			f := newPublicRoutingPGFixture(t)
			f.deployment(t, "production", "sha256:edge-source")
			var source state.App
			if failure != "unchanged-negative" {
				source = f.sourceApp(t, "edge-source", f.app.AccountID)
			}
			host := "edge-source.apps.gregale.dev"
			f.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: f.app.Slug}})
			f.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindRedirect, Redirect: &state.EdgeRuleRedirectAction{StatusCode: http.StatusTemporaryRedirect, To: "/accepted"}})
			router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
			handler := gateway.NewHandlerWith(gateway.NewPGBackend(router, gateway.NewFakeScheduler("node"), nil), nil, nil)
			handler.WithEdgeRules(newGatewaydEdgeRules(f.store, nil, nil, nil).withPublicHostRouter(router), nil, nil).WithEdgeTargetPolicyLoader(func(ctx context.Context, slug string) (gateway.App, bool, error) {
				switch failure {
				case "source-cutover":
					if _, err := f.pool.Exec(ctx, `UPDATE apps SET maintenance_mode=true WHERE id=$1`, source.ID); err != nil {
						t.Fatal(err)
					}
				case "target-outage":
					return gateway.App{}, false, errors.New("target policy unavailable")
				case "missing-target":
					return gateway.App{}, false, nil
				}
				return router.resolvePublicAppSlug(ctx, slug)
			})
			forwarded := false
			handler.WithForwarding(func(gateway.Target) http.Handler {
				return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded = true })
			})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil))
			want := http.StatusServiceUnavailable
			if strings.HasPrefix(failure, "unchanged") {
				want = http.StatusTemporaryRedirect
			}
			if rec.Code != want || forwarded {
				t.Fatalf("source/target agreement: status=%d want=%d forwarded=%v body=%s", rec.Code, want, forwarded, rec.Body.String())
			}
			if want == http.StatusTemporaryRedirect && (rec.Header().Get(gateway.TrafficPolicyRevisionHeader) == "" || rec.Header().Get("Location") != "/accepted") {
				t.Fatal("verified pure edge answer omitted its action or proof")
			}
		})
	}
}

func TestPublicEdgePolicyPostgresBoundsOutageAndCleanup(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:edge-bounds")
	host := f.app.Slug + ".apps.gregale.dev"
	preset := f.corsPreset(t, "https://allowed.example.test")
	f.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{CorsPresetID: &preset.ID}})
	f.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{}})
	owner := pgtype.UUID{Bytes: uuid.MustParse(f.app.AccountID), Valid: true}
	for _, limits := range []struct{ rows, bytes int32 }{{1, api.TrafficPolicyMaxHostBytes}, {api.TrafficPolicyMaxHostRules, 32}} {
		row, err := sqlc.New().ReadPublicHostEdgeRules(t.Context(), f.pool, sqlc.ReadPublicHostEdgeRulesParams{Host: host, AccountID: owner, MaxRows: limits.rows, MaxBytes: limits.bytes})
		if err != nil || !row.Oversized || len(row.Data) != 0 {
			t.Fatalf("oversized SQL projection transferred rows: oversized=%v bytes=%d err=%v", row.Oversized, len(row.Data), err)
		}
	}
	g := newGatewaydEdgeRules(f.store, nil, nil, nil)
	ctx, admitted := f.compiledApp(t, g, host)
	lock, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(t.Context(), `LOCK TABLE cors_presets IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := newPublicRoutingPinner(f.store)(bounded, admitted, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err == nil {
		t.Fatal("cached policy authorized during store failure")
	}
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := newPublicRoutingPinner(f.store)(ctx, admitted, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err != nil {
		t.Fatalf("store recovery: %v", err)
	}
	var escaped state.PublicHostPolicyReader
	if err := f.store.WithPublicHostPolicySnapshot(t.Context(), func(reader state.PublicHostPolicyReader) error {
		escaped = reader.NewProjectionReader()
		for _, account := range []string{"", "invalid-uuid"} {
			if _, err := reader.PublicHostEdgeRules(t.Context(), host, account, false); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("invalid owner widened the projection: %v", err)
			}
		}
		if rows, err := reader.PublicHostEdgeRules(t.Context(), host, uuid.NewString(), false); err != nil || len(rows) != 0 {
			t.Fatalf("rules crossed owner: %d/%v", len(rows), err)
		}
		if _, err := reader.GetCorsPresetByID(t.Context(), uuid.NewString(), preset.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("preset crossed owner: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := escaped.PublicHostEdgeRules(t.Context(), host, f.app.AccountID, false); err == nil {
		t.Fatal("projection reader escaped transaction cleanup")
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE cors_presets SET allow_origins=ARRAY[repeat('x',$2::integer)] WHERE id=$1`, preset.ID, api.TrafficPolicyMaxContractBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := f.store.WithPublicHostPolicySnapshot(t.Context(), func(reader state.PublicHostPolicyReader) error {
		row, err := reader.GetCorsPresetByID(t.Context(), f.app.AccountID, preset.ID)
		if err == nil || row.ID != "" {
			t.Fatalf("oversized preset entered projection: %+v/%v", row.ID, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := newPublicRoutingPinner(f.store)(ctx, admitted, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err == nil {
		t.Fatal("oversized preset authorized dispatch")
	}
}

func TestPublicEdgePolicyPostgresEnvironmentOverlayRepairsWithoutNotify(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	deployment := f.deployment(t, "staging", "sha256:edge-environment")
	environment, err := f.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: f.app.AccountID, ProjectID: f.project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	host := gateway.BuildEnvironmentHost(".gregale.dev", environment.ID, f.app.ID)
	f.edgeRule(t, host, state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
		ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Environment", Action: "set", Value: "global"}}}})
	g := newGatewaydEdgeRules(f.store, nil, nil, nil)
	ctx, admitted := f.compiledApp(t, g, host)
	if got := g.MatchHeaders(ctx, host, "/", "GET"); got == nil || got.ResponseHeaders[0].Value != "global" {
		t.Fatalf("missing overlay did not preserve fallback: %+v", got)
	}
	policy := state.ProjectEnvironmentEdgePolicy{AccountID: f.app.AccountID, ProjectID: f.project.ID, AppID: f.app.ID, EnvironmentSlug: "staging",
		Rules: []state.ProjectEnvironmentEdgeRule{{Kind: state.EdgeRuleKindHeaders, Enabled: true, MatchPath: "/*",
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
				ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Environment", Action: "set", Value: "staging"}}}}}}}
	if _, err := f.store.PutProjectEnvironmentEdgePolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "staging", HostDeploymentID: deployment.ID, HostScope: "staging"}
	if _, err := newPublicRoutingPinner(f.store)(ctx, admitted, inputs); err == nil {
		t.Fatal("new overlay combined with old policy baseline")
	}
	fresh, _ := f.compiledApp(t, g, host)
	if got := g.MatchHeaders(fresh, host, "/", "GET"); got == nil || got.ResponseHeaders[0].Value != "staging" {
		t.Fatalf("missed notification retained global policy: %+v", got)
	}
	if got := g.MatchHeaders(ctx, host, "/", "GET"); got == nil || got.ResponseHeaders[0].Value != "global" {
		t.Fatalf("admitted overlay fallback changed: %+v", got)
	}
	policy.Rules = []state.ProjectEnvironmentEdgeRule{}
	if _, err := f.store.PutProjectEnvironmentEdgePolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	empty, _ := f.compiledApp(t, g, host)
	if got := g.MatchHeaders(empty, host, "/", "GET"); got != nil {
		t.Fatalf("empty overlay inherited shared headers: %+v", got)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE project_environment_edge_policies
		SET rules=jsonb_build_array(jsonb_build_object('padding',repeat('x',$2::integer))) WHERE app_id=$1 AND environment_slug='staging'`, f.app.ID, api.TrafficPolicyMaxContractBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := f.store.WithPublicHostPolicySnapshot(t.Context(), func(reader state.PublicHostPolicyReader) error {
		policy, err := reader.GetProjectEnvironmentEdgePolicy(t.Context(), f.app.AccountID, f.app.ID, "staging")
		if err == nil || len(policy.Rules) != 0 {
			t.Fatalf("oversized environment policy transferred: rules=%d err=%v", len(policy.Rules), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
