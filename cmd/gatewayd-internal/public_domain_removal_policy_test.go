// adr: 570
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
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// Keep the fake target inside the deployment chosen by the real routing read.
type domainRemovalScheduler struct{ *gateway.FakeScheduler }

func (s domainRemovalScheduler) AdmitInstance(ctx context.Context, app, deployment, scope, trigger string) (string, string, string, string, int32, bool, int, error) {
	instance, node, _, wake, method, restored, port, err := s.FakeScheduler.AdmitInstance(ctx, app, deployment, scope, trigger)
	return instance, node, deployment, wake, method, restored, port, err
}

func TestPublicDomainRemovalPostgresHTTPPeersFallbackWithoutNotify(t *testing.T) {
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
	for name, owner := range map[string]string{host: f.app.ID, wildcard: peer.app.ID} {
		if _, err := f.store.CreateCustomDomain(t.Context(), name, owner, "private-token"); err != nil {
			t.Fatal(err)
		}
		if err := f.store.MarkDomainVerified(t.Context(), name); err != nil {
			t.Fatal(err)
		}
	}
	f.edgeRule(t, host, scopedPublicHeaders("original"))
	peer.edgeRule(t, host, scopedPublicHeaders("fallback"))
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
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
		if err != nil || response.StatusCode != http.StatusNoContent || response.Header.Get("X-Environment") != value {
			t.Fatalf("domain HTTP response: status=%d headers=%v body=%s err=%v", response.StatusCode, response.Header, body, err)
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
	if err := f.store.DeleteCustomDomainForApp(t.Context(), host, f.app.ID); !errors.As(err, &aggregate) || aggregate.Observed <= aggregate.Limit {
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
	if err := f.store.DeleteCustomDomainForApp(t.Context(), host, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := newPublicRoutingPinner(f.store)(old, admitted, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err == nil {
		t.Fatal("removed exact claim authorized the old owner's dispatch")
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

func TestPublicDomainPublicationPostgresHTTPPeersShadowAndActivateWithoutNotify(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:publication-new-owner")
	account, err := f.store.CreateAccount(t.Context(), "publication-fallback@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	peer := f
	peer.app = f.sourceApp(t, "publication-fallback", account.ID)
	peer.deployment(t, "default", "sha256:publication-prior-owner")
	const host, wildcard = "http-publication.example.test", "*.example.test"
	if _, err := f.store.CreateCustomDomain(t.Context(), wildcard, peer.app.ID, "private-fallback-token"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDomainVerified(t.Context(), wildcard); err != nil {
		t.Fatal(err)
	}
	f.edgeRule(t, host, scopedPublicHeaders("new-owner"))
	peer.edgeRule(t, host, scopedPublicHeaders("prior-owner"))
	router := pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", deploySuffix: ".gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
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
	request := func(server *httptest.Server, status int, value string) string {
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
		if err != nil || response.StatusCode != status || response.Header.Get("X-Environment") != value {
			t.Fatalf("publication response: status=%d headers=%v body=%s err=%v", response.StatusCode, response.Header, body, err)
		}
		if server.EnableHTTP2 && response.ProtoMajor != 2 {
			t.Fatal("HTTP/2 peer did not negotiate HTTP/2")
		}
		proof := response.Header.Get(gateway.TrafficPolicyRevisionHeader)
		if status == http.StatusNoContent && proof == "" {
			t.Fatal("serving publication omitted its revision proof")
		}
		return proof
	}
	prior := make([]string, len(peers))
	for i, server := range peers {
		prior[i] = request(server, http.StatusNoContent, "prior-owner")
	}
	claim, err := f.store.CreateCustomDomainIfUnderQuota(t.Context(), host, f.app.ID, "current-private-token", 100, 500)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newPublicRoutingPinner(f.store)(old, admitted, gateway.PublicRoutingInputs{Valid: true, Scope: "default"}); err == nil {
		t.Fatal("pending exact claim authorized the old wildcard dispatch")
	}
	if headers := policies.MatchHeaders(old, host, "/", "GET"); headers == nil || headers.ResponseHeaders[0].Value != "prior-owner" {
		t.Fatal("pending publication mutated an admitted policy")
	}
	for _, server := range peers {
		request(server, http.StatusNotFound, "")
	}
	legacy := scopedPublicHeaders("private-new-owner-policy")
	legacy.Validate = &state.EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 519) + "1e130000]")}
	action, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyID := uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO edge_rules(id,account_id,app_id,match_host,match_path,enabled,kind,action) VALUES($1,$2,$3,$4,'/',true,'headers',$5::jsonb)`, legacyID, f.app.AccountID, f.app.ID, host, action); err != nil {
		t.Fatal(err)
	}
	var aggregate *state.TrafficPolicyAggregateError
	if matched, err := f.store.MarkDomainVerifiedIfChallenge(t.Context(), host, claim.ChallengeToken); matched || !errors.As(err, &aggregate) {
		t.Fatalf("activation exposed oversized new owner: matched=%v err=%v", matched, err)
	}
	for _, server := range peers {
		request(server, http.StatusNotFound, "")
	}
	if err := f.store.DeleteEdgeRule(t.Context(), legacyID); err != nil {
		t.Fatal(err)
	}
	if matched, err := f.store.MarkDomainVerifiedIfChallenge(t.Context(), host, claim.ChallengeToken); err != nil || !matched {
		t.Fatalf("repaired activation failed: matched=%v err=%v", matched, err)
	}
	for i, server := range peers {
		if proof := request(server, http.StatusNoContent, "new-owner"); proof == prior[i] {
			t.Fatal("missed notification retained the previous owner's proof")
		}
	}
	if forwards.Load() != 4 {
		t.Fatalf("blocked publications forwarded: exchanges=%d", forwards.Load())
	}
}
