// adr: 1151 — a service call that reaches a frozen replica (paused in place
// by schedd) withdraws it without benching it in the circuit breaker, wakes
// the target so schedd thaws it, and retries on the thawed replica, which
// keeps its instance ID.
package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/circuit"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc"
)

// instanceFrozenStatus is vmmd's refusal of a forward to a frozen guest.
func instanceFrozenStatus() error {
	return grpcerr.ToStatus(api.NewProblem(503, api.CodeInstanceFrozen,
		"Instance frozen", "the instance is frozen; schedd thaws it on the next wake"))
}

// frozenServiceProxy serves app-orders from one replica that vmmd refuses
// while frozen is set; a wake thaws it.
func frozenServiceProxy(frozen *atomic.Bool, wakes, forwards *atomic.Int32, stale func(*http.Request)) *ServiceProxy {
	now := time.Unix(100, 0)
	return NewServiceProxy(ServiceProxyConfig{
		Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-orders",
			Endpoints: []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}}}},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
		Wake: func(context.Context, string) error {
			wakes.Add(1)
			frozen.Store(false)
			return nil
		},
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwards.Add(1)
				if frozen.Load() {
					stale(r)
					http.Error(w, "refused", http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			})
		},
		// One attempt: the thaw retry must not depend on the retry policy.
		RetryPolicy: RetryPolicy{MaxAttempts: 1},
		EndpointTTL: time.Minute,
		Now:         func() time.Time { return now },
	})
}

func TestServiceProxyThawsFrozenReplicaInsteadOfBenchingIt(t *testing.T) {
	var frozen atomic.Bool
	var wakes, forwards atomic.Int32
	proxy := frozenServiceProxy(&frozen, &wakes, &forwards, func(r *http.Request) {
		markStaleTargetErr(r.Context(), instanceFrozenStatus())
	})
	for i := 0; i < 5; i++ {
		frozen.Store(true)
		if rec := serviceProxyGET(t, proxy); rec.Code != http.StatusOK || rec.Body.String() != "ok" {
			t.Fatalf("call %d to a frozen replica = %d %q, want 200 ok from the thawed replica", i, rec.Code, rec.Body.String())
		}
	}
	if wakes.Load() != 5 || forwards.Load() != 10 {
		t.Fatalf("wakes=%d forwards=%d, want one thaw and one retry per call", wakes.Load(), forwards.Load())
	}
	if state := proxy.breaker.State(serviceProxyEndpointKey("app-orders", "instance-a")); state != circuit.StateClosed {
		t.Fatalf("frozen replica breaker = %s, want closed: a frozen replica is not a failure", state)
	}
}

// A replica that is dead rather than frozen is still quarantined, and its
// failure is not turned into a wake.
func TestServiceProxyDoesNotWakeForDeadReplica(t *testing.T) {
	var frozen atomic.Bool
	var wakes, forwards atomic.Int32
	proxy := frozenServiceProxy(&frozen, &wakes, &forwards, func(r *http.Request) {
		markStaleTarget(r.Context())
	})
	frozen.Store(true)
	if rec := serviceProxyGET(t, proxy); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("call to a dead replica = %d, want 503", rec.Code)
	}
	if wakes.Load() != 0 || forwards.Load() != 1 {
		t.Fatalf("wakes=%d forwards=%d, want no wake and no retry", wakes.Load(), forwards.Load())
	}
}

// A body that cannot be replayed is not retried after a frozen refusal. The
// proxy buffers declared-length bodies up to ServiceProxyReplayBodyMaxBytes
// (ADR-1133), so the body here streams with no declared length.
func TestServiceProxyFrozenRetryNeedsReplayableBody(t *testing.T) {
	var frozen atomic.Bool
	var wakes, forwards atomic.Int32
	proxy := frozenServiceProxy(&frozen, &wakes, &forwards, func(r *http.Request) {
		markStaleTargetErr(r.Context(), instanceFrozenStatus())
	})
	frozen.Store(true)
	req := httptest.NewRequest(http.MethodPost, "http://gateway/v1/internal/services/orders/charge", strings.NewReader(`{"amount":1}`))
	req.ContentLength = -1
	req.GetBody = nil
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || wakes.Load() != 0 || forwards.Load() != 1 {
		t.Fatalf("status=%d wakes=%d forwards=%d, want 503 with no thaw retry", rec.Code, wakes.Load(), forwards.Load())
	}
}

func TestServiceTCPProxyThawsFrozenReplica(t *testing.T) {
	h := newTCPTestHarness(t)
	h.provider.set("db", ServiceEndpoint{InstanceID: "i1", NodeID: "n1", Port: 8080})
	h.frozen["i1"] = true
	h.onWake = func() { delete(h.frozen, "i1") }
	outcome, reason := h.serve(t, "caller", "198.19.0.7:5432")
	if reason != "" || outcome != "success" {
		t.Fatalf("outcome=%q reason=%q, want success on the thawed replica", outcome, reason)
	}
	if h.wakes != 1 || len(h.hops) != 2 || h.hops[0].InstanceID != "i1" || h.hops[1].InstanceID != "i1" {
		t.Fatalf("wakes=%d hops=%+v, want one wake and a retry on i1", h.wakes, h.hops)
	}
	if state := h.proxy.cfg.Services.breaker.State(serviceProxyEndpointKey("db", "i1")); state != circuit.StateClosed {
		t.Fatalf("frozen replica breaker = %s, want closed", state)
	}
}

// frozenTCPVmmd answers every TCP forward with vmmd's frozen refusal.
type frozenTCPVmmd struct{ vmmdpb.VmmdClient }

type frozenTCPStream struct{ *tcpStubStream }

func (frozenTCPStream) Recv() (*vmmdpb.ForwardTCPResponse, error) { return nil, instanceFrozenStatus() }

func (frozenTCPVmmd) ForwardTCPStream(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardTCPRequest, vmmdpb.ForwardTCPResponse], error) {
	return frozenTCPStream{tcpStubStream: newTCPStubStream(ctx, "")}, nil
}

// The forwarder keeps vmmd's refusal as the cause, so the proxy can tell a
// frozen replica from a dead one.
func TestServeConnAwaitingDialKeepsFrozenRefusal(t *testing.T) {
	forwarder := TCPForwarder{Nodes: tcpStubNodes{cli: frozenTCPVmmd{}}, IdleTimeout: time.Minute}
	client, server := net.Pipe()
	defer func() { _ = client.Close(); _ = server.Close() }()
	err := forwarder.ServeConnAwaitingDial(context.Background(), server, Target{AppID: "app", NodeID: "node", InstanceID: "inst", Port: 5432})
	if !errors.Is(err, ErrTCPGuestUnreachable) || !instanceFrozenRefusal(err) {
		t.Fatalf("ServeConnAwaitingDial on a frozen guest = %v, want ErrTCPGuestUnreachable carrying instance_frozen", err)
	}
}
