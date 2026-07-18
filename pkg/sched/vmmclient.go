// vmmclient.go — schedd's typed wrapper over the generated vmmd gRPC client
// (ADR-014 names this "pkg/sched grpcclient that wraps a vmmd connection").
// schedd is the caller that resolves an app into vmmd's flat AppSpec and drives
// the microVM lifecycle; vmmd stays stateless about app config.
//
// The wrapper does two jobs the raw generated client doesn't:
//   - hides vmmdpb from the rest of pkg/sched (callers pass plain Go structs);
//   - re-lifts vmmd's gRPC error envelope back into *api.Problem via
//     pkg/grpcerr so a wake denial keeps its stable RFC 7807 code all the way
//     out to the gateway (ADR-013).

package sched

import (
	"context"
	"errors"
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// VMM is the slice of vmmd schedd depends on. Defined as an interface so the
// engine (engine.go, PR2) and its tests can substitute a fake vmmd without a
// real socket — mirrors pkg/vmmdgrpc.VmmdAPI on the server side.
type VMM interface {
	CreateColdBoot(ctx context.Context, instance string, app AppSpec) (*WakeOutcome, error)
	CreateFromSnapshot(ctx context.Context, instance string, app AppSpec, snap SnapshotRef) (*WakeOutcome, error)
	PauseAndSnapshot(ctx context.Context, instance, memPath, vmstatePath string) (SnapshotBytes, error)
	Destroy(ctx context.Context, instance string) error
}

// AppSpec is the flat set of fields vmmd needs to boot an instance (ADR-014).
// schedd fills it from its Postgres view of the app + deployment.
//
// SealedEnv carries the per-key ciphertext rows from `app_secrets` (spec
// §11/G2). schedd is the only writer that can load these rows (apid writes
// intent, schedd reads to drive wakes). Empty slice = no secrets file
// written; vmmd treats nil and empty as equivalent.
type AppSpec struct {
	BasePath   string // drive0 shared read-only base rootfs (spec §4.6)
	LayerPath  string // drive1 per-app layer
	VCPUCount  int32  // 2, or 4 for Scale
	MemSizeMiB int32  // plan RAM; the slice fences at +8 MiB (pkg/api/limits.go)
	EgressMbit int32  // per-plan tc cap (pkg/api/limits.EgressMbit); 0 = no cap
	SealedEnv  []fcvm.SealedEnvEntry
}

// SnapshotRef points at the snapshot files to restore from and the Firecracker
// version they were made with (ADR-005 pinning). An empty ref means cold boot.
type SnapshotRef struct {
	DeploymentID string
	MemPath      string
	VMStatePath  string
	FCVersion    string
}

// SnapshotBytes is the size accounting returned by PauseAndSnapshot; schedd
// records it on the snapshot row (fleet-size telemetry, spec §12).
type SnapshotBytes struct {
	MemBytes     int64
	VMStateBytes int64
}

// WakeOutcome is the decoded result of a vmmd wake. Method reports what vmmd
// actually did; RequestedMethod is what schedd asked for (a restore that fell
// back to cold boot per ADR-005 reads Method=WAKE_COLD_BOOT here).
type WakeOutcome struct {
	Instance        string
	LeaseUID        int32
	HostIP          string
	Netns           string
	VethHost        string
	VethPeer        string
	Method          vmmdpb.WakeMethod
	RequestedMethod vmmdpb.WakeMethod
}

// VMMClient is the production VMM: a gRPC connection to vmmd's unix socket.
type VMMClient struct {
	conn *grpc.ClientConn
	cli  vmmdpb.VmmdClient
}

// compile-time assertion that the client satisfies the interface the engine
// consumes.
var _ VMM = (*VMMClient)(nil)

// DialVMM opens a lazy gRPC connection to vmmd's unix socket (ADR-015: the
// socket's 0660/group-`faas` DAC is the only auth for v1.0, so the transport is
// insecure credentials over a trusted local socket). The connection dials on
// first RPC; DialVMM never blocks on vmmd being up.
func DialVMM(socketPath string) (*VMMClient, error) {
	if socketPath == "" {
		return nil, errors.New("sched: empty vmmd socket path")
	}
	conn, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("sched: dial vmmd %q: %w", socketPath, err)
	}
	return &VMMClient{conn: conn, cli: vmmdpb.NewVmmdClient(conn)}, nil
}

