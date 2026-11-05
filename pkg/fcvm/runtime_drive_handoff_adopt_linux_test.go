//go:build linux

package fcvm

// adr: 20261018 runtime admission re-bind, open question 1

import (
	"errors"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// A successor rebuilds an adopted measured guest's handoff from a real
// process's descriptors, observes it, and drops it once the process is gone.
func TestAdoptRuntimeDriveHandoffFromARealProcess(t *testing.T) {
	f := newMeasuredHandoff(t)
	main := openRuntimeDriveProcessMain(t, f.runtimeDriveFixture, "exact")
	defer main.Close()
	cmd, uid := startRuntimeDriveProcess(t, f.handoff.drives[0].file, main)
	if uid == 0 {
		t.Skip("the drive observation needs a non-root jail uid")
	}
	start, err := observeRuntimeDriveHandles(t.Context(), "/proc", cmd.Process.Pid, uid, f.handoff.drives)
	if err != nil {
		t.Fatal(err)
	}
	// The receipt the predecessor issued for this process.
	receipt := f.receipt.Clone()
	receipt.LeaseUID = int32(uid)
	receipt.ArtifactConsumption.ProcessPID, receipt.ArtifactConsumption.ProcessStart = uint32(cmd.Process.Pid), start
	lease := f.lease
	lease.UID = uid
	successor := NewJailerVMM(t.TempDir(), time.Second)
	adoptRuntimeDriveTestProcess(successor, lease, cmd)
	if err := successor.adoptRuntimeDriveHandoff(t.Context(), lease, cmd.Process.Pid, receipt, f.pins); err != nil {
		t.Fatal(err)
	}
	observed, err := successor.ObservedRuntimeDrives(t.Context(), lease)
	if err != nil || !runtimeConsumptionFromObservation(observed).Equal(receipt.ArtifactConsumption) {
		t.Fatalf("observation after adoption = %+v, %v", observed, err)
	}
	if pins := successor.adoptionDrivePins(lease); checkDrivePins(receipt, pins) != nil {
		t.Fatalf("the successor's own block pins = %+v", pins)
	}
	if err := successor.adoptRuntimeDriveHandoff(t.Context(), lease, cmd.Process.Pid, receipt, f.pins); !errors.Is(err, runtimeadmission.ErrReplay) {
		t.Fatalf("second adoption = %v, want replay", err)
	}
	if err := successor.releaseRuntimeDriveHandoff(lease.Instance); err != nil {
		t.Fatal(err)
	}

	// A receipt for another process start is refused and leaves nothing.
	stale := receipt.Clone()
	stale.ArtifactConsumption.ProcessStart = strconv.Itoa(mustAtoi(t, start) + 1)
	if err := successor.adoptRuntimeDriveHandoff(t.Context(), lease, cmd.Process.Pid, stale, f.pins); err == nil {
		t.Fatal("adopted a handoff for another process start")
	}
	if h, _ := successor.runtimeDriveHandoff(lease); h != nil {
		t.Fatal("a refused handoff stayed registered")
	}
}

// adoptRuntimeDriveTestProcess registers cmd as lease's process, as
// adoptGuestProcess does.
func adoptRuntimeDriveTestProcess(v *JailerVMM, lease Lease, cmd *exec.Cmd) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.proc[lease.Instance] = cmd
	v.recs[lease.Instance] = &instanceRecord{cmd: cmd, done: make(chan struct{})}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
