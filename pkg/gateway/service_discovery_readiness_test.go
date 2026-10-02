// adr: 375
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/circuit"
)

func TestPGBackendServiceEndpointsReadinessWithdrawalAndRecovery(t *testing.T) {
	for _, multi := range []bool{false, true} {
		name := "single-source"
		if multi {
			name = "multiple-sources"
		}
		t.Run(name, func(t *testing.T) {
			loads := 0
			b := NewPGBackend(nil, nil, nil).WithLiveTargetLoader(func(context.Context, string) ([]Target, error) {
				loads++
				return nil, nil
			})
			target := Target{NodeID: "node", InstanceID: "instance", DeploymentID: "dep", RequiresReadiness: true}
			source := ""
			if multi {
				target.ReadinessGates = &ReadinessGates{RequiredSources: []string{"primary_app", "sidecar:proxy"}}
				source = "primary_app"
			}
			b.RecordTarget("app", target)
			check := func(want int) {
				t.Helper()
				snapshot, err := b.ServiceEndpoints(t.Context(), "app")
				if err != nil || len(snapshot.Endpoints) != want || b.CapacityCount("app") != 1 || loads != 0 {
					t.Fatalf("readiness discovery: %+v err=%v capacity=%d loads=%d", snapshot, err, b.CapacityCount("app"), loads)
				}
			}
			check(0)
			at := time.Now().UTC()
			b.SetInstanceReadinessSource("app", "instance", source, "ready", at, 1)
			if multi {
				check(0)
				b.SetInstanceReadinessSource("app", "instance", "sidecar:proxy", "ready", at, 2)
			}
			check(1)
			b.SetInstanceReadinessSource("app", "instance", source, "unready", at.Add(time.Second), 3)
			check(0)
			b.SetInstanceReadinessSource("app", "instance", source, "ready", at, 1)
			check(0)
			b.SetInstanceReadinessSource("app", "instance", source, "ready", at.Add(2*time.Second), 4)
			check(1)
		})
	}
}

func readinessServiceProxy(b *PGBackend, protocol string, forward func(Target) http.Handler) *ServiceProxy {
	return NewServiceProxy(ServiceProxyConfig{
		Provider: b,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders", AppProtocol: protocol, WebSocketEnabled: protocol == "upgrade"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "app-client"}, nil
		},
		Forward: forward, RawForward: forward,
		EndpointTTL: time.Minute, Now: func() time.Time { return time.Unix(100, 0) },
	})
}

func readinessServiceRequest(proxy *ServiceProxy, upgrade bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/work", nil)
	r.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	if upgrade {
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
	}
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, r)
	return w
}

func TestServiceProxyReadinessWithdrawalBypassesCachedLease(t *testing.T) {
	for _, protocol := range []string{"http1", "grpc", "upgrade"} {
		t.Run(protocol, func(t *testing.T) {
			b := NewPGBackend(nil, nil, nil)
			b.RecordTarget("app-orders", Target{InstanceID: "instance", NodeID: "node", DeploymentID: "dep", RequiresReadiness: true, Ready: true})
			b.SetInstanceReadiness("app-orders", "instance", "ready", time.Now().UTC().Add(-time.Second), 0)
			calls := 0
			proxy := readinessServiceProxy(b, protocol, func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.WriteHeader(http.StatusOK)
				})
			})
			if response := readinessServiceRequest(proxy, protocol == "upgrade"); response.Code != http.StatusOK || calls != 1 {
				t.Fatalf("initial ready request: %d calls=%d body=%s", response.Code, calls, response.Body)
			}
			at := time.Now().UTC()
			b.SetInstanceReadiness("app-orders", "instance", "unready", at, 1)
			if response := readinessServiceRequest(proxy, protocol == "upgrade"); response.Code != http.StatusServiceUnavailable || calls != 1 {
				t.Fatalf("cached lease bypassed withdrawal: %d calls=%d", response.Code, calls)
			}
			b.SetInstanceReadiness("app-orders", "instance", "ready", at.Add(time.Second), 2)
			if response := readinessServiceRequest(proxy, protocol == "upgrade"); response.Code != http.StatusOK || calls != 2 {
				t.Fatalf("readiness recovery within same lease: %d calls=%d", response.Code, calls)
			}
			if b.CapacityCount("app-orders") != 1 {
				t.Fatal("readiness withdrawal freed resident capacity")
			}
		})
	}
}

