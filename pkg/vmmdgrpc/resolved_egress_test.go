// adr: 373 — DNS-gated egress hook; 20261026150000 — bound database port.
package vmmdgrpc_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
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
	owner        fcvm.ServiceCallerIdentity
	source       netip.Addr
	addrs        []netip.Addr
	ttl          time.Duration
	databasePort uint16
	err          error
}

func (r *recordingAllower) AllowResolvedEgress(_ context.Context, source netip.Addr, owner fcvm.ServiceCallerIdentity, addrs []netip.Addr, ttl time.Duration, databasePort uint16) error {
	r.owner = owner
	r.source, r.addrs, r.ttl, r.databasePort = source, addrs, ttl, databasePort
	return r.err
}

func TestAllowResolvedEgressRPC(t *testing.T) {
	a := &recordingAllower{fakeVMM: &fakeVMM{}}
	s := vmmdgrpc.New(a, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil)
	ack, err := s.AllowResolvedEgress(context.Background(), &vmmdpb.AllowResolvedEgressRequest{
		InstanceId: "instance", NetworkGeneration: "generation", SourceIp: "10.100.0.7", Addresses: []string{"198.51.100.10", "2001:db8::1"}, TtlSeconds: 300,
	})
	if err != nil || !ack.GetMatched() {
		t.Fatalf("ack = %v, %v", ack, err)
	}
	if a.owner.InstanceID != "instance" || a.owner.NetworkGeneration != "generation" || a.source.String() != "10.100.0.7" || len(a.addrs) != 2 || a.ttl != 300*time.Second || a.databasePort != 0 {
		t.Fatalf("forwarded = %v %v %v %d", a.source, a.addrs, a.ttl, a.databasePort)
	}
	// ADR 20261026150000: a bound database's port rides the same call.
	if _, err := s.AllowResolvedEgress(context.Background(), &vmmdpb.AllowResolvedEgressRequest{
		InstanceId: "instance", NetworkGeneration: "generation", SourceIp: "10.100.0.7", Addresses: []string{"198.51.100.10"}, TtlSeconds: 300, DatabasePort: 5432,
	}); err != nil || a.databasePort != 5432 {
		t.Fatalf("database port = %d, %v", a.databasePort, err)
	}
	a.err = fcvm.ErrResolvedEgressNoInstance
	if ack, err := s.AllowResolvedEgress(context.Background(), &vmmdpb.AllowResolvedEgressRequest{SourceIp: "10.100.0.8", InstanceId: "instance", NetworkGeneration: "generation"}); err != nil || ack.GetMatched() {
		t.Fatalf("unknown source = %v, %v; want matched=false, no error", ack, err)
	}
	for _, bad := range []*vmmdpb.AllowResolvedEgressRequest{
		{SourceIp: "10.100.0.7"},
		{SourceIp: "nope", InstanceId: "instance", NetworkGeneration: "generation"},
		{SourceIp: "10.100.0.7", InstanceId: "instance", NetworkGeneration: "generation", Addresses: []string{"999.1.1.1"}},
		{SourceIp: "10.100.0.7", InstanceId: "instance", NetworkGeneration: "generation", DatabasePort: 70000},
		{SourceIp: "10.100.0.7", InstanceId: "instance", NetworkGeneration: "generation", DatabasePort: 25}, // ADR-361 never opens SMTP
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

type namedRecordingAllower struct {
	recordingAllower
	name string
}

func (r *namedRecordingAllower) AllowResolvedEgressNamed(_ context.Context, source netip.Addr, _ fcvm.ServiceCallerIdentity, name string, addrs []netip.Addr, ttl time.Duration, databasePort uint16) error {
	r.name = name
	r.source, r.addrs, r.ttl, r.databasePort = source, addrs, ttl, databasePort
	return r.err
}

// ADR-20261027090013 §6: the answered name reaches a manager that checks it.
func TestAllowResolvedEgressRPCPassesTheName(t *testing.T) {
	a := &namedRecordingAllower{recordingAllower: recordingAllower{fakeVMM: &fakeVMM{}}}
	s := vmmdgrpc.New(a, wire.NewOpsMetrics("vmmd_test_named"), "1.10.0", nil)
	if ack, err := s.AllowResolvedEgress(context.Background(), &vmmdpb.AllowResolvedEgressRequest{
		SourceIp: "10.100.0.7", InstanceId: "sandbox-1", NetworkGeneration: "generation", Name: "registry.npmjs.org", Addresses: []string{"198.51.100.10"}, TtlSeconds: 60,
	}); err != nil || !ack.GetMatched() || a.name != "registry.npmjs.org" || len(a.addrs) != 1 {
		t.Fatalf("ack = %v, %v; name %q, addrs %v", ack, err, a.name, a.addrs)
	}
}

// rc.324 refused every builder's DNS grants without a log line on either
// side. A refusal is logged with the instance, once per interval.
func TestAllowResolvedEgressLogsARefusalOncePerInterval(t *testing.T) {
	var logs bytes.Buffer
	a := &recordingAllower{fakeVMM: &fakeVMM{}, err: fmt.Errorf("teardown: %w", fcvm.ErrResolvedEgressNoInstance)}
	s := vmmdgrpc.New(a, wire.NewOpsMetrics("vmmd_test_refusal_log"), "1.10.0", slog.New(slog.NewJSONHandler(&logs, nil)))
	req := &vmmdpb.AllowResolvedEgressRequest{SourceIp: "10.100.0.8", InstanceId: "build-1", NetworkGeneration: "generation", Addresses: []string{"104.16.212.131"}}
	for range 3 {
		if ack, err := s.AllowResolvedEgress(context.Background(), req); err != nil || ack.GetMatched() {
			t.Fatalf("refused grant = %v, %v; want matched=false, no error", ack, err)
		}
	}
	if got := strings.Count(logs.String(), "DNS answer not granted to its guest"); got != 1 {
		t.Fatalf("refusal log lines = %d, want 1:\n%s", got, logs.String())
	}
	for _, want := range []string{`"instance":"build-1"`, `"source":"10.100.0.8"`, `"reason"`} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("refusal log lacks %s:\n%s", want, logs.String())
		}
	}
	req.InstanceId = "build-2"
	if _, err := s.AllowResolvedEgress(context.Background(), req); err != nil || strings.Count(logs.String(), "DNS answer not granted") != 2 {
		t.Fatalf("another instance's refusal was not logged: %v\n%s", err, logs.String())
	}
}
