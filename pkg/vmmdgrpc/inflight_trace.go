// adr: 1138, 20261102082606
package vmmdgrpc

import (
	"context"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// traceparentPattern is the W3C trace-context version 00 header with a
// non-zero trace id and parent id.
var traceparentPattern = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}$`)

// inflightForward is what vmmd keeps of an HTTP forward while it is in
// flight: its traceparent (ADR-1138) and when its request budget runs out
// (ADR-20261102082606). Either may be empty.
type inflightForward struct {
	traceparent string
	deadline    time.Time
}

// inflightTraces keeps the traceparent and deadline of each HTTP forward
// while it is in flight, next to the activity counter and nothing else of
// the request. The zero value is ready to use.
type inflightTraces struct {
	mu         sync.Mutex
	next       uint64
	byInstance map[string]map[uint64]inflightForward
}

// begin records one HTTP forward and returns the func that ends it. The
// forward may carry neither a traceparent nor a deadline: it still counts, so
// an instance serving one traced and one untraced request is not mistaken
// for serving one.
func (t *inflightTraces) begin(instance string, forward inflightForward) func() {
	if instance == "" {
		return func() {}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byInstance == nil {
		t.byInstance = map[string]map[uint64]inflightForward{}
	}
	forwards := t.byInstance[instance]
	if forwards == nil {
		forwards = map[uint64]inflightForward{}
		t.byInstance[instance] = forwards
	}
	t.next++
	id := t.next
	forwards[id] = forward
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		delete(t.byInstance[instance], id)
		if len(t.byInstance[instance]) == 0 {
			delete(t.byInstance, instance)
		}
	}
}

// only returns the instance's one HTTP forward in flight.
func (t *inflightTraces) only(instance string) (inflightForward, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	forwards := t.byInstance[instance]
	if len(forwards) != 1 {
		return inflightForward{}, false
	}
	for _, forward := range forwards {
		return forward, true
	}
	return inflightForward{}, false
}

// forwardOf is what vmmd keeps of an HTTP forward that starts at now.
func forwardOf(req *vmmdpb.ForwardHTTPRequestInit, now time.Time) inflightForward {
	return inflightForward{traceparent: forwardTraceparent(req), deadline: forwardDeadline(req, now)}
}

// forwardDeadline is when the request's single X-Faas-Deadline-Ms budget,
// counted from now, runs out; zero without one. Only the gateway sets the
// header: it drops a customer-supplied x-faas-* header before forwarding.
func forwardDeadline(req *vmmdpb.ForwardHTTPRequestInit, now time.Time) time.Time {
	value, count := "", 0
	for _, h := range req.GetHeaders() {
		if strings.EqualFold(h.GetName(), api.DeadlineHeader) {
			value = strings.TrimSpace(h.GetValue())
			count++
		}
	}
	ms, err := strconv.ParseInt(value, 10, 64)
	if count != 1 || err != nil || ms <= 0 || ms > api.RequestBudgetPlanMax.Milliseconds() {
		return time.Time{}
	}
	return now.Add(time.Duration(ms) * time.Millisecond)
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

// InflightRequestTrace answers with the traceparent and deadline of the one
// request the serving instance behind a service call's source address is
// handling (ADR-1138, ADR-20261102082606): only when exactly one forward of
// any kind (HTTP, upgrade, TCP, UDP, dispatch) is in flight and it is an HTTP
// request. The traceparent is set when that request carried a valid one, the
// deadline when it carried a request budget. The caller comes from the same
// host-owned live identity as ResolveServiceCaller, never a cached address
// mapping. Anything else answers empty.
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
	forward, ok := s.inflightTraces.only(instance)
	if !ok {
		return &vmmdpb.InflightRequestTraceResponse{}, nil
	}
	resp := &vmmdpb.InflightRequestTraceResponse{Traceparent: forward.traceparent}
	if !forward.deadline.IsZero() {
		resp.DeadlineUnixMs = forward.deadline.UnixMilli()
	}
	return resp, nil
}
