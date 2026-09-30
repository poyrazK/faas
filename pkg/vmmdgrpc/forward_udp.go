package vmmdgrpc

import (
	"context"
	"errors"
	"io"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/udpwire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ForwardUDPStream serves one admitted peer. Listener ownership is validated
// by the edge; vmmd resolves only the live instance namespace and guest socket.
func (s *Server) ForwardUDPStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardUDPRequest, vmmdpb.ForwardUDPResponse]) error {
	start := time.Now()
	defer func() { s.ops.Observe("ForwardUDPStream", time.Since(start), nil) }()
	frame, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "expected UDP init frame: %v", err)
	}
	init := frame.GetInit()
	if init == nil || init.Instance == "" || init.Port == 0 || init.Port > 65535 || init.MaxBytes < 0 {
		return status.Error(codes.InvalidArgument, "UDP init requires instance, guest port 1..65535 and nonnegative caps")
	}
	if _, ok := s.vmm.NetnsFor(init.Instance); !ok {
		return status.Error(codes.NotFound, "UDP instance not live")
	}
	pid, ok := s.vmm.InstancePID(init.Instance)
	if !ok || pid <= 0 {
		return status.Error(codes.NotFound, "UDP instance has no live namespace")
	}
	s.beginActivity(init.Instance)
	defer s.endActivity(init.Instance)
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	cmd, input, output, ready, _, err := namespaceBridgeSpawn(ctx, pid, init.Port, "FAAS_VMMD_UDP_BRIDGE_PATH", "/opt/faas/current/bin/vmmd-udp-bridge")
	if err != nil {
		return err
	}
	defer func() { cancel(); closeFiles(input, output, ready); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if err := stream.Send(&vmmdpb.ForwardUDPResponse{Frame: &vmmdpb.ForwardUDPResponse_Init{Init: &vmmdpb.ForwardUDPResponseInit{}}}); err != nil {
		return err
	}
	requests := make(chan error, 1)
	go func() {
		err := udpRequestFrames(stream, input, newUDPBudget(init))
		requests <- err
		_ = input.Close()
		cancel()
	}()
	responseErr := udpResponseFrames(stream, output, newUDPBudget(init))
	select {
	case requestErr := <-requests:
		if requestErr != nil {
			return requestErr
		}
	default:
	}
	return responseErr
}

type udpBudget struct {
	bytes     int64
	datagrams uint64
}

func newUDPBudget(init *vmmdpb.ForwardUDPRequestInit) *udpBudget {
	b := &udpBudget{bytes: api.UDPStreamMaxBytes, datagrams: api.UDPStreamMaxDatagrams}
	if init.MaxBytes > 0 && init.MaxBytes < b.bytes {
		b.bytes = init.MaxBytes
	}
	if init.MaxDatagrams > 0 && init.MaxDatagrams < b.datagrams {
		b.datagrams = init.MaxDatagrams
	}
	return b
}
func (b *udpBudget) consume(payload []byte) error {
	if len(payload) > api.UDPDatagramMaxBytes {
		return status.Error(codes.InvalidArgument, "UDP datagram exceeds guest payload limit")
	}
	if b.datagrams == 0 || int64(len(payload)) > b.bytes {
		return status.Error(codes.ResourceExhausted, "UDP peer session budget exhausted")
	}
	b.datagrams--
	b.bytes -= int64(len(payload))
	return nil
}
func udpRequestFrames(stream grpc.BidiStreamingServer[vmmdpb.ForwardUDPRequest, vmmdpb.ForwardUDPResponse], output io.Writer, budget *udpBudget) error {
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		datagram, ok := frame.GetFrame().(*vmmdpb.ForwardUDPRequest_Datagram)
		if !ok {
			return status.Error(codes.InvalidArgument, "UDP datagram frame expected")
		}
		if err := budget.consume(datagram.Datagram); err != nil {
			return err
		}
		if err := udpwire.Write(output, datagram.Datagram); err != nil {
			return err
		}
	}
}
func udpResponseFrames(stream grpc.BidiStreamingServer[vmmdpb.ForwardUDPRequest, vmmdpb.ForwardUDPResponse], input io.Reader, budget *udpBudget) error {
	for {
		payload, err := udpwire.Read(input)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := budget.consume(payload); err != nil {
			return err
		}
		if err := stream.Send(&vmmdpb.ForwardUDPResponse{Frame: &vmmdpb.ForwardUDPResponse_Datagram{Datagram: payload}}); err != nil {
			return err
		}
	}
}
