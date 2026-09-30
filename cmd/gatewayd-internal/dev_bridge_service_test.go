package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type bridgeScenarioBackend struct {
	fixedBackend
	router      pgRouter
	deployments map[string]state.Deployment
}

func (b *bridgeScenarioBackend) Lookup(ctx context.Context, host string) (gateway.App, bool) {
	app, ok, _ := b.router.ResolveHost(ctx, host)
	return app, ok
}
func (b *bridgeScenarioBackend) Pick(app string) gateway.PickResult {
	return gateway.PickResult{OK: true, Target: gateway.Target{AppID: app, InstanceID: "remote-" + app, DeploymentID: b.deployments[app].ID, AddedAt: time.Now()}}
}
func (b *bridgeScenarioBackend) HealthyCount(string) int { return 1 }
func (b *bridgeScenarioBackend) PickForDeployment(app, deployment string) gateway.PickResult {
	return b.Pick(app)
}

type bridgeScenarioEndpoints struct{}

func (bridgeScenarioEndpoints) ServiceEndpoints(context.Context, string) (gateway.ServiceEndpointsSnapshot, error) {
	return gateway.ServiceEndpointsSnapshot{Endpoints: []gateway.ServiceEndpoint{{InstanceID: "remote-payments", NodeID: "compute-1", Port: 8080}}}, nil
}

