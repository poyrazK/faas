//go:build linux && metal

package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"context"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/wire"
)

// vmmd restart adoption ADR, slice 1: a real ready guest gets a version-7
// adoption block that matches /proc, and a fresh Manager's dry run over the
// reopened journal classifies it adoptable. The fresh Manager still has no
// ownership: quarantine is unchanged and the original owner cleans up.
func TestMetalAdoptionDryRunClassifiesALiveGuest(t *testing.T) {
	for _, key := range []string{"FAAS_TEST_KERNEL", "FAAS_TEST_BASE_ROOTFS", "FAAS_TEST_LAYER_ROOTFS"} {
		if os.Getenv(key) == "" {
			t.Skipf("required %s unset", key)
		}
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root and dedicated Linux KVM")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	path := t.TempDir()
	journal := openTestResourceJournal(t, path)
	old := NewAcceptanceManager(t)
	// Production pairs adoption records with the file console; a pipe
	// console guest is refused (console_pipe, V-A3).
	old.vmm.(*JailerVMM).WithFileConsole()
	if err := old.WithResourceJournal(journal); err != nil {
		t.Fatal(err)
	}
	writerCtx, stopWriter := context.WithCancel(ctx)
	defer stopWriter()
	if err := old.EnableAdoptionRecords(writerCtx); err != nil {
		t.Fatal(err)
	}
	freshVMM := newMetalVMM(t, 30*time.Second)
	fresh := NewManager(wire.ExecRunner{}, freshVMM, Paths{Kernel: os.Getenv("FAAS_TEST_KERNEL")}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
	activeJournal := journal
	t.Cleanup(func() {
		stopWriter()
		defer func() { _ = activeJournal.Close() }()
		cleanupCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		old.resourceJournal = activeJournal
		old.vmm.(*JailerVMM).SetResourceJournal(activeJournal)
		if err := old.Destroy(cleanupCtx, idLive); err != nil {
			t.Errorf("guest cleanup: %v", err)
		}
		records, err := activeJournal.snapshot()
		if err != nil || len(records) != 0 {
			t.Errorf("journal retirement: %d records, %v", len(records), err)
		}
		leakcheck.AssertZero(t)
	})
	req := WakeRequest{Instance: idLive, BaseKey: os.Getenv("FAAS_TEST_BASE_ROOTFS"), LayerKey: os.Getenv("FAAS_TEST_LAYER_ROOTFS"), VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro}
	if _, err := old.Wake(ctx, req); err != nil {
		t.Fatal(err)
	}
	pid, ok := old.InstancePID(idLive)
	if !ok {
		t.Fatal("missing guest PID")
	}
	// The block is committed off the wake path; wait for it.
	var record resourceJournalRecord
	deadline := time.Now().Add(10 * time.Second)
	for {
		r, ok, err := journal.lookup(idLive)
		if err != nil {
			t.Fatal(err)
		}
		if ok && r.Adoption != nil {
			record = r
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no adoption block 10 s after readiness")
		}
		time.Sleep(50 * time.Millisecond)
	}
	exe, err := statIdentity("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		t.Fatal(err)
	}
	if record.Version != resourceAdoptionVersion || record.Adoption.Firecracker.Exe != exe || record.Adoption.Kind != adoptionKindApp ||
		record.Adoption.Firecracker.VCPUs != 1 || record.Adoption.Firecracker.MemoryMiB != 512 {
		t.Fatalf("adoption block does not match the guest: %+v (exe %+v)", record.Adoption, exe)
	}

	stopWriter()
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	activeJournal, err = OpenResourceJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.WithResourceJournal(activeJournal); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.RecoverRestartQuarantine(ctx, freshVMM.JailRoot()); err != nil {
		t.Fatal(err)
	}
	rep, err := fresh.DryRunAdoption(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Adoptable != 1 || len(rep.Refused) != 0 {
		t.Fatalf("dry run over a live guest = %+v", rep)
	}
	if fresh.HasInstanceOwnership(idLive) || syscall.Kill(pid, 0) != nil {
		t.Fatal("dry run changed ownership or the guest")
	}
}
