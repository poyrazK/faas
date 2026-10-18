package e2etest

// adr: 595

import (
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func TestFakeVMMDDurableEgressDelivery(t *testing.T) {
	server := &FakeVMMD{}
	request := &vmmdpb.UpdateAppEgressPolicyRequest{AppId: "app", Revision: 7, EgressAllowlist: []string{"8.8.8.0/24"}, EgressPorts: []uint32{8443}}
	ack, err := server.UpdateAppEgressPolicy(t.Context(), request)
	if err != nil || ack.GetRevision() != request.GetRevision() {
		t.Fatal("exact policy revision was not acknowledged", err)
	}
	request.EgressAllowlist[0], request.EgressPorts[0] = "changed", 1
	updates := server.EgressUpdates()
	if len(updates) != 1 || updates[0].GetEgressAllowlist()[0] != "8.8.8.0/24" || updates[0].GetEgressPorts()[0] != 8443 {
		t.Fatal("delivery record was lost or aliased", updates)
	}
	request.Revision = 0
	if _, err := server.UpdateAppEgressPolicy(t.Context(), request); err == nil || len(server.EgressUpdates()) != 1 {
		t.Fatal("invalid revision was delivered")
	}
}
