package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type gitOpsServicePolicyStore struct {
	MemStore *state.MemStore
	route    state.EnvironmentGitOpsServiceBindingRoute
}

func (s *gitOpsServicePolicyStore) ResolveEnvironmentGitOpsServiceBinding(context.Context, string, string, string) (state.EnvironmentGitOpsServiceBindingRoute, bool, bool, error) {
	return s.route, true, s.route.TargetAppID != "", nil
}

type gitOpsPolicyEndpointProvider struct {
	target, deployment string
	calls              int
}

func (p *gitOpsPolicyEndpointProvider) ServiceEndpoints(context.Context, string) (gateway.ServiceEndpointsSnapshot, error) {
	p.calls++
	return gateway.ServiceEndpointsSnapshot{AppID: p.target, Endpoints: []gateway.ServiceEndpoint{{
		InstanceID: uuid.NewString(), NodeID: uuid.NewString(), DeploymentID: p.deployment, Port: 8080,
	}}}, nil
}

func gitOpsPolicyFixture(t *testing.T, policy api.ServiceBindingPolicy) (*gitOpsServicePolicyStore, state.App, *gitOpsPolicyEndpointProvider, gateway.ServiceProxyConfig) {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "gitops-policy@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "gitops-policy"})
	if err != nil {
		t.Fatal(err)
	}
	caller, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID,
		WorkloadName: "caller", Slug: "gitops-caller", Type: state.AppTypeApp, Status: state.AppActive,
		Manifest: state.AppManifest{ServiceBindingPolicy: policy}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID,
		WorkloadName: "api", Slug: "gitops-target", Type: state.AppTypeApp, Status: state.AppActive, AppProtocol: api.AppProtocolHTTP2})
	if err != nil {
		t.Fatal(err)
	}
	frozen := &gitOpsServicePolicyStore{MemStore: store, route: state.EnvironmentGitOpsServiceBindingRoute{
		CallerAppID: caller.ID, CallerDeploymentID: uuid.NewString(), TargetAppID: target.ID,
		TargetDeploymentID: uuid.NewString(), ReleaseSetID: uuid.NewString(), AccountID: account.ID,
		CallScope: &api.ServiceCallScope{Methods: []string{"GET"}, PathPrefixes: []string{"/work"}}, ReconciliationActive: true,
	}}
	provider := &gitOpsPolicyEndpointProvider{target: target.ID, deployment: frozen.route.TargetDeploymentID}
	config := gateway.ServiceProxyConfig{
		Provider: provider, Policy: newServicePolicyPinner(serviceSnapshotStoreFixture{reader: store}),
		Resolve: newServiceProxyResolver(store), Authorize: newServiceProxyAuthorizer(store),
		AuthorizeBound:            newServiceProxyBoundAuthorizer(store),
		ResolveEnvironmentBinding: newServiceProxyEnvironmentBindingResolver(statetest.NewMem(frozen)),
		ResolveCallerIdentity: func(context.Context, string) (string, string, error) {
			return caller.ID, frozen.route.CallerDeploymentID, nil
		},
		Forward: func(target gateway.Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if target.DeploymentID != frozen.route.TargetDeploymentID || r.Header.Get("x-faas-protocol") != api.AppProtocolHTTP2 {
					t.Error("frozen deployment or target protocol changed", target, r.Header)
				}
				w.WriteHeader(http.StatusNoContent)
			})
		},
	}
	return frozen, caller, provider, config
}

func TestGitOpsServicePolicyPinsScopedBindings(t *testing.T) {
	for _, policy := range []api.ServiceBindingPolicy{api.ServiceBindingPolicyAccount, api.ServiceBindingPolicyDeclared} {
		for _, secure := range []bool{false, true} {
			t.Run(string(policy)+"/"+map[bool]string{false: "http", true: "https"}[secure], func(t *testing.T) {
				store, _, _, config := gitOpsPolicyFixture(t, policy)
				store.route.RequireHTTPS = secure
				url := "http://api.svc.gregale/work"
				if secure {
					url = "https://api.internal/work"
				}
				proxy := gateway.NewServiceProxy(config)
				var revision string
				for range 2 {
					response := httptest.NewRecorder()
					proxy.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, url, nil))
					if response.Code != http.StatusNoContent || response.Header().Get(api.ReleaseHeader) != store.route.ReleaseSetID {
						t.Fatalf("scoped binding was replaced by ordinary app policy: %d %s", response.Code, response.Body.String())
					}
					current := response.Header().Get(gateway.TrafficPolicyRevisionHeader)
					if current == "" || revision != "" && revision != current {
						t.Fatal("frozen policy proof is absent or depends on per-request selection entropy", revision, current)
					}
					revision = current
				}
				store.route.ReleaseSetID = uuid.NewString()
				response := httptest.NewRecorder()
				proxy.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, url, nil))
				if response.Code != http.StatusNoContent || response.Header().Get(gateway.TrafficPolicyRevisionHeader) == revision {
					t.Fatal("changed reviewed release reused the old policy proof", response.Code, response.Header())
				}
			})
		}
	}
}

func TestGitOpsServicePolicyRetainsAuthorizationAndSelectors(t *testing.T) {
	for _, test := range []struct {
		name, method, url string
		change            func(*gitOpsServicePolicyStore, *gateway.ServiceProxyConfig, *http.Request)
		status            int
	}{
		{name: "missing bound authorizer", status: 503, change: func(_ *gitOpsServicePolicyStore, config *gateway.ServiceProxyConfig, _ *http.Request) {
			config.AuthorizeBound = nil
		}},
		{name: "undeclared alias", status: 403, change: func(store *gitOpsServicePolicyStore, _ *gateway.ServiceProxyConfig, _ *http.Request) {
			store.route.TargetAppID = ""
		}},
		{name: "wrong scope", method: "POST", status: 403},
		{name: "HTTPS required", url: "http://api.svc.gregale/work", status: 403},
		{name: "wrong release", status: 409, change: func(_ *gitOpsServicePolicyStore, _ *gateway.ServiceProxyConfig, r *http.Request) {
			r.Header.Set(api.ReleaseHeader, uuid.NewString())
		}},
		{name: "deployment override", status: 409, change: func(_ *gitOpsServicePolicyStore, _ *gateway.ServiceProxyConfig, r *http.Request) {
			r.Header.Set(api.TargetDeploymentHeader, uuid.NewString())
		}},
		{name: "wrong account", status: 403, change: func(store *gitOpsServicePolicyStore, _ *gateway.ServiceProxyConfig, _ *http.Request) {
			store.route.AccountID = uuid.NewString()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _, provider, config := gitOpsPolicyFixture(t, api.ServiceBindingPolicyDeclared)
			store.route.RequireHTTPS = true
			method, url := test.method, test.url
			if method == "" {
				method = http.MethodGet
			}
			if url == "" {
				url = "https://api.internal/work"
			}
			request := httptest.NewRequestWithContext(t.Context(), method, url, nil)
			if test.change != nil {
				test.change(store, &config, request)
			}
			response := httptest.NewRecorder()
			gateway.NewServiceProxy(config).ServeHTTP(response, request)
			if response.Code != test.status || provider.calls != 0 {
				t.Fatalf("invalid request reached routing: status=%d calls=%d body=%s", response.Code, provider.calls, response.Body.String())
			}
		})
	}
}
