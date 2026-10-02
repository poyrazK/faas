// spec: §4.5 — builder VMs are bounded, disposable work and must release
// their slot and scratch drive after terminal guest completion.
package fcvm

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestBuilderGuestHaltWatcherReapsWithoutDestroyRPC(t *testing.T) {
	console := t.TempDir() + "/builder.console"
	if err := os.WriteFile(console, []byte("guest-init: build failed\nreboot: System halted\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	killed := make(chan struct{}, 1)
	go watchBuilderGuestHalt(console, done, time.Millisecond, func() { killed <- struct{}{} })
	select {
	case <-killed:
	case <-time.After(time.Second):
		t.Fatal("halted builder was not reaped")
	}
	close(done)
}

func runningBuildProcess(t *testing.T) (*JailerVMM, string, *instanceRecord) {
	t.Helper()
	v := NewJailerVMM(t.TempDir(), time.Second)
	id := "build-interrupt-test"
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	rec := &instanceRecord{cmd: cmd, isBuilder: true, done: make(chan struct{})}
	v.proc[id], v.recs[id] = cmd, rec
	go func() {
		_ = cmd.Wait()
		v.mu.Lock()
		rec.exitCode = cmd.ProcessState.ExitCode()
		rec.exited = true
		v.mu.Unlock()
		close(rec.done)
	}()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-rec.done })
	return v, id, rec
}

func TestBuilderStopAfterDestroyRemovedLiveEntry(t *testing.T) {
	v, id, rec := runningBuildProcess(t)
	m := newTestManager(&fakeRunner{}, v)
	// This is the state while Destroy owns export: live is gone but the
	// builder registration and child record must remain reachable for stop.
	m.exportDirs[id] = t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	killed, _, err := m.SignalAndKill(ctx, id, syscall.SIGKILL, 0)
	if err != nil || !killed {
		t.Fatalf("interrupt: killed=%v err=%v", killed, err)
	}
	select {
	case <-rec.done:
	default:
		t.Fatal("builder still running")
	}
	v.mu.Lock()
	retained := v.recs[id] == rec
	v.mu.Unlock()
	if !retained || m.ExportDirFor(id) == "" {
		t.Fatal("stop stole teardown ownership")
	}
	if _, err := v.DestroyWithExport(ctx, Lease{Instance: id}, ""); err != nil {
		t.Fatal(err)
	}
	v.mu.Lock()
	_, exists := v.recs[id]
	v.mu.Unlock()
	if exists {
		t.Fatal("destroy leaked record")
	}
}

func TestManagerBuilderDestroyWaitDoesNotBlockInterrupt(t *testing.T) {
	v, id, rec := runningBuildProcess(t)
	v.destroyWait = 10 * time.Second
	m := newTestManager(&fakeRunner{}, v)
	lease, err := m.alloc.Acquire(id)
	if err != nil {
		t.Fatal(err)
	}
	lease.Networkless = true
	m.live[id] = &Instance{Lease: lease, ExecutionOnly: true}
	m.exportDirs[id] = t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	destroyResult := make(chan error, 1)
	go func() { _, err := m.DestroyWithExport(ctx, id, ""); destroyResult <- err }()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		waiting := m.live[id] == nil && m.teardowns[id] != nil && m.exportDirs[id] != ""
		m.mu.Unlock()
		if waiting {
			break
		}
		select {
		case err := <-destroyResult:
			t.Fatalf("builder destroy returned before interruption: %v", err)
		case <-ctx.Done():
			t.Fatal("builder destroy did not enter its export wait")
		case <-ticker.C:
		}
	}
	select {
	case <-rec.done:
		t.Fatal("builder exited before interruption")
	default:
	}
	if killed, _, err := m.SignalAndKill(ctx, id, syscall.SIGKILL, 0); err != nil || !killed {
		t.Fatalf("builder interruption joined its destroy wait: killed=%v err=%v", killed, err)
	}
	select {
	case err := <-destroyResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("interrupted builder destroy did not complete")
	}
	v.mu.Lock()
	_, retained := v.recs[id]
	v.mu.Unlock()
	if retained || m.ExportDirFor(id) != "" || m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("builder destroy leaked its record, registration or lease")
	}
}

func TestDestroyCancelledContextKillsChildAndCleansUp(t *testing.T) {
	v, id, rec := runningBuildProcess(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	finished := make(chan error, 1)
	go func() { _, err := v.DestroyWithExport(ctx, Lease{Instance: id}, ""); finished <- err }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("destroy ignored cancellation")
	}
	select {
	case <-rec.done:
	default:
		t.Fatal("child survived cancellation")
	}
	v.mu.Lock()
	_, exists := v.recs[id]
	v.mu.Unlock()
	if exists {
		t.Fatal("process record leaked")
	}
}

func TestAppDestroyDoesNotWaitForBuilderTimeout(t *testing.T) {
	v, id, rec := runningBuildProcess(t)
	rec.isBuilder = false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := v.DestroyWithExport(ctx, Lease{Instance: id}, ""); finished <- err }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("app destruction waited for natural exit instead of killing the VM")
	}
	select {
	case <-rec.done:
	default:
		t.Fatal("app process survived destroy")
	}
	v.mu.Lock()
	_, exists := v.recs[id]
	v.mu.Unlock()
	if exists {
		t.Fatal("app process record survived destroy")
	}
}
