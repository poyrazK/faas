// Package scheddgrpc turns pkg/sched.Engine into the gRPC service defined in
// api/proto/onebox/faas/schedd/v1 (ADR-018). Handlers stay thin — each wraps a
// single Engine call and translates its result into the proto envelope, routing
// errors through pkg/grpcerr so the gateway maps them straight to RFC 7807.
// Mirrors pkg/vmmdgrpc on the vmmd side.

package scheddgrpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/sched/instancestats"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// LogFrameSink is the per-frame callback the StreamAppLogs handler
// invokes for each frame decoded from the per-instance vmmd Logs
// RPC. It returns a non-nil error to abort the stream (the gRPC
// trailer carries it back to the apid caller); a nil return tells
// the handler to keep delivering the next frame.
//
// Shape (PR-B / issue #517 acceptance #4): the callback receives
// a typed sched.LogFrame (single argument) so the same path emits
// both line frames and gap frames. The IsGap field is the additive
// discriminator; pre-PR-B sinks that ignore it see only line
// frames. Type aliased to sched.LogFrameSink so callers in
// pkg/sched can name it without importing pkg/scheddgrpc.
//
// The production caller (Server.StreamAppLogs) renders the frame
// to a scheddpb.StreamAppLogsResponse proto and forwards it on
// the caller's gRPC stream. That work is bounded by the per-frame
// size (≤ a few KB); long-running work inside the callback would
// stall backpressure on the matching vmmd Logs stream.
//
// The writer goroutine that owns the gRPC stream only ever calls
// this from the select-arm that owns the Send — so the callback
// is serialised with the gRPC write and cannot race with the
// reader-side Recv.
type LogFrameSink = sched.LogFrameSink

// WarmHintSink is the per-event callback the StreamWarmHints
// handler invokes for each WarmHintEvent decoded from the engine's
// warmHintBroadcaster. Same shape as LogFrameSink — non-nil error
// aborts the stream, nil keeps delivering.
//
// Type-aliased here (not defined) so the SchedAPI interface
// signature can name scheddgrpc.WarmHintSink without pulling pkg/
// sched into every test that fakes the engine. Mirrors the
// LogFrameSink alias above.
type WarmHintSink = sched.WarmHintSink

// CapacitySink is the per-event callback the ReportCapacity
// handler invokes for each CapacityReport decoded from the
// vmmd→schedd stream (ADR-025 axis 5). Same shape as
// WarmHintSink — non-nil error aborts the stream, nil keeps
// delivering.
//
// Type-aliased here so the SchedAPI interface can name
// scheddgrpc.CapacitySink without pulling pkg/sched into every
// test that fakes the engine. The production caller
// (Server.ReportCapacity) applies the report to the engine's
// per-node table via this sink.
type CapacitySink = sched.CapacitySink

// LogFrame is the transport-neutral mirror of
// scheddpb.StreamAppLogsResponse (ADR-043 / Move 4 acceptance #5).
// Type-aliased to sched.LogFrame so there is exactly ONE struct
// definition to keep in sync when new additive gap fields land
// (issue #517 / PR-B adds IsGap / GapToWrittenAt / GapReason). The
// scheddgrpc package still owns the wire encode/decode + the
// Recv adapter that surfaces this typed frame to callers; the
// struct itself lives in pkg/sched where the per-instance
// goroutine constructs it.
//
// Pre-aliased callers (pkg/apislogs.RenderAppLogEvent,
// pkg/apislogs.RenderAppLogGap, cmd/gatewayd-internal) need no change:
// scheddgrpc.LogFrame and sched.LogFrame are the same type.
type LogFrame = sched.LogFrame

// mapStreamErr translates a non-nil Recv / engine-drain
// error into the wire codes the gateway / vmmd reconnect
// loops speak. Used by StreamWarmHints and ReportCapacity
// (and any future client-streaming handler that follows the
// same shape).
//
//   - io.EOF → caller handles separately (SendAndClose ack)
//   - context.Canceled / DeadlineExceeded → codes.Canceled
//   - everything else → codes.Unavailable
//
// Extracted from the inline switch in PR-1 review to keep
// each handler body under the "≤ 50 lines" cap from
// CLAUDE.md.
func mapStreamErr(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.Canceled, err.Error())
	default:
		return status.Error(codes.Unavailable, err.Error())
	}
}

