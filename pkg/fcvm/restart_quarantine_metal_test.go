//go:build linux && metal

// adr: 472
package fcvm

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalRestartQuarantineProtectsSurvivingGuest(t *testing.T) {
	for _, name := range []string{"FAAS_TEST_KERNEL", "FAAS_TEST_BASE_ROOTFS", "FAAS_TEST_LAYER_ROOTFS"} {
		if os.Getenv(name) == "" {
			t.Skipf("required %s is unset", name)
		}
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root and dedicated Linux KVM")
	}
	for _, test := range []struct{ name, instance string }{
		{"uuid", idLive}, {"builder_id", "build-" + idLive}, {"compact_id", "0123456789abcdef0123456789abcdef"},
	} {
		t.Run(test.name, func(t *testing.T) { testMetalRestartQuarantine(t, test.instance) })
	}
}

func testMetalRestartQuarantine(t *testing.T, instance string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	old := NewAcceptanceManager(t)
	freshVMM := newMetalVMM(t, 30*time.Second)
	fresh := NewManager(wire.ExecRunner{}, freshVMM, Paths{Kernel: os.Getenv("FAAS_TEST_KERNEL")}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
	// The original manager is kept only to clean up the fixture. This checks
	// a fresh allocator and VMM against surviving real resources, not a full
	// daemon crash, recovered routing or a recovered process watchdog.
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if err := fresh.Destroy(cleanupCtx, idOther); err != nil {
			t.Errorf("new guest cleanup: %v", err)
		}
		if err := old.Destroy(cleanupCtx, instance); err != nil {
			t.Errorf("surviving guest cleanup: %v", err)
		}
		leakcheck.AssertZero(t)
	})
	req := WakeRequest{Instance: instance, BaseKey: os.Getenv("FAAS_TEST_BASE_ROOTFS"), LayerKey: os.Getenv("FAAS_TEST_LAYER_ROOTFS"),
		VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro}
	first, err := old.Wake(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	pid, ok := old.InstancePID(instance)
	if !ok {
		t.Fatal("missing live guest PID")
	}
	rep, err := fresh.RecoverRestartQuarantine(ctx, freshVMM.JailRoot())
	if err != nil || rep.Slots < 1 || rep.Instances < 1 || rep.Processes < 1 {
		t.Fatalf("real resource inventory: %+v, %v", rep, err)
	}
	if err := fresh.Destroy(ctx, instance); !errors.Is(err, ErrRestartQuarantine) {
		t.Fatalf("unowned stop was acknowledged: %v", err)
	}
	if _, _, err := fresh.SignalAndKill(ctx, instance, syscall.SIGTERM, time.Second); !errors.Is(err, ErrRestartQuarantine) {
		t.Fatalf("unowned signal was acknowledged: %v", err)
	}
	if _, err := fresh.Wake(ctx, req); !errors.Is(err, ErrRestartQuarantine) {
		t.Fatalf("same instance boot was admitted: %v", err)
	}
	if fresh.HasInstanceOwnership(instance) || fresh.LiveCount() != 0 || syscall.Kill(pid, 0) != nil {
		t.Fatal("quarantine invented ownership or killed the surviving guest")
	}
	req.Instance = idOther
	second, err := fresh.Wake(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Lease.Slot == second.Lease.Slot || first.Lease.UID == second.Lease.UID || first.Lease.HostIP == second.Lease.HostIP || GuestVsockCID(first.Lease.Slot) == GuestVsockCID(second.Lease.Slot) {
		t.Fatal("fresh boot reused a surviving guest identity")
	}
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("fresh boot displaced the surviving guest")
	}
}
