// adr: 568 — graph selectors never substitute for observed VM identity.
package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentQualificationServiceLeaseSurvivesStreamBudgetDetachment(t *testing.T) {
	s, graph := privateServiceFixture()
	s.route.Deadline = time.Now().Add(100 * time.Millisecond)
	parent, cancelBudget, _ := reqbudget.WithRemaining(t.Context(), time.Minute, time.Minute, "forward", "private")
	defer cancelBudget()
	proxy := NewEnvironmentQualificationServiceProxy(EnvironmentQualificationServiceProxyConfig{Store: s,
		ResolveNode: func(context.Context, string) (string, error) { return uuid.NewString(), nil },
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				stream, detach, _, cancel := newStreamSession(r.Context(), time.Minute, time.Minute)
				defer cancel()
				detach()
				select {
				case <-stream.Done():
					w.WriteHeader(http.StatusNoContent)
				case <-time.After(time.Second):
					t.Error("stream detached its private execution lease")
				}
			})
		}})
	r := httptest.NewRequest("GET", EnvironmentQualificationServicePrefix+graph+"/backend/health", nil).WithContext(parent)
	r.RemoteAddr = "10.100.0.3:12345"
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatal(w.Code)
	}
}

func TestEnvironmentQualificationServiceUsesRealForwardingBridgeFrame(t *testing.T) {
	s, graph := privateServiceFixture()
	client := &stubVmmdClient{resp: &vmmdpb.ForwardHTTPResponseInit{Status: http.StatusNoContent}}
	proxy := NewEnvironmentQualificationServiceProxy(EnvironmentQualificationServiceProxyConfig{Store: s,
		ResolveNode: func(context.Context, string) (string, error) { return uuid.NewString(), nil },
		Forward:     ForwardingReverseProxy(&stubLookup{cli: client}, nil)})
	r := httptest.NewRequest("GET", EnvironmentQualificationServicePrefix+graph+"/backend/health?q=1", nil)
	r.RemoteAddr = "10.100.0.3:12345"
	r.Header.Set("x-faas-instance", "another-environment")
	r.Header.Set("x-faas-protocol", "grpc")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, r)
	client.mu.Lock()
	defer client.mu.Unlock()
	if w.Code != http.StatusNoContent || len(client.calls) != 1 {
		t.Fatal("private bridge request", w.Code, len(client.calls))
	}
	frame := client.calls[0]
	if frame.Instance != s.route.Target.InstanceID || frame.Port != 8087 || frame.RequestUri != "/health?q=1" || frame.AppProtocol != "http1" {
		t.Fatal("forwarding bridge lost original private target", frame)
	}
}

type qualificationServiceStub struct {
	mu    sync.Mutex
	held  bool
	route state.EnvironmentQualificationServiceRoute
	err   error
	last  state.EnvironmentQualificationServiceRequest
}

func (s *qualificationServiceStub) EnvironmentQualificationNetworkCaller(context.Context, string, string) (bool, error) {
	return s.held, nil
}
func (s *qualificationServiceStub) ResolveEnvironmentQualificationService(_ context.Context, request state.EnvironmentQualificationServiceRequest) (state.EnvironmentQualificationServiceRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = request
	return s.route, s.err
}

func privateServiceFixture() (*qualificationServiceStub, string) {
	graph := uuid.NewString()
	s := &qualificationServiceStub{held: true, route: state.EnvironmentQualificationServiceRoute{
		Caller: state.EnvironmentQualificationExecution{GraphID: graph, InstanceID: uuid.NewString()},
		Target: state.EnvironmentQualificationExecution{GraphID: graph, Resource: "workload/backend", AppID: uuid.NewString(), NodeID: uuid.NewString(), InstanceID: uuid.NewString(), DeploymentID: uuid.NewString(), WakeID: uuid.NewString()},
		Port:   8087, Deadline: time.Now().Add(time.Minute),
	}}
	return s, graph
}

