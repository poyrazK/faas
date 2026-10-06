// Wake-error classifier — issue #1059 / ADR-127.
//
// Maps every wake-failure site error to the closed reason vocabulary
// surfaced on the *_wake_failure_total{box, reason} Prometheus counter.
// The vocabulary is:
//
//   snapshot_stale, disk_full, jailer_fail, netns_fail, cgroup_fail,
//   vsock_fail, after_restore_failed, snapshot_restore_err, mem_backend_err
//
// Every wake-failure hook site calls ClassifyWakeError ONCE at the
// error boundary (pkg/fcvm/manager.go::Wake — bringUp restore-fallback,
// bringUp cold-boot terminal, setupNetwork; pkg/vmmdgrpc/server.go —
// CreateFromSnapshot / CreateColdBoot gRPC paths; pkg/fcvm/cgroup.go
// — writePlanCgroup / writeWorkloadCgroup) instead of scattering
// classification logic at each call site. Sub-classification uses
// errors.Is against the sentinel errors declared here and inspects
// the wrapped error string for ENOSPC (the only consumer today is
// stageReadOnlyAs I/O at pkg/fcvm/vmm.go:649-652).
//
// The closed-set rule is enforced by ADR-127 §3.1: the reason string
// is hardcoded at every call site (no bare-string passthrough from
// the wrapped error), and ClassifyWakeError returns one of the 9
// closed values — never an empty string or an arbitrary error
// fragment. The "call site hardcodes the literal" rule is the same
// posture ADR-074 takes for the warmSnapshotErrors{reason} counter
// (pkg/wire/metrics.go:1312-1317).

package fcvm

import (
	"errors"
	"strings"
)

// Closed-set reason vocabulary for the *_wake_failure_total counter
// (issue #1059 / ADR-127). Every value here MUST appear in the
// pre-instantiation loop in pkg/wire/metrics.go::NewOpsMetrics
// (Commit 2 of this PR), and every wake-failure hook site MUST
// pass one of these literals to OpsMetrics.WakeFailure. Adding a
// new value requires:
//  1. extend the closed-set slice in pkg/wire/metrics.go's
//     wakeFailureReasons array (Commit 2),
//  2. add a sentinel error here if the value maps to a typed
//     error (not strictly required — substring classification
//     also works),
//  3. extend ClassifyWakeError's switch with the new branch,
//  4. extend pkg/fcvm/wake_classify_test.go's table-driven
//     coverage,
//  5. update the §12 dashboard panel legend.
const (
	WakeReasonSnapshotStale      = "snapshot_stale"
	WakeReasonDiskFull           = "disk_full"
	WakeReasonJailerFail         = "jailer_fail"
	WakeReasonNetnsFail          = "netns_fail"
	WakeReasonCgroupFail         = "cgroup_fail"
	WakeReasonVSockFail          = "vsock_fail"
	WakeReasonAfterRestoreFailed = "after_restore_failed"
	WakeReasonSnapshotRestoreErr = "snapshot_restore_err"
	WakeReasonMemBackendErr      = "mem_backend_err"
)

