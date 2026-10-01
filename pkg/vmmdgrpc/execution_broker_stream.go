package vmmdgrpc

import (
	"context"
	"sync"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type outboundResponseResult struct {
	response executionproto.OutboundResponse
}

type executionBrokerBridge struct {
	stream    vmmdpb.Vmmd_ExecuteExecutionBrokerStreamServer
	instance  string
	identity  ExecutionIdentityAPI
	signer    *workloadidentity.Signer
	sendMu    sync.Mutex
	pendingMu sync.Mutex
	pending   map[uint64]chan outboundResponseResult
	recvErr   chan error
}

func newExecutionBrokerBridge(stream vmmdpb.Vmmd_ExecuteExecutionBrokerStreamServer, instance string, identity ExecutionIdentityAPI, signer *workloadidentity.Signer) *executionBrokerBridge {
	bridge := &executionBrokerBridge{
		stream: stream, instance: instance, identity: identity, signer: signer,
		pending: make(map[uint64]chan outboundResponseResult), recvErr: make(chan error, 1),
	}
	go receiveOutboundResponses(stream, &bridge.pendingMu, bridge.pending, bridge.recvErr)
	return bridge
}

func (b *executionBrokerBridge) send(event *vmmdpb.ExecuteExecutionBrokerEvent) error {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	return b.stream.Send(event)
}

func (b *executionBrokerBridge) call(ctx context.Context, request executionproto.OutboundRequest) (executionproto.OutboundResponse, error) {
	integrationIDs, err := api.NormalizeExecutionIntegrationIDs([]string{request.IntegrationID})
	if err != nil {
		return executionproto.OutboundResponse{}, executionproto.ErrInvalidRequest
	}
	integrationID := integrationIDs[0]
	accountID, executionID, leaseToken, identityErr := b.identity.ExecutionOutboundIdentity(b.instance, integrationID)
	if identityErr != nil {
		return executionproto.OutboundResponse{}, executionproto.ErrOutboundNotAuthorized
	}
	token, signErr := b.signer.MintExecution(time.Now(), accountID, executionID, leaseToken, "gregale:outbound:"+integrationID)
	if signErr != nil {
		return executionproto.OutboundResponse{}, status.Error(codes.Unavailable, "Run outbound identity is unavailable")
	}
	responseCh := make(chan outboundResponseResult, 1)
	b.pendingMu.Lock()
	if _, exists := b.pending[request.ID]; exists {
		b.pendingMu.Unlock()
		return executionproto.OutboundResponse{}, status.Error(codes.Internal, "duplicate Run outbound request ID")
	}
	b.pending[request.ID] = responseCh
	b.pendingMu.Unlock()
	defer func() {
		b.pendingMu.Lock()
		delete(b.pending, request.ID)
		b.pendingMu.Unlock()
	}()
	event := &vmmdpb.ExecuteExecutionBrokerEvent{Frame: &vmmdpb.ExecuteExecutionBrokerEvent_OutboundCall{
		OutboundCall: &vmmdpb.ExecuteExecutionOutboundCall{
			Id: request.ID, IntegrationId: integrationID, Method: request.Method,
			Path: request.Path, Body: append([]byte(nil), request.Body...), ExecutionIdentity: token.BearerValue(),
		},
	}}
	if err := b.send(event); err != nil {
		return executionproto.OutboundResponse{}, err
	}
	select {
	case response := <-responseCh:
		return response.response, nil
	case err := <-b.recvErr:
		return executionproto.OutboundResponse{}, err
	case <-ctx.Done():
		return executionproto.OutboundResponse{}, ctx.Err()
	}
}

func (b *executionBrokerBridge) output(ctx context.Context, outputStream string, chunk []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if outputStream != "stdout" && outputStream != "stderr" {
		return status.Error(codes.Internal, "execution guest returned an invalid output stream")
	}
	return b.send(&vmmdpb.ExecuteExecutionBrokerEvent{Frame: &vmmdpb.ExecuteExecutionBrokerEvent_Output{
		Output: &vmmdpb.ExecuteExecutionOutputChunk{Stream: outputStream, Chunk: append([]byte(nil), chunk...)},
	}})
}

// ExecuteExecutionBrokerStream is the full-duplex Runs path. The first
// client frame starts the execution; all later client frames must answer an
// outbound call previously emitted by vmmd.
func (s *Server) ExecuteExecutionBrokerStream(stream vmmdpb.Vmmd_ExecuteExecutionBrokerStreamServer) error {
	const op = "ExecuteExecutionBrokerStream"
	started := time.Now()
	var callErr error
	defer func() { s.ops.Observe(op, time.Since(started), callErr) }()
	callErr = s.serveExecutionBrokerStream(stream)
	return callErr
}

func (s *Server) serveExecutionBrokerStream(stream vmmdpb.Vmmd_ExecuteExecutionBrokerStreamServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "first execution broker frame must start an execution")
	}
	executionVMM, ok := s.vmm.(ExecutionBrokerVMMAPI)
	if !ok || s.identitySigner == nil {
		return status.Error(codes.Unimplemented, "Runs outbound broker is not configured")
	}
	identityAPI, ok := s.vmm.(ExecutionIdentityAPI)
	if !ok {
		return status.Error(codes.Unimplemented, "Runs execution identity is not configured")
	}
	wireReq, err := executionRequestFromProto(start)
	if err != nil {
		return grpcerr.ToStatus(toProblem(err))
	}
	if !wireReq.OutboundEnabled {
		return status.Error(codes.InvalidArgument, "outbound_enabled is required for the execution broker stream")
	}
	bridge := newExecutionBrokerBridge(stream, start.GetInstance(), identityAPI, s.identitySigner)
	result, err := executionVMM.ExecuteExecutionWithBroker(stream.Context(), start.GetInstance(), wireReq, bridge.output, bridge.call)
	if err != nil {
		return grpcerr.ToStatus(executionProblem(err))
	}
	if err := result.Validate(wireReq.MaxOutput); err != nil {
		return grpcerr.ToStatus(api.NewProblem(int(codes.Internal), api.CodeInternal,
			"Execution protocol failed", "vmmd received an invalid terminal result"))
	}
	terminal := executionResponseFromResult(wireReq.ExecutionID, result)
	terminal.Stdout = nil
	terminal.Stderr = nil
	return bridge.send(&vmmdpb.ExecuteExecutionBrokerEvent{Frame: &vmmdpb.ExecuteExecutionBrokerEvent_Terminal{Terminal: terminal}})
}

