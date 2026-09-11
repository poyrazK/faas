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
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// FirecrackerVersion asks vmmd for the version of the running Firecracker
// binary. Warm snapshot restore is valid only when this value matches the
// version captured in the slot metadata.
func (d *VMMDriver) FirecrackerVersion(ctx context.Context) (string, error) {
	if d == nil || d.cli == nil {
		return "", fmt.Errorf("builderd: VMMDriver not wired")
	}
	resp, err := d.cli.Ping(ctx, &vmmdpb.PingRequest{})
	if err != nil {
		return "", fmt.Errorf("builderd: vmmd ping: %w", err)
	}
	if resp == nil || resp.GetFcVersion() == "" {
		return "", fmt.Errorf("builderd: vmmd returned empty Firecracker version")
	}
	return resp.GetFcVersion(), nil
}

func buildManifestForRequest(req VMRequest, timeoutSec int) (api.BuildManifest, error) {
	workdir, err := buildWorkdir(req.SourceRoot)
	if err != nil {
		return api.BuildManifest{}, fmt.Errorf("builderd: source root: %w", err)
	}
	return api.BuildManifest{
		SchemaVersion:   1,
		BuildID:         req.BuildID,
		TenantID:        req.TenantID,
		DeploymentID:    req.DeploymentID,
		SourceTarPath:   "/build/src.tar",
		BuildContext:    "/build/src",
		Workdir:         workdir,
		OutDir:          "/build/out",
		Framework:       MapFramework(req.Framework),
		Runtime:         req.Runtime,
		RuntimeBaseRef:  req.RuntimeBaseRef,
		DependencyCache: req.DependencyCacheKey != "",
		KeepWarm:        req.KeepWarm,
		TimeoutSec:      timeoutSec,
		LogTailBytes:    64 * 1024,
	}, nil
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
	bManifest, err := buildManifestForRequest(req, timeoutSec)
	if err != nil {
		os.Remove(drive1Path)
		return BuildHandle{}, err
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
		WarmScopeKey:            req.WarmScopeKey,
	}, nil
}

// RestoreWarmBuilder refreshes only the per-build inputs on a retained
// builder drive, then restores the Firecracker memory/vmstate pair. The drive
// itself stays in place so the guest's BuildKit state survives the restore.
func (d *VMMDriver) RestoreWarmBuilder(ctx context.Context, req VMRequest, snapshot WarmSnapshot) (BuildHandle, error) {
	if d == nil || d.cli == nil {
		return BuildHandle{}, fmt.Errorf("builderd: VMMDriver not wired")
	}
	if req.BuildID == "" || snapshot.StorageKey == "" || snapshot.VMStateStorageKey == "" {
		return BuildHandle{}, fmt.Errorf("builderd: incomplete warm restore request")
	}
	if snapshot.LayerPath == "" {
		return BuildHandle{}, fmt.Errorf("builderd: warm snapshot has no retained builder drive")
	}
	if info, err := os.Stat(snapshot.LayerPath); err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return BuildHandle{}, fmt.Errorf("builderd: retained builder drive: %w", err)
	}
	if err := os.MkdirAll(d.exportDir, 0o755); err != nil {
		return BuildHandle{}, fmt.Errorf("builderd: mkdir export dir: %w", err)
	}
	timeoutSec := req.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = api.BuildTimeoutSeconds
	}
	manifest, err := buildManifestForRequest(req, timeoutSec)
	if err != nil {
		return BuildHandle{}, err
	}
	if err := refreshWarmBuilderDrive(ctx, snapshot.LayerPath, manifest, req.SourcePath); err != nil {
		return BuildHandle{}, fmt.Errorf("builderd: refresh warm drive: %w", err)
	}
	instance := "build-" + req.BuildID
	buildExportDir := filepath.Join(d.exportDir, req.BuildID)
	resp, err := d.cli.CreateFromSnapshot(ctx, &vmmdpb.CreateFromSnapshotRequest{
		Instance: instance,
		App: &vmmdpb.AppSpec{
			BaseKey:    sched.BaseKey("builder"),
			LayerKey:   snapshot.LayerPath,
			VcpuCount:  api.BuildVMVCPU,
			MemSizeMib: int32(api.BuildVMRAMMB),
		},
		Snapshot: &vmmdpb.SnapshotRef{
			StorageKey:        snapshot.StorageKey,
			VmstateStorageKey: snapshot.VMStateStorageKey,
			FcVersion:         snapshot.FCVersion,
		},
		Build: &vmmdpb.BuildSpec{
			ExportDir:  buildExportDir,
			TimeoutSec: int32(timeoutSec),
		},
		Plan:      req.Plan,
		AccountId: req.TenantID,
	})
	if err != nil {
		return BuildHandle{}, fmt.Errorf("builderd: warm restore: %w", err)
	}
	if resp == nil {
		return BuildHandle{}, fmt.Errorf("builderd: nil warm restore outcome")
	}
	if resp.GetMethod() != vmmdpb.WakeMethod_WAKE_RESTORE {
		cleanupErr := d.stopAndDestroy(context.WithoutCancel(ctx), instance)
		if cleanupErr != nil {
			return BuildHandle{}, fmt.Errorf("builderd: warm restore fell back to cold boot and cleanup failed: %w", cleanupErr)
		}
		return BuildHandle{}, fmt.Errorf("builderd: warm restore fell back to cold boot")
	}
	return BuildHandle{
		Instance:                instance,
		HostDrive1:              snapshot.LayerPath,
		ExportDir:               buildExportDir,
		BuildID:                 req.BuildID,
		TimeoutSec:              timeoutSec,
		StartedAt:               time.Now(),
		DependencyCacheKey:      req.DependencyCacheKey,
		DependencyCacheRestored: true,
		WarmScopeKey:            req.WarmScopeKey,
	}, nil
}