// Sentinel errors for typed classification (issue #1059 / ADR-127).
// These wrap the lower-level errors at the boundary site so the
// classifier can use errors.Is without inspecting the wrapped
// message. The fcvm package wraps lower-level I/O at the boundary
// (pkg/fcvm/vmm.go stage functions return wrapped errors), so a
// dedicated sentinel lets ClassifyWakeError stay a one-liner per
// branch instead of a string-match cascade.
//
// The senders are NOT required to use these — they exist so that
// when an upstream package wants to expose a typed error path, it
// can. Today only the wake-failure sites that already wrap an error
// with `fmt.Errorf("vmm: ...: %w", lowerErr)` get classified; sites
// that wrap a bare fmt.Errorf without a sentinel fall through to
// the default snapshot_restore_err branch.
var (
	// ErrDiskFull — staged when an ENOSPC is observed on the lv-fc
	// backing volume during stageReadOnlyAs / os.Link / os.WriteFile
	// (the I/O surface at pkg/fcvm/vmm.go:649-652, :2240+). Distinct
	// from a generic I/O error because the §12 alert fires on this
	// specific reason — operators triage disk_full differently from
	// snapshot_restore_err (lvm expansion vs. snapshot regeneration).
	ErrDiskFull = errors.New("fcvm: disk full")

	// ErrJailerFail — the jailer process did not start, did not bind
	// TUN, did not provision KVM, or exited before /dev/kvm was
	// attached (pkg/fcvm/vmm.go:2078-2158 startJailer,
	// pkg/fcvm/vmm.go:2416-2470 bindTunDeviceInJailer). Distinct
	// from vsock_fail because the jailer side is the host-side
	// process boundary (no vsock dial yet), so triage is different
	// (firecracker binary path / seccomp / cgroup parent / KVM
	// device).
	ErrJailerFail = errors.New("fcvm: jailer fail")

	// ErrNetnsFail — netns create / ip-link add / ip-addr add /
	// ip-route add / nft reset+add failed (pkg/netns/config.go:167-212
	// 12-argv sequence; pkg/fcvm/manager.go:3527-3574 setupNetwork).
	// The wrap "wake %s: network setup: %w" already names the surface
	// in manager.go:2227, but ClassifyWakeError also gets called from
	// the cold-boot fallback path where the wrap may be a deeper
	// error chain.
	ErrNetnsFail = errors.New("fcvm: netns fail")

	// ErrCgroupFail — cgroup v2 scope write failed
	// (pkg/fcvm/cgroup.go:99-113 writeMemoryMaxAt,
	// pkg/fcvm/cgroup.go:161-180 writeCPUMaxTo,
	// pkg/fcvm/cgroup.go:237-268 writeWorkloadCgroup). The cgroup
	// fence today is warn-and-continue (manager.go:2377, 2405, 2410),
	// so this reason fires for a non-fatal degradation — still
	// counted because a sustained cgroup_fail rate means the host's
	// cgroup v2 mount is misconfigured (CLAUDE.md invariant §11
	// requires cgroups v2 with memory.max = plan + 8 MB).
	ErrCgroupFail = errors.New("fcvm: cgroup fail")

	// ErrVSockFail — AF_UNIX dial to the guest's vsock UDS proxy
	// failed, or the guest's "CONNECT 1024\n" handshake timed out,
	// or the entropy read after handshake failed
	// (pkg/fcvm/vmm.go:822-910 TriggerResumeHook). Distinct from
	// jailer_fail because the host-side jailer is up — the failure
	// is on the guest-init side after restore (per CLAUDE.md §6
	// security rule "Post-restore resume hook must re-seed entropy
	// + step clock before readiness").
	ErrVSockFail = errors.New("fcvm: vsock fail")

	// ErrAfterRestoreHook is returned only when guest-init ACKs that the
	// configured application callback failed. Other resume NACKs remain
	// transport/platform failures or generic snapshot restore errors.
	ErrAfterRestoreHook = errors.New("application after_restore failed")
)

// WakeContext bundles the inputs ClassifyWakeError needs from the
// failure site. Building a single struct (instead of passing four
// positional args) keeps the call sites readable — every wake-failure
// site builds one WakeContext from its local variables and passes it
// to the classifier.
type WakeContext struct {
	// Snapshot is the snapshot the wake was attempting to restore
	// (nil on a pure cold-boot path). Used to disambiguate
	// snapshot_stale from snapshot_restore_err: a FCVersion mismatch
	// is snapshot_stale, a /snapshot/load failure on a fresh
	// snapshot is snapshot_restore_err.
	Snapshot *Snapshot
	// FCVersion is the host's currently-detected Firecracker
	// version (empty string if detectFC failed — see
	// cmd/vmmd/main.go:512-515 detectFC). Used in the same
	// snapshot_stale-vs-snapshot_restore_err disambiguation as
	// Snapshot.
	FCVersion string
}