func TestServiceProxyReadinessWithdrawalBeforeRetry(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	for _, id := range []string{"instance-a", "instance-b"} {
		b.RecordTarget("app-orders", Target{InstanceID: id, NodeID: "node", DeploymentID: "dep", RequiresReadiness: true, Ready: true})
		b.SetInstanceReadiness("app-orders", id, "ready", time.Now().UTC().Add(-time.Second), 0)
	}
	calls := 0
	var decision *trafficDecision
	proxy := readinessServiceProxy(b, api.AppProtocolHTTP1, func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			decision = trafficDecisionFrom(r.Context())
			if target.InstanceID == "instance-a" {
				b.SetInstanceReadiness("app-orders", "instance-a", "unready", time.Now().UTC(), 1)
				b.SetInstanceReadiness("app-orders", "instance-b", "unready", time.Now().UTC(), 1)
				markStaleTarget(r.Context())
				http.Error(w, "stale", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
	})
	response := readinessServiceRequest(proxy, false)
	if response.Code != http.StatusServiceUnavailable || calls != 1 {
		t.Fatalf("retry forwarded to readiness-withdrawn alternate: %d calls=%d", response.Code, calls)
	}
	if decision == nil {
		t.Fatal("forwarded attempt had no decision record")
	}
	decision.Lock()
	evidence := decision.trafficDecisionSnapshot
	decision.Unlock()
	if evidence.refusal != "capacity" || evidence.circuit != "admitted" || evidence.attempts != 1 {
		t.Fatalf("readiness refusal misreported as a circuit denial: %+v", evidence)
	}
}

func TestServiceProxyCachedLeaseRejectsChangedPlacement(t *testing.T) {
	for _, change := range []string{"node", "port", "deployment", "eviction"} {
		t.Run(change, func(t *testing.T) {
			b := NewPGBackend(nil, nil, nil)
			target := Target{InstanceID: "instance", NodeID: "node", DeploymentID: "dep", Port: 8080}
			b.RecordTarget("app-orders", target)
			var selected []Target
			proxy := readinessServiceProxy(b, api.AppProtocolHTTP1, func(target Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					selected = append(selected, target)
					w.WriteHeader(http.StatusOK)
				})
			})
			if response := readinessServiceRequest(proxy, false); response.Code != http.StatusOK || len(selected) != 1 {
				t.Fatalf("initial placement: %d %+v", response.Code, selected)
			}
			switch change {
			case "node":
				target.NodeID = "replacement-node"
			case "port":
				target.Port = 9090
			case "deployment":
				b.EvictInstance("app-orders", target.InstanceID)
				target.InstanceID = "replacement-instance"
				target.DeploymentID = "replacement-dep"
			case "eviction":
				b.EvictInstance("app-orders", target.InstanceID)
			}
			if change != "eviction" {
				b.RecordTarget("app-orders", target)
			}
			response := readinessServiceRequest(proxy, false)
			if change == "eviction" {
				if response.Code != http.StatusServiceUnavailable || len(selected) != 1 {
					t.Fatalf("evicted endpoint was forwarded: %d %+v", response.Code, selected)
				}
			} else if response.Code != http.StatusOK || len(selected) != 2 || selected[1].NodeID != target.NodeID || selected[1].Port != target.Port || selected[1].DeploymentID != target.DeploymentID {
				t.Fatalf("cached placement survived replacement: %d %+v", response.Code, selected)
			}
		})
	}
}