// WaitForWarmCompletion waits for the guest's successful build handoff,
// captures the reusable memory state, destroys the live VM, and retains the
// host drive for the next restore. Failed builds use the ordinary teardown
// path and return no warm snapshot.
func (d *VMMDriver) WaitForWarmCompletion(ctx context.Context, h BuildHandle) (BuildOutcome, WarmSnapshot, error) {
	if d == nil || d.cli == nil {
		return BuildOutcome{}, WarmSnapshot{}, fmt.Errorf("builderd: VMMDriver not wired")
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(h.TimeoutSec+600)*time.Second)
	defer cancel()
	readyResp, err := d.cli.WaitBuilderReady(waitCtx, &vmmdpb.WaitBuilderReadyRequest{Instance: h.Instance})
	if err != nil {
		cleanupErr := d.stopAndDestroy(context.WithoutCancel(ctx), h.Instance)
		return BuildOutcome{}, WarmSnapshot{}, errors.Join(fmt.Errorf("builderd: wait builder ready: %w", err), cleanupErr)
	}
	if readyResp == nil {
		cleanupErr := d.stopAndDestroy(context.WithoutCancel(ctx), h.Instance)
		return BuildOutcome{}, WarmSnapshot{}, errors.Join(errors.New("builderd: nil builder readiness outcome"), cleanupErr)
	}
	if !readyResp.GetReady() {
		out, waitErr := d.waitForCompletion(ctx, h, false)
		return out, WarmSnapshot{}, waitErr
	}

	fcVersion, err := d.FirecrackerVersion(ctx)
	if err != nil {
		cleanupErr := d.stopAndDestroy(context.WithoutCancel(ctx), h.Instance)
		return BuildOutcome{}, WarmSnapshot{}, errors.Join(fmt.Errorf("builderd: warm snapshot Firecracker version: %w", err), cleanupErr)
	}
	memKey, vmstateKey := warmBuilderSnapshotKeys(h.BuildID)
	if _, err := d.cli.WarmSnapshot(ctx, &vmmdpb.WarmSnapshotRequest{
		Instance:          h.Instance,
		StorageKey:        memKey,
		VmstateStorageKey: vmstateKey,
	}); err != nil {
		cleanupErr := d.stopAndDestroy(context.WithoutCancel(ctx), h.Instance)
		return BuildOutcome{}, WarmSnapshot{}, errors.Join(fmt.Errorf("builderd: warm snapshot: %w", err), cleanupErr)
	}
	snapshot := WarmSnapshot{
		StorageKey:        memKey,
		VMStateStorageKey: vmstateKey,
		LayerPath:         h.HostDrive1,
		ScopeKey:          h.WarmScopeKey,
		FCVersion:         fcVersion,
		CreatedAt:         time.Now(),
		LastUsedAt:        time.Now(),
	}
	if _, err := d.cli.StopInstance(context.WithoutCancel(ctx), &vmmdpb.StopInstanceRequest{Instance: h.Instance, Signal: 9}); err != nil {
		cleanupErr := d.stopAndDestroy(context.WithoutCancel(ctx), h.Instance)
		_ = d.DeleteWarmSnapshot(context.WithoutCancel(ctx), snapshot)
		_ = os.Remove(h.HostDrive1)
		return BuildOutcome{}, WarmSnapshot{}, errors.Join(fmt.Errorf("builderd: stop warm builder: %w", err), cleanupErr)
	}
	out, err := d.waitForCompletion(ctx, h, true)
	if err != nil {
		_ = d.DeleteWarmSnapshot(context.WithoutCancel(ctx), snapshot)
		_ = os.Remove(h.HostDrive1)
		return BuildOutcome{}, WarmSnapshot{}, err
	}
	return out, snapshot, nil
}

