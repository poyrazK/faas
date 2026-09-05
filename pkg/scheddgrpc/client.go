package scheddgrpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ScheddClient is the gateway-side interface a per-node schedd
// router holds (Phase 2 / Gate A). Production wires it to *Client;
// tests substitute a fake so the router doesn't need a real gRPC
// dial. The surface is the union of every gRPC callgatewayd-internal
// makes against schedd: admit, wake, activity flush, log stream,
// warmhint stream, close. Each method maps 1:1 to a method on
// *Client, so any fake only needs to forward the same shape.
type ScheddClient interface {
	// AdmitInstance (issue #272 / ADR-095 / PR-B): scope is the
	// preview scope (`pr-{N}`) forwarded from the gateway's
	// Host-header parse. Empty = prod (legacy single-deployment
	// behaviour). Threaded through schedd's AdmitInstanceRequest.
	//
	// trigger (ADR-127): wake-boot trigger enum value forwarded to
	// schedd's AdmitInstanceRequest.Trigger wire field and stamped
	// on the emitted wake.boot_started / wake.boot_completed events.
	AdmitInstance(ctx context.Context, appID, deploymentID, scope, trigger string) (instanceID, nodeID, deploymentIDOut, wakeID string, method int32, atCapacity bool, port int, err error)
	// Wake (issue #556 / PR-C): deploymentID is the optional
	// per-deployment wake hint forwarded to schedd. Empty falls
	// through to the newest live deployment. Return tuple gains
	// deploymentIDOut (the deployment schedd actually woke onto;
	// "" on error).
	//
	// scope (PR-B): preview scope forwarded to schedd.
	Wake(ctx context.Context, appID, deploymentID, scope string) (instanceID, nodeID, deploymentIDOut, wakeID string, port int, err error)
	// EnsureWake (ADR-098) is the schedd-side single-flight wake entry.
	// Schedd coalesces every concurrent EnsureWake for the same app into
	// one virtual boot; followers see the leader's outcome. Pre-ADR-098
	// callers continue to use Wake / AdmitInstance on the legacy wire —
	// this method is additive per ADR-016.
	EnsureWake(ctx context.Context, appID, trigger string) (instanceID, nodeID, deploymentIDOut, wakeID string, method int32, port int, err error)
	// AdmitMirrorInstance (issue #72 / ADR-124 / ADR-125 PR-A3) is
	// the mirror-VM admission sibling. Schedd stamps mode='mirror'
	// on the new instances row (PR-A1's 00385) and the per-rule
	// concurrent-mirror-VM cap (default 5) gates the dispatch.
	AdmitMirrorInstance(ctx context.Context, appID, mirrorDeploymentID, mirrorRuleID string) (instanceID, wakeID string, err error)
	ReportActivity(ctx context.Context, touches []state.InstanceTouch) (int, error)
	// ParkInstance (PR-#TBD / C6): traceID is the optional
	// OTel-format 32-char-hex value forwarded via the gRPC
	// x-faas-trace-id metadata envelope (see
	// wire.CorrelationFields.TraceID). Empty string = no
	// envelope attached; the schedd-side handler then sees
	// zero trace_id context. The gregalectl CLI generates a
	// fresh trace_id on every force-* invocation (mirrors
	// the apid middleware at pkg/middleware/traceid.go) so
	// the operator's incident log joins to the schedd-side
	// audit emit on a single key.
	ParkInstance(ctx context.Context, instanceID, reason, traceID string) error
	// StreamAppLogs (issue #309 / tier-2 DX): level + grep are
	// the customer-facing --level / --grep filters forwarded
	// to schedd (issue #254 / Move 4). Both empty = no filter;
	// schedd applies them at the per-instance fan-out and
	// increments apid_logs_dropped_total{reason=...} on drop.
	StreamAppLogs(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, level string, grep string) (LogStream, error)
	StreamWarmHints(ctx context.Context) (WarmHintStream, error)
	Close() error
}

