// adr: 1138
package vmmdgrpc

import (
	"context"
	"net/netip"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/activity"
)

const testTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestForwardTraceparent(t *testing.T) {
	header := func(pairs ...string) *vmmdpb.ForwardHTTPRequestInit {
		req := &vmmdpb.ForwardHTTPRequestInit{}
		for i := 0; i < len(pairs); i += 2 {
			req.Headers = append(req.Headers, &vmmdpb.Header{Name: pairs[i], Value: pairs[i+1]})
		}
		return req
	}
	for _, tc := range []struct {
		name string
		req  *vmmdpb.ForwardHTTPRequestInit
		want string
	}{
		{"valid", header("Traceparent", testTraceparent), testTraceparent},
		{"absent", header("X-Other", "1"), ""},
		{"repeated", header("traceparent", testTraceparent, "traceparent", testTraceparent), ""},
		{"malformed", header("traceparent", "00-xyz-00f067aa0ba902b7-01"), ""},
		{"unknown version", header("traceparent", "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"), ""},
		{"zero trace id", header("traceparent", "00-00000000000000000000000000000000-00f067aa0ba902b7-01"), ""},
		{"zero parent id", header("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01"), ""},
		{"uppercase hex", header("traceparent", "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := forwardTraceparent(tc.req); got != tc.want {
				t.Fatalf("forwardTraceparent = %q, want %q", got, tc.want)
			}
		})
	}
}

// tracingCallerVMM resolves source addresses to live callers like the
// production manager; InflightRequestTrace calls nothing else on it.
type tracingCallerVMM struct {
	VmmdAPI
	callers map[string]fcvm.ServiceCallerIdentity
}

func (v *tracingCallerVMM) ResolveServiceCaller(address netip.Addr) (fcvm.ServiceCallerIdentity, bool) {
	identity, ok := v.callers[address.String()]
	return identity, ok
}

// The trace context is returned only while the caller's instance serves
// exactly one forward of any kind, and that forward is an HTTP request with a
// traceparent.
func TestInflightRequestTrace(t *testing.T) {
	ctx := context.Background()
	s := &Server{activity: activity.New(nil), vmm: &tracingCallerVMM{callers: map[string]fcvm.ServiceCallerIdentity{
		"10.100.0.7": {InstanceID: "i-1", AppID: "caller"},
		"10.100.0.8": {InstanceID: "task", AppID: "caller", AppTask: true},
		"10.100.0.9": {InstanceID: "job", AppID: "caller", JobTask: true},
	}}}
	ask := func(sourceIP string) string {
		t.Helper()
		resp, err := s.InflightRequestTrace(ctx, &vmmdpb.InflightRequestTraceRequest{SourceIp: sourceIP})
		if err != nil {
			t.Fatal(err)
		}
		return resp.GetTraceparent()
	}
	// One traced HTTP forward.
	s.activity.Begin("i-1")
	endFirst := s.inflightTraces.begin("i-1", testTraceparent)
	if got := ask("10.100.0.7"); got != testTraceparent {
		t.Fatalf("one traced request: %q", got)
	}
	if got := ask("10.100.0.6"); got != "" {
		t.Fatalf("an unknown caller address linked: %q", got)
	}
	// A second forward of another kind (a TCP session) makes it ambiguous.
	s.activity.Begin("i-1")
	if got := ask("10.100.0.7"); got != "" {
		t.Fatalf("a TCP session alongside the request still linked: %q", got)
	}
	s.activity.End("i-1")
	// A second HTTP forward without trace context is ambiguous too.
	s.activity.Begin("i-1")
	endSecond := s.inflightTraces.begin("i-1", "")
	if got := ask("10.100.0.7"); got != "" {
		t.Fatalf("two requests in flight still linked: %q", got)
	}
	endSecond()
	s.activity.End("i-1")
	if got := ask("10.100.0.7"); got != testTraceparent {
		t.Fatalf("back to one traced request: %q", got)
	}
	endFirst()
	s.activity.End("i-1")
	if got := ask("10.100.0.7"); got != "" {
		t.Fatalf("an idle instance linked: %q", got)
	}
	// One untraced request answers nothing.
	s.activity.Begin("i-1")
	endUntraced := s.inflightTraces.begin("i-1", "")
	if got := ask("10.100.0.7"); got != "" {
		t.Fatalf("an untraced request linked: %q", got)
	}
	endUntraced()
	s.activity.End("i-1")
	if len(s.inflightTraces.byInstance) != 0 {
		t.Fatalf("registry kept finished forwards: %+v", s.inflightTraces.byInstance)
	}
	// App and Job tasks serve no requests, even with a forward recorded.
	for _, tc := range []struct{ ip, instance string }{{"10.100.0.8", "task"}, {"10.100.0.9", "job"}} {
		s.activity.Begin(tc.instance)
		end := s.inflightTraces.begin(tc.instance, testTraceparent)
		if got := ask(tc.ip); got != "" {
			t.Fatalf("%s linked: %q", tc.instance, got)
		}
		end()
		s.activity.End(tc.instance)
	}
	for _, bad := range []string{"", "not-an-ip", "fd00::7"} {
		if _, err := s.InflightRequestTrace(ctx, &vmmdpb.InflightRequestTraceRequest{SourceIp: bad}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("source %q = %v, want InvalidArgument", bad, err)
		}
	}
	if resp, err := (&Server{vmm: struct{ VmmdAPI }{}}).InflightRequestTrace(ctx, &vmmdpb.InflightRequestTraceRequest{SourceIp: "10.100.0.7"}); err != nil || resp.GetTraceparent() != "" {
		t.Fatalf("a runtime without live caller identity = %v, %v", resp, err)
	}
}
