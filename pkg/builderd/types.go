// Package builderd — driver-agnostic types shared between the metal
// implementation (vm_metal.go, //go:build metal) and the orchestrator
// (builderd.go, no tag). Splitting these out avoids the orchestrator
// referring to types that aren't visible in non-metal builds.

package builderd

import (
	"context"
	"time"
)

// VM is the small builder-VM surface the orchestrator consumes.
// The metal implementation lives in vm_metal.go; the non-metal stub in vm_stub.go.
type VM interface {
	Spawn(ctx context.Context, req VMRequest) (BuildHandle, error)
	WaitForCompletion(ctx context.Context, h BuildHandle) (BuildOutcome, error)
	// Cancel interrupts the VM without taking over export and cleanup from
	// WaitForCompletion. Notifications trigger it promptly; the orchestrator
	// also polls the durable claim while waiting and retries failed stops.
	Cancel(ctx context.Context, buildID string) error
}

// VMRequest is the input to a builder VM spawn. The orchestrator at
// builderd.go::ProcessOne populates this from a queued Build row.
type VMRequest struct {
	BuildID        string
	TenantID       string
	DeploymentID   string
	SourcePath     string // tarball or dockerfile source on disk
	SourceRoot     string // repository-relative build root inside the archive; empty = archive root
	Framework      Framework
	Runtime        string // app runtime id (node22, python312, go124-alpine, ...)
	RuntimeBaseRef string // resolved OCI ref used by Railpack for this build
	// DependencyCacheKey is a platform-derived, tenant-scoped digest. Empty
	// keeps the builder fully ephemeral; developer sessions set it so matching
	// BuildKit layers can cross otherwise-isolated builder VM lifetimes.
	DependencyCacheKey string
	LogPath            string // build log appended by the VM
	RAMMB              int    // from the plan's BuildVMRAMMB (spec §1, §4.5)
	TimeoutSec         int    // wall-clock build budget (0 ⇒ pkg/api/limits.go default)
	// Plan is the owning account's plan tier. vmmd validates it on
	// CreateColdBoot (issue #301 / ADR-043) and routes the builder VM
	// into the per-plan cgroup sub-slice. Empty = legacy path that
	// vmmd now rejects; the orchestrator always populates it.
	Plan string
}

// VMResult is the legacy single-step result — kept for backwards compat
// with the cache-hit path (no VM spawn). New code uses BuildOutcome.
type VMResult struct {
	LayerPath string
	Bytes     int64
	ExitCode  int
	LogBytes  int64
}

// BuildHandle is what Spawn returns; it's the caller's handle into a running
// (or recently-running) builder VM. Always pair with WaitForCompletion.
type BuildHandle struct {
	Instance                string    // "build-<BuildID>" — the vmmd instance name
	HostDrive1              string    // host-side 28 GiB tmp file (cleaned up by WaitForCompletion)
	ExportDir               string    // host dir vmmd copies build-done.json + /build/out/* into
	BuildID                 string    // echoes req.BuildID
	TimeoutSec              int       // wall-clock budget the caller selected
	StartedAt               time.Time // when Spawn returned; for log lines / metrics
	DependencyCacheKey      string    // cache generation to publish after success
	DependencyCacheRestored bool      // a prior BuildKit cache was staged into drive1
}

// BuildOutcome is what WaitForCompletion returns. The orchestrator at
// builderd.go::ProcessOne turns this into a deployable RootfsPath on success
// or a marked-failed build row on failure. Named BuildOutcome to avoid
// clashing with the orchestrator's BuildResult (whole-ProcessOne return).
type BuildOutcome struct {
	BuildID      string // echoes handle.BuildID
	InstanceID   string // echoes handle.Instance
	ExportDir    string // host dir the artifacts live in (caller may rm)
	OCIImage     string // absolute path to the produced OCI tarball
	LogTailBytes int64  // bytes guest-init wrote to build-done.json's `log_tail`
	ExitCode     int    // the in-VM build's exit code (0 = success)
	FailureClass string // mirrors builderd's FailureClass table; "" on success
	// FailureCode is the RFC 7807 stable code guest-init stamped on
	// BuildDone.FailureCode (app_arch_mismatch / dep_install_failed).
	// Error-explanations cluster (spec §6.4 amendment 1): empty on
	// legacy builds; populated when guest-init could identify the
	// root cause at exit time. The orchestrator's ProcessOne
	// stamps this on the deployment row via SetDeploymentFailedEx
	// so the customer sees hint/why/fix from pkg/whycopy.
	FailureCode string
	// FailurePkg is the package manager discriminator for
	// dep_install_failed (npm / pip / go / cargo). Empty for
	// every other code; the orchestrator templates this into
	// the whycopy Observed renderer's Fix field.
	FailurePkg string
	// Toolchain versions are reported by guest-init from the binaries that
	// actually ran inside the builder VM.
	BuildkitVer string
	RailpackVer string
	// Dependency-cache publication is best-effort and never changes the build
	// result. The orchestrator surfaces a warning when the next sync will be cold.
	DependencyCacheStored     bool
	DependencyCacheStoreError string
}