// Client is gatewayd-internal's handle to schedd's gRPC surface (ADR-018). It satisfies
// the gateway.Scheduler shape (Wake) and carries the last_request_at flush
// (ReportActivity) — schedd is the sole writer to `instances`, so the gateway
// hands it activity batches rather than touching the table (CLAUDE.md ownership).
type Client struct {
	conn *grpc.ClientConn
	cli  scheddpb.ScheddClient
}

// compile-time assertion: *Client satisfies ScheddClient so the
// per-node schedd router can type its cache as ScheddClient without
// a wrapping shim. Add new methods to the ScheddClient surface as
// they appear on *Client; the compiler will refuse to drift.
var _ ScheddClient = (*Client)(nil)

// Dial opens a lazy gRPC connection to schedd's unix socket. As with vmmd
// (ADR-015) the socket's 0660/group-`faas` DAC is the only auth in v1.0, so the
// transport uses insecure credentials over a trusted local socket. The
// connection dials on first RPC; Dial never blocks on schedd being up.
//
// This is the legacy entrypoint retained for source compatibility with
// existing callers and tests; production code should call DialContext so the
// caller's context controls the dial. Issue #95 keeps the legacy shape
// working unchanged.
func Dial(socketPath string) (*Client, error) {
	return DialContext(context.Background(), socketPath, nil)
}

// DialContext opens a lazy gRPC connection to schedd. tlsCfg is required
// for tcp/dns targets (issue #95); a nil tlsCfg is fine for the
// single-box unix default. Wire layer performs the mTLS gating — see
// pkg/wire.DialContext.
func DialContext(ctx context.Context, target string, tlsCfg *tls.Config) (*Client, error) {
	if target == "" {
		return nil, errors.New("scheddgrpc: empty schedd target")
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("scheddgrpc: dial schedd %q: %w", target, err)
	}
	return &Client{conn: conn, cli: scheddpb.NewScheddClient(conn)}, nil
}

// NewClient wraps an already-dialed connection (used by bufconn tests).
func NewClient(conn *grpc.ClientConn) *Client {
	return &Client{conn: conn, cli: scheddpb.NewScheddClient(conn)}
}

// Close releases the underlying connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Wake asks schedd to bring up an instance for appID and returns the
// instance id + the compute_node.id the instance lives on
// (issue #98 / ADR-028) + the per-wake correlation handle
// (gaps analysis 2026-07-23).
//
//   - instanceID: instances.id row PK. Stable per-row; lets the
//     gateway attribute last_request_at touches (ADR-018).
//   - nodeID: compute_node.id (uuid). Lets the gateway look up the
//     per-node vmmd gRPC client in its routing cache.
//   - wakeID: the per-wake correlation handle. On the Phase-1
//     fast-path (instance already RUNNING) this is the wake_id of
//     the wake that brought the instance up, surfaced from the row;
//     on every other path it's the UUIDv7 schedd minted in Phase 2.
//     Propagated to the client as x-faas-wake-id.
//
// Admission denials arrive as an *api.Problem so gateway.writeWakeError
// maps them straight to the right RFC 7807 status. Satisfies
// gateway.Scheduler.
func (c *Client) Wake(ctx context.Context, appID, deploymentID, scope string) (instanceID, nodeID, deploymentIDOut, wakeID string, port int, err error) {
	resp, err := c.cli.Wake(ctx, &scheddpb.WakeRequest{AppId: appID, DeploymentId: deploymentID, Scope: scope})
	if err != nil {
		return "", "", "", "", 0, liftErr(err)
	}
	return resp.GetInstanceId(), resp.GetNodeId(), resp.GetDeploymentId(), resp.GetWakeId(), int(resp.GetPort()), nil
}

