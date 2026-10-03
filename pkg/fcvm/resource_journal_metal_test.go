//go:build linux && metal

// adr: 399
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

func TestMetalResourceJournalSurvivingGuest(t *testing.T) {
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
	if err := old.WithResourceJournal(journal); err != nil {
		t.Fatal(err)
	}
	freshVMM := newMetalVMM(t, 30*time.Second)
	fresh := NewManager(wire.ExecRunner{}, freshVMM, Paths{Kernel: os.Getenv("FAAS_TEST_KERNEL")}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
	activeJournal := journal
	t.Cleanup(func() {
		defer func() { _ = activeJournal.Close() }()
		cleanupCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if err := fresh.Destroy(cleanupCtx, idOther); err != nil {
			t.Errorf("fresh cleanup: %v", err)
		}
		// Only the original fixture owner has the process watchdog, mounts and
		// artifact bookkeeping. Rebind its journal storage solely for cleanup;
		// the fresh Manager never receives that lifecycle ownership.
		old.resourceJournal = activeJournal
		old.vmm.(*JailerVMM).SetResourceJournal(activeJournal)
		if err := old.Destroy(cleanupCtx, idLive); err != nil {
			t.Errorf("survivor cleanup: %v", err)
		}
		records, err := activeJournal.snapshot()
		if err != nil || len(records) != 0 {
			t.Errorf("journal retirement: %d records, %v", len(records), err)
		}
		leakcheck.AssertZero(t)
	})
	req := WakeRequest{Instance: idLive, BaseKey: os.Getenv("FAAS_TEST_BASE_ROOTFS"), LayerKey: os.Getenv("FAAS_TEST_LAYER_ROOTFS"), VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro}
	first, err := old.Wake(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	pid, ok := old.InstancePID(idLive)
	if !ok {
		t.Fatal("missing guest PID")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	activeJournal, err = OpenResourceJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	records, err := activeJournal.snapshot()
	if err != nil || len(records) != 1 || records[0].Process == nil || records[0].Process.PID != pid || records[0].Process.StartTicks == 0 {
		t.Fatalf("durable process checkpoint: %+v, %v", records, err)
	}
	binds := 0
	jails, namespaces, links := 0, 0, 0
	for _, a := range records[0].Assets {
		if a.Kind == "jail" {
			jails++
			if a.File == nil || a.Namespace == nil {
				t.Fatalf("surviving jail lacks directory/context provenance: %+v", a)
			}
		}
		if a.Kind == "netns" {
			namespaces++
			if a.File == nil || a.Mount == nil {
				t.Fatalf("surviving namespace lacks binding provenance: %+v", a)
			}
		}
		if a.Kind == "veth" {
			links++
			if a.Link == nil || a.Link.Index <= 0 || a.Namespace == nil {
				t.Fatalf("surviving veth lacks index/creator provenance: %+v", a)
			}
		}
		if a.Kind == "bind" {
			binds++
			if a.Target == nil || a.File == nil || a.Mount == nil || a.Mount.MountID == 0 {
				t.Fatalf("surviving guest lacks bind provenance: %+v", a)
			}
		}
	}
	if binds == 0 {
		t.Fatal("surviving guest lacks image-bind checkpoints")
	}
	if jails != 2 || namespaces != 1 || links != 1 || records[0].Version != 4 {
		t.Fatalf("incomplete placement inventory: jails=%d namespaces=%d links=%d", jails, namespaces, links)
	}
	if err := fresh.WithResourceJournal(activeJournal); err != nil {
		t.Fatal(err)
	}
	rep, err := fresh.RecoverRestartQuarantine(ctx, freshVMM.JailRoot())
	if err != nil || rep.JournalRecords != 1 || rep.JournalProcessMatches != 1 {
		t.Fatalf("real process provenance: %+v, %v", rep, err)
	}
	if fresh.HasInstanceOwnership(idLive) {
		t.Fatal("journal provenance became lifecycle ownership")
	}
	if err := fresh.Destroy(ctx, idLive); !errors.Is(err, ErrRestartQuarantine) {
		t.Fatalf("unowned destroy: %v", err)
	}
	req.Instance = idOther
	second, err := fresh.Wake(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Lease.Slot == second.Lease.Slot || first.Lease.UID == second.Lease.UID || syscall.Kill(pid, 0) != nil {
		t.Fatal("journal survivor displaced by fresh boot")
	}
}
