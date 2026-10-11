//go:build linux

package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
)

// activeRestoreReseedBarrier is the barrier the resume hook drives. nil means
// the barrier never started: the resume hook then skips the userspace step
// and omits the reseed capability, so vmmd refuses the restore.
var activeRestoreReseedBarrier atomic.Pointer[restoreReseedBarrier]

// startRestoreReseedServer materializes the preloads and binds the reseed
// socket (ADR-680). It must run after pivot_root, where the workload sees the
// same /run/guest-init, and before the workload starts, because env stamping
// only injects the preloads once they exist.
func startRestoreReseedServer(log *slog.Logger, uid int) error {
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(filepath.Dir(RestoreReseedSocketPath), 0o755); err != nil {
		return fmt.Errorf("restore reseed mkdir: %w", err)
	}
	if err := writeRestoreReseedAssets(RestoreReseedAssetDir); err != nil {
		return fmt.Errorf("restore reseed assets: %w", err)
	}
	if err := os.Remove(RestoreReseedSocketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("restore reseed unlink: %w", err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: RestoreReseedSocketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("restore reseed listen: %w", err)
	}
	if err := os.Chown(RestoreReseedSocketPath, 0, uid); err != nil {
		_ = ln.Close()
		return fmt.Errorf("restore reseed socket ownership: %w", err)
	}
	if err := os.Chmod(RestoreReseedSocketPath, RestoreReseedSocketMode); err != nil {
		_ = ln.Close()
		return fmt.Errorf("restore reseed socket mode: %w", err)
	}
	barrier := newRestoreReseedBarrier(log)
	activeRestoreReseedBarrier.Store(barrier)
	restoreReseedEnabled.Store(true)
	go barrier.serve(ln)
	return nil
}

// reseedRestoredWorkloads runs the barrier if it started. The resume hook
// calls it after the kernel reseed and before acknowledging vmmd.
func reseedRestoredWorkloads() error {
	b := activeRestoreReseedBarrier.Load()
	if b == nil {
		return nil
	}
	return b.Reseed(RestoreReseedTimeout)
}

// restoreReseedContractHolds reports whether this guest may advertise the
// ADR-680 capability after a successful resume. An app guest holds it only
// when its barrier started (otherwise its Node and Python processes carry
// no preload). A warm builder holds it trivially: it runs no workload
// process across the snapshot, and each build's processes start after the
// restore with fresh state.
func restoreReseedContractHolds() bool {
	return activeRestoreReseedBarrier.Load() != nil || warmBuilderEnabled.Load()
}
