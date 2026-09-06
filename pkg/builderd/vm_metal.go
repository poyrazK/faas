//go:build metal && linux

// Package builderd (metal) — ephemeral builder microVM spawn.
//
// builderd is the ONLY process that runs Railpack/buildkit. The build
// happens inside an ephemeral Firecracker microVM booted from
// /srv/fc/base/runner-builder-<arch>.ext4 (drive0, shared read-only — the image
// built from images/builder-base.Dockerfile). cgroup: faas-cp.slice
// (spec §13), not the tenant slice — that's what makes the M6 §14 OOM-bomb
// acceptance gate work: an OOM in a builder kills the builder, never a
// tenant.
//
// Spec ref §4.5, ADR-003. Caveat (CLAUDE.md): this code is metal-only; the
// arm64 Lima loop exercises the arch-agnostic boot path, the EX44 remains
// the source of truth for §14's production acceptance.

package builderd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"google.golang.org/grpc"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/wire"
)

// VMMDriver is the metal VM driver. It owns a single gRPC connection to
// vmmd's unix socket (the same one schedd uses, ADR-014/015). Builder VMs
// are produced by: CreateBuildDrive1 → gRPC CreateColdBoot with BuildSpec;
// teardown is via gRPC Destroy (which captures the in-VM exit code and
// copies the produced OCI tarball into ExportDir for builderd to consume).
type VMMDriver struct {
	cli  vmmdpb.VmmdClient
	conn *grpc.ClientConn

	// builderBase is drive0: the read-only shared base that holds
	// buildkit/Railpack/etc. Default is the canonical per-architecture
	// runner-builder path.
	builderBase string

	// driveDir hosts the temporary per-VM drive1 images we create at
	// CreateBuildDrive1 time. Cleanup happens via WaitForCompletion's
	// defer + the startup janitor.
	driveDir string

	// exportDir is the parent of all build artifact exports: vmmd writes
	// <exportDir>/<build_id>/build-done.json and /build/out/* here.
	exportDir string

	// dependencyCacheMu serializes seed-copy and atomic publication. Only
	// developer sessions use it; the platform has at most two builder slots.
	dependencyCacheMu sync.Mutex
}

// NewVMMDriver opens a lazy gRPC connection to vmmd's socket.
//
// Legacy entrypoint kept for source compatibility with cmd/builderd and
// existing tests; production code should call NewVMMDriverContext so the
// caller's context controls the dial.
func NewVMMDriver(socketPath, builderBase, driveDir, exportDir string) (*VMMDriver, error) {
	return NewVMMDriverContext(context.Background(), socketPath, nil, builderBase, driveDir, exportDir)
}

// NewVMMDriverContext opens a lazy gRPC connection to vmmd. tlsCfg is
// required for tcp/dns targets (issue #95); nil tlsCfg is fine for the
// single-box unix default. Wire layer performs the mTLS gating.
func NewVMMDriverContext(ctx context.Context, socketPath string, tlsCfg *tls.Config, builderBase, driveDir, exportDir string) (*VMMDriver, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("builderd: empty vmmd socket path")
	}
	if builderBase == "" {
		builderBase = "/srv/fc/base/runner-builder-" + runtime.GOARCH + ".ext4"
	}
	if driveDir == "" {
		driveDir = "/var/lib/faas/build-drive"
	}
	if exportDir == "" {
		exportDir = "/var/lib/faas/build-out"
	}
	conn, err := wire.DialContext(ctx, socketPath, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("builderd: dial vmmd: %w", err)
	}
	cli := vmmdpb.NewVmmdClient(conn)
	return &VMMDriver{
		cli:         cli,
		conn:        conn,
		builderBase: builderBase,
		driveDir:    driveDir,
		exportDir:   exportDir,
	}, nil
}

// Close shuts the underlying gRPC connection. Safe to call multiple times.
func (d *VMMDriver) Close() error {
	if d == nil {
		return nil
	}
	// grpc.ClientConn has its own Close; the connection is reference-counted.
	// Closing here breaks the last reference and frees the socket dialer.
	return d.conn.Close()
}

// BuildEnvironment binds deployment-cache reuse to the staged builder image,
// its injected boot contract, and the architecture selected by this binary.
func (d *VMMDriver) BuildEnvironment() (BuildEnvironment, error) {
	return readBuildEnvironment(d.builderBase, runtime.GOOS+"/"+runtime.GOARCH)
}

