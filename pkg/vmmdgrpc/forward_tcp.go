package vmmdgrpc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	vmmdTCPBridgePath = "/opt/faas/current/bin/vmmd-tcp-bridge"
	tcpBridgePathEnv  = "FAAS_VMMD_TCP_BRIDGE_PATH"
)

// ForwardTCPStream is the protocol-neutral bridge for a declared public TCP
// listener. It deliberately lives beside ForwardRawStream: the latter owns
// HTTP Upgrade framing and its response-head contract, while this RPC carries
// only application bytes.
func (s *Server) ForwardTCPStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardTCPRequest, vmmdpb.ForwardTCPResponse]) error {
	const op = "ForwardTCPStream"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), nil) }()

	frame, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "expected init frame: %v", err)
	}
	init := frame.GetInit()
	if init == nil || init.GetInstance() == "" {
		return status.Error(codes.InvalidArgument, "first frame must include instance")
	}

	instance := init.GetInstance()
	if _, ok := s.vmm.NetnsFor(instance); !ok {
		return status.Errorf(codes.NotFound, "instance %q not live", instance)
	}
	pid, ok := s.vmm.InstancePID(instance)
	if !ok || pid <= 0 {
		return status.Errorf(codes.NotFound, "instance %q has no live network namespace", instance)
	}
	port := init.GetPort()
	if port == 0 || port > 65535 {
		return status.Error(codes.InvalidArgument, "guest TCP port must be between 1 and 65535")
	}

	s.beginActivity(instance)
	defer s.endActivity(instance)

	cmd, stdinW, stdoutR, readyR, stderr, err := tcpBridgeSpawn(stream.Context(), pid, port)
	if err != nil {
		return sendTCPInitError(stream, err)
	}
	defer func() { _ = stdinW.Close() }()
	defer func() { _ = stdoutR.Close() }()
	defer func() { _ = readyR.Close() }()

	if err := stream.Send(&vmmdpb.ForwardTCPResponse{
		Frame: &vmmdpb.ForwardTCPResponse_Init{Init: &vmmdpb.ForwardTCPResponseInit{}},
	}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return status.Errorf(codes.Canceled, "send TCP readiness: %v", err)
	}

	bodyErrCh, bodyWG := tcpBridgeBodyLoop(stream, stdinW, init.GetMaxBytes())
	pumpErr := tcpBridgePumpBody(stream, stdoutR, init.GetMaxBytes())
	bridgeErr := tcpBridgeFinish(cmd, bodyErrCh, bodyWG, stream.Context(), stderr.String())
	if pumpErr != nil {
		return pumpErr
	}
	if bridgeErr != nil {
		return bridgeErr
	}
	return nil
}

func sendTCPInitError(stream grpc.BidiStreamingServer[vmmdpb.ForwardTCPRequest, vmmdpb.ForwardTCPResponse], err error) error {
	_ = stream.Send(&vmmdpb.ForwardTCPResponse{
		Frame: &vmmdpb.ForwardTCPResponse_Init{Init: &vmmdpb.ForwardTCPResponseInit{Error: err.Error()}},
	})
	return err
}

func tcpBridgeSpawn(ctx context.Context, instancePID int, port uint32) (*exec.Cmd, *os.File, *os.File, *os.File, *bytes.Buffer, error) {
	bridgePath := os.Getenv(tcpBridgePathEnv)
	if bridgePath == "" {
		bridgePath = vmmdTCPBridgePath
	}
	if !strings.HasPrefix(bridgePath, "/") {
		return nil, nil, nil, nil, nil, status.Errorf(codes.FailedPrecondition, "%s must be an absolute path", tcpBridgePathEnv)
	}
	if _, err := os.Stat(bridgePath); err != nil {
		return nil, nil, nil, nil, nil, status.Errorf(codes.FailedPrecondition, "TCP bridge binary missing at %s: %v", bridgePath, err)
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, nil, nil, status.Errorf(codes.Internal, "TCP bridge stdin pipe: %v", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		return nil, nil, nil, nil, nil, status.Errorf(codes.Internal, "TCP bridge stdout pipe: %v", err)
	}
	readyR, readyW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return nil, nil, nil, nil, nil, status.Errorf(codes.Internal, "TCP bridge readiness pipe: %v", err)
	}

	cmd := exec.CommandContext(ctx, "nsenter", "--target", strconv.Itoa(instancePID), "--net", "--", bridgePath, netns.GuestIP, strconv.FormatUint(uint64(port), 10))
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.ExtraFiles = []*os.File{readyW}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		closeFiles(stdinR, stdinW, stdoutR, stdoutW, readyR, readyW)
		return nil, nil, nil, nil, nil, status.Errorf(codes.Unavailable, "start TCP bridge: %v", err)
	}
	_ = stdinR.Close()
	_ = stdoutW.Close()
	_ = readyW.Close()

	ready, readErr := bufio.NewReader(readyR).ReadString('\n')
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = readyR.Close()
		return nil, nil, nil, nil, nil, status.Errorf(codes.Unavailable, "TCP bridge readiness: %v (stderr=%q)", readErr, stderr.String())
	}
	if strings.HasPrefix(ready, "ERR ") {
		_ = cmd.Wait()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = readyR.Close()
		return nil, nil, nil, nil, nil, status.Errorf(codes.Unavailable, "guest TCP dial failed: %s", strings.TrimSpace(strings.TrimPrefix(ready, "ERR ")))
	}
	if ready != "OK\n" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = readyR.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("unexpected TCP bridge readiness %q", ready)
	}
	return cmd, stdinW, stdoutR, readyR, &stderr, nil
}