// SchedAPI is the slice of pkg/sched.Engine the handlers need. Defined here (not
// imported as a concrete type) so unit tests can pass a fake without standing up
// a store + vmmd.
type SchedAPI interface {
	// Wake (issue #556 / PR-C): the optional deploymentID hint is
	// passed through to the engine; empty falls through to the
	// newest live deployment (Phase-1 fast path). Additive per
	// ADR-016.
	Wake(ctx context.Context, appID, deploymentID string) (sched.WakeResult, error)
	// AdmitInstance is the schedule scale-out primitive (issue #168).
	// Bypasses the Phase-1 fast-path so a gateway can demand a new
	// instance even when others are already RUNNING. Returns a typed
	// AtCapacity result on the benign "already at max_concurrency"
	// outcome — see sched.WakeResult.AtCapacity.
	// AdmitInstance is the schedule scale-out primitive (issue #168).
	// Bypasses the Phase-1 fast-path so a gateway can demand a new
	// instance even when others are already RUNNING. Returns a typed
	// AtCapacity result on the benign "already at max_concurrency"
	// outcome — see sched.WakeResult.AtCapacity.
	//
	// deploymentID (issue #556 / PR-C): the optional per-deployment
	// wake hint for the wake-fan-out path. Empty falls through to
	// the engine's default (newest live deployment) — the legacy
	// single-deployment path. Non-empty asks the engine to admit on
	// that specific live deployment. Additive per ADR-016.
	AdmitInstance(ctx context.Context, appID, deploymentID string) (sched.WakeResult, error)
	// EnsureWake (ADR-095) is the single-flight-safe wake RPC. The engine
	// coalesces every concurrent EnsureWake call for the same app into one
	// virtual boot; followers see the leader's outcome. The leader runs
	// on a detached ctx (context.Background + WakeQueueTTLSeconds); only
	// followers see the caller's ctx.
	//
	// Returns sched.ErrQueueFull when the per-app follower cap is exceeded
	// (handlers map to ResourceExhausted / wake_queue_full) and
	// sched.ErrAppDeleted when the app was deleted while a wake was in
	// flight (Forget route; C6 wires the pg-notify channel).
	EnsureWake(ctx context.Context, appID string) (sched.CoordOutcome, error)
	ReportActivity(ctx context.Context, touches []state.InstanceTouch) (int, error)
	// ParkWithReason is the meterd-triggered variant (M7, spec §4.7).
	// The reason string is for the audit log; the park semantics are
	// identical to the idle-reaper Park.
	ParkWithReason(ctx context.Context, instanceID, reason string) error
	// StreamAppLogs (issue #254 / Move 4, issue #517 / PR-B
	// acceptance #3 + #4) fans out the per-instance log stream
	// to a callback. The engine resolves the live instances,
	// opens one vmmd Logs RPC per instance, and invokes the
	// sink for every frame (initial-page + live tail) until
	// the context cancels. Returns nil on a clean shutdown; the
	// underlying gRPC / vmmd errors propagate as-is so the
	// handlers can decide to lift or map them.
	//
	// sinceWrittenAt (PR-B acceptance #3) is the host-side
	// lower bound on the per-instance written_at; the zero time
	// is the "no bound" sentinel and is skipped on the wire.
	//
	// deploymentID (PR-B acceptance #3) is the per-deployment
	// soft scoping; empty = fan out to every live instance.
	StreamAppLogs(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, sink LogFrameSink) error
	// StreamWarmHints (ADR-025 axis 4) is the push-side
	// sticky-warm affinity stream. The engine fans out
	// WarmHintEvents from its warmHintBroadcaster to the sink
	// until the context cancels. Returns nil on a clean
	// shutdown; a non-nil sink error propagates so the
	// handler can carry it back as a gRPC trailer. Engine
	// emits only on (appID, nodeID) changes, so the wire is
	// quiet for the steady-state "same-app-same-node" path.
	StreamWarmHints(ctx context.Context, sink WarmHintSink) error

	// CapacitySink (ADR-025 axis 5) returns a closure the
	// ReportCapacity handler invokes per stream Recv. The
	// closure applies the report to the engine's per-node
	// table; nil is tolerated (the handler treats nil as
	// "no cache; silently drop every report"). Returning
	// the sink rather than a full table reference keeps
	// the table type unexported in pkg/sched — a deliberate
	// narrow seam so tests can fake the engine with a
	// single function field.
	CapacitySink() CapacitySink

	// NodeKeyRegistry (ADR-053 slice 3) returns the
	// sched-side signature verification registry. nil
	// disables signature verification — the handler
	// accepts every report as in pre-slice-3. Slice-3
	// schedd always returns a non-nil registry; tests
	// pass nil to bypass verification.
	NodeKeyRegistry() *sched.NodeKeyRegistry

	// DestroyForLivenessFailure (issue #554 / ADR-078)
	// is the vmmd-triggered destroy path. The vmmd
	// poll goroutine calls this RPC when the
	// consecutive-failure counter reaches the per-plan
	// N (default 3). The handler returns nil on a
	// non-RUNNING instance (state-machine guard
	// returns nil rather than a gRPC error so a
	// re-report doesn't trip the error path). The
	// typed error path surfaces real failures
	// (db hitches, missing rows).
	DestroyForLivenessFailure(ctx context.Context, instanceID, reason string) error
}

// StatsReader is the per-instance snapshot the schedd's
// instancestats.Poller publishes. The scheddgrpc server exposes
// this to meterd via ListInstanceStats (issue #279 / PR-B). A thin
// interface keeps pkg/scheddgrpc decoupled from pkg/sched — tests
// pass a fake without standing up the poller.
type StatsReader interface {
	SnapshotAll() []instancestats.InstanceStat
}

// Server implements scheddpb.ScheddServer.
type Server struct {
	scheddpb.UnimplementedScheddServer

	engine SchedAPI
	stats  StatsReader
	ops    *wire.OpsMetrics
	log    *slog.Logger
	// owner is the durable Phase 2 / Gate A shard key this schedd
	// serves. Empty for the legacy single-box posture. Set via
	// (*Server).WithOwner at wiring time (cmd/schedd resolves it
	// from cfg.NodeName → compute_nodes.id at startup). The
	// ownership guard (authorizeApp / authorizeInstance) consults
	// this on every gRPC handler that mutates or admits state.
	owner OwnerNodeID
	// resolver is the slice of state.Store the ownership guard
	// needs to load app + instance rows. nil is safe: the
	// ownership guard short-circuits to "in-process" when owner
	// is empty (the legacy single-box posture); a non-empty owner
	// with a nil resolver trips an Internal status error so a
	// wiring bug surfaces as 500 rather than a silent
	// FailedPrecondition.
	resolver AppResolver
}

