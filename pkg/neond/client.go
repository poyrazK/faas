package neond

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	neondpb "github.com/onebox-faas/faas/api/proto/onebox/faas/neond/v1"
	"github.com/onebox-faas/faas/pkg/wire"
)

// BrokerEnv names neond's socket for apid's Neon backend
// (unix:///run/faas/neond.sock). Set, the backend forwards every Neon call to
// neond instead of calling Neon with a key of its own.
const BrokerEnv = "FAAS_MANAGED_POSTGRES_NEON_BROKER"

// Client forwards Neon calls to neond.
type Client struct {
	conn *grpc.ClientConn
	rpc  neondpb.NeondClient
}

// Dial connects to neond's unix socket. The connection is established
// lazily; a neond that is down fails the call, not the dial.
func Dial(ctx context.Context, target string) (*Client, error) {
	parsed, err := wire.ParseTarget(target)
	if err != nil || parsed.Scheme != wire.SchemeUnix {
		return nil, fmt.Errorf("neond: %s=%q: want unix:///path/to/neond.sock", BrokerEnv, target)
	}
	conn, err := wire.DialContext(ctx, target, nil)
	if err != nil {
		return nil, fmt.Errorf("neond: dial: %w", err)
	}
	return &Client{conn: conn, rpc: neondpb.NewNeondClient(conn)}, nil
}

// NewClient wraps an existing connection, for tests.
func NewClient(conn grpc.ClientConnInterface) *Client {
	return &Client{rpc: neondpb.NewNeondClient(conn)}
}

// Forward sends one Neon call through neond and returns Neon's status, body
// and Retry-After. A call neond refuses returns ErrRefused or ErrRateLimited.
func (c *Client) Forward(ctx context.Context, method, path, rawQuery string, body []byte) (int, []byte, string, error) {
	resp, err := c.rpc.Forward(ctx, &neondpb.ForwardRequest{Method: method, Path: path, RawQuery: rawQuery, Body: body})
	if err != nil {
		switch status.Code(err) {
		case codes.PermissionDenied:
			return 0, nil, "", fmt.Errorf("%w: %s %s", ErrRefused, method, path)
		case codes.ResourceExhausted:
			return 0, nil, "", ErrRateLimited
		default:
			return 0, nil, "", err
		}
	}
	return int(resp.GetStatus()), resp.GetBody(), resp.GetRetryAfter(), nil
}

// Close closes the connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
