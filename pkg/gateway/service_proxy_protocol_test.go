// adr: 197
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type staticProvider struct{ endpoints []ServiceEndpoint }

func (p staticProvider) ServiceEndpoints(context.Context, string) (ServiceEndpointsSnapshot, error) {
	return ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: p.endpoints}, nil
}

func newProtocolTestProxy(target ServiceTarget, forward, rawForward func(Target) http.Handler) *ServiceProxy {
	return NewServiceProxy(ServiceProxyConfig{
		Provider:   staticProvider{endpoints: []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}}},
		Resolve:    func(context.Context, string) (ServiceTarget, bool, error) { return target, true, nil },
		Authorize:  func(context.Context, string, string) error { return nil },
		Forward:    forward,
		RawForward: rawForward,
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
}

func okForwarder(seen *atomic.Value, count *atomic.Int32) func(Target) http.Handler {
	return func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if count != nil {
				count.Add(1)
			}
			if seen != nil {
				seen.Store(r.Header.Get("x-faas-protocol"))
			}
			w.WriteHeader(http.StatusOK)
		})
	}
}

// vmmd maps x-faas-protocol onto the H1 or H2C guest bridge. If the service
// hop never stamps it the forwarder defaults to http1, which silently
// downgrades every internal gRPC and HTTP/2 call to the wrong bridge.
func TestServiceProxyStampsTargetProtocol(t *testing.T) {
	tests := []struct {
		name        string
		appProtocol string
		want        string
	}{
		{"grpc target", "grpc", "grpc"},
		{"http2 target", "http2", "http2"},
		{"http1 target", "http1", "http1"},
		{"unset defaults to http1", "", "http1"},
		// The column CHECK makes this unreachable in practice; degrade to the
		// legacy bridge rather than failing a customer's call.
		{"unknown value degrades to http1", "quic", "http1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var seen atomic.Value
			proxy := newProtocolTestProxy(
				ServiceTarget{AppID: "app-orders", AppProtocol: tc.appProtocol},
				okForwarder(&seen, nil), nil)

			req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/rpc", nil)
			req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got, _ := seen.Load().(string); got != tc.want {
				t.Errorf("x-faas-protocol = %q, want %q", got, tc.want)
			}
		})
	}
}

func upgradeRequest() *http.Request {
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/ws", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	return req
}

// An Upgrade request must take the verbatim-bytes bridge. The ordinary
// forwarder strips Connection/Upgrade as hop-by-hop headers, so routing it
// there turns the handshake into an upstream error.
func TestServiceProxyRoutesUpgradeToRawBridge(t *testing.T) {
	var rawCalls, plainCalls atomic.Int32
	var seenUpgradeFlag atomic.Value
	raw := func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawCalls.Add(1)
			seenUpgradeFlag.Store(r.Header.Get("x-faas-upgrade"))
			w.WriteHeader(http.StatusSwitchingProtocols)
		})
	}
	proxy := newProtocolTestProxy(
		ServiceTarget{AppID: "app-orders", WebSocketEnabled: true},
		okForwarder(nil, &plainCalls), raw)

	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, upgradeRequest())

	if got := rawCalls.Load(); got != 1 {
		t.Errorf("raw bridge calls = %d, want 1", got)
	}
	if got := plainCalls.Load(); got != 0 {
		t.Errorf("plain forwarder calls = %d, want 0", got)
	}
	if got, _ := seenUpgradeFlag.Load().(string); got != "true" {
		t.Errorf("x-faas-upgrade = %q, want \"true\"", got)
	}
}

func TestServiceProxyUpgradeGates(t *testing.T) {
	tests := []struct {
		name       string
		target     ServiceTarget
		rawWired   bool
		wantStatus int
	}{
		{
			// A customer who turned WebSockets off must not get them back
			// through the service mesh.
			name:       "target has websockets disabled",
			target:     ServiceTarget{AppID: "app-orders", WebSocketEnabled: false},
			rawWired:   true,
			wantStatus: http.StatusNotImplemented,
		},
		{
			// A deterministic 501 names the cause; falling through to the
			// plain forwarder would strip the handshake and produce a 502
			// that clients retry in a loop.
			name:       "raw bridge not wired on this node",
			target:     ServiceTarget{AppID: "app-orders", WebSocketEnabled: true},
			rawWired:   false,
			wantStatus: http.StatusNotImplemented,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var plainCalls atomic.Int32
			var raw func(Target) http.Handler
			if tc.rawWired {
				raw = func(Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusSwitchingProtocols)
					})
				}
			}
			proxy := newProtocolTestProxy(tc.target, okForwarder(nil, &plainCalls), raw)

			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, upgradeRequest())

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := plainCalls.Load(); got != 0 {
				t.Errorf("plain forwarder calls = %d, want 0 (handshake would be stripped)", got)
			}
		})
	}
}

// gRPC reports its status in a trailer. The response buffer snapshots the
// header map when the response commits, so without an explicit trailer pass
// the caller reads a complete stream carrying no grpc-status.
func TestServiceProxyPropagatesTrailers(t *testing.T) {
	forward := func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("Trailer", "grpc-status")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("payload"))
			// Declared trailer, written after the header block.
			w.Header().Set("grpc-status", "0")
			// Undeclared trailer, Go's http.TrailerPrefix convention.
			w.Header().Set(http.TrailerPrefix+"grpc-message", "ok")
		})
	}
	proxy := newProtocolTestProxy(ServiceTarget{AppID: "app-orders", AppProtocol: "grpc"}, forward, nil)

	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/rpc", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("grpc-status"); got != "0" {
		t.Errorf("grpc-status trailer = %q, want %q", got, "0")
	}
	if got := rec.Header().Get(http.TrailerPrefix + "grpc-message"); got != "ok" {
		t.Errorf("undeclared grpc-message trailer = %q, want %q", got, "ok")
	}
}

// An upgrade cannot be retried onto a second endpoint once bytes have flowed,
// so the pick must be able to fail cleanly. With the only replica quarantined
// the raw bridge must never be dialled with a zero Target.
func TestServiceProxyUpgradeWithNoPickableEndpoint(t *testing.T) {
	var rawCalls atomic.Int32
	raw := func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			rawCalls.Add(1)
			w.WriteHeader(http.StatusSwitchingProtocols)
		})
	}
	// The plain forwarder reports the single endpoint stale, which quarantines
	// it for the endpoint lease.
	stale := func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			markStaleTarget(r.Context())
			http.Error(w, "stale", http.StatusServiceUnavailable)
		})
	}
	proxy := newProtocolTestProxy(ServiceTarget{AppID: "app-orders", WebSocketEnabled: true}, stale, raw)

	warm := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	warm.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	proxy.ServeHTTP(httptest.NewRecorder(), warm)

	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, upgradeRequest())

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if got := rawCalls.Load(); got != 0 {
		t.Errorf("raw bridge calls = %d, want 0 when no endpoint is pickable", got)
	}
}
