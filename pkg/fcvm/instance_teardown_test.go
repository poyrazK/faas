// adr: 395 — stop acknowledgements require retained ownership and confirmed exit.
package fcvm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type heldTeardownVMM struct {
	*fakeVMM
	entered, release chan struct{}
	once             atomic.Bool
	signal           bool
}

func (v *heldTeardownVMM) hold() {
	if v.once.CompareAndSwap(false, true) {
		close(v.entered)
		<-v.release
	}
}

func (v *heldTeardownVMM) DestroyWithExport(ctx context.Context, lease Lease, dir string) (int, error) {
	if !v.signal {
		v.hold()
	}
	return v.fakeVMM.DestroyWithExport(ctx, lease, dir)
}

func (v *heldTeardownVMM) SignalAndKill(ctx context.Context, lease Lease, signal syscall.Signal, grace time.Duration) (bool, int32, error) {
	v.hold()
	return v.fakeVMM.SignalAndKill(ctx, lease, signal, grace)
}

func TestConcurrentStopsWaitForTeardownOwner(t *testing.T) {
	for _, kind := range []string{"destroy", "signal"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			v := &heldTeardownVMM{fakeVMM: &fakeVMM{}, entered: make(chan struct{}), release: make(chan struct{}), signal: kind == "signal"}
			m := newTestManager(&fakeRunner{}, v)
			request := admissionWake("held-teardown")
			if _, err := m.Wake(ctx, request); err != nil {
				t.Fatal(err)
			}
			first := make(chan error, 1)
			go func() {
				if kind == "signal" {
					_, _, err := m.SignalAndKill(ctx, request.Instance, syscall.SIGTERM, time.Second)
					first <- err
				} else {
					first <- m.Destroy(ctx, request.Instance)
				}
			}()
			waitBootSignal(t, ctx, v.entered)
			waiterCtx, waiterCancel := context.WithTimeout(ctx, 30*time.Millisecond)
			err := m.Destroy(waiterCtx, request.Instance)
			waiterCancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("second stop acknowledged an unfinished teardown: %v", err)
			}
			if m.LiveCount() != 1 || m.LeasedCount() != 1 {
				t.Fatal("stop lost the owned instance or lease")
			}
			if err := m.ResumeVM(ctx, request.Instance); err == nil {
				t.Fatal("resume crossed a stop reservation")
			}
			if _, err := m.Wake(ctx, request); err == nil {
				t.Fatal("boot crossed a stop reservation")
			}
			close(v.release)
			if err := waitInstanceResult(t, ctx, first); err != nil {
				t.Fatal(err)
			}
			if m.LiveCount() != 0 || m.LeasedCount() != 0 {
				t.Fatal("owner did not finish cleanup")
			}
		})
	}
}

func TestFailedBootRetainsCleanupForDestroyRetry(t *testing.T) {
	for _, kind := range []string{"cold", "restore", "job"} {
		t.Run(kind, func(t *testing.T) {
			failure := errors.New("unconfirmed kill")
			v := &fakeVMM{bootErr: errors.New("boot failed"), restoreErr: errors.New("restore failed"), killErr: failure}
			m := newTestManager(&fakeRunner{}, v)
			request := admissionWake("failed-boot-owned")
			var err error
			switch kind {
			case "restore":
				request.Snapshot = usableSnapshot()
				_, err = m.Wake(t.Context(), request)
				if v.boots() != 0 {
					t.Fatal("cold boot started before failed restore teardown was confirmed")
				}
			case "job":
				_, err = m.BootJob(t.Context(), testJobBootRequest(request.Instance))
			default:
				_, err = m.Wake(t.Context(), request)
			}
			if !errors.Is(err, failure) {
				t.Fatalf("failed boot lost cleanup failure: %v", err)
			}
			if m.LiveCount() != 0 || m.LeasedCount() != 1 || len(m.pendingCleanup) != 1 {
				t.Fatal("failed boot forgot its cleanup identity")
			}
			if _, err := m.Wake(t.Context(), request); err == nil || !strings.Contains(err.Error(), "teardown pending") {
				t.Fatalf("boot reused an unconfirmed cleanup identity: %v", err)
			}
			v.mu.Lock()
			v.killErr = nil
			v.mu.Unlock()
			if err := m.Destroy(t.Context(), request.Instance); err != nil {
				t.Fatal(err)
			}
			if m.LeasedCount() != 0 || len(m.pendingCleanup) != 0 {
				t.Fatal("retry did not release failed boot resources")
			}
		})
	}
}

func TestParkRetainsInstanceWhenKillFails(t *testing.T) {
	v := &fakeVMM{killErr: errors.New("unconfirmed exit")}
	m := newTestManager(&fakeRunner{}, v)
	if _, err := m.Wake(t.Context(), admissionWake("park-owned")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Park(t.Context(), "park-owned", SnapshotSpec{}); !errors.Is(err, v.killErr) {
		t.Fatalf("park acknowledged failed cleanup: %v", err)
	}
	if m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Fatal("park released an unconfirmed guest")
	}
	v.killErr = nil
	if err := m.Destroy(t.Context(), "park-owned"); err != nil {
		t.Fatal(err)
	}
}

