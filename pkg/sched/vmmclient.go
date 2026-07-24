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
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/overlay"
	"google.golang.org/grpc"
)

// VMM is the slice of vmmd schedd depends on. Defined as an interface so the
// engine (engine.go, PR2) and its tests can substitute a fake vmmd without a
// real socket — mirrors pkg/vmmdgrpc.VmmdAPI on the server side.
type VMM interface {
	CreateColdBoot(ctx context.Context, instance string, app AppSpec) (*WakeOutcome, error)
	CreateFromSnapshot(ctx context.Context, instance string, app AppSpec, snap SnapshotRef) (*WakeOutcome, error)
	// PauseAndSnapshot (issue #121 / ADR-025 axis 2 slice 4) takes
	// the vmstate_storage_key as a third string alongside vmstatePath
	// and storageKey. The empty string means "single-box default-local
	// uses the legacy host vmstate_path"; a populated value means
	// "vmmd publishes via the configured StorageBackend".
	PauseAndSnapshot(ctx context.Context, instance, vmstatePath, storageKey, vmstateStorageKey string) (SnapshotBytes, error)
	Destroy(ctx context.Context, instance string) error
	// Ping is the wire-level liveness probe (issue #97 / ADR-025
	// axis 3, PR #114). schedd's heartbeat loop calls this every
	// HeartbeatInterval on every active compute_node; a non-error
	// round-trip proves both gRPC socket reachability and that
	// vmmd is responsive enough to schedule the handler. The
	// returned FcVersion lets schedd's admin surface show
	// per-node FC versions without a separate Stats call.
	Ping(ctx context.Context) (*PingOutcome, error)
	// Close releases the underlying transport. Issue #120: the
	// heartbeat goroutine dials fresh per tick and relies on this
	// to keep its conn churn bounded (no goroutine/conn leak).
	Close() error
}

// PingOutcome is the sched-side view of vmmdpb.PingResponse.
// Decoupled from the proto so the engine + heartbeat loop never
// import pkg/api/proto — same shape, plain time.Time for the
// server-stamped timestamp.
type PingOutcome struct {
	FcVersion  string
	ServerTime time.Time
}

// AppSpec is the flat set of fields vmmd needs to boot an instance (ADR-014).
// schedd fills it from its Postgres view of the app + deployment.
//
// Issue #96 / ADR-025 axis 2 / PR #116: BaseKey / LayerKey are the
// StorageBackend keys (not host paths) the wake wire carries. vmmd
// resolves them locally via Storage.Get before staging the chroot.
// The local StorageBackend's Get maps keys to the same files the
// legacy BasePath / LayerPath fields used, so single-box behaviour is
// preserved. BasePath / LayerPath were removed cleanly (internal-only
// consumers, no wire-compat shim).
//
// SealedEnv carries the per-key ciphertext rows from `app_secrets` (spec
// §11/G2). schedd is the only writer that can load these rows (apid writes
// intent, schedd reads to drive wakes). Empty slice = no secrets file
// written; vmmd treats nil and empty as equivalent.
//
// EgressAllowlist (ADR-031) carries the per-app outbound IP allowlist —
// CIDR strings (e.g. "1.2.3.0/24"), parsed upstream by apid on PUT/PATCH
// and re-validated by the apps.egress_allowlist cidr[] CHECK (v4-only).
// Empty slice = no allowlist rule emitted in the per-netns forward chain
// (current behaviour preserved).
type AppSpec struct {
	BaseKey         string // drive0 base rootfs StorageBackend key (e.g. "base/runtime-node22.ext4")
	LayerKey        string // drive1 per-app layer StorageBackend key (e.g. "apps/<slug>/<depID>.ext4")
	VCPUCount       int32  // 2, or 4 for Scale
	MemSizeMiB      int32  // plan RAM; the slice fences at +8 MiB (pkg/api/limits.go)
	EgressMbit      int32  // per-plan tc cap (pkg/api/limits.EgressMbit); 0 = no cap
	SealedEnv       []fcvm.SealedEnvEntry
	EgressAllowlist []string // ADR-031; v4 CIDRs; empty = no allowlist rule
}

