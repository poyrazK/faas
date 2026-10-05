// adr: 590
package fcvm

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"net/netip"
	"testing"
)

func TestAdmittedAppEgressPolicyNativeReceiptAndFailure(t *testing.T) {
	run := &policyNetworkRunner{failInput: true}
	vmm := &consumedRuntimeVMM{runtimeSourceCapableVMM: runtimeSourceCapableVMM{fakeVMM: &fakeVMM{}}}
	m := newTestManager(run, vmm).WithRuntimeAdmissionNodeID(uuid.NewString())
	identity, err := m.RuntimeAdmissionIdentity()
	if err != nil {
		t.Fatal(err)
	}
	p := runtimeadmission.EgressPolicy{AppID: uuid.NewString(), Revision: 4, Allowlist: []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}, Ports: []int{80, 8443}}
	seedPolicyInstance(m, "live", p.AppID, api.PlanPro)
	stale := identity
	stale.Incarnation = uuid.NewString()
	if r, err := m.UpdateAdmittedAppEgressPolicy(t.Context(), stale, p); !errors.Is(err, runtimeadmission.ErrStale) || r != (runtimeadmission.EgressReceipt{}) || run.inputCount() != 0 {
		t.Fatal("another process caused writes", err)
	}
	if r, err := m.UpdateAdmittedAppEgressPolicy(t.Context(), identity, p); err == nil || r != (runtimeadmission.EgressReceipt{}) {
		t.Fatal("partial physical failure produced receipt", err)
	}
	run.inputMu.Lock()
	run.failInput = false
	run.inputMu.Unlock()
	r, err := m.UpdateAdmittedAppEgressPolicy(t.Context(), identity, p)
	if err != nil || r.Check(identity, p) != nil {
		t.Fatalf("native receipt: %+v %v", r, err)
	}
	p.Revision--
	if r, err := m.UpdateAdmittedAppEgressPolicy(t.Context(), identity, p); err == nil || r != (runtimeadmission.EgressReceipt{}) {
		t.Fatal("older revision produced receipt")
	}
}

func TestAdmittedAppEgressPolicyDownlevelManager(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithRuntimeAdmissionNodeID(uuid.NewString())
	i, err := m.RuntimeAdmissionIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateAdmittedAppEgressPolicy(t.Context(), i, runtimeadmission.EgressPolicy{AppID: uuid.NewString(), Revision: 1}); !errors.Is(err, runtimeadmission.ErrUnavailable) {
		t.Fatal("downlevel native capability accepted", err)
	}
}