func warmBuilderSnapshotKeys(buildID string) (string, string) {
	sum := sha256.Sum256([]byte(buildID))
	suffix := hex.EncodeToString(sum[:])
	return "snap/builder/" + suffix + "/mem", "snap/builder/" + suffix + "/vmstate"
}

func (d *VMMDriver) stopAndDestroy(ctx context.Context, instance string) error {
	if d == nil || d.cli == nil || instance == "" {
		return nil
	}
	stopCtx, cancel := context.WithTimeout(ctx, activeVMCancelTimeout)
	defer cancel()
	_, stopErr := d.cli.StopInstance(stopCtx, &vmmdpb.StopInstanceRequest{Instance: instance, Signal: 9})
	destroyCtx, destroyCancel := context.WithTimeout(context.WithoutCancel(ctx), activeVMCancelTimeout)
	defer destroyCancel()
	_, destroyErr := d.cli.Destroy(destroyCtx, &vmmdpb.DestroyRequest{Instance: instance})
	return errors.Join(stopErr, destroyErr)
}

// DeleteWarmSnapshot removes both vmmd-owned snapshot blobs and the retained
// local builder drive. The latter is part of the warm snapshot because
// Firecracker's memory snapshot does not include virtio block-device bytes.
func (d *VMMDriver) DeleteWarmSnapshot(ctx context.Context, snapshot WarmSnapshot) error {
	if d == nil || d.cli == nil {
		return fmt.Errorf("builderd: VMMDriver not wired")
	}
	var errs []error
	if snapshot.StorageKey != "" || snapshot.VMStateStorageKey != "" {
		_, err := d.cli.DeleteWarmSnapshot(ctx, &vmmdpb.DeleteWarmSnapshotRequest{
			StorageKey:        snapshot.StorageKey,
			VmstateStorageKey: snapshot.VMStateStorageKey,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("delete vmmd snapshot: %w", err))
		}
	}
	if snapshot.LayerPath != "" {
		if err := os.Remove(snapshot.LayerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove warm builder drive: %w", err))
		}
	}
	return errors.Join(errs...)
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
	return d.waitForCompletion(ctx, h, false)
}

