// adr: 168
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// localityProxy records "<nodeID>/<instanceID>" per forwarded call so tests
// can assert both which node was chosen and which replica within it.
func localityProxy(t *testing.T, localNodeID string, endpoints []ServiceEndpoint, seen *[]string) *ServiceProxy {
	t.Helper()
	return NewServiceProxy(ServiceProxyConfig{
		Provider: staticProvider{endpoints: endpoints},
		Resolve: func(context.Context, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize:   func(context.Context, string, string) error { return nil },
		LocalNodeID: localNodeID,
		Forward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				*seen = append(*seen, target.NodeID+"/"+target.InstanceID)
				w.WriteHeader(http.StatusOK)
			})
		},
		EndpointTTL: time.Minute,
		Now:         func() time.Time { return time.Unix(100, 0) },
	})
}

func localityCall(t *testing.T, proxy *ServiceProxy) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	return rec.Code
}

// The caller is a workload on this node, so a local replica keeps the whole
// exchange inside one host: no inter-node hop, and no dependency on a peer
// node staying reachable for a call between two services that both live here.
func TestServiceProxyPrefersLocalReplica(t *testing.T) {
	var seen []string
	proxy := localityProxy(t, "node-local", []ServiceEndpoint{
		{InstanceID: "remote-a", NodeID: "node-remote", Port: 8080},
		{InstanceID: "local-a", NodeID: "node-local", Port: 8080},
		{InstanceID: "remote-b", NodeID: "node-remote", Port: 8080},
	}, &seen)

	for i := 0; i < 4; i++ {
		if code := localityCall(t, proxy); code != http.StatusOK {
			t.Fatalf("call %d status = %d, want 200", i, code)
		}
	}
	for i, hit := range seen {
		if !strings.HasPrefix(hit, "node-local/") {
			t.Errorf("call %d went to %q, want a node-local replica", i, hit)
		}
	}
}

// Locality is a preference, not a constraint: with no local replica the call
// must still cross the network rather than fail.
func TestServiceProxyFallsBackToRemote(t *testing.T) {
	var seen []string
	proxy := localityProxy(t, "node-local", []ServiceEndpoint{
		{InstanceID: "remote-a", NodeID: "node-remote", Port: 8080},
	}, &seen)

	if code := localityCall(t, proxy); code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with only a remote replica", code)
	}
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "node-remote/") {
		t.Errorf("forwarded to %v, want a node-remote replica", seen)
	}
}

// An unset node id is the legacy single-box shape; selection must stay flat
// round-robin rather than treating "" as a node that nothing matches.
func TestServiceProxyWithoutLocalNodeIDRoundRobins(t *testing.T) {
	var seen []string
	proxy := localityProxy(t, "", []ServiceEndpoint{
		{InstanceID: "a", NodeID: "node-a", Port: 8080},
		{InstanceID: "b", NodeID: "node-b", Port: 8080},
	}, &seen)

	for i := 0; i < 2; i++ {
		if code := localityCall(t, proxy); code != http.StatusOK {
			t.Fatalf("call %d status = %d, want 200", i, code)
		}
	}
	if len(seen) != 2 || seen[0] == seen[1] {
		t.Errorf("forwarded to %v, want both nodes visited", seen)
	}
}

// Round-robin must still spread load across several local replicas; a
// local-first rule that always returned the first match would pin one
// instance and defeat multi-instance routing.
func TestServiceProxyRoundRobinsAmongLocalReplicas(t *testing.T) {
	var seen []string
	proxy := localityProxy(t, "node-local", []ServiceEndpoint{
		{InstanceID: "local-a", NodeID: "node-local", Port: 8080},
		{InstanceID: "local-b", NodeID: "node-local", Port: 8080},
		{InstanceID: "remote", NodeID: "node-remote", Port: 8080},
	}, &seen)

	for i := 0; i < 4; i++ {
		localityCall(t, proxy)
	}
	distinct := map[string]struct{}{}
	for _, hit := range seen {
		if !strings.HasPrefix(hit, "node-local/") {
			t.Fatalf("forwarded to %q, want only node-local replicas", hit)
		}
		distinct[hit] = struct{}{}
	}
	if len(seen) != 4 {
		t.Fatalf("forwarded %d times, want 4", len(seen))
	}
	if len(distinct) != 2 {
		t.Errorf("hit %d distinct local replicas (%v), want 2 — local-first must still round-robin", len(distinct), distinct)
	}
}
