// adr: 595
package sched_test

import (
	"context"
	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"net/netip"
	"testing"
)

type admittedEgressWireServer struct {
	vmmdpb.UnimplementedVmmdServer
	calls  int
	mutate func(*vmmdpb.UpdateAdmittedAppEgressPolicyAck)
}

func (s *admittedEgressWireServer) UpdateAdmittedAppEgressPolicy(_ context.Context, req *vmmdpb.UpdateAdmittedAppEgressPolicyRequest) (*vmmdpb.UpdateAdmittedAppEgressPolicyAck, error) {
	s.calls++
	i, p, err := runtimeadmission.EgressFromProto(req)
	if err != nil {
		return nil, err
	}
	hash, _ := p.Hash()
	ack := runtimeadmission.EgressReceipt{Identity: i, AppID: p.AppID, Revision: p.Revision, PolicyHash: hash}.ToProto()
	if s.mutate != nil {
		s.mutate(ack)
	}
	return ack, nil
}

func TestVMMClientAdmittedEgressWireAndRejectedAcknowledgments(t *testing.T) {
	identity := runtimeadmission.Identity{NodeID: uuid.NewString(), Incarnation: uuid.NewString(), ProtocolVersion: runtimeadmission.ArtifactProtocolVersion}
	p := runtimeadmission.EgressPolicy{AppID: uuid.NewString(), Revision: 9, Allowlist: []netip.Prefix{netip.MustParsePrefix("8.8.8.1/24")}, Ports: []int{80, 8443}}
	s := &admittedEgressWireServer{}
	client := newPolicyWireClient(t, s)
	if receipt, err := client.UpdateAdmittedAppEgressPolicy(t.Context(), identity, p); err != nil || receipt.Check(identity, p) != nil || s.calls != 1 {
		t.Fatal("wire receipt not verified", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*vmmdpb.UpdateAdmittedAppEgressPolicyAck)
	}{
		{"node", func(a *vmmdpb.UpdateAdmittedAppEgressPolicyAck) { a.Identity.NodeId = uuid.NewString() }},
		{"incarnation", func(a *vmmdpb.UpdateAdmittedAppEgressPolicyAck) { a.Identity.Incarnation = uuid.NewString() }},
		{"protocol", func(a *vmmdpb.UpdateAdmittedAppEgressPolicyAck) { a.Identity.ProtocolVersion = 1 }},
		{"app", func(a *vmmdpb.UpdateAdmittedAppEgressPolicyAck) { a.AppId = uuid.NewString() }},
		{"revision", func(a *vmmdpb.UpdateAdmittedAppEgressPolicyAck) { a.Revision++ }},
		{"hash", func(a *vmmdpb.UpdateAdmittedAppEgressPolicyAck) { a.PolicyHash = "" }},
		{"missing identity", func(a *vmmdpb.UpdateAdmittedAppEgressPolicyAck) { a.Identity = nil }},
		{"unknown", func(a *vmmdpb.UpdateAdmittedAppEgressPolicyAck) {
			a.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
		{"nested unknown", func(a *vmmdpb.UpdateAdmittedAppEgressPolicyAck) {
			a.Identity.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &admittedEgressWireServer{mutate: tc.mutate}
			client := newPolicyWireClient(t, server)
			receipt, err := client.UpdateAdmittedAppEgressPolicy(t.Context(), identity, p)
			if err == nil || receipt != (runtimeadmission.EgressReceipt{}) || server.calls != 1 {
				t.Fatal("malformed acknowledgment accepted", err)
			}
		})
	}
	old := newPolicyWireClient(t, &vmmdpb.UnimplementedVmmdServer{})
	if _, err := old.UpdateAdmittedAppEgressPolicy(t.Context(), identity, p); err == nil {
		t.Fatal("old node silently fell back")
	}
}
