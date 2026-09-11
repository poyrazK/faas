// spec: §4.5
package fcvm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitBuilderReadyAcceptsGuestMarker(t *testing.T) {
	console := filepath.Join(t.TempDir(), "builder.console")
	if err := os.WriteFile(console, []byte("guest-init: stage build-ready\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	v := NewJailerVMM(t.TempDir(), time.Second)
	v.recs["build-1"] = &instanceRecord{
		consolePath: console,
		isBuilder:   true,
		done:        make(chan struct{}),
	}

	ready, exitCode, err := v.WaitBuilderReady(context.Background(), "build-1", time.Second)
	if err != nil {
		t.Fatalf("WaitBuilderReady: %v", err)
	}
	if !ready || exitCode != 0 {
		t.Fatalf("result = ready %v exit %d, want ready true exit 0", ready, exitCode)
	}
}

func TestWaitBuilderReadyReturnsFailedBuildExitCode(t *testing.T) {
	console := filepath.Join(t.TempDir(), "builder.console")
	if err := os.WriteFile(console, []byte("guest-init: build failed\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	v := NewJailerVMM(t.TempDir(), time.Second)
	v.recs["build-2"] = &instanceRecord{
		consolePath: console,
		isBuilder:   true,
		exitCode:    17,
		done:        done,
	}

	ready, exitCode, err := v.WaitBuilderReady(context.Background(), "build-2", time.Second)
	if err != nil {
		t.Fatalf("WaitBuilderReady: %v", err)
	}
	if ready || exitCode != 17 {
		t.Fatalf("result = ready %v exit %d, want ready false exit 17", ready, exitCode)
	}
}