// AdmitInstance (issue #168) is the schedule scale-out RPC. Distinct
// from Wake: it skips the Phase-1 "return newest RUNNING" shortcut so
// each call either admits a new instance or signals at_capacity=true.
//
// Return shape:
//   - instanceID, nodeID, wakeID: non-empty on the admitted path,
//     empty on the at-capacity path.
//   - method: the wake-outcome schedd actually performed (PR
//     scale-out readiness). The wire value is scheddpb.WakeMethod;
//     this method returns it as int32 so pkg/gateway can translate
//     it via gateway.scheddWakeMethodToGateway without importing the
//     protobuf package directly. WAKE_RESTORE (proto value 1) and
//     WAKE_COLD_BOOT (proto value 0) pass through; any other value
//     is left as-is and the gateway default-branch maps it to
//     WakeMethodColdBoot (the safer "slow but always works" outcome,
//     matching scheddgrpc.mapMethod's defense).
//   - atCapacity: true when the app is already at effective
//     max_concurrency. The gateway treats this as a benign no-op
//     when it already has ≥1 cached target.
//   - err: non-nil only on real admission failures (RAM headroom,
//     chooser, store). The benign app_concurrency_reached outcome is
//     never lifted to an error.
//   - deploymentID (issue #556 / PR-B): the live deployment id the
//     new instance was admitted for. Empty on the at-capacity path;
//     "" pre-PR-B callers see empty and the gateway treats that as
//     "single-deployment legacy mode". Schedd surfaces this from
//     Engine.AdmitInstance's WakeResult.DeploymentID (engine.go).
//
// deploymentID hint (issue #556 / PR-C): when non-empty schedd
// admits the new instance on this specific live deployment
// (wake-fan-out path); empty falls through to the newest live
// deployment (legacy single-deployment path). Additive per
// ADR-016.
func (c *Client) AdmitInstance(ctx context.Context, appID, deploymentID, scope, trigger string) (instanceID, nodeID, deploymentIDOut, wakeID string, method int32, atCapacity bool, port int, err error) {
	resp, err := c.cli.AdmitInstance(ctx, &scheddpb.AdmitInstanceRequest{AppId: appID, DeploymentId: deploymentID, Scope: scope, Trigger: trigger})
	if err != nil {
		return "", "", "", "", 0, false, 0, liftErr(err)
	}
	return resp.GetInstanceId(), resp.GetNodeId(), resp.GetDeploymentId(), resp.GetWakeId(), int32(resp.GetMethod()), resp.GetAtCapacity(), int(resp.GetPort()), nil
}

// AdmitInstances carries the scheduler's bounded-burst primitive over the
// existing AdmitInstance RPC. The first call is ordinary; continuation calls
// carry an additive marker so schedd bypasses only the per-app scale-out
// cooldown that the first call already passed. Every result is reported,
// including individual continuation failures, and sibling calls remain
// independent just like sched.Engine.AdmitInstances.
func (c *Client) AdmitInstances(ctx context.Context, appID, scope, trigger string, count int, report func(instanceID, nodeID, deploymentID, wakeID string, method int32, atCapacity bool, port int, err error)) error {
	if count <= 0 {
		return nil
	}
	if count > api.ScaleUpMaxBurstPerTick {
		count = api.ScaleUpMaxBurstPerTick
	}
	firstID, firstNode, firstDeployment, firstWake, firstMethod, firstAtCapacity, firstPort, err := c.AdmitInstance(ctx, appID, "", scope, trigger)
	if report != nil {
		report(firstID, firstNode, firstDeployment, firstWake, firstMethod, firstAtCapacity, firstPort, err)
	}
	if err != nil || firstAtCapacity || count == 1 {
		return err
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	for i := 1; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, callErr := c.cli.AdmitInstance(ctx, &scheddpb.AdmitInstanceRequest{
				AppId:             appID,
				Scope:             scope,
				Trigger:           trigger,
				BurstContinuation: true,
			})
			if callErr != nil {
				callErr = liftErr(callErr)
				if report != nil {
					report("", "", "", "", 0, false, 0, callErr)
				}
				mu.Lock()
				if firstErr == nil {
					firstErr = callErr
				}
				mu.Unlock()
				return
			}
			if report != nil {
				report(resp.GetInstanceId(), resp.GetNodeId(), resp.GetDeploymentId(), resp.GetWakeId(), int32(resp.GetMethod()), resp.GetAtCapacity(), int(resp.GetPort()), nil)
			}
		}()
	}
	wg.Wait()
	return firstErr
}