// Spawn materialises the per-VM drive1, cold-boots the VM, and returns a
// BuildHandle the caller can pass to WaitForCompletion. The VM base is
// d.builderBase; drive1 is a throwaway 28 GiB ext4 that carries BuildManifest
// at /etc/faas/build.json; the produced OCI tarball comes back through
// ExportDir during Destroy.
//
// Spawn returns when vmmd has accepted the cold-boot; it does NOT wait for
// the in-VM build to finish. Use WaitForCompletion for that. cmd/builderd's
// orchestrator runs Spawn then WaitForCompletion back-to-back.
func (d *VMMDriver) Spawn(ctx context.Context, req VMRequest) (BuildHandle, error) {
	if d == nil || d.cli == nil {
		return BuildHandle{}, fmt.Errorf("builderd: VMMDriver not wired")
	}
	if req.BuildID == "" {
		return BuildHandle{}, fmt.Errorf("builderd: empty BuildID")
	}

	instance := "build-" + req.BuildID

	// 1. Materialise drive1 with BuildManifest.
	if err := os.MkdirAll(d.driveDir, 0o755); err != nil {
		return BuildHandle{}, fmt.Errorf("builderd: mkdir drive dir: %w", err)
	}
	if err := os.MkdirAll(d.exportDir, 0o755); err != nil {
		return BuildHandle{}, fmt.Errorf("builderd: mkdir export dir: %w", err)
	}
	// Janitor runs best-effort on each Spawn — caller doesn't notice if it
	// can't clean up. (The only thing we ever want gone is a >1h old *.ext4
	// that wasn't WaitForCompletion'd.)
	d.runJanitor()

	drive1Path := filepath.Join(d.driveDir, instance+".ext4")
	hostDrive1, err := os.CreateTemp(d.driveDir, instance+"-*.ext4")
	if err != nil {
		return BuildHandle{}, fmt.Errorf("builderd: mktemp drive1: %w", err)
	}
	hostDrive1.Close()
	if err := os.Rename(hostDrive1.Name(), drive1Path); err != nil {
		os.Remove(hostDrive1.Name())
		return BuildHandle{}, fmt.Errorf("builderd: rename drive1: %w", err)
	}

	timeoutSec := req.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = api.BuildTimeoutSeconds
	}
	workdir, err := buildWorkdir(req.SourceRoot)
	if err != nil {
		os.Remove(drive1Path)
		return BuildHandle{}, fmt.Errorf("builderd: source root: %w", err)
	}
	bManifest := api.BuildManifest{
		SchemaVersion:  1,
		BuildID:        req.BuildID,
		TenantID:       req.TenantID,
		DeploymentID:   req.DeploymentID,
		SourceTarPath:  "/build/src.tar",
		BuildContext:   "/build/src",
		Workdir:        workdir,
		OutDir:         "/build/out",
		Framework:      MapFramework(req.Framework),
		Runtime:        req.Runtime,
		RuntimeBaseRef: req.RuntimeBaseRef,
		TimeoutSec:     timeoutSec,
		LogTailBytes:   64 * 1024,
	}
	cachePath := ""
	if req.DependencyCacheKey != "" {
		var cachePathErr error
		cachePath, cachePathErr = dependencyCachePath(d.driveDir, req.DependencyCacheKey)
		if cachePathErr != nil {
			_ = os.Remove(drive1Path)
			return BuildHandle{}, fmt.Errorf("builderd: dependency cache: %w", cachePathErr)
		}
		bManifest.DependencyCache = true
	}
	var cacheRestored bool
	var driveErr error
	if cachePath == "" {
		cacheRestored, driveErr = createBuildDrive1(ctx, drive1Path, bManifest, req.SourcePath, "")
	} else {
		d.dependencyCacheMu.Lock()
		cacheRestored, driveErr = createBuildDrive1(ctx, drive1Path, bManifest, req.SourcePath, cachePath)
		d.dependencyCacheMu.Unlock()
	}
	if driveErr != nil {
		os.Remove(drive1Path)
		return BuildHandle{}, fmt.Errorf("builderd: create drive1: %w", driveErr)
	}

	// 2. Cold-boot. BuildSpec carries the export dir; vmmd's Destroy will
	//    loopback-mount drive1 and copy out /build/out/* + build-done.json.
	buildExportDir := filepath.Join(d.exportDir, req.BuildID)
	resp, err := d.cli.CreateColdBoot(ctx, &vmmdpb.CreateColdBootRequest{
		Instance: instance,
		App: &vmmdpb.AppSpec{
			// Keep the builder base on the same per-arch key contract as
			// imaged's staged base and vmmd's scan gate. The legacy
			// builder-base.ext4 spelling bypassed the published scan
			// sidecar and made every metal build fail closed.
			BaseKey:    sched.BaseKey("builder"), // ADR-025: storage key → vmmd resolves via StorageBackend
			LayerKey:   drive1Path,               // absolute host path; vmmd treats as direct path (abs path bypass)
			VcpuCount:  api.BuildVMVCPU,
			MemSizeMib: int32(api.BuildVMRAMMB),
		},
		Build: &vmmdpb.BuildSpec{
			ExportDir:  buildExportDir,
			TimeoutSec: int32(timeoutSec),
		},
		// Issue #301 / ADR-043: vmmd validates Plan on every cold
		// boot and routes the VM into the per-plan cgroup slice.
		// The legacy empty value is rejected.
		Plan:      req.Plan,
		AccountId: req.TenantID,
	})
	if err != nil {
		os.Remove(drive1Path)
		return BuildHandle{}, fmt.Errorf("builderd: cold boot: %w", err)
	}
	if resp == nil {
		os.Remove(drive1Path)
		return BuildHandle{}, fmt.Errorf("builderd: nil wake outcome")
	}

	return BuildHandle{
		Instance:                instance,
		HostDrive1:              drive1Path,
		ExportDir:               buildExportDir,
		BuildID:                 req.BuildID,
		TimeoutSec:              timeoutSec,
		StartedAt:               time.Now(),
		DependencyCacheKey:      req.DependencyCacheKey,
		DependencyCacheRestored: cacheRestored,
	}, nil
}