// SnapshotRef points at the snapshot to restore from and the Firecracker
// version it was made with (ADR-005 pinning). An empty ref means cold boot.
//
// #96 / ADR-025 axis 2: StorageKey is the canonical storage key the VMM
// pulls the mem blob from (e.g. "snap/<deploymentID>/mem"). vmmd's
// StorageBackend resolves the bytes through the configured driver into a
// tmp staging path before firing the FC restore. MemPath is gone — the
// deprecation window expired with #96 slice 3.
//
// #121 / ADR-025 axis 2 slice 4: VMStateStorageKey is the canonical
// storage key the VMM pulls the vmstate blob from
// (e.g. "snap/<deploymentID>/vmstate"). When non-empty, vmmd's
// StorageBackend resolves the bytes through the configured driver;
// when empty (default-local), the VMM falls back to VMStatePath (the
// legacy host-path branch the engine reconstructs deterministically on
// wake). The two locators are inclusive in principle but the engine
// only populates one for a given wake: empty for default-local, the
// canonical key for remote nodes. Cold-boot-fallback still requires
// StorageKey (mem F-1 contract).
type SnapshotRef struct {
	DeploymentID string
	VMStatePath  string
	FCVersion    string
	StorageKey   string
	// VMStateStorageKey is the canonical StorageBackend key for the
	// vmstate blob (issue #121 / ADR-025 axis 2 slice 4). Empty on
	// default-local; populated on remote compute nodes.
	VMStateStorageKey string
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
//
// Per-call deadlines (spec §6.1, commit 1) live at the engine call site,
// not in this client. Each vmmd RPC has a different spec budget (5s for
// WAKING, 30s for COLD_BOOTING, 10s for Destroy) and the engine wraps
// the call with the appropriate context.WithTimeout; centralising
// deadlines here would either over-budget (every RPC gets the largest
// budget) or under-budget (every RPC gets the smallest). Leave the
// client transport-only.
//
// Legacy entrypoint kept for source compatibility with existing
// callers and tests; production code should call DialVMMContext so the
// caller's context controls the dial.
func DialVMM(socketPath string) (*VMMClient, error) {
	return DialVMMContext(context.Background(), socketPath, nil)
}

// DialVMMContext opens a lazy gRPC connection to vmmd. tlsCfg is
// required for tcp/dns targets (issue #95); nil tlsCfg is fine for the
// single-box unix default. Issue #120: the dial routes through
// pkg/overlay so the cross-box dial primitive lives in one place.
// wire.DialContext is still the underlying transport; overlay.Dial
// is the per-compute-node wrapper that ADR-025 axis 3 promises.
func DialVMMContext(ctx context.Context, target string, tlsCfg *tls.Config) (*VMMClient, error) {
	if target == "" {
		return nil, errors.New("sched: empty vmmd target")
	}
	conn, err := overlay.Dial(ctx, overlay.New(target), tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("sched: dial vmmd %q: %w", target, err)
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
			DeploymentId:      snap.DeploymentID,
			VmstatePath:       snap.VMStatePath,
			FcVersion:         snap.FCVersion,
			StorageKey:        snap.StorageKey,
			VmstateStorageKey: snap.VMStateStorageKey,
		},
	})
	if err != nil {
		return nil, liftErr(err)
	}
	return outcomeFromProto(resp), nil
}

func (c *VMMClient) PauseAndSnapshot(ctx context.Context, instance, vmstatePath, storageKey, vmstateStorageKey string) (SnapshotBytes, error) {
	resp, err := c.cli.PauseAndSnapshot(ctx, &vmmdpb.PauseAndSnapshotRequest{
		Instance:          instance,
		VmstatePath:       vmstatePath,
		StorageKey:        storageKey,
		VmstateStorageKey: vmstateStorageKey,
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

// Ping implements VMM. Wire-level liveness probe (issue #97 /
// ADR-025 axis 3, PR #114); see RoutedVMM.Ping for the contract.
func (c *VMMClient) Ping(ctx context.Context) (*PingOutcome, error) {
	resp, err := c.cli.Ping(ctx, &vmmdpb.PingRequest{})
	if err != nil {
		return nil, liftErr(err)
	}
	out := &PingOutcome{FcVersion: resp.GetFcVersion()}
	if t := resp.GetServerTime(); t != nil {
		out.ServerTime = t.AsTime()
	}
	return out, nil
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
		BaseKey:         a.BaseKey,
		LayerKey:        a.LayerKey,
		VcpuCount:       a.VCPUCount,
		MemSizeMib:      a.MemSizeMiB,
		EgressMbit:      a.EgressMbit,
		SealedEnv:       sealed,
		EgressAllowlist: a.EgressAllowlist,
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
