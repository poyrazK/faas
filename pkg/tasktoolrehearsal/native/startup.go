// Package native wires hosted Task acceptance to the private scheduler and
// gateway clients on the dedicated acceptance host.
package native

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/taskrehearsal"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// Startup binds the owner entry to the enrolled node's existing clients. The
// caller supplies the host enrollment check and trusted fixture source pins.
func Startup(store *state.PgStore, scheduler *scheddgrpc.Client, nodes gateway.NodeClientLookup, nodeID string,
	fixture taskrehearsal.StartupFixture, checkHost func(ctx context.Context, accountID, nodeID string) error,
	log *slog.Logger) (*taskrehearsal.NativeStartup, error) {
	if store == nil || scheduler == nil || nodes == nil || checkHost == nil {
		return nil, state.ErrInvalidArgument
	}
	forward := gateway.ForwardingReverseProxyWithEvents(nodes, log, nil)
	return &taskrehearsal.NativeStartup{
		Store: store, NodeID: nodeID, SourceSHA256: fixture, CheckHost: checkHost,
		WakeWeb: scheduler.Wake,
		OpenClaimLogs: func(ctx context.Context, app, deployment string, since time.Time, event string) (taskrehearsal.ClaimLogStream, error) {
			stream, err := scheduler.StreamAppLogs(ctx, app, 0, since, true, deployment, "", event)
			if err != nil {
				return nil, err
			}
			return func() (taskrehearsal.ClaimLogFrame, error) {
				frame, err := stream.Recv()
				return taskrehearsal.ClaimLogFrame{InstanceID: frame.InstanceID, DeploymentID: frame.DeploymentID,
					Stream: frame.Stream, Line: frame.Line, WrittenAt: frame.WrittenAt, IsGap: frame.IsGap}, err
			}, nil
		},
		Forward: func(ins state.Instance, port int) http.RoundTripper {
			return pinnedTransport{forward(gateway.Target{
				AppID: ins.AppID, DeploymentID: ins.DeploymentID, NodeID: ins.NodeID,
				InstanceID: ins.ID, WakeID: ins.WakeID, Port: port,
			})}
		},
	}, nil
}

// Recovery adds the private local scheduler interruption to the pinned
// startup fixture. Use a client connected to the enrolled schedd Unix socket.
func Recovery(store *state.PgStore, scheduler *scheddgrpc.Client, nodes gateway.NodeClientLookup, nodeID string,
	fixture taskrehearsal.StartupFixture, checkHost func(ctx context.Context, accountID, nodeID string) error,
	log *slog.Logger) (*taskrehearsal.NativeRecovery, error) {
	startup, err := Startup(store, scheduler, nodes, nodeID, fixture, checkHost, log)
	if err != nil {
		return nil, err
	}
	return &taskrehearsal.NativeRecovery{NativeStartup: startup, Interrupt: scheduler.InterruptHostedTaskWorker}, nil
}

// The acceptance client only needs two small, finite MCP replies. The
// handler is the private per-instance forwarder, with no HTTP listener.
type pinnedTransport struct {
	handler http.Handler
}

func (t pinnedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer func() { _ = req.Body.Close() }()
	}
	reply := &boundedReply{ResponseRecorder: httptest.NewRecorder()}
	t.handler.ServeHTTP(reply, req)
	if reply.overflow {
		return nil, errReplyTooLarge
	}
	return reply.Result(), nil
}

const maxReplyBytes = 64 << 10

var errReplyTooLarge = errors.New("private rehearsal MCP reply exceeds its capture limit")

type boundedReply struct {
	*httptest.ResponseRecorder
	overflow bool
}

func (r *boundedReply) Write(data []byte) (int, error) {
	if err := r.checkSize(len(data)); err != nil {
		return 0, err
	}
	return r.ResponseRecorder.Write(data)
}

func (r *boundedReply) WriteString(data string) (int, error) {
	if err := r.checkSize(len(data)); err != nil {
		return 0, err
	}
	return r.ResponseRecorder.WriteString(data)
}

func (r *boundedReply) checkSize(size int) error {
	if r.overflow || size > maxReplyBytes-r.Body.Len() {
		r.overflow = true
		return errReplyTooLarge
	}
	return nil
}