// WaitForCompletion blocks until the build VM exits (capped at
// handle.TimeoutSec + 60s slack for the snapshot_prime handshake) and
// returns the produced BuildOutcome. It always releases the host-side
// drive1 tmp file even if vmmd's Destroy RPC errors.
//
// BuildOutcome covers three things:
//   - ExitCode: the in-VM build's exit code (0=success); 137=OOM,
//     124=timeout, per the failure-class table in builderd.go.
//   - FailureClass: prefers /etc/faas/build-done.json's `failure_class`,
//     falls back to the exit-code table.
//   - OCIImagePath: the host path of the produced OCI tarball, suitable
//     to hand to imaged's snapshot_prime.
func (d *VMMDriver) WaitForCompletion(ctx context.Context, h BuildHandle) (BuildOutcome, error) {
	if d == nil || d.cli == nil {
		return BuildOutcome{}, fmt.Errorf("builderd: VMMDriver not wired")
	}
	defer func() {
		if h.HostDrive1 != "" {
			_ = os.Remove(h.HostDrive1)
		}
	}()

	// vmmd's Destroy blocks until firecracker exits AND has exported drive1
	// (the proto contract — see pkg/vmmdgrpc/server.go::Destroy). The
	// deadline covers the build's wall-clock budget plus headroom for the
	// host-side export. A builder drive is a large ext4 scratch image; after
	// guest poweroff, loopback setup may have to flush several GiB before the
	// read-only mount can complete. Keep that export headroom separate from
	// the guest's own build timeout so a timed-out build still reaches a
	// durable build-done marker instead of becoming an infra error.
	// The guest starts its build clock after VM boot and BuildKit readiness;
	// leave enough time for that deadline plus vmmd's artifact export. The VMM
	// also enforces the same builder-only headroom in DestroyWithExport.
	deadline := time.Duration(h.TimeoutSec+600) * time.Second
	dctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	resp, err := d.cli.Destroy(dctx, &vmmdpb.DestroyRequest{Instance: h.Instance})
	if err != nil {
		return BuildOutcome{}, fmt.Errorf("builderd: destroy: %w", err)
	}
	if resp == nil {
		return BuildOutcome{}, fmt.Errorf("builderd: nil destroy outcome")
	}

	exitCode := int(resp.GetExitCode())
	ociImage := filepath.Join(h.ExportDir, "build", "out", "image.tar")
	res := BuildOutcome{
		BuildID:    h.BuildID,
		ExitCode:   exitCode,
		OCIImage:   ociImage,
		ExportDir:  h.ExportDir,
		InstanceID: h.Instance,
	}
	// The guest's build-done manifest is authoritative for builder results.
	// Firecracker may be SIGKILLed after the guest has reached "System
	// halted" because a halted Linux guest does not always terminate the
	// VMM process. In that case the host process status is -9, while the
	// guest has already flushed a successful OCI image and recorded exit 0.
	// Prefer the durable in-guest result whenever vmmd exported it.
	if done, ok := readBuildDone(h.ExportDir); ok {
		exitCode = done.ExitCode
		res.ExitCode = exitCode
		res.LogTailBytes = int64(len(done.LogTail))
		res.FailureClass = done.FailureClass
		res.FailureCode = done.FailureCode
		res.FailurePkg = done.FailurePkg
	}
	if exitCode == 0 {
		if h.DependencyCacheKey != "" {
			cachePath, pathErr := dependencyCachePath(d.driveDir, h.DependencyCacheKey)
			cacheSource := filepath.Join(h.ExportDir, "build", "out", "cache")
			if pathErr != nil {
				res.DependencyCacheStoreError = pathErr.Error()
			} else {
				d.dependencyCacheMu.Lock()
				cacheErr := publishDependencyCache(cacheSource, cachePath, dependencyCacheMaxBytes)
				if cacheErr == nil {
					cacheErr = sweepDependencyCaches(d.driveDir, time.Now())
				}
				d.dependencyCacheMu.Unlock()
				_ = os.RemoveAll(cacheSource)
				if cacheErr != nil {
					res.DependencyCacheStoreError = cacheErr.Error()
				} else {
					res.DependencyCacheStored = true
				}
			}
		}
		res.FailureClass = ""
		return res, nil
	}

	// Best-effort enrichment from build-done.json. Missing file is OK — the
	// guest died before guest-init wrote it; fall back to exit-code class.
	if res.FailureClass == "" {
		res.FailureClass, res.FailureCode, res.FailurePkg = classifyBuildFailure(exitCode, h.ExportDir)
	}
	return res, nil
}

