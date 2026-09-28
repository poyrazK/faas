// adr: 341
// Table-driven coverage for the wake-error classifier (issue #1059 /
// ADR-127). Each row exercises one classification path so the closed
// reason vocabulary is locked: a future refactor that drops a branch
// trips the corresponding table row at PR review time. The shape
// mirrors the closed-enum tests at pkg/wire/metrics_test.go (which
// pin the label-value sets for the underlying metrics).
//
// The naming convention `ClassifyWakeError_<area>` keeps the test
// list greppable for "reason = X" when triaging an incident — the
// runbook docs/runbooks/FaasColdBootRatioHigh.md §Check cross-
// references this file by row name.

package fcvm

import (
	"errors"
	"fmt"
	"testing"
)

// TestClassifyWakeError_Table drives the closed reason vocabulary.
// One row per (input kind → expected reason) tuple. Sentinel matching
// (errors.Is) is exercised by wrap chains; substring matching is
// exercised by ENOSPC; the snapshot-stale branch is exercised by a
// Snapshot whose FCVersion differs from the host's. The fallback
// (snapshot_restore_err) is exercised for every input that doesn't
// hit a more specific branch.
func TestClassifyWakeError_Table(t *testing.T) {
	const hostFCVersion = "1.10.0"
	type row struct {
		name string
		// err is the wrapped error the classifier receives. A
		// nil err exercises the defensive-default branch
		// (snapshot_restore_err).
		err error
		// ctx.Snapshot + ctx.FCVersion drive the snapshot-stale
		// disambiguation. nil snapshot covers the
		// pure-cold-boot path.
		ctx        WakeContext
		wantReason string
	}
	rows := []row{
		// --- typed sentinel matching (errors.Is) ---
		{
			name:       "direct_disk_full",
			err:        ErrDiskFull,
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonDiskFull,
		},
		{
			name:       "wrapped_disk_full",
			err:        fmt.Errorf("vmm: stage read-only: %w", ErrDiskFull),
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonDiskFull,
		},
		{
			name:       "wrapped_jailer_fail",
			err:        fmt.Errorf("vmm: start jailer: %w", ErrJailerFail),
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonJailerFail,
		},
		{
			name:       "wrapped_netns_fail",
			err:        fmt.Errorf("wake i-abc: network setup: %w", ErrNetnsFail),
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonNetnsFail,
		},
		{
			name:       "wrapped_cgroup_fail",
			err:        fmt.Errorf("cgroup: write memory.max: %w", ErrCgroupFail),
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonCgroupFail,
		},
		{
			name:       "wrapped_vsock_fail",
			err:        fmt.Errorf("resume hook: %w", ErrVSockFail),
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonVSockFail,
		},
		{
			name:       "wrapped_after_restore_failed",
			err:        fmt.Errorf("vmm: resume hook: %w", ErrAfterRestoreHook),
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonAfterRestoreFailed,
		},
		{
			name:       "after_restore_text_without_sentinel",
			err:        errors.New("application after_restore failed"),
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonSnapshotRestoreErr,
		},
		// --- substring fallback (ENOSPC, no sentinel) ---
		{
			name:       "raw_ENOSPC",
			err:        errors.New("/dev/fcvg/lv-fc: write: ENOSPC"),
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonDiskFull,
		},
		{
			name:       "raw_no_space_left",
			err:        errors.New("failed to write: no space left on device"),
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonDiskFull,
		},
		{
			// ENOSPC substring also fires when wrapped — keeps
			// the legacy stage-function wraps classified correctly
			// before they grow typed sentinels (ADR-127 §3.2).
			name: "wrapped_ENOSPC",
			err: fmt.Errorf("vmm: stage read-only as: %w",
				errors.New("linkat /dev/fcvg/lv-fc: ENOSPC")),
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonDiskFull,
		},
		// --- snapshot-stale disambiguation ---
		{
			name: "snapshot_fc_version_mismatch",
			err:  fmt.Errorf("vmm: /snapshot/load: bad magic"),
			ctx: WakeContext{
				Snapshot: &Snapshot{
					FCVersion: "1.9.0", // older than host's 1.10.0
				},
				FCVersion: hostFCVersion,
			},
			wantReason: WakeReasonSnapshotStale,
		},
		{
			name: "snapshot_fc_version_match",
			err:  fmt.Errorf("vmm: /snapshot/load: bad magic"),
			ctx: WakeContext{
				Snapshot: &Snapshot{
					FCVersion: hostFCVersion,
				},
				FCVersion: hostFCVersion,
			},
			wantReason: WakeReasonSnapshotRestoreErr,
		},
		{
			name: "snapshot_nil",
			err:  fmt.Errorf("cold-boot: kernel not found"),
			ctx: WakeContext{
				FCVersion: hostFCVersion,
			},
			wantReason: WakeReasonSnapshotRestoreErr,
		},
		// --- defensive defaults ---
		{
			name:       "nil_err_default",
			err:        nil,
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonSnapshotRestoreErr,
		},
		{
			name:       "bare_bare_restore_failure",
			err:        fmt.Errorf("vmm: /snapshot/load: ack-nack"),
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonSnapshotRestoreErr,
		},
		{
			name:       "bare_cold_boot_kernel",
			err:        fmt.Errorf("vmm: BootColdBoot: netlink: device not found"),
			ctx:        WakeContext{FCVersion: hostFCVersion},
			wantReason: WakeReasonSnapshotRestoreErr,
		},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			got := ClassifyWakeError(r.err, r.ctx)
			if got != r.wantReason {
				t.Errorf("ClassifyWakeError() = %q, want %q", got, r.wantReason)
			}
		})
	}
}

