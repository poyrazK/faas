// adr: 375
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

func TestServiceTrafficRevocationRefusesCallerTargetAndRetryDeployment(t *testing.T) {
	for _, kind := range []string{"caller", "source-deployment", "account", "target", "retry-deployment"} {
		t.Run(kind, func(t *testing.T) {
			store := &gatewaySecurityStore{}
			registry := trafficrevocation.New(store)
			defer registry.Close()
			scopes := map[string]trafficrevocation.Scope{
				"caller": {Kind: "app", ID: "caller"}, "source-deployment": {Kind: "deployment", ID: "source"},
				"account": {Kind: "account", ID: "account"}, "target": {Kind: "app", ID: "target"},
				"retry-deployment": {Kind: "deployment", ID: "dep-b"},
			}
			if kind != "retry-deployment" {
				store.set(scopes[kind], 1, true)
			}
			provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "target", Endpoints: []ServiceEndpoint{
				{InstanceID: "a", NodeID: "node", Port: 8080, DeploymentID: "dep-a"}, {InstanceID: "b", NodeID: "node", Port: 8080, DeploymentID: "dep-b"},
			}}}
			forwarded, wakes := 0, 0
			proxy := NewServiceProxy(ServiceProxyConfig{
				TrafficRevocations: registry, Provider: provider,
				ResolveCallerIdentity: func(context.Context, string) (string, string, error) { return "caller", "source", nil },
				Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
					return ServiceTarget{AppID: "target"}, true, nil
				},
				Authorize: func(context.Context, string, string) (ServiceCaller, error) {
					return ServiceCaller{AppID: "caller", AccountID: "account"}, nil
				},
				Wake: func(context.Context, string) error { wakes++; return nil },
				Forward: func(Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						forwarded++
						store.set(scopes["retry-deployment"], 1, true)
						markStaleTarget(r.Context())
						http.Error(w, "stale", http.StatusServiceUnavailable)
					})
				},
			})
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/", nil))
			wantForwarded := 0
			if kind == "retry-deployment" {
				wantForwarded = 1
			}
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeTrafficRevoked) || forwarded != wantForwarded || wakes != 0 {
				t.Fatalf("status=%d body=%q forwarded=%d wakes=%d", rec.Code, rec.Body, forwarded, wakes)
			}
			if exchanges, scopes := registry.Tracked(); exchanges != 0 || scopes != 0 {
				t.Fatalf("service leaked registrations: %d/%d", exchanges, scopes)
			}
		})
	}
}

func TestTrafficRevocationCapsScopesAcrossAttempts(t *testing.T) {
	registry := trafficrevocation.New(&gatewaySecurityStore{})
	defer registry.Close()
	r := httptest.NewRequest(http.MethodGet, "http://app/", nil)
	defer func() { cancelStampedRequestBudget(r.Context()) }()
	for index := 0; index < api.TrafficSecurityMaxRequestScopes; index++ {
		if enrollTrafficScopes(httptest.NewRecorder(), r, registry, trafficrevocation.Scope{Kind: "deployment", ID: string(rune('a' + index))}) {
			t.Fatal("valid bounded enrollment refused")
		}
	}
	rec := httptest.NewRecorder()
	if !enrollTrafficScopes(rec, r, registry, trafficrevocation.Scope{Kind: "deployment", ID: "overflow"}) {
		t.Fatal("cross-attempt scope cap bypassed")
	}
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"limit":16`) || !strings.Contains(rec.Body.String(), `"observed":17`) || !strings.Contains(rec.Body.String(), `"docs_url"`) {
		t.Fatalf("capacity evidence=%d/%s", rec.Code, rec.Body)
	}
}

func TestPublicTrafficRetryVerifiesSiblingDeployment(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	store := &gatewaySecurityStore{}
	registry := trafficrevocation.New(store)
	defer registry.Close()
	h.WithTrafficRevocations(registry).WithRetryEnabled(true).WithRetryDefault(testPolicy())
	backend.targets = []Target{{InstanceID: "b", NodeID: "node", DeploymentID: "dep-b"}}
	r := httptest.NewRequest(http.MethodGet, "http://app/", nil)
	defer func() { cancelStampedRequestBudget(r.Context()) }()
	if enrollTrafficScopes(httptest.NewRecorder(), r, registry, trafficrevocation.Scope{Kind: "account", ID: "acct-1"}, trafficrevocation.Scope{Kind: "app", ID: "app-1"}) {
		t.Fatal("initial admission refused")
	}
	forwarded := 0
	rec := httptest.NewRecorder()
	h.proxyAttempt(rec, r, Target{InstanceID: "a", NodeID: "node", DeploymentID: "dep-a"}, false, func(Target) {}, func(w http.ResponseWriter, r *http.Request, target Target) {
		forwarded++
		store.set(trafficrevocation.Scope{Kind: "deployment", ID: "dep-b"}, 1, true)
		deadTargetAttempt(w, r, target)
	}, backend.app)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeTrafficRevoked) || forwarded != 1 {
		t.Fatalf("retry=%d/%s forwards=%d", rec.Code, rec.Body, forwarded)
	}
}

func TestPublicTrafficInitialAccountContractsAndReleasedCachedFlags(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "suspension", true: "hold"}[held], func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			backend.setLegacyHot()
			backend.targets[0].DeploymentID = "dep"
			backend.app.AccountStatus = "suspended"
			backend.app.AccountAbuseHeld = held
			store := &gatewaySecurityStore{}
			store.set(trafficrevocation.Scope{Kind: "account", ID: "acct-1"}, 1, true)
			registry := trafficrevocation.New(store)
			defer registry.Close()
			h.WithTrafficRevocations(registry).WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
			want := api.ErrAccountSuspended()
			if held {
				want = api.ErrAccountAbuseHold()
			}
			if rec.Code != want.Status || !strings.Contains(rec.Body.String(), want.Code) {
				t.Fatalf("initial account contract=%d/%s", rec.Code, rec.Body)
			}
			store.set(trafficrevocation.Scope{Kind: "account", ID: "acct-1"}, 2, false)
			fresh := httptest.NewRecorder()
			h.ServeHTTP(fresh, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
			if fresh.Code != http.StatusNoContent {
				t.Fatalf("cached old suspension survived verified release: %d/%s", fresh.Code, fresh.Body)
			}
		})
	}
}
