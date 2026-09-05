package fcvm

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

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