// EnsureWake (ADR-098) is the schedd-side single-flight wake entry.
// Mirrors Engine.EnsureWake on the wire. Schedd coalesces every concurrent
// EnsureWake for the same app into one virtual boot; followers see the
// leader's outcome. Pre-ADR-098 callers continue to use Wake / AdmitInstance
// on the legacy wire — this method is additive per ADR-016.
//
// trigger (ADR-127): forwarded to the leader's Engine.Wake call and
// stamped on the emitted wake.boot_started / wake.boot_completed events.
func (c *Client) EnsureWake(ctx context.Context, appID, trigger string) (instanceID, nodeID, deploymentIDOut, wakeID string, method int32, port int, err error) {
	resp, err := c.cli.EnsureWake(ctx, &scheddpb.EnsureWakeRequest{AppId: appID, Trigger: trigger})
	if err != nil {
		return "", "", "", "", 0, 0, liftErr(err)
	}
	return resp.GetInstanceId(), resp.GetNodeId(), resp.GetDeploymentId(), resp.GetWakeId(), int32(resp.GetMethod()), int(resp.GetPort()), nil
}

// AdmitMirrorInstance (issue #72 / ADR-124 / ADR-125 PR-A3) is
// the mirror-VM admission sibling to AdmitInstance. The protocol
// reuses AdmitInstanceRequest with the new is_mirror=true +
// mirror_rule_id fields (PR-A2 / commit 2). Errors carry
// api.CodeMirrorSlotAtCapacity so the gateway can detect the
// benign cap-at-max outcome via errors.Is rather than string
// matching.
func (c *Client) AdmitMirrorInstance(ctx context.Context, appID, mirrorDeploymentID, mirrorRuleID string) (instanceID, wakeID string, err error) {
	resp, err := c.cli.AdmitInstance(ctx, &scheddpb.AdmitInstanceRequest{
		AppId:        appID,
		DeploymentId: mirrorDeploymentID,
		IsMirror:     true,
		MirrorRuleId: mirrorRuleID,
	})
	if err != nil {
		return "", "", liftErr(err)
	}
	return resp.GetInstanceId(), resp.GetWakeId(), nil
}

// ReportActivity flushes a batch of last_request_at touches to schedd. Returns
// the number of rows schedd applied (touches for parked/gone instances are
// silently dropped on its side).
func (c *Client) ReportActivity(ctx context.Context, touches []state.InstanceTouch) (int, error) {
	pb := make([]*scheddpb.Touch, 0, len(touches))
	for _, t := range touches {
		pb = append(pb, &scheddpb.Touch{
			InstanceId: t.InstanceID,
			UnixMs:     t.LastRequest.UnixMilli(),
		})
	}
	resp, err := c.cli.ReportActivity(ctx, &scheddpb.ReportActivityRequest{Touches: pb})
	if err != nil {
		return 0, liftErr(err)
	}
	return int(resp.GetApplied()), nil
}

// ParkInstance forces schedd to park one instance (M7, spec §4.7). The
// reason string is for the audit log. NotFound returns state.ErrNotFound
// so meterd's quota loop can decide to log-and-continue vs bubble up.
//
// traceID (PR-#TBD / C6) — optional OTel 32-char-hex value
// forwarded via the gRPC x-faas-trace-id envelope (see
// wire.CorrelationFields.TraceID + wire.WithCorrelationOutgoing).
// The schedd-side handler extracts it via
// wire.CorrelationFromIncoming(ctx) and stamps it onto any
// audit row the engine path emits; an empty traceID is a
// no-op (no envelope attached). The gregalectl CLI generates
// a fresh trace_id per force-* invocation so the operator's
// incident log joins to the schedd-side audit emit on a
// single key — same pattern the apid middleware
// (pkg/middleware/traceid.go) uses for the apid path.
func (c *Client) ParkInstance(ctx context.Context, instanceID, reason, traceID string) error {
	ctx = wire.WithCorrelationOutgoing(ctx, wire.CorrelationFields{TraceID: traceID})
	resp, err := c.cli.ParkInstance(ctx, &scheddpb.ParkInstanceRequest{
		InstanceId: instanceID,
		Reason:     reason,
	})
	if err != nil {
		// Map gRPC NotFound → state.ErrNotFound so the meterd quota
		// loop's errors.Is checks work against the in-memory store.
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return state.ErrNotFound
		}
		return liftErr(err)
	}
	if !resp.GetOk() {
		return state.ErrNotFound
	}
	return nil
}

