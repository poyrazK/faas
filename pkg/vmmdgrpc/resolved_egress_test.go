// adr: 373 — DNS-gated egress hook.
package vmmdgrpc_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

type recordingAllower struct {
	*fakeVMM
	source netip.Addr
	addrs  []netip.Addr
	ttl    time.Duration
	err    error
}

func (r *recordingAllower) AllowResolvedEgress(_ context.Context, source netip.Addr, addrs []netip.Addr, ttl time.Duration) error {
	r.source, r.addrs, r.ttl = source, addrs, ttl
	return r.err
}

func TestAllowResolvedEgressRPC(t *testing.T) {
	a := &recordingAllower{fakeVMM: &fakeVMM{}}
	s := vmmdgrpc.New(a, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil)
	ack, err := s.AllowResolvedEgress(context.Background(), &vmmdpb.AllowResolvedEgressRequest{
		SourceIp: "10.100.0.7", Addresses: []string{"198.51.100.10", "2001:db8::1"}, TtlSeconds: 300,
	})
	if err != nil || !ack.GetMatched() {
		t.Fatalf("ack = %v, %v", ack, err)
	}
	if a.source.String() != "10.100.0.7" || len(a.addrs) != 2 || a.ttl != 300*time.Second {
		t.Fatalf("forwarded = %v %v %v", a.source, a.addrs, a.ttl)
	}
	a.err = fcvm.ErrResolvedEgressNoInstance
	if ack, err := s.AllowResolvedEgress(context.Background(), &vmmdpb.AllowResolvedEgressRequest{SourceIp: "10.100.0.8"}); err != nil || ack.GetMatched() {
		t.Fatalf("unknown source = %v, %v; want matched=false, no error", ack, err)
	}
	for _, bad := range []*vmmdpb.AllowResolvedEgressRequest{
		{SourceIp: "nope"},
		{SourceIp: "10.100.0.7", Addresses: []string{"999.1.1.1"}},
	} {
		if _, err := s.AllowResolvedEgress(context.Background(), bad); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%v: code = %v, want InvalidArgument", bad, status.Code(err))
		}
	}
	plain := vmmdgrpc.New(&fakeVMM{}, wire.NewOpsMetrics("vmmd_test_plain"), "1.10.0", nil)
	if _, err := plain.AllowResolvedEgress(context.Background(), &vmmdpb.AllowResolvedEgressRequest{SourceIp: "10.100.0.7"}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("manager without the hook: code = %v, want Unimplemented", status.Code(err))
	}
}
