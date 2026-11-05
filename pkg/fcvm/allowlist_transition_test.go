package fcvm

// adr: 20261103194727

import (
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAllowlistShapeFailureDoesNotAcknowledgeOrCachePhysicalSuccess(t *testing.T) {
	run := &policyNetworkRunner{failInput: true}
	m := newTestManager(run, &fakeVMM{})
	seedPolicyInstance(m, "transition", "app-transition", api.PlanPro)
	wanted := []netip.Prefix{netip.MustParsePrefix("8.8.8.8/32")}
	if err := m.UpdateAppEgressPolicy(t.Context(), "app-transition", 1, wanted, nil); err == nil {
		t.Fatal("failed replacement acknowledged")
	}
	if len(m.live["transition"].Net.EgressAllowlist) != 0 {
		t.Fatal("failed replacement cached as applied")
	}
	if err := m.UpdateAppEgressPolicy(t.Context(), "app-transition", 1, nil, nil); err == nil {
		t.Fatal("conflicting intent replaced pending policy")
	}
	run.failInput = false
	if err := m.UpdateAppEgressPolicy(t.Context(), "app-transition", 1, wanted, nil); err != nil {
		t.Fatal(err)
	}
	if !samePrefixSet(m.live["transition"].Net.EgressAllowlist, wanted) {
		t.Fatal("retry failed to converge")
	}
}