// TestClassifyWakeError_ClosedVocabulary asserts the classifier
// returns EXACTLY one of the 9 closed values. A future refactor
// that adds a 10th reason outside the closed vocabulary breaks this
// test and forces the reviewer to extend the pre-instantiation loop
// in pkg/wire/metrics.go. This is the second guard against label
// drift (the first is the closed enum at metrics.go's
// wakeFailureReasons array; this is the classifier-side mirror).
func TestClassifyWakeError_ClosedVocabulary(t *testing.T) {
	allowed := map[string]bool{
		WakeReasonSnapshotStale:      true,
		WakeReasonDiskFull:           true,
		WakeReasonJailerFail:         true,
		WakeReasonNetnsFail:          true,
		WakeReasonCgroupFail:         true,
		WakeReasonVSockFail:          true,
		WakeReasonAfterRestoreFailed: true,
		WakeReasonSnapshotRestoreErr: true,
		WakeReasonMemBackendErr:      true,
	}
	errs := []error{
		errors.New("anything"),
		nil,
		errors.New("ENOSPC"),
		fmt.Errorf("wrap: %w", ErrDiskFull),
		fmt.Errorf("wrap: %w", ErrAfterRestoreHook),
	}
	for _, err := range errs {
		got := ClassifyWakeError(err, WakeContext{FCVersion: "1.10.0"})
		if !allowed[got] {
			t.Errorf("ClassifyWakeError(%v) = %q, not in closed vocabulary", err, got)
		}
	}
}

// TestClassifyWakeError_NeverEmpty exercises the "never returns an
// empty string" contract from ADR-127 §3.1. The classifier must
// always have a reason label so the Prometheus counter never sees
// an empty `reason=""` series (which would fragment the TSDB series
// into one per box and break the §12 alert FireThreshold rule).
func TestClassifyWakeError_NeverEmpty(t *testing.T) {
	cases := []struct {
		err error
		ctx WakeContext
	}{
		{errors.New(""), WakeContext{}},
		{errors.New("any error"), WakeContext{FCVersion: "x"}},
		{errors.New("ENOSPC"), WakeContext{}},
	}
	for i, tc := range cases {
		if got := ClassifyWakeError(tc.err, tc.ctx); got == "" {
			t.Errorf("case %d: ClassifyWakeError returned empty string", i)
		}
	}
}