func TestPGBackendServiceEndpointsHydratesUnreadyResident(t *testing.T) {
	loads := 0
	b := NewPGBackend(nil, nil, nil).WithLiveTargetLoader(func(context.Context, string) ([]Target, error) {
		loads++
		return []Target{{InstanceID: "instance", NodeID: "node", RequiresReadiness: true}}, nil
	})
	for range 2 {
		snapshot, err := b.ServiceEndpoints(t.Context(), "app")
		if err != nil || len(snapshot.Endpoints) != 0 || b.CapacityCount("app") != 1 || loads != 1 {
			t.Fatalf("unready hydration: %+v err=%v capacity=%d loads=%d", snapshot, err, b.CapacityCount("app"), loads)
		}
	}
	b.SetInstanceReadiness("app", "instance", "ready", time.Now(), 1)
	snapshot, err := b.ServiceEndpoints(t.Context(), "app")
	if err != nil || len(snapshot.Endpoints) != 1 || loads != 1 {
		t.Fatalf("hydrated readiness recovery: %+v err=%v loads=%d", snapshot, err, loads)
	}
}

func TestServiceProxyReadinessWithdrawalPreservesHalfOpenProbe(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	b.RecordTarget("app-orders", Target{InstanceID: "instance", NodeID: "node", RequiresReadiness: true})
	at := time.Now().UTC()
	b.SetInstanceReadiness("app-orders", "instance", "ready", at, 1)
	snapshot, err := b.ServiceEndpoints(t.Context(), "app-orders")
	if err != nil || len(snapshot.Endpoints) != 1 {
		t.Fatalf("initial ready snapshot: %+v err=%v", snapshot, err)
	}
	clock := time.Unix(100, 0)
	config := circuit.DefaultConfig()
	config.MinRequests, config.FailureThreshold, config.OpenDuration = 1, 1, time.Second
	group := circuit.NewGroup(config, func() time.Time { return clock })
	key := serviceProxyEndpointKey("app-orders", "instance")
	group.Failure(key)
	clock = clock.Add(2 * time.Second)
	proxy := NewServiceProxy(ServiceProxyConfig{Provider: b, Breaker: group})
	b.SetInstanceReadiness("app-orders", "instance", "unready", at.Add(time.Second), 2)
	if _, picked, eligible := proxy.pickForAttempt("app-orders", snapshot.Endpoints); picked || eligible {
		t.Fatal("withdrawn cached endpoint entered the breaker")
	}
	b.SetInstanceReadiness("app-orders", "instance", "ready", at.Add(2*time.Second), 3)
	if endpoint, picked, eligible := proxy.pickForAttempt("app-orders", snapshot.Endpoints); !picked || !eligible || endpoint.InstanceID != "instance" {
		t.Fatalf("readiness refusal consumed the recovering circuit's only probe: %+v picked=%v eligible=%v", endpoint, picked, eligible)
	}
	proxy.healthy("app-orders", "instance")
	if group.State(key) != circuit.StateClosed {
		t.Fatal("successful readiness recovery did not settle the probe")
	}
}

func TestPGBackendServiceEndpointRoutabilityRetainsExplicitZeroTrafficPin(t *testing.T) {
	b := NewPGBackend(nil, nil, nil).WithStore(serviceDiscoveryWeightStore{rows: []DeploymentWeightsRow{
		{ID: "retained", TrafficPercent: 0}, {ID: "current", TrafficPercent: 100},
	}})
	b.RecordTarget("app", Target{InstanceID: "old", NodeID: "node", DeploymentID: "retained"})
	b.RecordTarget("app", Target{InstanceID: "new", NodeID: "node", DeploymentID: "current"})
	snapshot, err := b.ServiceEndpoints(t.Context(), "app")
	if err != nil {
		t.Fatal(err)
	}
	pinned := serviceEndpointsForDeployment(snapshot.Endpoints, "retained")
	if len(pinned) != 1 || !b.ServiceEndpointRoutable("app", pinned[0]) {
		t.Fatalf("live readiness check rejected an explicit retained pin: %+v", pinned)
	}
	if ordinary := serviceEndpointsForDeployment(snapshot.Endpoints, ""); len(ordinary) != 1 || ordinary[0].InstanceID != "new" {
		t.Fatalf("retained pin leaked into ordinary traffic: %+v", ordinary)
	}
}