// New wires the server. ops may be nil (a throwaway registry used by
// bufconn tests); log may be nil (slog default). The default ops
// registry is namespaced "schedd" so metrics from a misconfigured
// test harness don't collide with production series.
func New(engine SchedAPI, ops *wire.OpsMetrics, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	if ops == nil {
		ops = wire.NewOpsMetrics("schedd")
	}
	return &Server{engine: engine, ops: ops, log: log}
}

// NewWithStats wires the server with an instancestats.Reader for
// the ListInstanceStats RPC. Production calls this from cmd/schedd;
// tests that don't exercise CPU stats use plain New. Issue #279 /
// PR-B.
func NewWithStats(engine SchedAPI, stats StatsReader, ops *wire.OpsMetrics, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	if ops == nil {
		ops = wire.NewOpsMetrics("schedd")
	}
	return &Server{engine: engine, stats: stats, ops: ops, log: log}
}

// WithOwner stamps the Phase 2 / Gate A owner id + the AppResolver
// the guard reads. Safe to call concurrently with the gRPC
// handlers: reads of s.owner race only with the initial stamp and
// the one-tick-delayed fallback to "in-process" on the very first
// RPC is benign — cmd/schedd wires WithOwner before Register
// binds the gRPC server. If the stamp is forgotten on a multi-node
// fleet, the very first inbound Wake returns codes.Internal with
// "owner node id not configured" so a wiring bug fails loud.
func (s *Server) WithOwner(owner OwnerNodeID, resolver AppResolver) *Server {
	if s == nil {
		return s
	}
	s.owner = owner
	s.resolver = resolver
	return s
}

// Register binds s to a gRPC server.
func (s *Server) Register(g *grpc.Server) {
	scheddpb.RegisterScheddServer(g, s)
}

// Wake asks the engine to bring up an instance for the app and returns its
// address. Admission denials arrive as *api.Problem and travel as a
// ResourceExhausted status the gateway turns into a 503.
func (s *Server) Wake(ctx context.Context, req *scheddpb.WakeRequest) (*scheddpb.WakeResponse, error) {
	const op = "Wake"
	// Phase 2 / Gate A: ownership guard. Empty owner = legacy
	// single-box posture (in-process); non-empty owner =
	// FailedPrecondition on mismatch. The gateway's per-node
	// schedd cache should never dial the wrong schedd, but a
	// stale direct dial hits this guard and returns 503 to the
	// customer rather than silently waking the wrong fleet's
	// instances.
	if _, err := authorizeApp(ctx, s.owner, s.resolver, req.GetAppId()); err != nil {
		return nil, err
	}
	start := time.Now()
	res, err := s.engine.Wake(ctx, req.GetAppId(), req.GetDeploymentId())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &scheddpb.WakeResponse{
		InstanceId:   res.InstanceID,
		NodeId:       res.NodeID,
		Method:       mapMethod(res.Method),
		WakeId:       res.WakeID,
		Port:         int32(res.Port),
		DeploymentId: res.DeploymentID,
	}, nil
}

// AdmitInstance (issue #168) is the schedule scale-out RPC. Unlike
// Wake it does not run the Phase-1 fast-path: each call either admits
// a fresh instance or returns at_capacity=true. The gateway calls
// this to fan-out across max_concurrency without hitting the
// WakeGate's single-flight coalescing.
//
// Three outcomes map to three wire shapes:
//   - admitted:        instance_id/node_id/wake_id populated, at_capacity=false
//   - at_capacity:    at_capacity=true, identity fields empty
//   - failure:        ResourceExhausted / Internal status with the
//     RFC 7807 problem in the response's `problem`
//     field — only on real admission errors (RAM
//     headroom, chooser, store). The benign
//     app_concurrency_reached outcome is NEVER lifted
//     to a gRPC error: it surfaces as at_capacity=true
//     so the gateway can treat it as a no-op.
func (s *Server) AdmitInstance(ctx context.Context, req *scheddpb.AdmitInstanceRequest) (*scheddpb.AdmitInstanceResponse, error) {
	const op = "AdmitInstance"
	if _, err := authorizeApp(ctx, s.owner, s.resolver, req.GetAppId()); err != nil {
		return nil, err
	}
	start := time.Now()
	res, err := s.engine.AdmitInstance(ctx, req.GetAppId(), req.GetDeploymentId())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &scheddpb.AdmitInstanceResponse{
		InstanceId: res.InstanceID,
		NodeId:     res.NodeID,
		Method:     mapMethod(res.Method),
		WakeId:     res.WakeID,
		AtCapacity: res.AtCapacity,
		Port:       int32(res.Port),
		// deployment_id (issue #556 / PR-B) — see engine.go WakeResult
		// doc comment. Empty on the at-capacity path; "" pre-PR-B callers
		// see empty and the gateway treats that as "single-deployment
		// legacy mode" (Target.DeploymentID empty, picker collapses).
		DeploymentId: res.DeploymentID,
		// request_count (ADR-095 C9) — populated by the batched
		// writer (request_count column added by 00216). 0 on
		// at-capacity paths; the engine's WakeResult.RequestCount
		// is read after the Ledger admit and stamped on the
		// wire for the gateway's per-instance cache.
		RequestCount: int32(res.RequestCount),
	}, nil
}

