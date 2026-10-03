//go:build linux

// adr: 521 — environment intent and runtime ownership contracts.
package fcvm

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestNativePIDFDConfirmsTaskExit(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	handle, err := openNativeProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := handle.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("live process exit wait: %v", err)
	}
	if err := handle.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := handle.Wait(ctx); err != nil {
		t.Fatalf("killed process exit wait: %v", err)
	}
}