// ClassifyWakeError maps a wake-failure error + the surrounding
// context to one of the 9 closed reason values. The function is the
// single source of truth for the reason vocabulary — every wake-
// failure hook site calls it. The classifier prefers typed sentinel
// matching (errors.Is) over substring matching; substring matching
// is the fallback for legacy error chains that don't carry the
// sentinel.
//
// Contract (issue #1059 / ADR-127 §3):
//   - returns exactly one of the 9 WakeReason* constants,
//   - never returns an empty string,
//   - never returns the wrapped error's message verbatim (the
//     Prometheus label is a closed enum, not a free-form string),
//   - is safe to call on a nil err (returns snapshot_restore_err
//     as a defensive default — the hook site must still skip the
//     .Inc() if err is nil).
func ClassifyWakeError(err error, ctx WakeContext) string {
	if err == nil {
		// Defensive default — hook sites must guard on err != nil
		// before calling. Returning snapshot_restore_err (the most
		// common wake-failure reason) keeps the metric labelled
		// even on the rare path where a call site forgets the
		// guard; surfacing the bug is better than a mis-classified
		// "0" or empty string.
		return WakeReasonSnapshotRestoreErr
	}
	// Typed sentinel matching first — errors.Is walks the wrap
	// chain so the lower-level I/O errors at pkg/fcvm/vmm.go's
	// stage functions get classified correctly without
	// substring-matching the entire message.
	switch {
	case errors.Is(err, ErrDiskFull):
		return WakeReasonDiskFull
	case errors.Is(err, ErrJailerFail):
		return WakeReasonJailerFail
	case errors.Is(err, ErrNetnsFail):
		return WakeReasonNetnsFail
	case errors.Is(err, ErrCgroupFail):
		return WakeReasonCgroupFail
	case errors.Is(err, ErrAfterRestoreHook):
		return WakeReasonAfterRestoreFailed
	case errors.Is(err, ErrVSockFail):
		return WakeReasonVSockFail
	case errors.Is(err, ErrSnapshotBackingChanged), errors.Is(err, ErrSnapshotBackingUnverified):
		// ADR-510: the capture no longer matches this host's kernel/base.
		return WakeReasonSnapshotStale
	}
	// Substring fallback — the stage functions at pkg/fcvm/vmm.go
	// wrap lower-level I/O without sentinel-bearing errors today
	// (they pre-date ADR-127). ENOSPC substring match is the
	// canonical signal for a lv-fc volume exhaustion — no other
	// host-side error produces this string in the vmm layer.
	if strings.Contains(err.Error(), "ENOSPC") ||
		strings.Contains(err.Error(), "no space left on device") {
		return WakeReasonDiskFull
	}
	// Snapshot-stale disambiguation: if a Snapshot was supplied
	// and its FCVersion differs from the host's, the failure was a
	// version mismatch (snapshot_stale), not a generic restore
	// failure. This branch fires when the wake attempted
	// PlanWake == WakeRestore AND the snapshot was Usable() at
	// admission but failed under load (rare — Usable() also
	// checks FCVersion, so a mismatch takes the cold-boot path
	// without invoking Restore). Defensive: covers a future
	// regression where Usable() is bypassed.
	if ctx.Snapshot != nil && ctx.FCVersion != "" &&
		ctx.Snapshot.FCVersion != "" &&
		ctx.Snapshot.FCVersion != ctx.FCVersion {
		return WakeReasonSnapshotStale
	}
	// Default — the catch-all bucket for any restore or cold-boot
	// failure that didn't match a typed sentinel or a substring
	// signature. Operators triage this bucket by reading the
	// wrapped error in the daemon slog (the metric label is
	// intentionally lossy, matching the accountLabelSet / ipLabelSet
	// "operators check daemon slog" precedent at
	// docs/runbooks/FaasApidAuditWriteFailures.md:85-103).
	return WakeReasonSnapshotRestoreErr
}