// EnsureWake (ADR-095) is the single-flight-safe wake RPC. The engine
// coalesces every concurrent EnsureWake call for the same app into one
// virtual boot; followers see the leader's outcome.
//
// Pre-ADR-095 callers continue to use Wake / AdmitInstance on the
// legacy wire — this method is additive per ADR-016.
func (s *Server) EnsureWake(ctx context.Context, req *scheddpb.EnsureWakeRequest) (*scheddpb.EnsureWakeResponse, error) {
	const op = "EnsureWake"
	if _, err := authorizeApp(ctx, s.owner, s.resolver, req.GetAppId()); err != nil {
		return nil, err
	}
	start := time.Now()
	out, err := s.engine.EnsureWake(ctx, req.GetAppId())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		// Per-app queue-full is a 503; the engine returns the typed
		// sentinel. Other failures (ErrAppDeleted, ledger, chooser,
		// store) are forwarded as RFC 7807 problems via toProblem.
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	// CoordOutcome can carry a non-nil Err even when the engine's
	// direct return is nil — the leader's ensure path encodes
	// wake-level failures (ErrAppDeleted, ErrQueueFull, ledger
	// errors set by Forget) onto out.Err so followers inherit
	// them. Otherwise the gateway would observe a phantom 200
	// with empty Instance fields.
	if out.Err != nil {
		return nil, grpcerr.ToStatus(toProblem(out.Err))
	}
	if out.Instance == nil {
		// Defensive: a successful EnsureWake with nil instance is a
		// bug — surface it as an internal problem so the gateway
		// sees a 500 rather than a phantom 200.
		return nil, grpcerr.ToStatus(api.NewProblem(int(codes.Internal), "ensure_wake_nil_instance",
			"engine EnsureWake returned nil instance without error", ""))
	}
	return &scheddpb.EnsureWakeResponse{
		InstanceId:   out.Instance.InstanceID,
		NodeId:       out.Instance.NodeID,
		Method:       coordMethodToWakeMethod(out.Instance),
		WakeId:       out.Instance.WakeID,
		Port:         out.Instance.Port,
		DeploymentId: out.Instance.DeploymentID,
	}, nil
}

// coordMethodToWakeMethod maps the coordinator's ColdBoot bool to the
// WakeMethod enum. The legacy Wake / AdmitInstance paths go through
// mapMethod (see below) which copies the vmmdpb.WakeMethod verbatim;
// the coordinator collapses to a bool because the per-caller awaiting
// approach doesn't need to round-trip the original WakeMethod through
// the vmmd RPC layer.
func coordMethodToWakeMethod(ci *sched.CoordInstance) scheddpb.WakeMethod {
	if ci.ColdBoot {
		return scheddpb.WakeMethod_WAKE_COLD_BOOT
	}
	return scheddpb.WakeMethod_WAKE_RESTORE
}

// ReportActivity persists a last_request_at batch from the gateway.
func (s *Server) ReportActivity(ctx context.Context, req *scheddpb.ReportActivityRequest) (*scheddpb.ReportActivityResponse, error) {
	const op = "ReportActivity"
	// Phase 2 / Gate A: each touch belongs to a single instance;
	// ownership is per-instance (load the parent app + compare
	// node_id). A bad-routed touch is dropped silently via the
	// for-loop below rather than failing the whole batch —
	// the gateway already partitions touches by owner via
	// instance.node_id before dialling, so this loop is the
	// second-line check (defence-in-depth).
	in := req.GetTouches()
	touches := make([]state.InstanceTouch, 0, len(in))
	for _, t := range in {
		if _, err := authorizeInstance(ctx, s.owner, s.resolver, t.GetInstanceId()); err != nil {
			// Drop silently: the legacy ReportActivity already
			// tolerates dropped touches (a non-owner instance
			// belongs to a different schedd that will receive
			// the touch on its own dial).
			continue
		}
		touches = append(touches, state.InstanceTouch{
			InstanceID:   t.GetInstanceId(),
			LastRequest:  time.UnixMilli(t.GetUnixMs()),
			RequestDelta: t.GetRequestDelta(),
		})
	}
	start := time.Now()
	applied, err := s.engine.ReportActivity(ctx, touches)
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &scheddpb.ReportActivityResponse{Applied: int32(applied)}, nil
}