func TestEnvironmentQualificationServiceForwardsOnlyOriginalTargetAndSanitizesRequest(t *testing.T) {
	s, graph := privateServiceFixture()
	node := uuid.NewString()
	h := NewEnvironmentQualificationServiceProxy(EnvironmentQualificationServiceProxyConfig{Store: s,
		ResolveNode: func(context.Context, string) (string, error) { return node, nil },
		Next:        http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("private request fell back") }),
		Forward: func(target Target) http.Handler {
			want := s.route.Target
			if target.AppID != want.AppID || target.InstanceID != want.InstanceID || target.NodeID != want.NodeID || target.DeploymentID != want.DeploymentID || target.WakeID != want.WakeID || target.Port != 8087 {
				t.Fatal("wrong exact target", target)
			}
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/health" || r.RequestURI != "/health?q=one%20two" || r.Host != "backend.qualification.gregale.invalid" {
					t.Fatal("wrong private request", r.URL, r.RequestURI, r.Host)
				}
				for _, name := range []string{"X-Gregale-Graph", "Forwarded", "X-Forwarded-Host"} {
					if r.Header.Get(name) != "" {
						t.Fatal("guest supplied authority survived", name)
					}
				}
				if _, exists := r.Header["x-faas-instance"]; exists {
					t.Fatal("noncanonical guest identity survived sanitization")
				}
				if r.Header.Get("x-faas-instance") != want.InstanceID || r.Header.Get("x-faas-app") != want.AppID || r.Header.Get("x-faas-protocol") != "http1" {
					t.Fatal("forwarding bridge did not receive the original target identity")
				}
				if r.Header.Get("Authorization") != "Bearer app-token" {
					t.Fatal("application credential stripped")
				}
				w.WriteHeader(http.StatusNoContent)
			})
		}})
	r := httptest.NewRequest(http.MethodGet, EnvironmentQualificationServicePrefix+graph+"/backend/health?q=one%20two", nil)
	r.RemoteAddr = "10.100.0.3:45000"
	for _, name := range []string{"X-Faas-App", "X-Faas-Instance", "X-Faas-Protocol", "X-Gregale-Graph", "Forwarded", "X-Forwarded-Host"} {
		r.Header.Set(name, "forged")
	}
	r.Header.Set("Authorization", "Bearer app-token")
	r.Header["x-faas-instance"] = []string{"noncanonical-forged"}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || s.last.NodeID != node || s.last.HostIP != "10.100.0.3" || s.last.GraphID != graph || s.last.Binding != "backend" {
		t.Fatal(w.Code, s.last)
	}
}

func TestEnvironmentQualificationServiceDenialsNeverWakeOrFallBack(t *testing.T) {
	for _, change := range []string{"ordinary_path", "non_qualification", "wrong_graph", "expired", "call_scope", "https", "upgrade", "dot_path", "encoded_dot_path", "duplicate_slash", "backend_error"} {
		t.Run(change, func(t *testing.T) {
			s, graph := privateServiceFixture()
			path := EnvironmentQualificationServicePrefix + graph + "/backend/health"
			want := http.StatusForbidden
			switch change {
			case "ordinary_path":
				path = "/v1/internal/services/production/health"
			case "non_qualification":
				s.held = false
			case "wrong_graph":
				s.err = state.ErrConflict
			case "expired":
				s.route.Deadline = time.Now().Add(-time.Second)
			case "call_scope":
				s.route.CallScope = &api.ServiceCallScope{Methods: []string{"POST"}, PathPrefixes: []string{"/health"}}
			case "https":
				s.route.RequireHTTPS = true
			case "dot_path":
				path += "/../admin"
			case "encoded_dot_path":
				path += "/%252e%252e/admin"
			case "duplicate_slash":
				path += "//admin"
			case "backend_error":
				s.err = errors.New("database offline")
				want = http.StatusServiceUnavailable
			}
			h := NewEnvironmentQualificationServiceProxy(EnvironmentQualificationServiceProxyConfig{Store: s, ResolveNode: func(context.Context, string) (string, error) { return uuid.NewString(), nil },
				Next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("denied caller fell back") }), Forward: func(Target) http.Handler { t.Fatal("denied caller was forwarded"); return nil }})
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.RemoteAddr = "10.100.0.3:45000"
			if change == "upgrade" {
				r.Header.Set("Connection", "upgrade")
				r.Header.Set("Upgrade", "websocket")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestEnvironmentQualificationServiceAllowsHTTPSAndCancelsRevokedStream(t *testing.T) {
	s, graph := privateServiceFixture()
	s.route.RequireHTTPS = true
	h := NewEnvironmentQualificationServiceProxy(EnvironmentQualificationServiceProxyConfig{Store: s, ResolveNode: func(context.Context, string) (string, error) { return uuid.NewString(), nil },
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s.mu.Lock()
				s.err = state.ErrConflict
				s.mu.Unlock()
				select {
				case <-r.Context().Done():
					w.WriteHeader(http.StatusNoContent)
				case <-time.After(4 * api.EnvironmentGitOpsQualificationRuntimeCheckInterval):
					t.Error("revoked stream retained authority")
				}
			})
		}})
	r := httptest.NewRequest(http.MethodGet, EnvironmentQualificationServicePrefix+graph+"/backend/health", nil)
	r.RemoteAddr = "10.100.0.3:45000"
	r.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatal(w.Code)
	}
}