func readBuildDone(exportDir string) (api.BuildDone, bool) {
	var done api.BuildDone
	data, err := os.ReadFile(filepath.Join(exportDir, "build-done.json"))
	if err != nil || json.Unmarshal(data, &done) != nil {
		return api.BuildDone{}, false
	}
	return done, true
}

// Cancel interrupts the builder process through StopInstance. The original
// Destroy RPC remains the sole owner of waiting, exporting and cleanup.
func (d *VMMDriver) Cancel(ctx context.Context, buildID string) error {
	if d == nil || d.cli == nil {
		return fmt.Errorf("builderd: VMMDriver not wired")
	}
	if buildID == "" {
		return fmt.Errorf("builderd: empty buildID")
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := d.cli.StopInstance(cctx, &vmmdpb.StopInstanceRequest{Instance: "build-" + buildID, Signal: 9})
	if err != nil {
		return fmt.Errorf("builderd: cancel stop: %w", err)
	}
	return nil
}

// runJanitor scans d.driveDir for *.ext4 older than 1h and removes them.
// Best-effort: no error returned. Per the plan's Risks, vmmd crashes
// between boot and destroy would otherwise leak 28 GiB scratch files; this
// is the cheap, conservative cleanup.
func (d *VMMDriver) runJanitor() {
	cutoff := time.Now().Add(-1 * time.Hour)
	entries, err := os.ReadDir(d.driveDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".ext4" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(d.driveDir, e.Name()))
		}
	}
	d.dependencyCacheMu.Lock()
	_ = sweepDependencyCaches(d.driveDir, time.Now())
	d.dependencyCacheMu.Unlock()
}

// classifyBuildFailure resolves the failure class for a non-zero build exit.
// It prefers BuildDone.FailureClass (guest-init's classification) when
// /build-done.json exists in the export, then falls back to the canonical
// exit-code table (137→OOM, 124→Timeout, else UserError). The vocabulary
// here matches the canonical names used by pkg/state.FailureClass:
// "FailureUserError" / "FailureInfra" / "FailureOOM" / "FailureTimeout".
// builderd.go's ProcessOne translates these to the column-friendly
// strings ("oom" etc) at the state.Store boundary.
//
// Error-explanations cluster (spec §6.4 amendment 1): the second
// return value is the RFC 7807 stable code guest-init stamped on
// BuildDone.FailureCode (app_arch_mismatch / dep_install_failed),
// plus the package manager discriminator for dep_install_failed
// (npm / pip / go / cargo). Empty strings when guest-init fell back
// to the coarse FailureClass only — the caller stamps the legacy
// CodeDeployFailed path.
func classifyBuildFailure(exitCode int, exportDir string) (string, string, string) {
	done := filepath.Join(exportDir, "build-done.json")
	if data, err := os.ReadFile(done); err == nil {
		var bd api.BuildDone
		if json.Unmarshal(data, &bd) == nil && bd.FailureClass != "" {
			return bd.FailureClass, bd.FailureCode, bd.FailurePkg
		}
	}
	switch exitCode {
	case 137:
		return "FailureOOM", "", ""
	case 124:
		return "FailureTimeout", "", ""
	default:
		return "FailureUserError", "", ""
	}
}

// unused import guard.
var _ vmmdpb.WakeMethod = vmmdpb.WakeMethod_WAKE_COLD_BOOT