func (d *VMMDriver) waitForCompletion(ctx context.Context, h BuildHandle, retainDrive bool) (BuildOutcome, error) {
	if d == nil || d.cli == nil {
		return BuildOutcome{}, fmt.Errorf("builderd: VMMDriver not wired")
	}
	defer func() {
		if h.HostDrive1 != "" && !retainDrive {
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
		res.BuildkitVer = done.BuildkitVersion
		res.RailpackVer = done.RailpackVersion
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

// refreshWarmBuilderDrive updates the manifest, source archive, and one-shot
// entropy seed in an offline retained ext4 image. debugfs edits the image
// without CAP_SYS_ADMIN, so builderd can prepare it before vmmd stages the
// same image into a fresh jail. BuildKit's cache directories are untouched.
func refreshWarmBuilderDrive(ctx context.Context, drivePath string, manifest api.BuildManifest, sourcePath string) error {
	if drivePath == "" || sourcePath == "" {
		return fmt.Errorf("warm drive and source path are required")
	}
	if info, err := os.Stat(drivePath); err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return fmt.Errorf("stat drive: %w", err)
	}
	if info, err := os.Stat(sourcePath); err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return fmt.Errorf("stat source: %w", err)
	}

	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	manifestFile, err := os.CreateTemp("", "faas-warm-manifest-")
	if err != nil {
		return fmt.Errorf("create manifest staging file: %w", err)
	}
	manifestPath := manifestFile.Name()
	defer func() { _ = os.Remove(manifestPath) }()
	if _, err := manifestFile.Write(manifestData); err != nil {
		_ = manifestFile.Close()
		return fmt.Errorf("stage manifest: %w", err)
	}
	if err := manifestFile.Sync(); err != nil {
		_ = manifestFile.Close()
		return fmt.Errorf("sync manifest: %w", err)
	}
	if err := manifestFile.Close(); err != nil {
		return fmt.Errorf("close manifest: %w", err)
	}

	sourceFile, err := os.CreateTemp("", "faas-warm-source-")
	if err != nil {
		return fmt.Errorf("create source staging file: %w", err)
	}
	sourceTempPath := sourceFile.Name()
	defer func() { _ = os.Remove(sourceTempPath) }()
	//nolint:forbidigo // sourcePath was validated by builderd before the warm
	// restore; this copy is an intermediate file for debugfs, not a customer
	// path opened by vmmd.
	sourceIn, err := os.Open(sourcePath)
	if err != nil {
		_ = sourceFile.Close()
		return fmt.Errorf("open source: %w", err)
	}
	if _, err := io.Copy(sourceFile, sourceIn); err != nil {
		_ = sourceIn.Close()
		_ = sourceFile.Close()
		return fmt.Errorf("stage source: %w", err)
	}
	_ = sourceIn.Close()
	if err := sourceFile.Sync(); err != nil {
		_ = sourceFile.Close()
		return fmt.Errorf("sync source: %w", err)
	}
	if err := sourceFile.Close(); err != nil {
		return fmt.Errorf("close source: %w", err)
	}

	seed := make([]byte, 256)
	if _, err := rand.Read(seed); err != nil {
		return fmt.Errorf("generate entropy seed: %w", err)
	}
	seedFile, err := os.CreateTemp("", "faas-warm-entropy-")
	if err != nil {
		return fmt.Errorf("create entropy staging file: %w", err)
	}
	seedPath := seedFile.Name()
	defer func() { _ = os.Remove(seedPath) }()
	if err := seedFile.Chmod(0o600); err != nil {
		_ = seedFile.Close()
		return fmt.Errorf("chmod entropy: %w", err)
	}
	if _, err := seedFile.Write(seed); err != nil {
		_ = seedFile.Close()
		return fmt.Errorf("stage entropy: %w", err)
	}
	if err := seedFile.Sync(); err != nil {
		_ = seedFile.Close()
		return fmt.Errorf("sync entropy: %w", err)
	}
	if err := seedFile.Close(); err != nil {
		return fmt.Errorf("close entropy: %w", err)
	}

	if err := replaceWarmFile(ctx, drivePath, manifestPath, "/upper/etc/faas/build.json", true); err != nil {
		return fmt.Errorf("replace build manifest: %w", err)
	}
	if err := replaceWarmFile(ctx, drivePath, sourceTempPath, "/upper/build/src.tar", true); err != nil {
		return fmt.Errorf("replace source archive: %w", err)
	}
	if err := replaceWarmFile(ctx, drivePath, seedPath, "/upper/etc/faas/entropy.seed", false); err != nil {
		return fmt.Errorf("replace entropy seed: %w", err)
	}
	return nil
}

func replaceWarmFile(ctx context.Context, image, hostPath, guestPath string, requireRemove bool) error {
	removeErr := runDebugfs(ctx, image, "rm "+guestPath)
	if removeErr != nil && requireRemove {
		return removeErr
	}
	return runDebugfs(ctx, image, "write "+debugfsToken(hostPath)+" "+guestPath)
}

func runDebugfs(ctx context.Context, image, command string) error {
	output, err := exec.CommandContext(ctx, "debugfs", "-w", "-R", command, image).CombinedOutput()
	if err != nil {
		return fmt.Errorf("debugfs %q: %w (%s)", command, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func debugfsToken(path string) string {
	return `"` + strings.ReplaceAll(path, `"`, `\"`) + `"`
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