// ParkInstance is the meterd-driven explicit park (M7, spec §4.7).
// Idempotent: parking an already-parked instance is a no-op + Ok=true.
func (s *Server) ParkInstance(ctx context.Context, req *scheddpb.ParkInstanceRequest) (*scheddpb.ParkInstanceResponse, error) {
	const op = "ParkInstance"
	if _, err := authorizeInstance(ctx, s.owner, s.resolver, req.GetInstanceId()); err != nil {
		return nil, err
	}
	start := time.Now()
	err := s.engine.ParkWithReason(ctx, req.GetInstanceId(), req.GetReason())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		// ErrNotFound → NotFound status; everything else Internal.
		if errors.Is(err, state.ErrNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &scheddpb.ParkInstanceResponse{Ok: true}, nil
}

// ReportLivenessFailed (issue #554 / ADR-078) is the vmmd
// poll-goroutine's drain into the sched state machine. vmmd's
// per-instance loop increments a consecutive-failure counter
// against the guest-init vsock 1028 STREAM listener; once the
// counter reaches the per-plan N (default 3), vmmd fires this
// RPC. Schedd's response drives the lazy snapshot-stale mark
// (ADR-005), the per-app restart-window counter
// (LivenessWindow — see pkg/sched/liveness_window.go), and
// eventually an Engine.Park path if the window fires the
// park threshold (3 restarts / 300 s).
//
// The reason field is the closed set the vmmd run-time
// classifies into the relay:
//
//   - liveness_timeout       — read deadline fired
//   - liveness_conn_refused  — guest-init listener not up
//   - liveness_conn_err      — wire-shape or syscall failure
//   - liveness_non_200       — guest-init returned a non-2xx
//   - liveness_n_consecutive — the catch-all "counter reached N"
//
// Anything outside the closed set is still accepted by the
// schedd side (the reason string flows verbatim into the
// audit-log emit) so a future classification drift in vmmd
// doesn't trip the handler with an InvalidArgument. The
// closure of the set is enforced on the vmmd side, not here.
//
// Idempotent (mirrors ParkInstance): if the instance is no
// longer RUNNING (already parked by the idle reaper, mid-restore
// after the previous restart) the engine returns nil and the
// RPC replies with Ok=true. The vmmd loop has already exited
// on its end so re-reports are benign.
//
// Wire error mapping:
//
//   - codes.OK + Ok=true on success or no-op
//   - codes.NotFound when the instance_id doesn't resolve
//     (state.ErrNotFound from the engine)
//   - codes.Unauthenticated / codes.PermissionDenied if the
//     ownership guard rejects (defence-in-depth; vmmd always
//     dials the schedd on the same node)
//   - codes.Internal for any other engine failure (db hitches,
//     pg_notify backlog, etc.)
//
// Additive per ADR-016: the new RPC + new messages append at
// the end of the proto file.
func (s *Server) ReportLivenessFailed(ctx context.Context, req *scheddpb.LivenessFailedReport) (*scheddpb.LivenessFailedAck, error) {
	const op = "ReportLivenessFailed"
	if _, err := authorizeInstance(ctx, s.owner, s.resolver, req.GetInstanceId()); err != nil {
		return nil, err
	}
	start := time.Now()
	err := s.engine.DestroyForLivenessFailure(ctx, req.GetInstanceId(), req.GetReason())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &scheddpb.LivenessFailedAck{Ok: true}, nil
}

// StreamAppLogs (issue #254 / Move 4, issue #517 / PR-B
// acceptance #3 + #4) implements the schedd-side half of the
// per-app log stream. It fans out the per-instance vmmd Logs
// RPCs into a single server-streaming response so the apid /
// gatewayd-internal consumer can render one SSE stream per app.
//
// Two phases that mirror the vmmd Logs RPC:
//
//  1. Initial page — every line with Seq > since_seq, emitted in
//     per-instance arrival order (then instance order, so a
//     reconnect carries the same frame sequence the client last
//     saw — identical to the vmmd snapshot semantics).
//  2. Live tail — schedd subscribes to each live instance's
//     vmmd Logs stream and emits new lines as they arrive. On
//     instance death (park / snapshot) the per-instance stream
//     ends with io.EOF and the next iteration of the outer loop
//     drops that goroutine from the wart set.
//
// PR-B extends the request envelope with two additive fields:
//
//   - deployment_id: per-deployment soft scoping. Empty = fan
//     out to every live instance.
//   - since_written_at: host-side lower bound on the
//     per-instance written_at. Zero = no bound.
//
// The response envelope gains issue #517 / PR-B acceptance #4's
// gap signalling: when vmmd reports a synthetic gap frame
// (cursor fell below the ring's high-water mark), the per-
// instance vmmd.Recv returns a LogLine with IsGap=true and the
// per-frame sink translates it into an `is_gap=true` response
// carrying the GapToWrittenAt timestamp so the caller can
// render a meaningful diagnostic. Wire is additive per
// ADR-016.
//
// Backpressure: the per-instance vmmd Logs stream is gRPC; the
// apid-side caller's gRPC stream is also gRPC. The two-stage
// pipeline keeps the per-instance ring's Subscribe channel drops
// bounded by the round-trip time of the matching vmmd RPC.
//
// Wire error mapping:
//
//   - codes.NotFound when the app has zero live instances — apid
//     maps this to its 404 "the app is parked; wake it first"
//   - codes.OK + nil on clean shutdown (caller cancels, all
//     instances died)
//
// Additive per ADR-016: the new RPC + new messages append at the
// end of the proto file.
func (s *Server) StreamAppLogs(req *scheddpb.StreamAppLogsRequest, stream scheddpb.Schedd_StreamAppLogsServer) error {
	const op = "StreamAppLogs"
	start := time.Now()
	var sendErr error
	defer func() {
		s.ops.Observe(op, time.Since(start), sendErr)
	}()
	if req.GetAppId() == "" {
		sendErr = status.Error(codes.InvalidArgument, "app_id is required")
		return sendErr
	}
	// Phase 2 / Gate A: ownership guard. Logs stream only over
	// apps this schedd owns; the stream opens from the first
	// non-terminal instance under apps.node_id == owner. A
	// non-owner dial returns FailedPrecondition immediately so
	// the apid SSE handler surfaces a clean 4xx rather than
	// hanging on a closed stream.
	if _, err := authorizeApp(stream.Context(), s.owner, s.resolver, req.GetAppId()); err != nil {
		sendErr = err
		return err
	}
	sinceSeq := req.GetSinceSeq()
	if sinceSeq < 0 {
		sinceSeq = 0
	}
	sinceWrittenAt := req.GetSinceWrittenAt().AsTime()
	deploymentID := req.GetDeploymentId()
	// Parse the customer-supplied level/grep filters once per
	// RPC (issue #309 / tier-2 DX). Pre-#309 the gateway parsed
	// these and discarded them with `_ = level; _ = grep`; the
	// additive proto fields (StreamAppLogsRequest.level/grep,
	// ADR-016) carry them from gatewayd-internal → schedd and we apply
	// them at the per-instance fan-out so a single regex
	// compile / level matcher covers all instances. Both empty
	// = NoFilter; the gateway already validated the regex and
	// the level enum at parse time, so a Compile failure here
	// would only fire under a buggy direct RPC client. We log
	// + reject rather than silently dropping the request.
	filter, err := ParseLogFilter(req.GetLevel(), req.GetGrep())
	if err != nil {
		// InvalidArgument is the right code: the level/grep
		// values are caller-supplied, and the gateway already
		// validates them at parse time (apislogs.ValidateLogFilters).
		// Reaching this branch means a buggy direct-RPC client
		// (tests, future SDKs) — surface the error so the
		// operator can see it in the gRPC trace rather than
		// silently dropping the request.
		sendErr = status.Error(codes.InvalidArgument, err.Error())
		return sendErr
	}
	// sink is the per-frame callback that writes one
	// StreamAppLogsResponse onto the caller's gRPC stream. We
	// synchronise on stream.Context() so a cancelled caller
	// short-circuits the vmmd-side drain immediately.
	//
	// Shape (PR-B acceptance #4): the callback receives the
	// typed sched.LogFrame (single-arg) so the same path emits
	// both line frames and gap frames. IsGap=true on the latter
	// flips the response into the `is_gap`/`gap_to_written_at`
	// arm; the line-frame fields stay zero (matches the wire
	// shape mirrors the vmmd proto).
	sink := func(f sched.LogFrame) error {
		if stream.Context().Err() != nil {
			return stream.Context().Err()
		}
		// Filter (issue #309 / tier-2 DX). Apply BEFORE building
		// the proto response so a filtered line costs us only
		// the MatchLine check, not the proto marshal + Send.
		// Gap frames always pass through — the gap signal is
		// sequencing metadata the customer must see regardless
		// of grep/level (a dropped gap would hide a stall from
		// the customer's session, defeating the issue #254
		// "show me when logs stopped" guarantee). The counter
		// increments only on line-frame drops so the dashboard
		// panel surfaces a real rate.
		if !f.IsGap && !filter.NoFilter() && !filter.MatchLine(f.Line) {
			// MatchLine already returned false — the line
			// failed at least one active filter. Recompute
			// the per-filter result inline so the counter
			// credits the actually-failing filter (a line
			// that passes the level floor but fails the
			// grep substring is a grep drop, not a level
			// drop). When BOTH filters reject the line we
			// attribute it to filter_level — customers tend
			// to set --level first as the broad noise filter
			// and add --grep to narrow further, so a line
			// that fails both is most often "below the floor
			// and would have been dropped anyway"; the
			// operator looking at the rate can read the
			// `apid_logs_dropped_total` panel by reason.
			grepOK := filter.Grep == "" || strings.Contains(strings.ToLower(f.Line), strings.ToLower(filter.Grep))
			levelOK := filter.Level == nil || filter.Level.Match(f.Line)
			switch {
			case !grepOK && !levelOK:
				s.ops.IncLogDropped("filter_level")
			case !grepOK:
				s.ops.IncLogDropped("filter_grep")
			default:
				s.ops.IncLogDropped("filter_level")
			}
			return nil
		}
		if f.IsGap {
			resp := &scheddpb.StreamAppLogsResponse{
				InstanceId: f.InstanceID,
				IsGap:      true,
				GapReason:  f.GapReason,
			}
			if !f.GapToWrittenAt.IsZero() {
				resp.GapToWrittenAt = timestamppb.New(f.GapToWrittenAt)
			}
			return stream.Send(resp)
		}
		resp := &scheddpb.StreamAppLogsResponse{
			InstanceId: f.InstanceID,
			Seq:        f.Seq,
			Stream:     f.Stream,
			Line:       f.Line,
		}
		if !f.WrittenAt.IsZero() {
			resp.WrittenAt = timestamppb.New(f.WrittenAt)
		}
		return stream.Send(resp)
	}
	if err := s.engine.StreamAppLogs(stream.Context(), req.GetAppId(), sinceSeq, sinceWrittenAt, deploymentID, sink); err != nil {
		// Engine.StreamAppLogs surfaces "no live instances" as
		// state.ErrNotFound (apid maps this to its 404 "the app
		// is parked; wake it first"). A clean caller cancel
		// surfaces as context.Canceled — gRPC's expected
		// behaviour on a dropped streaming caller (codes.Canceled
		// is what the apid sees). Anything else (per-instance
		// vmmd dial failure that escalated to a Send error) lands
		// here as codes.Unavailable so the apid can render a
		// degraded frame and let the consumer retry.
		sendErr = err
		switch {
		case errors.Is(err, state.ErrNotFound):
			return status.Error(codes.NotFound, err.Error())
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return status.Error(codes.Canceled, err.Error())
		default:
			return status.Error(codes.Unavailable, err.Error())
		}
	}
	return nil
}

// StreamWarmHints (ADR-025 axis 4) implements the schedd-side
// half of the sticky-warm affinity stream. The engine owns the
// warmHintBroadcaster (pkg/sched/warmhint.go) and emits a
// WarmHintEvent every time a RecordWake actually changes
// (appID → nodeID); this handler subscribes via
// Engine.StreamWarmHints and forwards each event on the caller's
// gRPC stream until the context cancels.
//
// Wire error mapping:
//
//   - codes.OK + nil on clean shutdown (caller cancels, or
//     Engine returns nil after the broadcaster closes)
//   - codes.Canceled when the caller cancels mid-stream
//   - codes.Unavailable for any other unexpected drain failure
//     (a non-nil sink error from the proto Send, or an unexpected
//     state from the engine). The gateway consumer freezes its
//     cache on Unavailable and reconnects — see
//     cmd/gatewayd-internal/warmhints.go::Run for the policy.
//
// Observability note: the single Observe call below times the
// entire stream lifetime (open → close), not per-event. Long-
// lived streams skew the latency histogram toward minute-scale
// buckets; the next metric-review pass should split this into
// a per-event observation (or a separate events-counter) so the
// histogram bucket boundaries can be tuned to admission bursts
// rather than connection duration. The shape is consistent with
// StreamAppLogs; fixing both together is a follow-up ADR.
//
// Additive per ADR-016: new RPC + new messages append at the end.
func (s *Server) StreamWarmHints(req *scheddpb.StreamWarmHintsRequest, stream scheddpb.Schedd_StreamWarmHintsServer) error {
	const op = "StreamWarmHints"
	start := time.Now()
	var sendErr error
	defer func() {
		s.ops.Observe(op, time.Since(start), sendErr)
	}()
	// sink is the per-event callback that writes one
	// StreamWarmHintsResponse onto the caller's gRPC stream.
	// Same shape as StreamAppLogs's sink: synchronise on
	// stream.Context() so a cancelled caller short-circuits the
	// engine-side drain immediately.
	sink := func(ev sched.WarmHintEvent) error {
		if stream.Context().Err() != nil {
			return stream.Context().Err()
		}
		resp := &scheddpb.StreamWarmHintsResponse{
			AppId:  ev.AppID,
			NodeId: ev.NodeID,
		}
		if !ev.WrittenAt.IsZero() {
			resp.WrittenAt = timestamppb.New(ev.WrittenAt)
		}
		return stream.Send(resp)
	}
	if err := s.engine.StreamWarmHints(stream.Context(), sink); err != nil {
		sendErr = err
		return mapStreamErr(err)
	}
	return nil
}

// ReportCapacity (ADR-025 axis 5) is the schedd-side half of the
// vmmd→schedd live-capacity push. vmmd is the gRPC client and
// sends one CapacityReport per second on a long-lived stream;
// the handler decodes each report, applies it to the engine's
// per-node capacity table via the SchedAPI.CapacitySink seam,
// and replies with a single ReportCapacityAck on stream close.
//
// Slice 3 (ADR-053) adds node_signature verification at the
// stream boundary: every report carries a 64-byte ECDSA-P-256
// (r||s) signature over the canonical payload plus a key_id
// pointer into the schedd-side NodeKeyRegistry. A bad signature
// rejects the whole stream (not the bad frame) with
// codes.Unauthenticated so an attacker can't DoS by injecting
// one valid frame + 1000 garbage ones — the stream closes and
// vmmd reconnects. Pre-slice-3 schedd (nil registry) skips
// verification entirely (back-compat).
//
// Wire error mapping:
//
//   - codes.OK + nil on clean shutdown (vmmd cancels after
//     CloseAndRecv completes, or sends CloseAndRecv naturally
//     when its loop exits)
//   - codes.Canceled when vmmd cancels mid-stream
//   - codes.Unavailable for any other unexpected drain failure
//     (non-EOF / non-Canceled Recv error, or a sink-side failure)
//   - codes.InvalidArgument if a report's node_id is empty —
//     a programming bug in vmmd; the handler must NOT silently
//     drop empty-id reports because that would let a future
//     publisher regression poison the cache silently.
//   - codes.Unauthenticated on signature failure (slice-3 strict).
//
// Observability note: same shape as StreamWarmHints — the single
// Observe call times the entire stream lifetime, not per-event.
// Long-lived streams skew the latency histogram toward
// minute-scale buckets; the next metric-review pass should split
// this into a per-event observation (or a separate events-counter).
// The shape is consistent with StreamAppLogs and StreamWarmHints;
// fixing all three together is a follow-up ADR.
//
// Additive per ADR-016: new RPC + new messages append at the end.
func (s *Server) ReportCapacity(stream scheddpb.Schedd_ReportCapacityServer) error {
	const op = "ReportCapacity"
	start := time.Now()
	var sendErr error
	defer func() {
		s.ops.Observe(op, time.Since(start), sendErr)
	}()
	table := s.engine.CapacitySink()
	if table == nil {
		// Pre-axis-5 fixture (no engine-side table). Block
		// until ctx cancel so the gRPC trailer carries codes.OK
		// + nil. A real vmmd in production never sees this
		// path because production always wires the table.
		<-stream.Context().Done()
		return nil
	}
	// Slice-3 signature verification. A nil registry means
	// pre-slice-3 schedd — skip verification (back-compat).
	// A non-nil registry means slice-3 strict: every report
	// must carry a valid signature or the stream closes.
	keys := s.engine.NodeKeyRegistry()
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// vmmd closed the send side cleanly; reply with
			// the typed ack.
			return stream.SendAndClose(&scheddpb.ReportCapacityAck{})
		}
		if err != nil {
			sendErr = err
			return mapStreamErr(err)
		}
		// ADR-016: empty node_id is a programming bug.
		// Surface as codes.InvalidArgument so vmmd's publisher
		// logs + reconnects instead of silently dropping.
		if msg.GetNodeId() == "" {
			sendErr = status.Error(codes.InvalidArgument, "capacity report missing node_id")
			return sendErr
		}
		report := sched.CapacityReport{
			NodeID:        msg.GetNodeId(),
			SampledAt:     time.UnixMilli(msg.GetSampledAtUnixMs()),
			LiveCount:     msg.GetLiveCount(),
			LeasedCount:   msg.GetLeasedCount(),
			UsedMB:        msg.GetUsedMb(),
			RAMHeadroomMB: msg.GetRamHeadroomMb(),
			VCPUBusy:      msg.GetVcpuBusy(),
			NodeSignature: msg.GetNodeSignature(),
			NodeKeyID:     msg.GetNodeKeyId(),
		}
		// Slice-3 verification. Pre-slice-3 vmmd sends an
		// empty signature; pre-slice-3 schedd has keys == nil
		// so this block is skipped. Slice-3 vmmd + slice-3
		// schedd verifies; slice-3 vmmd + pre-slice-3 schedd
		// (legacy) silently accepts — the additive field is
		// unused on the legacy side.
		if keys != nil {
			if verr := sched.VerifyNodeSignature(report, report.NodeSignature, keys); verr != nil {
				sendErr = status.Errorf(codes.Unauthenticated,
					"capacity report signature rejected: %v", verr)
				s.log.Warn("schedd: capacity signature rejected; closing stream",
					"node_id", report.NodeID, "err", verr)
				// ADR-053 §3: one increment per rejected stream
				// (the handler closes the stream on the first
				// bad frame, so per-frame increment would
				// over-count). The operator's "which node?"
				// question is answered by the audit log
				// stream-rejection event, not by the metric
				// label — the counter is unlabelled.
				if c := s.ops.CapacitySignatureRejected(); c != nil {
					c.Inc()
				}
				return sendErr
			}
		}
		// Apply to the table. The sink closure handles the
		// empty-nodeid no-op as a defensive fallback (the
		// explicit check above is the load-bearing gate).
		if err := table(report); err != nil {
			sendErr = err
			return status.Error(codes.Unavailable, err.Error())
		}
	}
}