func receiveOutboundResponses(stream vmmdpb.Vmmd_ExecuteExecutionBrokerStreamServer, mu *sync.Mutex, pending map[uint64]chan outboundResponseResult, recvErr chan<- error) {
	for {
		frame, err := stream.Recv()
		if err != nil {
			recvErr <- err
			return
		}
		wireResponse := frame.GetOutboundResponse()
		if wireResponse == nil {
			recvErr <- status.Error(codes.InvalidArgument, "execution broker accepts only outbound responses after start")
			return
		}
		response := executionproto.OutboundResponse{
			ID: wireResponse.GetId(), Status: int(wireResponse.GetStatus()),
			Headers: cloneStringMap(wireResponse.GetHeaders()), Body: append([]byte(nil), wireResponse.GetBody()...),
		}
		if err := response.Validate(); err != nil {
			recvErr <- status.Error(codes.InvalidArgument, "execution broker response is invalid")
			return
		}
		mu.Lock()
		responseCh := pending[response.ID]
		mu.Unlock()
		if responseCh == nil {
			recvErr <- status.Error(codes.InvalidArgument, "execution broker response has no matching request")
			return
		}
		select {
		case responseCh <- outboundResponseResult{response: response}:
		default:
			recvErr <- status.Error(codes.InvalidArgument, "execution broker response was duplicated")
			return
		}
	}
}
