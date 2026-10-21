// adr: 1138
package vmmdgrpc

import (
	"context"
	"net/netip"
	"regexp"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// traceparentPattern is the W3C trace-context version 00 header with a
// non-zero trace id and parent id.
var traceparentPattern = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}$`)

// inflightTraces keeps the traceparent of each HTTP forward while it is in
// flight, next to the activity counter and nothing else of the request
// (ADR-1138). The zero value is ready to use.
type inflightTraces struct {
	mu         sync.Mutex
	next       uint64
	byInstance map[string]map[uint64]string
}

// begin records one HTTP forward and returns the func that ends it.
// traceparent may be empty: the forward still counts, so an instance serving
// one traced and one untraced request is not mistaken for serving one.
func (t *inflightTraces) begin(instance, traceparent string) func() {
	if instance == "" {
		return func() {}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byInstance == nil {
		t.byInstance = map[string]map[uint64]string{}
	}
	forwards := t.byInstance[instance]
	if forwards == nil {
		forwards = map[uint64]string{}
		t.byInstance[instance] = forwards
	}
	t.next++
	id := t.next
	forwards[id] = traceparent
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		delete(t.byInstance[instance], id)
		if len(t.byInstance[instance]) == 0 {
			delete(t.byInstance, instance)
		}
	}
}

// only returns the traceparent of the instance's one HTTP forward in flight.
func (t *inflightTraces) only(instance string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	forwards := t.byInstance[instance]
	if len(forwards) != 1 {
		return "", false
	}
	for _, traceparent := range forwards {
		return traceparent, traceparent != ""
	}
	return "", false
}

// forwardTraceparent is the request's single valid W3C traceparent, or "".
func forwardTraceparent(req *vmmdpb.ForwardHTTPRequestInit) string {
	value, count := "", 0
	for _, h := range req.GetHeaders() {
		if strings.EqualFold(h.GetName(), "traceparent") {
			value = strings.TrimSpace(h.GetValue())
			count++
		}
	}
	match := traceparentPattern.FindStringSubmatch(value)
	if count != 1 || match == nil || match[1] == strings.Repeat("0", 32) || match[2] == strings.Repeat("0", 16) {
		return ""
	}
	return value
}

// InflightRequestTrace answers with the traceparent of the one request the
// serving instance behind a service call's source address is handling
// (ADR-1138): only when exactly one forward of any kind (HTTP, upgrade, TCP,
// UDP, dispatch) is in flight and it is an HTTP request that carried a valid
// traceparent. The caller comes from the same host-owned live identity as
// ResolveServiceCaller, never a cached address mapping. Anything else
// answers empty.
func (s *Server) InflightRequestTrace(_ context.Context, req *vmmdpb.InflightRequestTraceRequest) (*vmmdpb.InflightRequestTraceResponse, error) {
	address, err := netip.ParseAddr(req.GetSourceIp())
	if err != nil || !address.Is4() {
		return nil, status.Error(codes.InvalidArgument, "invalid service caller address")
	}
	provider, ok := s.vmm.(interface {
		ResolveServiceCaller(netip.Addr) (fcvm.ServiceCallerIdentity, bool)
	})
	if !ok || s.activity == nil {
		return &vmmdpb.InflightRequestTraceResponse{}, nil
	}
	identity, matched := provider.ResolveServiceCaller(address)
	// App and Job tasks serve no requests.
	if !matched || identity.InstanceID == "" || identity.AppTask || identity.JobTask {
		return &vmmdpb.InflightRequestTraceResponse{}, nil
	}
	instance := identity.InstanceID
	if inflight, seen := s.activity.Inflight(instance); !seen || inflight != 1 {
		return &vmmdpb.InflightRequestTraceResponse{}, nil
	}
	traceparent, ok := s.inflightTraces.only(instance)
	if !ok {
		return &vmmdpb.InflightRequestTraceResponse{}, nil
	}
	return &vmmdpb.InflightRequestTraceResponse{Traceparent: traceparent}, nil
}