// ForceColdBootNextWake (P2b of the operator-side observability
// mega-PR) asks schedd to mark a deployment's latest warm + init
// snapshots stale so the next customer Wake cold-boots from rootfs
// per ADR-005. Returns the snap IDs that were marked stale — empty
// list when the deployment has no snapshots in either tier (durable
// no-op). NotFound is mapped to state.ErrNotFound so the apid
// handler can render a 404 with code "deployment_not_found".
//
// traceID (PR-#TBD / C6) — optional OTel 32-char-hex forwarded
// via the gRPC x-faas-trace-id envelope; empty = no envelope.
// See ParkInstance doc-comment for the rationale.
func (c *Client) ForceColdBootNextWake(ctx context.Context, deploymentID, traceID string) ([]string, error) {
	ctx = wire.WithCorrelationOutgoing(ctx, wire.CorrelationFields{TraceID: traceID})
	resp, err := c.cli.ForceColdBootNextWake(ctx, &scheddpb.ForceColdBootNextWakeRequest{
		DeploymentId: deploymentID,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil, state.ErrNotFound
		}
		return nil, liftErr(err)
	}
	return resp.GetSnapIdsMarkedStale(), nil
}

// ForceRestartInstance (P2d follow-on to PR #1099) asks schedd to
// kill a live instance and flip its deployment's snapshots stale.
// Returns the snap IDs marked stale on success. Maps the wire
// outcomes:
//
//   - ok=true + snap_ids_marked_stale populated + error_msg
//     empty → happy path. Returns (snapIDs, nil).
//   - ok=true + snap_ids_marked_stale populated + error_msg
//     non-empty → partial success. Returns (snapIDs, error);
//     the caller can decide to log the destroy cause without
//     failing the operator-visible result (the durable signal
//     is the snap-stale work; the next wake WILL cold-boot).
//   - ok=false + error_msg non-empty → race-loser. Returns
//     (nil, state.ErrInstanceNotRunning) so the CLI's errors.Is
//     dispatch matches the engine-layer contract.
//   - gRPC NotFound → state.ErrNotFound (mirrors ForceColdBootNextWake).
//   - any other gRPC error → lifted via liftErr.
//
// traceID (PR-#TBD / C6) — optional OTel 32-char-hex forwarded
// via the gRPC x-faas-trace-id envelope; empty = no envelope.
// See ParkInstance doc-comment for the rationale.
func (c *Client) ForceRestartInstance(ctx context.Context, instanceID, reason, traceID string) ([]string, error) {
	ctx = wire.WithCorrelationOutgoing(ctx, wire.CorrelationFields{TraceID: traceID})
	resp, err := c.cli.ForceRestartInstance(ctx, &scheddpb.ForceRestartInstanceRequest{
		InstanceId: instanceID,
		Reason:     reason,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil, state.ErrNotFound
		}
		return nil, liftErr(err)
	}
	snapIDs := resp.GetSnapIdsMarkedStale()
	if !resp.GetOk() {
		// Race-loser posture — the engine observed a non-RUNNING
		// state on the locked re-read. Map the typed sentinel
		// back so the CLI uses errors.Is like the rest of the
		// pkg/state surface. Wrap the wire-side error_msg so the
		// operator sees both "state: instance not in running
		// state" + the engine's reason detail.
		if msg := resp.GetErrorMsg(); msg != "" {
			return nil, fmt.Errorf("%w: %s", state.ErrInstanceNotRunning, msg)
		}
		return nil, state.ErrInstanceNotRunning
	}
	if msg := resp.GetErrorMsg(); msg != "" {
		// Partial success — destroy failed but snaps were flipped.
		// Surface the destroy cause with the snap IDs; the caller
		// decides how to present it.
		return snapIDs, errors.New(msg)
	}
	return snapIDs, nil
}