// NewVMMClient wraps an already-dialed connection (used by bufconn tests).
func NewVMMClient(conn *grpc.ClientConn) *VMMClient {
	return &VMMClient{conn: conn, cli: vmmdpb.NewVmmdClient(conn)}
}

// Close releases the underlying connection.
func (c *VMMClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *VMMClient) CreateColdBoot(ctx context.Context, instance string, app AppSpec) (*WakeOutcome, error) {
	resp, err := c.cli.CreateColdBoot(ctx, &vmmdpb.CreateColdBootRequest{
		Instance: instance,
		App:      app.toProto(),
	})
	if err != nil {
		return nil, liftErr(err)
	}
	return outcomeFromProto(resp), nil
}

func (c *VMMClient) CreateFromSnapshot(ctx context.Context, instance string, app AppSpec, snap SnapshotRef) (*WakeOutcome, error) {
	resp, err := c.cli.CreateFromSnapshot(ctx, &vmmdpb.CreateFromSnapshotRequest{
		Instance: instance,
		App:      app.toProto(),
		Snapshot: &vmmdpb.SnapshotRef{
			DeploymentId: snap.DeploymentID,
			MemPath:      snap.MemPath,
			VmstatePath:  snap.VMStatePath,
			FcVersion:    snap.FCVersion,
		},
	})
	if err != nil {
		return nil, liftErr(err)
	}
	return outcomeFromProto(resp), nil
}

func (c *VMMClient) PauseAndSnapshot(ctx context.Context, instance, memPath, vmstatePath string) (SnapshotBytes, error) {
	resp, err := c.cli.PauseAndSnapshot(ctx, &vmmdpb.PauseAndSnapshotRequest{
		Instance:    instance,
		MemPath:     memPath,
		VmstatePath: vmstatePath,
	})
	if err != nil {
		return SnapshotBytes{}, liftErr(err)
	}
	return SnapshotBytes{MemBytes: resp.GetMemBytes(), VMStateBytes: resp.GetVmstateBytes()}, nil
}

func (c *VMMClient) Destroy(ctx context.Context, instance string) error {
	if _, err := c.cli.Destroy(ctx, &vmmdpb.DestroyRequest{Instance: instance}); err != nil {
		return liftErr(err)
	}
	return nil
}

func (a AppSpec) toProto() *vmmdpb.AppSpec {
	sealed := make([]*vmmdpb.SealedSecret, 0, len(a.SealedEnv))
	for _, e := range a.SealedEnv {
		sealed = append(sealed, &vmmdpb.SealedSecret{
			Key:        e.Key,
			Ciphertext: e.Ciphertext,
		})
	}
	return &vmmdpb.AppSpec{
		BasePath:   a.BasePath,
		LayerPath:  a.LayerPath,
		VcpuCount:  a.VCPUCount,
		MemSizeMib: a.MemSizeMiB,
		EgressMbit: a.EgressMbit,
		SealedEnv:  sealed,
	}
}

func outcomeFromProto(r *vmmdpb.WakeResponse) *WakeOutcome {
	return &WakeOutcome{
		Instance:        r.GetInstance(),
		LeaseUID:        r.GetLeaseUid(),
		HostIP:          r.GetHostIp(),
		Netns:           r.GetNetns(),
		VethHost:        r.GetVethHost(),
		VethPeer:        r.GetVethPeer(),
		Method:          r.GetMethod(),
		RequestedMethod: r.GetRequestedMethod(),
	}
}

// liftErr converts a vmmd gRPC error back into the platform's *api.Problem so
// its stable Code + Limit/Observed survive to the gateway. Errors that aren't
// status-shaped (e.g. a dial failure) pass through unchanged.
func liftErr(err error) error {
	if p, ok := grpcerr.FromStatus(err); ok && p != nil {
		return p
	}
	return err
}
