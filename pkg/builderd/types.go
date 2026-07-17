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
}

// VMRequest is the input to a builder VM spawn. The orchestrator at
// builderd.go::ProcessOne populates this from a queued Build row.
type VMRequest struct {
	BuildID      string
	TenantID     string
	DeploymentID string
	SourcePath   string // tarball or dockerfile source on disk
	Framework    Framework
	LogPath      string // build log appended by the VM
	RAMMB        int    // from the plan's BuildVMRAMMB (spec §1, §4.5)
	TimeoutSec   int    // wall-clock build budget (0 ⇒ pkg/api/limits.go default)
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
	Instance   string    // "build-<BuildID>" — the vmmd instance name
	HostDrive1 string    // host-side 8 GiB tmp file (cleaned up by WaitForCompletion)
	ExportDir  string    // host dir vmmd copies build-done.json + /build/out/* into
	BuildID    string    // echoes req.BuildID
	TimeoutSec int       // wall-clock budget the caller selected
	StartedAt  time.Time // when Spawn returned; for log lines / metrics
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
}