func closeFiles(files ...*os.File) {
	for _, f := range files {
		if f != nil {
			_ = f.Close()
		}
	}
}

func tcpBridgeBodyLoop(stream grpc.BidiStreamingServer[vmmdpb.ForwardTCPRequest, vmmdpb.ForwardTCPResponse], stdinW *os.File, requestedMax int64) (chan error, *sync.WaitGroup) {
	maxBytes := api.RawTCPStreamMaxBytes
	if requestedMax > 0 && requestedMax < maxBytes {
		maxBytes = requestedMax
	}
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() { _ = stdinW.Close() }()
		var total int64
		for {
			frame, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				errCh <- nil
				return
			}
			if err != nil {
				errCh <- err
				return
			}
			if frame == nil {
				errCh <- status.Error(codes.InvalidArgument, "TCP stream received an empty frame")
				return
			}
			if _, ok := frame.GetFrame().(*vmmdpb.ForwardTCPRequest_BodyChunk); !ok {
				errCh <- status.Error(codes.InvalidArgument, "TCP stream body frame expected")
				return
			}
			chunk := frame.GetBodyChunk()
			if len(chunk) == 0 {
				continue
			}
			total += int64(len(chunk))
			if total > maxBytes {
				errCh <- status.Errorf(codes.ResourceExhausted, "TCP stream request exceeded %d bytes", maxBytes)
				return
			}
			if _, err := stdinW.Write(chunk); err != nil {
				errCh <- err
				return
			}
		}
	}()
	return errCh, &wg
}

func tcpBridgePumpBody(stream grpc.BidiStreamingServer[vmmdpb.ForwardTCPRequest, vmmdpb.ForwardTCPResponse], stdoutR *os.File, requestedMax int64) error {
	maxBytes := api.RawTCPStreamMaxBytes
	if requestedMax > 0 && requestedMax < maxBytes {
		maxBytes = requestedMax
	}
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := stdoutR.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > maxBytes {
				return status.Errorf(codes.ResourceExhausted, "TCP stream response exceeded %d bytes", maxBytes)
			}
			if sendErr := stream.Send(&vmmdpb.ForwardTCPResponse{
				Frame: &vmmdpb.ForwardTCPResponse_BodyChunk{BodyChunk: append([]byte(nil), buf[:n]...)},
			}); sendErr != nil {
				return status.Errorf(codes.Canceled, "send TCP response: %v", sendErr)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return status.Errorf(codes.Unavailable, "read TCP bridge: %v", err)
		}
	}
}

func tcpBridgeFinish(cmd *exec.Cmd, bodyErrCh <-chan error, bodyWG *sync.WaitGroup, streamCtx context.Context, stderr string) error {
	waitErr := cmd.Wait()
	done := make(chan struct{})
	go func() { bodyWG.Wait(); close(done) }()
	var bodyErr error
	select {
	case <-done:
		bodyErr = <-bodyErrCh
	case <-streamCtx.Done():
		bodyErr = status.Errorf(codes.Canceled, "TCP stream body: %v", streamCtx.Err())
	case <-time.After(2 * time.Second):
		bodyErr = errors.New("TCP stream body did not terminate")
	}
	if st, ok := status.FromError(bodyErr); ok && st.Code() == codes.ResourceExhausted {
		return bodyErr
	}
	if bodyErr != nil && !errors.Is(bodyErr, io.EOF) {
		return status.Errorf(codes.Unavailable, "TCP stream request: %v", bodyErr)
	}
	if waitErr != nil {
		return status.Errorf(codes.Unavailable, "TCP bridge exited: %v (stderr=%q)", waitErr, stderr)
	}
	return nil
}
