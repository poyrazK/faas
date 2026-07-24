package scheddgrpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Client is gatewayd's handle to schedd's gRPC surface (ADR-018). It satisfies
// the gateway.Scheduler shape (Wake) and carries the last_request_at flush
// (ReportActivity) — schedd is the sole writer to `instances`, so the gateway
// hands it activity batches rather than touching the table (CLAUDE.md ownership).
type Client struct {
	conn *grpc.ClientConn
	cli  scheddpb.ScheddClient
}

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
func (c *Client) Wake(ctx context.Context, appID string) (instanceID, nodeID, wakeID string, err error) {
	resp, err := c.cli.Wake(ctx, &scheddpb.WakeRequest{AppId: appID})
	if err != nil {
		return "", "", "", liftErr(err)
	}
	return resp.GetInstanceId(), resp.GetNodeId(), resp.GetWakeId(), nil
}

// AdmitInstance (issue #168) is the schedule scale-out RPC. Distinct
// from Wake: it skips the Phase-1 "return newest RUNNING" shortcut so
// each call either admits a new instance or signals at_capacity=true.
//
// Return shape:
//   - instanceID, nodeID, wakeID: non-empty on the admitted path,
//     empty on the at-capacity path.
//   - atCapacity: true when the app is already at effective
//     max_concurrency. The gateway treats this as a benign no-op
//     when it already has ≥1 cached target.
//   - err: non-nil only on real admission failures (RAM headroom,
//     chooser, store). The benign app_concurrency_reached outcome is
//     never lifted to an error.
func (c *Client) AdmitInstance(ctx context.Context, appID string) (instanceID, nodeID, wakeID string, atCapacity bool, err error) {
	resp, err := c.cli.AdmitInstance(ctx, &scheddpb.AdmitInstanceRequest{AppId: appID})
	if err != nil {
		return "", "", "", false, liftErr(err)
	}
	return resp.GetInstanceId(), resp.GetNodeId(), resp.GetWakeId(), resp.GetAtCapacity(), nil
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
func (c *Client) ParkInstance(ctx context.Context, instanceID, reason string) error {
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
