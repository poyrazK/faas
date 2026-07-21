// Package vmmdgrpc turns pkg/fcvm.Manager into the gRPC service defined in
// api/proto/onebox/faas/vmmd/v1. Handlers stay thin — each one wraps a single
// Manager call and translates its result into the proto envelope. The wire
// shape is fixed by ADR-013/014/016; this file does not invent fields.
//
// Every handler ≤ 50 lines per spec §Conventions line 472. Anything bigger
// gets extracted to proto.go (type adapters) or stats.go (Stats workload).

package vmmdgrpc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// VmmdAPI is the slice of pkg/fcvm.Manager that the handlers need. Defined
// here (not imported) so the unit tests can pass a fake without depending
// on the firecracker side effects.
type VmmdAPI interface {
	Wake(ctx context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error)
	Park(ctx context.Context, instance string, spec fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error)
	Destroy(ctx context.Context, instance string) error
	DestroyWithExport(ctx context.Context, instance, exportDir string) (int, error)
	LiveCount() int
	LeasedCount() int
}

// Server implements vmmdpb.VmmdServer.
type Server struct {
	vmmdpb.UnimplementedVmmdServer

	vmm   VmmdAPI
	ops   *wire.OpsMetrics
	fcVer string
	log   *slog.Logger
}

// New wires the server. ops may be nil (noop metrics), log may be nil
// (slog default).
func New(vmm VmmdAPI, ops *wire.OpsMetrics, fcVer string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	if ops == nil {
		// Use a fresh registry with a no-op prefix; observe still records but
		// never exported. Tests that don't assert metrics use this path.
		ops = wire.NewOpsMetrics("vmmd_test")
	}
	return &Server{vmm: vmm, ops: ops, fcVer: fcVer, log: log}
}

// Register binds s to a gRPC server.
func (s *Server) Register(g *grpc.Server) {
	vmmdpb.RegisterVmmdServer(g, s)
}

// CreateFromSnapshot wires the snapshot-restore path. Falls back to cold
// boot inside Manager.Wake — the response's `method` reports what
// actually happened. ADR-005 is enforced one layer down.
func (s *Server) CreateFromSnapshot(ctx context.Context, req *vmmdpb.CreateFromSnapshotRequest) (*vmmdpb.WakeResponse, error) {
	const op = "CreateFromSnapshot"
	start := time.Now()
	wr, err := toWakeRequest(req)
	if err != nil {
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	inst, err := s.vmm.Wake(ctx, wr)
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return wakeResponseFromInstance(req.GetInstance(), wr, inst, vmmdpb.WakeMethod_WAKE_RESTORE), nil
}

// CreateColdBoot primes an instance for the deploy-pipeline first-boot
// path (no snapshot).
func (s *Server) CreateColdBoot(ctx context.Context, req *vmmdpb.CreateColdBootRequest) (*vmmdpb.WakeResponse, error) {
	const op = "CreateColdBoot"
	start := time.Now()
	wr, err := toColdBootRequest(req)
	if err != nil {
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	inst, err := s.vmm.Wake(ctx, wr)
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		s.log.Error("vmmd: cold boot failed", "instance", req.GetInstance(), "err", err.Error())
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return wakeResponseFromInstance(req.GetInstance(), wr, inst, vmmdpb.WakeMethod_WAKE_COLD_BOOT), nil
}

// PauseAndSnapshot parks an instance, writing its full snapshot to the
// requested files. Destroy happens inside Manager.Park.
func (s *Server) PauseAndSnapshot(ctx context.Context, req *vmmdpb.PauseAndSnapshotRequest) (*vmmdpb.SnapshotResponse, error) {
	const op = "PauseAndSnapshot"
	start := time.Now()
	if req.GetMemPath() == "" || req.GetVmstatePath() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing paths", "mem_path and vmstate_path are required").
			WithDocs("https://docs/DOMAIN/vmmd#pause")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	info, err := s.vmm.Park(ctx, req.GetInstance(), fcvm.SnapshotSpec{
		MemPath:     req.GetMemPath(),
		VMStatePath: req.GetVmstatePath(),
		// #96 / ADR-025 axis 2: when set, vmmd publishes the mem blob
		// under this StorageBackend key alongside the legacy in-place
		// move. Empty keeps the mem-path-only workflow intact for one
		// release — the migration slice flips the contract.
		StorageKey: req.GetStorageKey(),
	})
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.SnapshotResponse{
		MemBytes:     info.MemBytes,
		VmstateBytes: info.VMStateBytes,
	}, nil
}

// Destroy tears down an instance. Idempotent for unknown instances. The
// optional ExportDir (passed via CreateColdBoot.BuildSpec and remembered by
// vmmd) triggers a build-aware teardown: vmmd waits for the in-VM build to
// exit, captures the exit code, and copies /build/out/* + build-done.json
// into ExportDir before releasing the chroot. The response carries the exit
// code on the wire so builderd can classify (FailureUserError / OOM / Timeout).
func (s *Server) Destroy(ctx context.Context, req *vmmdpb.DestroyRequest) (*vmmdpb.DestroyResponse, error) {
	const op = "Destroy"
	start := time.Now()
	exportDir := s.exportDirFor(req.GetInstance())
	code, err := s.vmm.DestroyWithExport(ctx, req.GetInstance(), exportDir)
	if err != nil {
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	s.ops.Observe(op, time.Since(start), nil)
	return &vmmdpb.DestroyResponse{Instance: req.GetInstance(), ExitCode: int32(code)}, nil
}

// exportDirFor asks the Manager whether the instance was registered as a
// builder VM at cold-boot. App VMs return "" (so the gRPC Destroy stays
// backwards-compatible — same teardown behaviour as before M6).
func (s *Server) exportDirFor(instance string) string {
	if getter, ok := s.vmm.(interface {
		ExportDirFor(string) string
	}); ok {
		return getter.ExportDirFor(instance)
	}
	return ""
}

// Stats returns Manager's view: live/leased counts and per-instance
// resident bytes sourced from cgroup memory.current.
func (s *Server) Stats(ctx context.Context, _ *vmmdpb.StatsRequest) (*vmmdpb.StatsResponse, error) {
	const op = "Stats"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), nil) }()

	resp := &vmmdpb.StatsResponse{
		LiveCount:   int32(s.vmm.LiveCount()),
		LeasedCount: int32(s.vmm.LeasedCount()),
	}

	resident, ok := leakcheck.ResidentBytes()
	if !ok {
		// Non-Linux host (dev). Unset rather than zero — DistinctValue
		// semantics let dashboards distinguish "no data" from "0".
		resp.TotalResidentBytes = nil
		return resp, nil
	}

	var total int64
	resp.Instances = make([]*vmmdpb.InstanceStats, 0, len(resident))
	for inst, b := range resident {
		total += b
		resp.Instances = append(resp.Instances, &vmmdpb.InstanceStats{
			Instance:      inst,
			ResidentBytes: wrapperspb.Int64(b),
		})
	}
	resp.TotalResidentBytes = wrapperspb.Int64(total)
	return resp, nil
}

// toProblem lifts a plain error to *api.Problem if it isn't one already.
// Manager errors are *fmt.Errorf-wrapped strings, so we synthesise an
// Internal problem rather than risk leaking go-internals across the wire.
func toProblem(err error) *api.Problem {
	if err == nil {
		return nil
	}
	if p := api.AsProblem(err); p != nil {
		return p
	}
	return api.NewProblem(int(codes.Internal), "internal",
		"vmmd operation failed", err.Error())
}

// unused import guard.
var _ = fmt.Sprintf
