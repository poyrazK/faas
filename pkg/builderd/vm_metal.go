//go:build metal

// Package builderd (metal) — ephemeral builder microVM spawn.
//
// builderd is the ONLY process that runs Railpack/buildkit. The build
// happens inside an ephemeral Firecracker microVM booted from
// /srv/fc/base/builder-base.ext4 (drive0, shared read-only — the image
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
	"time"

	"google.golang.org/grpc"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
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
	// buildkit/Railpack/etc. Default /srv/fc/base/builder-base.ext4.
	builderBase string

	// driveDir hosts the temporary per-VM drive1 images we create at
	// CreateBuildDrive1 time. Cleanup happens via WaitForCompletion's
	// defer + the startup janitor.
	driveDir string

	// exportDir is the parent of all build artifact exports: vmmd writes
	// <exportDir>/<build_id>/build-done.json and /build/out/* here.
	exportDir string
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
		builderBase = "/srv/fc/base/builder-base.ext4"
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

// Spawn materialises the per-VM drive1, cold-boots the VM, and returns a
// BuildHandle the caller can pass to WaitForCompletion. The VM base is
// d.builderBase; drive1 is a throwaway 8 GiB ext4 that carries BuildManifest
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
	bManifest := api.BuildManifest{
		SchemaVersion: 1,
		BuildID:       req.BuildID,
		TenantID:      req.TenantID,
		DeploymentID:  req.DeploymentID,
		SourceTarPath: "/build/src.tar",
		Workdir:       "/build/src",
		OutDir:        "/build/out",
		Framework:     MapFramework(req.Framework),
		TimeoutSec:    timeoutSec,
		LogTailBytes:  64 * 1024,
	}
	if err := CreateBuildDrive1(ctx, drive1Path, bManifest, req.SourcePath); err != nil {
		os.Remove(drive1Path)
		return BuildHandle{}, fmt.Errorf("builderd: create drive1: %w", err)
	}

	// 2. Cold-boot. BuildSpec carries the export dir; vmmd's Destroy will
	//    loopback-mount drive1 and copy out /build/out/* + build-done.json.
	buildExportDir := filepath.Join(d.exportDir, req.BuildID)
	resp, err := d.cli.CreateColdBoot(ctx, &vmmdpb.CreateColdBootRequest{
		Instance: instance,
		App: &vmmdpb.AppSpec{
			BasePath:   d.builderBase,
			LayerPath:  drive1Path, // vmmd's stageWritable will copy into the chroot
			VcpuCount:  api.BuildVMVCPU,
			MemSizeMib: int32(api.BuildVMRAMMB),
		},
		Build: &vmmdpb.BuildSpec{ExportDir: buildExportDir},
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
		Instance:   instance,
		HostDrive1: drive1Path,
		ExportDir:  buildExportDir,
		BuildID:    req.BuildID,
		TimeoutSec: timeoutSec,
		StartedAt:  time.Now(),
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
	// snapshot_prime handshake the caller may run after us.
	deadline := time.Duration(h.TimeoutSec+60) * time.Second
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
	if exitCode == 0 {
		res.FailureClass = ""
		return res, nil
	}

	// Best-effort enrichment from build-done.json. Missing file is OK — the
	// guest died before guest-init wrote it; fall back to exit-code class.
	res.FailureClass = classifyBuildFailure(exitCode, h.ExportDir)
	return res, nil
}

// runJanitor scans d.driveDir for *.ext4 older than 1h and removes them.
// Best-effort: no error returned. Per the plan's Risks, vmmd crashes
// between boot and destroy would otherwise leak 8 GiB scratch files; this
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
}

// classifyBuildFailure resolves the failure class for a non-zero build exit.
// It prefers BuildDone.FailureClass (guest-init's classification) when
// /build-done.json exists in the export, then falls back to the canonical
// exit-code table (137→OOM, 124→Timeout, else UserError). The vocabulary
// here matches the canonical names used by pkg/state.FailureClass:
// "FailureUserError" / "FailureInfra" / "FailureOOM" / "FailureTimeout".
// builderd.go's ProcessOne translates these to the column-friendly
// strings ("oom" etc) at the state.Store boundary.
func classifyBuildFailure(exitCode int, exportDir string) string {
	done := filepath.Join(exportDir, "build-done.json")
	if data, err := os.ReadFile(done); err == nil {
		var bd struct {
			FailureClass string `json:"failure_class"`
		}
		if json.Unmarshal(data, &bd) == nil && bd.FailureClass != "" {
			return bd.FailureClass
		}
	}
	switch exitCode {
	case 137:
		return "FailureOOM"
	case 124:
		return "FailureTimeout"
	default:
		return "FailureUserError"
	}
}

// unused import guard.
var _ vmmdpb.WakeMethod = vmmdpb.WakeMethod_WAKE_COLD_BOOT
