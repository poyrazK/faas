package gateway

import (
	"context"
	"errors"
	"io"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DatagramPeer represents one public endpoint/client pair. Send and Receive
// must honor context cancellation; Receive transfers ownership of its payload.
// The listener retains its shared socket, which this transport never closes.
type DatagramPeer interface {
	Receive(context.Context) ([]byte, error)
	Send(context.Context, []byte) error
}

type UDPForwarder struct {
	Nodes        NodeClientLookup
	MaxBytes     int64
	MaxDatagrams uint64
	IdleTimeout  time.Duration
}

// ServePeer connects an already admitted peer to one instance UDP port.
func (f UDPForwarder) ServePeer(ctx context.Context, peer DatagramPeer, target Target) error { //nolint:contextcheck // the caller context is augmented with activity-based idle cancellation.
	if f.Nodes == nil || peer == nil {
		return status.Error(codes.FailedPrecondition, "UDP forwarder requires node lookup and peer")
	}
	if target.InstanceID == "" || target.Port < 1 || target.Port > 65535 {
		return status.Error(codes.InvalidArgument, "UDP target requires live instance and guest port 1..65535")
	}
	idle := f.IdleTimeout
	if idle <= 0 {
		idle = api.UDPIdleTimeoutDefault
	}
	session := newIdleSession(ctx, idle)
	defer session.stop()
	streamCtx, cancel := context.WithCancel(session.ctx)
	defer cancel()
	cli, closer, ok := f.Nodes.ClientFor(streamCtx, target.NodeID) //nolint:contextcheck // streamCtx inherits the caller through the idle session and adds peer cancellation.
	if !ok || cli == nil {
		return status.Error(codes.Unavailable, "UDP compute node unavailable")
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}
	stream, err := cli.ForwardUDPStream(streamCtx) //nolint:contextcheck // streamCtx inherits the caller through the idle session and adds peer cancellation.
	if err != nil {
		return err
	}
	maxBytes, maxDatagrams := f.MaxBytes, f.MaxDatagrams
	if maxBytes <= 0 || maxBytes > api.UDPStreamMaxBytes {
		maxBytes = api.UDPStreamMaxBytes
	}
	if maxDatagrams == 0 || maxDatagrams > api.UDPStreamMaxDatagrams {
		maxDatagrams = api.UDPStreamMaxDatagrams
	}
	if err := stream.Send(&vmmdpb.ForwardUDPRequest{Frame: &vmmdpb.ForwardUDPRequest_Init{Init: &vmmdpb.ForwardUDPRequestInit{Instance: target.InstanceID, Port: uint32(target.Port), MaxBytes: maxBytes, MaxDatagrams: maxDatagrams}}}); err != nil {
		return err
	}
	ready, err := stream.Recv()
	if err != nil {
		return err
	}
	if ready.GetInit() == nil {
		return status.Error(codes.Unavailable, "UDP readiness frame missing")
	}
	if ready.GetInit().Error != "" {
		return status.Error(codes.Unavailable, ready.GetInit().Error)
	}
	sent := make(chan error, 1)
	go func() {
		remainingBytes, remainingDatagrams := maxBytes, maxDatagrams
		for {
			payload, err := peer.Receive(streamCtx)
			if errors.Is(err, io.EOF) {
				err = nil
				_ = stream.CloseSend()
				sent <- err
				cancel()
				return
			}
			if err == nil {
				err = consumeUDPForwardBudget(payload, &remainingBytes, &remainingDatagrams)
			}
			if err == nil {
				session.touch()
				err = stream.Send(&vmmdpb.ForwardUDPRequest{Frame: &vmmdpb.ForwardUDPRequest_Datagram{Datagram: payload}})
			}
			if err != nil {
				sent <- err
				cancel()
				return
			}
		}
	}()
	receiveErr := func() error {
		remainingBytes, remainingDatagrams := maxBytes, maxDatagrams
		for {
			frame, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			datagram, ok := frame.GetFrame().(*vmmdpb.ForwardUDPResponse_Datagram)
			if !ok {
				return status.Error(codes.Unavailable, "UDP response datagram expected")
			}
			if err := consumeUDPForwardBudget(datagram.Datagram, &remainingBytes, &remainingDatagrams); err != nil {
				return err
			}
			session.touch()
			if err := peer.Send(streamCtx, datagram.Datagram); err != nil {
				return err
			}
		}
	}()
	cancel()
	sendErr := <-sent
	if session.timedOut() {
		return status.Error(codes.DeadlineExceeded, "UDP peer idle timeout")
	}
	if ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}
	if sendErr != nil && !errors.Is(sendErr, context.Canceled) {
		return sendErr
	}
	if status.Code(receiveErr) == codes.Canceled || errors.Is(receiveErr, context.Canceled) {
		// Only a clean peer EOF may cancel the receive half successfully.
		// Unexpected remote cancellation must retain its failure status.
		if sendErr == nil {
			return nil
		}
		if errors.Is(receiveErr, context.Canceled) {
			return status.FromContextError(receiveErr).Err()
		}
	}
	return receiveErr
}

func consumeUDPForwardBudget(payload []byte, remainingBytes *int64, remainingDatagrams *uint64) error {
	if len(payload) > api.UDPDatagramMaxBytes {
		return status.Error(codes.InvalidArgument, "UDP datagram exceeds payload limit")
	}
	if *remainingDatagrams == 0 || int64(len(payload)) > *remainingBytes {
		return status.Error(codes.ResourceExhausted, "UDP peer budget exhausted")
	}
	*remainingDatagrams--
	*remainingBytes -= int64(len(payload))
	return nil
}