func TestKillRequiresWatchdogReceiptBeforeRemovingResources(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	v.destroyWait = 20 * time.Millisecond
	id := "unconfirmed-child"
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-exited })
	receipt := make(chan struct{})
	rec := &instanceRecord{cmd: cmd, done: receipt}
	v.proc[id], v.recs[id] = cmd, rec
	root, err := v.mkChroot(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Kill(t.Context(), Lease{Instance: id}); err == nil {
		t.Fatal("Kill acknowledged a missing exit receipt")
	}
	if v.recs[id] != rec || v.proc[id] != cmd {
		t.Fatal("Kill forgot the process before confirmed exit")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("Kill removed the jail before confirmed exit: %v", err)
	}
	<-exited
	close(receipt)
	if err := v.Kill(t.Context(), Lease{Instance: id}); err != nil {
		t.Fatal(err)
	}
	if v.recs[id] != nil || v.proc[id] != nil {
		t.Fatal("retry retained a confirmed dead process")
	}
}

func TestKillRetainsFailedMaterialisedRemoval(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	id := "cleanup-file-owned"
	done := make(chan struct{})
	close(done)
	rec := &instanceRecord{done: done}
	v.recs[id] = rec
	path := filepath.Join(t.TempDir(), "nonempty")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "marker")
	if err := os.WriteFile(marker, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	v.trackMaterialised(id, path)
	if err := v.Kill(t.Context(), Lease{Instance: id}); err == nil {
		t.Fatal("Kill swallowed resource removal failure")
	}
	if v.recs[id] != rec || len(v.materialisedTmp[id]) != 1 {
		t.Fatal("failed resource removal lost retry identity")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := v.Kill(t.Context(), Lease{Instance: id}); err != nil {
		t.Fatal(err)
	}
}

// A builder stop must interrupt export's child instead of waiting behind the
// export owner. The interrupt leaves the lease for that owner's cleanup.
type interruptibleExportVMM struct{ *heldTeardownVMM }

func (v *interruptibleExportVMM) InterruptBuild(context.Context, string) (int32, error) {
	close(v.release)
	return 0, nil
}

func TestBuilderInterruptDoesNotWaitBehindExportOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	v := &interruptibleExportVMM{&heldTeardownVMM{fakeVMM: &fakeVMM{}, entered: make(chan struct{}), release: make(chan struct{})}}
	m := newTestManager(&fakeRunner{}, v)
	request := admissionWake("export-owner")
	if _, err := m.Wake(ctx, request); err != nil {
		t.Fatal(err)
	}
	exportDir := t.TempDir()
	m.exportDirs[request.Instance] = exportDir
	result := make(chan error, 1)
	go func() { _, err := m.DestroyWithExport(ctx, request.Instance, exportDir); result <- err }()
	waitBootSignal(t, ctx, v.entered)
	if killed, _, err := m.SignalAndKill(ctx, request.Instance, syscall.SIGKILL, 0); err != nil || !killed {
		t.Fatalf("builder interrupt: killed=%v err=%v", killed, err)
	}
	if err := waitInstanceResult(t, ctx, result); err != nil {
		t.Fatal(err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 || m.ExportDirFor(request.Instance) != "" {
		t.Fatal("export owner did not finish cleanup")
	}
}

func TestKillRetainsRecordWhenCgroupRemovalFails(t *testing.T) {
	root := withFakeCgroupRoot(t)
	parent := filepath.Join(root, ParentCgroupFor("hobby"))
	if err := os.MkdirAll(filepath.Dir(parent), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := NewJailerVMM(t.TempDir(), time.Second)
	done := make(chan struct{})
	close(done)
	rec := &instanceRecord{done: done}
	lease := Lease{Instance: "cgroup-cleanup-owned", Plan: "hobby"}
	v.recs[lease.Instance] = rec
	if err := v.Kill(t.Context(), lease); err == nil {
		t.Fatal("Kill swallowed cgroup cleanup failure")
	}
	if v.recs[lease.Instance] != rec {
		t.Fatal("Kill forgot the failed cgroup cleanup owner")
	}
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	if err := v.Kill(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
}

func TestKillDoesNotTreatFailedWaitAsConfirmedExit(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	done := make(chan struct{})
	close(done)
	waitErr := errors.New("wait failed")
	v.recs["failed-wait"] = &instanceRecord{done: done, waitErr: waitErr}
	root, err := v.mkChroot("failed-wait")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Kill(t.Context(), Lease{Instance: "failed-wait"}); !errors.Is(err, waitErr) {
		t.Fatalf("failed watchdog wait was acknowledged: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("failed watchdog wait removed owned resources: %v", err)
	}
}
