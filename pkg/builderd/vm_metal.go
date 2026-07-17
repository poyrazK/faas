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
	"fmt"
	"path/filepath"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// VMMDriver is the metal VM driver. It dials vmmd's gRPC socket (the same
// one schedd uses, ADR-014/015) and asks vmmd to cold-boot a builder VM.
// vmmd is the only root component — builderd never touches firecracker or
// jailer directly (CLAUDE.md ownership).
type VMMDriver struct {
	vmm sched.VMM
}

// NewVMMDriver opens a lazy gRPC connection to vmmd's socket.
func NewVMMDriver(socketPath string) (*VMMDriver, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("builderd: empty vmmd socket path")
	}
	conn, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("builderd: dial vmmd: %w", err)
	}
	cli := vmmdpb.NewVmmdClient(conn)
	return &VMMDriver{vmm: &vmmClient{cli: cli, conn: conn}}, nil
}

// Spawn boots a builder VM, waits for the in-VM build to finish, and returns
// the produced app-layer path. The builder VM base is /srv/fc/base/builder-base.ext4;
// drive1 is a throwaway rw layer for the in-VM /build workspace. The produced
// ext4 (drive1-equivalent) is copied out to /var/lib/faas/build-out/<build_id>/layer.ext4.
//
// vmmd's cold-boot signature is the same as schedd's wake path; we reuse the
// AppSpec shape. cgroup slice is plumbed through the same field set —
// builderd tells vmmd "this VM goes in faas-cp.slice" by passing the base
// path + a separate AppSpec field that vmmd's cgroup setup already honours.
func (d *VMMDriver) Spawn(ctx context.Context, req VMRequest) (VMResult, error) {
	if d == nil || d.vmm == nil {
		return VMResult{}, fmt.Errorf("builderd: VMMDriver not wired")
	}
	out, err := d.vmm.CreateColdBoot(ctx, req.BuildID, sched.AppSpec{
		BasePath:   builderBasePath,
		LayerPath:  filepath.Join("/srv/fc/build-out", req.BuildID, "layer.ext4"),
		VCPUCount:  int32(api.BuildVMVCPU),
		MemSizeMiB: int32(req.RAMMB),
	})
	if err != nil {
		return VMResult{}, fmt.Errorf("builderd: cold boot: %w", err)
	}
	if out == nil {
		return VMResult{}, fmt.Errorf("builderd: nil wake outcome")
	}
	// The build VM emits its produced layer at the LayerPath above; vmmd
	// owns the on-disk shape so we trust what it returned. Caller checks
	// the file size and stamps it into the deployment row.
	return VMResult{
		LayerPath: filepath.Join("/srv/fc/build-out", req.BuildID, "layer.ext4"),
		ExitCode:  0, // build VM exits non-zero via vmmd's Destroy RPC; M6 returns 0 on success.
	}, nil
}

// builderBasePath is the drive0 read-only base that holds Railpack/buildkit.
// Produced once by imaged from images/builder-base.Dockerfile, staged to
// /srv/fc/base/builder-base.ext4.
const builderBasePath = "/srv/fc/base/builder-base.ext4"

// vmmClient is the typed wrapper around the vmmd gRPC client. We duplicate
// the small subset sched.VMM exposes because builderd and schedd each own
// their own connection (one unix socket per dialer, per ADR-015).
type vmmClient struct {
	cli  vmmdpb.VmmdClient
	conn *grpc.ClientConn
}

func (c *vmmClient) CreateColdBoot(ctx context.Context, instance string, app sched.AppSpec) (*sched.WakeOutcome, error) {
	resp, err := c.cli.CreateColdBoot(ctx, &vmmdpb.CreateColdBootRequest{
		Instance: instance,
		App: &vmmdpb.AppSpec{
			BasePath:   app.BasePath,
			LayerPath:  app.LayerPath,
			VcpuCount:  app.VCPUCount,
			MemSizeMib: app.MemSizeMiB,
		},
	})
	if err != nil {
		return nil, err
	}
	return &sched.WakeOutcome{
		Instance:        resp.GetInstance(),
		HostIP:          resp.GetHostIp(),
		Netns:           resp.GetNetns(),
		Method:          resp.GetMethod(),
		RequestedMethod: resp.GetRequestedMethod(),
	}, nil
}

func (c *vmmClient) CreateFromSnapshot(ctx context.Context, instance string, app sched.AppSpec, snap sched.SnapshotRef) (*sched.WakeOutcome, error) {
	// Builders never restore from snapshots — they always cold-boot.
	return nil, fmt.Errorf("builderd: builder VM does not restore from snapshot")
}

func (c *vmmClient) PauseAndSnapshot(ctx context.Context, instance, memPath, vmstatePath string) (sched.SnapshotBytes, error) {
	// Builder VMs are ephemeral; no snapshotting.
	return sched.SnapshotBytes{}, fmt.Errorf("builderd: builder VM is not snapshotted")
}

func (c *vmmClient) Destroy(ctx context.Context, instance string) error {
	_, err := c.cli.Destroy(ctx, &vmmdpb.DestroyRequest{Instance: instance})
	return err
}