func TestDevBridgeRemoteFrontendLocalPaymentsRemoteInventory(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "bridge-scenario@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "development"})
	if err != nil {
		t.Fatal(err)
	}
	apps := map[string]state.App{}
	deployments := map[string]state.Deployment{}
	for _, name := range []string{"payments", "frontend", "inventory"} {
		visibility := api.AppVisibilityInternal
		if name == "frontend" {
			visibility = api.AppVisibilityPublic
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: name, Type: state.AppTypeApp, Status: state.AppActive, Visibility: visibility})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: env.Slug, Status: state.DeployLive, ImageDigest: "sha256:" + name})
		if err != nil {
			t.Fatal(err)
		}
		apps[name], deployments[app.ID] = app, dep
	}
	production, err := store.CreateDeployment(ctx, state.Deployment{AppID: apps["frontend"].ID, Scope: "default", Status: state.DeployLive, ImageDigest: "sha256:production"})
	if err != nil {
		t.Fatal(err)
	}
	backend := &bridgeScenarioBackend{router: pgRouter{store: store, appsSuffix: wire.DeployWildcardSuffix, deploySuffix: wire.DeployWildcardSuffix}, deployments: deployments}
	router := gateway.NewHandlerWith(backend, gateway.NewMetrics(), discardLogger())
	relay := devbridge.NewRelay(4)
	dependencyRoute := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clone := r.Clone(r.Context())
		clone.Host = gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, r.Header.Get("X-Gregale-Dev-Environment"), r.Header.Get("X-Gregale-Dev-Dependency"))
		router.ServeHTTP(w, clone)
	})
	relayServer := httptest.NewServer(devbridge.NewServer(relay, store.DevBridgeByID, dependencyRoute))
	defer relayServer.Close()
	relayURL, _ := url.Parse(relayServer.URL)
	router.WithDevBridge(developmentBridgeAuthorization(store), developmentBridgeForwarder(store, relayURL))
	var callerDeployment atomic.Value
	callerDeployment.Store(deployments[apps["frontend"].ID].ID)
	serviceProxy := gateway.NewServiceProxy(gateway.ServiceProxyConfig{
		Provider: bridgeScenarioEndpoints{},
		Resolve: func(context.Context, string, string) (gateway.ServiceTarget, bool, error) {
			return gateway.ServiceTarget{AppID: apps["payments"].ID}, true, nil
		},
		Authorize: func(context.Context, string, string) (gateway.ServiceCaller, error) {
			return gateway.ServiceCaller{AppID: apps["frontend"].ID, AccountID: account.ID}, nil
		},
		ResolveCallerIdentity: func(context.Context, string) (string, string, error) {
			return apps["frontend"].ID, callerDeployment.Load().(string), nil
		},
		DevBridge: developmentBridgeServiceForwarder(store, router),
		Forward: func(gateway.Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "remote-payments") })
		},
	})
	serviceServer := httptest.NewServer(serviceProxy)
	defer serviceServer.Close()
	serviceURL, _ := url.Parse(serviceServer.URL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, serviceURL.Host)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: devbridge.PropagationTransport{Base: transport}}
	frontend := httptest.NewServer(devbridge.PropagationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, _ := http.NewRequestWithContext(r.Context(), "POST", "http://payments.svc.gregale/charge", r.Body)
		response, err := client.Do(request)
		if err != nil {
			http.Error(w, "service unavailable", 503)
			return
		}
		defer response.Body.Close()
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	})))
	defer frontend.Close()
	frontendURL, _ := url.Parse(frontend.URL)
	router.WithForwarding(func(target gateway.Target) http.Handler {
		if target.AppID == apps["frontend"].ID {
			return httputil.NewSingleHostReverseProxy(frontendURL)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if target.AppID != apps["inventory"].ID {
				t.Error("unexpected remote target")
			}
			if r.Header.Get(devbridge.TokenHeader) != "" {
				t.Error("attachment authority reached remote dependency")
			}
			_, _ = io.WriteString(w, "inventory")
		})
	})
	for _, developer := range []string{"alice", "bob"} {
		session, creds, err := devbridge.NewSession(devbridge.Scope{AccountID: account.ID, DeveloperID: developer, ProjectID: project.ID, EnvironmentID: env.ID, TargetAppID: apps["payments"].ID, DependencyAppIDs: []string{apps["frontend"].ID, apps["inventory"].ID}}, time.Now(), time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CreateDevBridge(ctx, session); err != nil {
			t.Fatal(err)
		}
		local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			request, _ := http.NewRequestWithContext(r.Context(), "GET", relayServer.URL+"/v1/dev/bridges/"+session.ID+"/dependencies/"+apps["inventory"].ID+"/stock", nil)
			request.Header.Set(devbridge.AccountHeader, account.ID)
			request.Header.Set(devbridge.TokenHeader, creds.AttachmentToken)
			request.Header.Set(devbridge.ContextHeader, r.Header.Get(devbridge.ContextHeader))
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				http.Error(w, "dependency unavailable", 503)
				return
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode != 200 {
				t.Errorf("dependency status=%d body=%s", response.StatusCode, body)
			}
			_, _ = io.WriteString(w, developer+"-payments-"+string(body))
		}))
		defer local.Close()
		localURL, _ := url.Parse(local.URL)
		socket, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(relayServer.URL, "http")+"/v1/dev/bridges/"+session.ID+"/connect", http.Header{devbridge.AccountHeader: []string{account.ID}, devbridge.TokenHeader: []string{creds.AttachmentToken}})
		if err != nil {
			t.Fatal(err)
		}
		defer socket.Close()
		defer relay.CloseSession(session.ID)
		go func() { _ = devbridge.ServeLocal(ctx, devbridge.NewWebSocketConn(socket), localURL, 4) }()
		deadline := time.Now().Add(3 * time.Second)
		for !relay.Connected(session.ID) {
			if time.Now().After(deadline) {
				t.Fatal("laptop did not attach")
			}
			time.Sleep(time.Millisecond)
		}
		request := httptest.NewRequest("POST", "http://"+gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, env.ID, apps["frontend"].ID)+"/checkout", strings.NewReader("charge"))
		request.Header.Set(devbridge.ContextHeader, (devbridge.RequestContext{AccountID: account.ID, SessionID: session.ID, Token: creds.RequestToken}).Encode())
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, request)
		if rec.Code != 200 || rec.Body.String() != developer+"-payments-inventory" {
			t.Fatalf("scenario status=%d body=%s", rec.Code, rec.Body)
		}
		callerDeployment.Store(production.ID)
		request = httptest.NewRequest("POST", "http://"+gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, env.ID, apps["frontend"].ID)+"/checkout", strings.NewReader("charge"))
		request.Header.Set(devbridge.ContextHeader, (devbridge.RequestContext{AccountID: account.ID, SessionID: session.ID, Token: creds.RequestToken}).Encode())
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, request)
		if rec.Code != 403 {
			t.Fatalf("production caller joined dev session: %d %s", rec.Code, rec.Body)
		}
		callerDeployment.Store(deployments[apps["frontend"].ID].ID)
	}
	ordinary := httptest.NewRequest("POST", "http://"+gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, env.ID, apps["frontend"].ID)+"/checkout", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, ordinary)
	if rec.Code != 200 || rec.Body.String() != "remote-payments" {
		t.Fatalf("ordinary traffic changed: %d %s", rec.Code, rec.Body)
	}
}
