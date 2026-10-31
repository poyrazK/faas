package gateway

// Planned vmmd restarts (vmmd restart adoption ADR §3). An adopting vmmd
// sets compute_nodes.vmmd_restart_notice_until as it stops, and its
// successor clears the notice once it serves. While a node's notice is
// active, a forward whose stream cannot be opened because vmmd is
// unreachable waits for the successor instead of answering 503 and
// evicting a target whose guest is still running. A stream open fails
// before any request byte is sent, so waiting is safe for every method.
//
// The successor clears the notice as it starts serving, while the gateway's
// channel may still be in gRPC's reconnect backoff from the old socket
// (about a second). So every retry first reconnects the channel and waits
// for it briefly, and one retry is made after the notice ends.
//
// Streams that are already open are still cut by the restart; carrying them
// across it is the connection-survival ADR's forwardd. A crash sets no
// notice and fails fast as before.

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

// RestartNoticeReader lists the nodes whose vmmd is restarting under an
// unexpired notice. state.VMMDRestartNoticeStore implements it.
type RestartNoticeReader interface {
	ListComputeNodeVMMDRestartNotices(ctx context.Context) (map[string]time.Time, error)
}

const (
	// restartHoldNoticeAge bounds how stale a notice read may be. The notice
	// is set just before vmmd stops, and the forwards that fail together
	// when it stops share one read.
	restartHoldNoticeAge = 100 * time.Millisecond
	// restartHoldRetryEvery bounds each reconnect wait of a held open.
	restartHoldRetryEvery = 100 * time.Millisecond
)

// restartNotices caches notice reads. Nil reports no notice.
type restartNotices struct {
	store RestartNoticeReader
	now   func() time.Time

	mu      sync.Mutex
	fetched time.Time
	until   map[string]time.Time
}

// active reports whether nodeID's vmmd is restarting under a notice. A
// failed read reports none, so the forward fails as without the notice.
func (n *restartNotices) active(ctx context.Context, nodeID string) bool {
	if n == nil || n.store == nil || nodeID == "" {
		return false
	}
	now := n.now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.until == nil || now.Sub(n.fetched) >= restartHoldNoticeAge {
		fresh, err := n.store.ListComputeNodeVMMDRestartNotices(ctx)
		if err != nil {
			return false
		}
		n.until, n.fetched = fresh, now
	}
	until, ok := n.until[nodeID]
	return ok && now.Before(until)
}

// restartHoldClient is a node's VmmdClient whose stream opens wait out a
// planned restart of that node's vmmd.
type restartHoldClient struct {
	vmmdpb.VmmdClient
	conn    *grpc.ClientConn
	node    string
	notices *restartNotices
	log     *slog.Logger
}

func (c restartHoldClient) ForwardHTTPStream(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	return holdThroughRestart(ctx, c, func() (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
		return c.VmmdClient.ForwardHTTPStream(ctx, opts...)
	})
}

func (c restartHoldClient) ForwardRawStream(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse], error) {
	return holdThroughRestart(ctx, c, func() (grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse], error) {
		return c.VmmdClient.ForwardRawStream(ctx, opts...)
	})
}

func (c restartHoldClient) ForwardTCPStream(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardTCPRequest, vmmdpb.ForwardTCPResponse], error) {
	return holdThroughRestart(ctx, c, func() (grpc.BidiStreamingClient[vmmdpb.ForwardTCPRequest, vmmdpb.ForwardTCPResponse], error) {
		return c.VmmdClient.ForwardTCPStream(ctx, opts...)
	})
}

func (c restartHoldClient) ForwardUDPStream(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardUDPRequest, vmmdpb.ForwardUDPResponse], error) {
	return holdThroughRestart(ctx, c, func() (grpc.BidiStreamingClient[vmmdpb.ForwardUDPRequest, vmmdpb.ForwardUDPResponse], error) {
		return c.VmmdClient.ForwardUDPStream(ctx, opts...)
	})
}

// holdThroughRestart opens a stream, and while the open fails because the
// node's vmmd is unreachable under an active notice, reconnects and retries
// it until the successor accepts, the request ends or the hold limit
// passes. One retry follows the end of the notice, and one is made without
// a notice: the channel may still be backing off from a vmmd that is back.
func holdThroughRestart[S any](ctx context.Context, c restartHoldClient, open func() (S, error)) (S, error) {
	s, err := open()
	if !restartHoldable(err) {
		return s, err
	}
	start := c.notices.now()
	deadline := start.Add(api.GatewayVMMDRestartHoldSeconds * time.Second)
	for {
		last := !c.notices.active(ctx, c.node)
		c.reconnect(ctx, restartHoldRetryEvery)
		s, err = open()
		if err == nil && c.log != nil {
			c.log.Info("gateway: forward held through a vmmd restart",
				"node", c.node, "held_ms", c.notices.now().Sub(start).Milliseconds())
		}
		if !restartHoldable(err) || last || ctx.Err() != nil || !c.notices.now().Before(deadline) {
			return s, err
		}
	}
}

// reconnect resets the channel's reconnect backoff, so the successor's
// socket is dialed at once, and waits up to d for the channel to be ready.
func (c restartHoldClient) reconnect(ctx context.Context, d time.Duration) {
	wait, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	if c.conn == nil {
		<-wait.Done()
		return
	}
	c.conn.ResetConnectBackoff()
	c.conn.Connect()
	for state := c.conn.GetState(); state != connectivity.Ready; state = c.conn.GetState() {
		if !c.conn.WaitForStateChange(wait, state) {
			return
		}
	}
}

// restartHoldable reports an open that failed because vmmd was unreachable.
func restartHoldable(err error) bool {
	return err != nil && status.Code(err) == codes.Unavailable
}
