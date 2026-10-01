// adr: 385 — old nodes must refuse before applying a revisioned tuple.
package vmmdgrpc_test

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type revisionedEgressVMM struct {
	*fakeVMM
	update func(context.Context, string, int64, []netip.Prefix, []uint16) error
}

func (v *revisionedEgressVMM) UpdateAppEgressPolicy(ctx context.Context, app string, revision int64, cidrs []netip.Prefix, ports []uint16) error {
	return v.update(ctx, app, revision, cidrs, ports)
}

func TestUpdateAppEgressPolicyCompleteTupleAndAck(t *testing.T) {
	called := 0
	vmm := &revisionedEgressVMM{fakeVMM: &fakeVMM{}, update: func(_ context.Context, app string, revision int64, cidrs []netip.Prefix, ports []uint16) error {
		called++
		if app != "app" || revision != 7 || !slices.Equal(cidrs, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}) || !slices.Equal(ports, []uint16{5432}) {
			t.Fatalf("wrong complete tuple: %s %d %v %v", app, revision, cidrs, ports)
		}
		return nil
	}}
	s := vmmdgrpc.New(vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil)
	ack, err := s.UpdateAppEgressPolicy(t.Context(), &vmmdpb.UpdateAppEgressPolicyRequest{AppId: "app", Revision: 7, EgressAllowlist: []string{"8.8.8.0/24"}, EgressPorts: []uint32{5432}})
	if err != nil || ack.GetRevision() != 7 || called != 1 {
		t.Fatalf("ack=%v err=%v calls=%d", ack, err, called)
	}
	vmm.update = func(context.Context, string, int64, []netip.Prefix, []uint16) error {
		return errors.New("physical apply failed")
	}
	ack, err = s.UpdateAppEgressPolicy(t.Context(), &vmmdpb.UpdateAppEgressPolicyRequest{AppId: "app", Revision: 8})
	if err == nil || ack != nil {
		t.Fatalf("failed physical update acknowledged: ack=%v err=%v", ack, err)
	}
}

func TestUpdateAppEgressPolicyRejectsMalformedBeforeMutation(t *testing.T) {
	called := 0
	vmm := &revisionedEgressVMM{fakeVMM: &fakeVMM{}, update: func(context.Context, string, int64, []netip.Prefix, []uint16) error { called++; return nil }}
	s := vmmdgrpc.New(vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil)
	for _, tc := range []struct {
		name string
		req  *vmmdpb.UpdateAppEgressPolicyRequest
	}{
		{"nil", nil},
		{"missing app", &vmmdpb.UpdateAppEgressPolicyRequest{Revision: 1}},
		{"missing revision", &vmmdpb.UpdateAppEgressPolicyRequest{AppId: "app"}},
		{"negative revision", &vmmdpb.UpdateAppEgressPolicyRequest{AppId: "app", Revision: -1}},
		{"CIDR", &vmmdpb.UpdateAppEgressPolicyRequest{AppId: "app", Revision: 1, EgressAllowlist: []string{"invalid"}}},
		{"default route", &vmmdpb.UpdateAppEgressPolicyRequest{AppId: "app", Revision: 1, EgressAllowlist: []string{"::/0"}}},
		{"port zero", &vmmdpb.UpdateAppEgressPolicyRequest{AppId: "app", Revision: 1, EgressPorts: []uint32{0}}},
		{"port overflow", &vmmdpb.UpdateAppEgressPolicyRequest{AppId: "app", Revision: 1, EgressPorts: []uint32{65536}}},
		{"forbidden port", &vmmdpb.UpdateAppEgressPolicyRequest{AppId: "app", Revision: 1, EgressPorts: []uint32{25}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ack, err := s.UpdateAppEgressPolicy(t.Context(), tc.req)
			if status.Code(err) != codes.InvalidArgument || ack != nil {
				t.Fatalf("ack=%v err=%v, want InvalidArgument", ack, err)
			}
		})
	}
	if called != 0 {
		t.Fatalf("malformed request reached physical updater %d times", called)
	}
}

func TestUpdateAppEgressPolicyUnsupportedBackendNeverFallsBack(t *testing.T) {
	legacyCalls := 0
	vmm := &fakeVMM{updateAllowlistFn: func(context.Context, string, []netip.Prefix) error { legacyCalls++; return nil }}
	s := vmmdgrpc.New(vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil)
	ack, err := s.UpdateAppEgressPolicy(t.Context(), &vmmdpb.UpdateAppEgressPolicyRequest{AppId: "app", Revision: 1})
	if status.Code(err) != codes.Unimplemented || ack != nil || legacyCalls != 0 {
		t.Fatalf("ack=%v err=%v legacy calls=%d", ack, err, legacyCalls)
	}
}