// ListInstanceStats returns the per-instance CPU-µs snapshot the
// schedd's instancestats.Poller maintains. The meterd sampler
// calls this once per minute and computes the per-min CPU delta
// per instance, writing it to usage_minutes.cpu_usec. Issue #279 /
// PR-B.
//
// The response carries every live instance's current row
// (deterministic (app_id, instance_id) order — the Reader's
// contract). The cpu_valid field mirrors instancestats.Validity:
// 0 = Valid, 1 = Unknown. Callers MUST skip rows where
// cpu_valid != 0; otherwise the post-regression baseline becomes
// "stuck" on the pre-regression counter and the per-minute delta
// is meaningless. The vmmd cpustats.Cache already drops the
// baseline on regression, so the next sample the poller sees comes
// back as Valid and the meterd sampler picks up from the new
// counter.
//
// stats may be nil (e.g. a test harness that doesn't wire the
// Reader); the handler returns an empty response rather than a
// gRPC error so the meterd sampler can degrade to "no CPU data"
// without restarting the loop.
// int32SliceToProto (issue #463 / ADR-070 / PR-C) maps an
// int-sidecar-MB slice to the protobuf []int32 shape. Nil/empty
// yields nil so the wire stays compact (no empty repeated field
// over the gRPC stream). The proto field's int32 width is
// deliberately wider than the SidecarCapMax * 32..512 ceiling
// (= 1024) — the int32 width is for future-proofing, not a
// near-term budget signal.
func int32SliceToProto(in []int) []int32 {
	if len(in) == 0 {
		return nil
	}
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}

