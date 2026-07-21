package imaged

// imaged-local diagnostics: lvs probe + Firecracker version detection.
//
// These are *thin* exec wrappers, not VM lifecycle. They do not import
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
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// LvFcName is the canonical name of the lv-fc logical volume the GC
// pressure probe reads. Kept here (not in pkg/fcvm) so imaged has no
// depguard-flagged import surface to fcvm.
const LvFcName = "lv-fc"

// DefaultLvFcUsedPct returns a closure that runs
// `lvs --noheadings -o data_percent <lvName>` and parses the trailing
// percent.
//
// On failure (lvs not on PATH, lv missing, parse error) the closure
// returns math.NaN() and a non-nil error. NaN is the load-bearing
// choice: the F1 GC tick treats NaN as "no data" and stays in the
// safe-noop mode (per-app sweep only, no pressure eviction) rather
// than reading a misleading "0% used" that would leave the fleet
// snapshot budget unchecked.
//
// The 1 s ctx budget matches the loop-tick cadence; lv-fc stats are cheap.
func DefaultLvFcUsedPct(lvName string) func(ctx context.Context) (float64, error) {
	return func(ctx context.Context) (float64, error) {
		if lvName == "" {
			return math.NaN(), errors.New("imaged: empty lv name")
		}
		cctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		out, err := exec.CommandContext(cctx, "lvs", "--noheadings", "-o", "data_percent", lvName).Output()
		if err != nil {
			return math.NaN(), err
		}
		// Output looks like "  37.42\n" — trim, drop trailing %, parse.
		s := strings.TrimSpace(string(out))
		s = strings.TrimSuffix(s, "%")
		if s == "" {
			return math.NaN(), nil
		}
		pct, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return math.NaN(), err
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
