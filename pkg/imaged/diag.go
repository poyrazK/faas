package imaged

// imaged-local diagnostics: fc volume usage probe + Firecracker version detection.
//
// These are thin host probes, not VM lifecycle. They do not import
// pkg/fcvm because imaged must not touch firecracker/jailer (CLAUDE.md
// ownership: vmmd is the ONLY root component that does). The probes
// are the only outward-facing firecracker-adjacent concerns imaged has:
// the F1 GC pressure check (lv-fc usage) and the F2 startup sweep
// (mark every snapshot whose FC version doesn't match the on-disk
// binary as stale, ADR-005).
//
// Both helpers are deliberately stateless and run-on-call so the
// daemon loop can call them with their own ctx + tick cadence.

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"

	"github.com/onebox-faas/faas/pkg/hostsize"
)

// DefaultFcVolumeUsedPct returns a closure that reports how full the
// filesystem holding root (the spec §8 lv-fc volume, FAAS_STORAGE_ROOT,
// /srv/fc by default) is, using statfs.
//
// It used to read `lvs -o data_percent lv-fc`. data_percent is only
// populated for thin pools and snapshots, and fleets on cloud disks have
// no LVM at all, so the probe always failed and the F1 GC tick never
// entered budget-pressure eviction however full the volume became.
//
// On failure the closure returns math.NaN() and the error. The GC tick
// treats NaN as "no data" and stays in the safe-noop mode (per-app sweep
// only, no pressure eviction) rather than acting on a guessed value.
func DefaultFcVolumeUsedPct(root string) func(ctx context.Context) (float64, error) {
	return func(context.Context) (float64, error) {
		pct, err := hostsize.FilesystemUsedPct(root)
		if err != nil {
			return math.NaN(), fmt.Errorf("imaged: fc volume usage: %w", err)
		}
		return pct, nil
	}
}

// DetectFirecrackerVersion runs `firecracker --version` and returns the
// version string (e.g. "1.7.0"). Snapshots are pinned to this value
// (ADR-005); on a change every snapshot goes stale and apps re-snapshot
// via cold boot. vmmd is the OWNER of the firecracker binary on disk,
// but the version string itself is a firecracker-agnostic identifier
// the snapshot table cares about — safe for imaged to read.
//
// On failure (binary missing, exec error) the returned error is non-nil
// and the F2 startup sweep fails open: no rows are marked stale, the
// loop logs Warn, and traffic continues against the existing freshness
// window. The next operator-driven imaged restart will retry.
func DetectFirecrackerVersion(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "firecracker", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("imaged: firecracker --version: %w", err)
	}
	// First line looks like "Firecracker v1.7.0".
	line := out
	if i := bytes.IndexByte(out, '\n'); i >= 0 {
		line = out[:i]
	}
	fields := bytes.Fields(line)
	if len(fields) == 0 {
		return "", fmt.Errorf("imaged: unexpected version output %q", out)
	}
	return string(bytes.TrimPrefix(fields[len(fields)-1], []byte("v"))), nil
}
