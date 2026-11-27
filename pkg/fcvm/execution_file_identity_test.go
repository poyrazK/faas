package fcvm

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestExecutionFileIdentityWithoutOutboundGrant(t *testing.T) {
	m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{Kernel: "kernel/test"}, "1.0.0", nil, nil)
	inst, err := m.WakeExecution(t.Context(), ExecutionWakeRequest{
		Instance: "file-run", AccountID: "file-account", Plan: api.PlanPro,
		Runtime: string(api.ExecutionRuntimeNode24), KernelKey: "kernel/test",
		BaseKey: "base/test", LayerKey: "layer/test", VcpuCount: 1,
		MemSizeMiB: 256, CPUMillicores: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Destroy(t.Context(), inst.Lease.Instance) }()
	account, run, err := m.ExecutionFileIdentity(inst.Lease.Instance)
	if err != nil || account != "file-account" || run != "file-run" {
		t.Fatalf("plain Run file identity = (%q, %q), %v", account, run, err)
	}
	if _, _, _, err := m.ExecutionOutboundIdentity(inst.Lease.Instance, "11111111-1111-4111-8111-111111111111"); err == nil {
		t.Fatal("file access granted an outbound capability")
	}
	if _, _, _, _, err := m.ExecutionRelayIdentity(inst.Lease.Instance); err == nil {
		t.Fatal("file access granted relay identity")
	}
	if err := m.Destroy(t.Context(), inst.Lease.Instance); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.ExecutionFileIdentity(inst.Lease.Instance); !errors.Is(err, ErrExecutionInstanceNotFound) {
		t.Fatal("retired Run retained file identity")
	}
}