// InstanceStatsRow is the typed mirror of scheddpb.InstanceStatsRow
// the meterd sampler reads. Defined here so pkg/meter doesn't reach
// into the protobuf package directly. Issue #279 / PR-B.
type InstanceStatsRow struct {
	InstanceID   string
	AppID        string
	NodeID       string
	CPUUsageUsec uint64
	// CPUValid mirrors instancestats.Validity (0 = Valid, 1 =
	// Unknown). Callers MUST skip rows where CPUValid != 0.
	CPUValid uint32
	// NetTxBytes (ADR-046) is the cumulative byte counter
	// on root-side vethHost.rx_bytes for this instance,
	// surfaced via the vmmd `net_tx_bytes` wire field. Unit
	// is interface bytes; same kernel counter the per-plan
	// tc tbf qdisc reads. TxValid mirrors instancestats.
	// Validity (0 = Valid, 1 = Unknown — first sample /
	// regression / netstats cache miss); callers MUST skip
	// rows where TxValid != 0.
	NetTxBytes uint64
	TxValid    uint32
	// NetRxBytes (ADR-048) is the cumulative byte counter on
	// root-side vethHost.tx_bytes for this instance — mirror of
	// NetTxBytes but on the root→guest (= ingress) direction.
	// Same kernel counter family (interface bytes, includes
	// Ethernet framing); same TxValid gate as egress (a cache
	// regression / first-sample state zeroes BOTH columns).
	NetRxBytes uint64
	RxValid    uint32
	// SidecarMBs (issue #463 / ADR-070 §Decision 6 / PR-C) is the
	// per-sidecar RAM slice sourced from the deployment's
	// `sidecars jsonb` column at Tick time. Empty/nil when the
	// deployment has no sidecars — the meterd sampler collapses
	// to the no-sidecar admission shutter via
	// api.BillableRAMMBWithSidecars. Length is bounded by
	// api.SidecarCapMax = 2. Mirrors the scheddpb field via the
	// ListInstanceStats RPC; meterd reads this column from the
	// Row to compute the per-minute mb_seconds.
	SidecarMBs []int
}

// ListInstanceStats returns the per-instance CPU-µs snapshot the
// schedd's instancestats.Poller maintains. The meterd sampler calls
// this once per minute and computes the per-minute CPU delta per
// instance, writing it to usage_minutes.cpu_usec. Issue #279 / PR-B.
func (c *Client) ListInstanceStats(ctx context.Context) ([]InstanceStatsRow, error) {
	resp, err := c.cli.ListInstanceStats(ctx, &scheddpb.ListInstanceStatsRequest{})
	if err != nil {
		return nil, liftErr(err)
	}
	out := make([]InstanceStatsRow, 0, len(resp.GetRows()))
	for _, r := range resp.GetRows() {
		sidecarMBs := make([]int, 0, len(r.GetSidecarRamMbs()))
		for _, v := range r.GetSidecarRamMbs() {
			sidecarMBs = append(sidecarMBs, int(v))
		}
		out = append(out, InstanceStatsRow{
			InstanceID:   r.GetInstanceId(),
			AppID:        r.GetAppId(),
			SidecarMBs:   sidecarMBs,
			NodeID:       r.GetNodeId(),
			CPUUsageUsec: r.GetCpuUsec(),
			CPUValid:     r.GetCpuValid(),
			NetTxBytes:   r.GetNetTxBytes(),
			TxValid:      r.GetTxValid(),
			NetRxBytes:   r.GetNetRxBytes(),
			RxValid:      r.GetRxValid(),
		})
	}
	return out, nil
}

// liftErr converts a schedd gRPC error back into the platform's *api.Problem so
// its stable Code + Limit/Observed survive to the gateway. Errors that aren't
// status-shaped (e.g. a dial failure) pass through unchanged. Mirrors
// sched.liftErr on the vmmd side.
func liftErr(err error) error {
	if p, ok := grpcerr.FromStatus(err); ok && p != nil {
		return p
	}
	return err
}
