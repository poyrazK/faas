package scheddgrpc

import (
	"context"
	"errors"
	"fmt"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
func Dial(socketPath string) (*Client, error) {
	if socketPath == "" {
		return nil, errors.New("scheddgrpc: empty schedd socket path")
	}
	conn, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("scheddgrpc: dial schedd %q: %w", socketPath, err)
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

// Wake asks schedd to bring up an instance for appID and returns which instance
// serves it and the address it should be proxied to (host_ip:8080). The instance
// id lets the gateway attribute last_request_at touches (ADR-018). Admission
// denials arrive as an *api.Problem so gateway.writeWakeError maps them straight
// to the right RFC 7807 status. Satisfies gateway.Scheduler.
func (c *Client) Wake(ctx context.Context, appID string) (string, string, error) {
	resp, err := c.cli.Wake(ctx, &scheddpb.WakeRequest{AppId: appID})
	if err != nil {
		return "", "", liftErr(err)
	}
	return resp.GetInstanceId(), resp.GetAddr(), nil
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
