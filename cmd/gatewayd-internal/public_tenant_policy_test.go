// adr: 375
package main

import (
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
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPublicTenantPostgresHTTPPeersRefuseAndRepairWithoutNotify(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:tenant-serving-bounds")
	const host = "http-tenant.example.test"
	limits := api.MustLimitsFor(api.PlanPro)
	surface, err := f.store.CreateTenantSurfaceIfUnderQuota(t.Context(), state.CreateTenantSurfaceParams{AccountID: f.app.AccountID, AppID: f.app.ID, Name: "traffic-tenant"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	hostname, err := f.store.CreateTenantHostnameIfUnderQuota(t.Context(), state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: strings.ToUpper(host), ChallengeToken: "private-tenant-token"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	f.edgeRule(t, host, scopedPublicHeaders("tenant-owner"))
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev", tenantSurfacesEnabled: func() bool { return true }}
	if _, err := newGatewaydEdgeRules(f.store, nil, nil, nil).withPublicHostRouter(router).PinHostPolicy(t.Context(), host); err != nil {
		t.Fatalf("pending tenant route graph: %v", err)
	}
	forwards := &atomic.Int32{}
	peers := make([]*httptest.Server, 0, 2)
	for _, http2 := range []bool{false, true} {
		handler := gateway.NewHandlerWith(gateway.NewPGBackend(router, domainRemovalScheduler{gateway.NewFakeScheduler("node").WithInstanceID(uuid.NewString())}, nil), nil, nil)
		handler.SetWakeGateHook()
		handler.WithPublicRoutingPolicy(newPublicRoutingPinner(f.store)).WithEdgeRules(newGatewaydEdgeRules(f.store, nil, nil, nil).withPublicHostRouter(router), nil, nil)
		handler.WithForwarding(func(gateway.Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forwards.Add(1); w.WriteHeader(http.StatusNoContent) })
		})
		peer := httptest.NewUnstartedServer(handler)
		peer.EnableHTTP2 = http2
		if http2 {
			peer.StartTLS()
		} else {
			peer.Start()
		}
		t.Cleanup(peer.Close)
		peer.Client().Timeout = 2 * time.Second
		peers = append(peers, peer)
	}
	request := func(status int) {
		t.Helper()
		for _, peer := range peers {
			req, err := http.NewRequest(http.MethodGet, peer.URL+"/", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = host
			response, err := peer.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil || response.StatusCode != status || peer.EnableHTTP2 && response.ProtoMajor != 2 {
				t.Fatalf("tenant HTTP response: status=%d proto=%s body=%s err=%v", response.StatusCode, response.Proto, body, readErr)
			}
			if status == http.StatusNoContent && (response.Header.Get("X-Environment") != "tenant-owner" || response.Header.Get(gateway.TrafficPolicyRevisionHeader) == "") {
				t.Fatalf("serving tenant omitted policy or revision: %v", response.Header)
			}
		}
	}
	seedOversized := func() string {
		t.Helper()
		legacy := scopedPublicHeaders("private-tenant-policy")
		legacy.Validate = &state.EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 519) + "1e130000]")}
		action, err := json.Marshal(legacy)
		if err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		if _, err := f.pool.Exec(t.Context(), `INSERT INTO edge_rules(id,account_id,app_id,match_host,match_path,enabled,kind,action) VALUES($1,$2,$3,$4,'/',true,'headers',$5::jsonb)`, id, f.app.AccountID, f.app.ID, host, action); err != nil {
			t.Fatal(err)
		}
		return id
	}
	request(http.StatusNotFound)
	legacyID := seedOversized()
	var aggregate *state.TrafficPolicyAggregateError
	if matched, err := f.store.MarkTenantHostnameVerifiedIfChallenge(t.Context(), host, hostname.ChallengeToken); matched || !errors.As(err, &aggregate) {
		t.Fatalf("tenant verification exposed oversized policy: matched=%v err=%v", matched, err)
	}
	request(http.StatusNotFound)
	if err := f.store.DeleteEdgeRule(t.Context(), legacyID); err != nil {
		t.Fatal(err)
	}
	if matched, err := f.store.MarkTenantHostnameVerifiedIfChallenge(t.Context(), host, hostname.ChallengeToken); err != nil || !matched {
		t.Fatalf("tenant verification after repair: matched=%v err=%v", matched, err)
	}
	policies := newGatewaydEdgeRules(f.store, nil, nil, nil).withPublicHostRouter(router)
	admittedContext, admitted := f.compiledAppWithRouter(t, policies, router, host)
	if _, err := newPublicRoutingPinner(f.store)(admittedContext, admitted, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err != nil {
		t.Fatalf("verified tenant dispatch policy: %v", err)
	}
	request(http.StatusNoContent)
	tenant, _, err := f.store.CreatePlatformTenant(t.Context(), f.app.AccountID, "http-tenant", "HTTP tenant", api.PlanPro.ConsumerKeysPerAccount())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.LinkPlatformTenantSurface(t.Context(), f.app.AccountID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	request(http.StatusNoContent)
	if _, err := f.store.SetPlatformTenantStatus(t.Context(), f.app.AccountID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	request(http.StatusNotFound)
	legacyID = seedOversized()
	if _, err := f.store.SetPlatformTenantStatus(t.Context(), f.app.AccountID, tenant.ID, state.PlatformTenantActive); !errors.As(err, &aggregate) {
		t.Fatalf("tenant reactivation exposed oversized policy: %v", err)
	}
	request(http.StatusNotFound)
	if err := f.store.DeleteEdgeRule(t.Context(), legacyID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SetPlatformTenantStatus(t.Context(), f.app.AccountID, tenant.ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	request(http.StatusNoContent)
	if forwards.Load() != 6 {
		t.Fatalf("unexpected forwarded exchanges: %d", forwards.Load())
	}
}

func TestPublicTenantRemovalPostgresHTTPPeersFallbackWithoutNotify(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:domain-before-removal")
	account, err := f.store.CreateAccount(t.Context(), "domain-fallback@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	peer := f
	peer.app = f.sourceApp(t, "domain-fallback", account.ID)
	peer.deployment(t, "default", "sha256:domain-after-removal")
	const host, wildcard = "http-removal.example.test", "*.example.test"
	limits := api.MustLimitsFor(api.PlanPro)
	surface, err := f.store.CreateTenantSurfaceIfUnderQuota(t.Context(), state.CreateTenantSurfaceParams{AccountID: f.app.AccountID, AppID: f.app.ID, Name: "tenant-removal"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateTenantHostnameIfUnderQuota(t.Context(), state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: strings.ToUpper(host), ChallengeToken: "private-tenant-token"}, limits); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkTenantHostnameVerified(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateCustomDomain(t.Context(), wildcard, peer.app.ID, "private-fallback-token"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDomainVerified(t.Context(), wildcard); err != nil {
		t.Fatal(err)
	}
	f.edgeRule(t, host, scopedPublicHeaders("original"))
	peer.edgeRule(t, host, scopedPublicHeaders("fallback"))
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev", tenantSurfacesEnabled: func() bool { return true }}
	policies := newGatewaydEdgeRules(f.store, nil, nil, nil).withPublicHostRouter(router)
	old, admitted := f.compiledAppWithRouter(t, policies, router, host)
	peers := make([]*httptest.Server, 0, 2)
	forwards := &atomic.Int32{}
	for _, h2 := range []bool{false, true} {
		scheduler := domainRemovalScheduler{gateway.NewFakeScheduler("node").WithInstanceID(uuid.NewString())}
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
	request := func(server *httptest.Server, value string) string {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusNoContent || server.EnableHTTP2 && response.ProtoMajor != 2 || response.Header.Get("X-Environment") != value {
			t.Fatalf("tenant fallback HTTP response: status=%d headers=%v body=%s err=%v", response.StatusCode, response.Header, body, err)
		}
		proof := response.Header.Get(gateway.TrafficPolicyRevisionHeader)
		if proof == "" {
			t.Fatal("domain response omitted policy revision")
		}
		return proof
	}
	prior := make([]string, len(peers))
	for i, server := range peers {
		prior[i] = request(server, "original")
	}
	legacy := scopedPublicHeaders("private-fallback-policy")
	legacy.Validate = &state.EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 519) + "1e130000]")}
	action, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyID := uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO edge_rules(id,account_id,app_id,match_host,match_path,enabled,kind,action) VALUES($1,$2,$3,$4,'/',true,'headers',$5::jsonb)`, legacyID, account.ID, peer.app.ID, host, action); err != nil {
		t.Fatal(err)
	}
	var aggregate *state.TrafficPolicyAggregateError
	if err := f.store.DeleteTenantHostnameForSurface(t.Context(), host, surface.ID); !errors.As(err, &aggregate) || aggregate.Observed <= aggregate.Limit {
		t.Fatalf("removal exposed overloaded fallback: %v", err)
	}
	for i, server := range peers {
		if proof := request(server, "original"); proof != prior[i] {
			t.Fatal("refused removal changed the serving policy")
		}
	}
	if err := f.store.DeleteEdgeRule(t.Context(), legacyID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteTenantHostnameForSurface(t.Context(), host, surface.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := newPublicRoutingPinner(f.store)(old, admitted, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err == nil {
		t.Fatal("removed tenant claim authorized the old owner's dispatch")
	}
	if headers := policies.MatchHeaders(old, host, "/", "GET"); headers == nil || headers.ResponseHeaders[0].Value != "original" {
		t.Fatal("removal mutated an admitted policy")
	}
	for i, server := range peers {
		if proof := request(server, "fallback"); proof == prior[i] {
			t.Fatal("missed notification retained the removed owner's proof")
		}
	}
	if forwards.Load() != 6 {
		t.Fatalf("unexpected forwarded exchange count: %d", forwards.Load())
	}
}