func (s *Server) ListInstanceStats(ctx context.Context, _ *scheddpb.ListInstanceStatsRequest) (*scheddpb.ListInstanceStatsResponse, error) {
	const op = "ListInstanceStats"
	start := time.Now()
	rows := make([]*scheddpb.InstanceStatsRow, 0)
	if s.stats != nil {
		// Time the SnapshotAll fan-out + per-row proto-build
		// separately on `schedd_list_instance_stats_collect_seconds`
		// (issue #279 / PR-B / ADR-039) so an operator can graph
		// the CPU rate-and-accumulator hot path on the schedd
		// side in isolation from the rest of the RPC.
		cpuStart := time.Now()
		for _, r := range s.stats.SnapshotAll() {
			row := &scheddpb.InstanceStatsRow{
				InstanceId:    r.InstanceID,
				AppId:         r.AppID,
				NodeId:        r.NodeID,
				CpuUsec:       r.CPUUsageUsec,
				CpuValid:      uint32(r.CPU),
				NetTxBytes:    r.TXBytes,
				TxValid:       uint32(r.TX),
				SidecarRamMbs: int32SliceToProto(r.SidecarMBs),
			}
			rows = append(rows, row)
		}
		if h := s.ops.CPUStatsCollectDuration(); h != nil {
			h.Observe(time.Since(cpuStart).Seconds())
		}
	}
	s.ops.Observe(op, time.Since(start), nil)
	return &scheddpb.ListInstanceStatsResponse{Rows: rows}, nil
}

// mapMethod translates the engine's vmmd-side WakeMethod to the schedd wire
// enum. The two enums share values (0=cold boot, 1=restore); the switch keeps
// them decoupled if either drifts.
func mapMethod(m vmmdpb.WakeMethod) scheddpb.WakeMethod {
	if m == vmmdpb.WakeMethod_WAKE_RESTORE {
		return scheddpb.WakeMethod_WAKE_RESTORE
	}
	return scheddpb.WakeMethod_WAKE_COLD_BOOT
}

// toProblem lifts a plain error to *api.Problem if it isn't one already, so
// internal go-stack details never leak across the wire.
func toProblem(err error) *api.Problem {
	if err == nil {
		return nil
	}
	if errors.Is(err, sched.ErrQueueFull) {
		return api.NewProblem(int(codes.ResourceExhausted), "wake_queue_full",
			"schedd per-app wake queue is full", err.Error())
	}
	if errors.Is(err, sched.ErrAppDeleted) {
		return api.NewProblem(int(codes.FailedPrecondition), "app_deleted",
			"app was deleted while wake was in flight", err.Error())
	}
	if p := api.AsProblem(err); p != nil {
		return p
	}
	return api.NewProblem(int(codes.Internal), "internal", "schedd operation failed", err.Error())
}
