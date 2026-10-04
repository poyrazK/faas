// adr: 171
package vmmdgrpc_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func (f *fakeVMM) ExecuteExecution(_ context.Context, _ string, _ executionproto.Request) (executionproto.Result, error) {
	return executionproto.Result{
		Status: api.ExecutionStatusSucceeded,
		Result: json.RawMessage(`{"answer":42}`),
		Stdout: []byte("hello\n"),
		Usage:  api.ExecutionUsage{WallTimeMS: 12, CPUTimeMS: 3, PeakMemoryMB: 64},
	}, nil
}

func TestExecuteExecution_RoundTripsBoundedResult(t *testing.T) {
	cli, _ := newServer(t, &fakeVMM{})
	resp, err := cli.ExecuteExecution(context.Background(), &vmmdpb.ExecuteExecutionRequest{
		Instance:       "exec-vm-1",
		Version:        uint32(executionproto.Version),
		ExecutionId:    "exec-1",
		Runtime:        string(api.ExecutionRuntimeNode22),
		Source:         "1 + 1",
		Input:          []byte(`{"value":1}`),
		TimeoutMs:      1000,
		MaxOutputBytes: 1024,
		NetworkMode:    string(api.ExecutionNetworkNone),
	})
	if err != nil {
		t.Fatalf("ExecuteExecution: %v", err)
	}
	if resp.GetExecutionId() != "exec-1" || resp.GetStatus() != string(api.ExecutionStatusSucceeded) {
		t.Fatalf("response identity/status = %q/%q", resp.GetExecutionId(), resp.GetStatus())
	}
	if string(resp.GetResult()) != `{"answer":42}` || string(resp.GetStdout()) != "hello\n" {
		t.Fatalf("response output = %q/%q", resp.GetResult(), resp.GetStdout())
	}
	if resp.GetWallTimeMs() != 12 || resp.GetCpuTimeMs() != 3 || resp.GetPeakMemoryMb() != 64 {
		t.Fatalf("response usage = %d/%d/%d", resp.GetWallTimeMs(), resp.GetCpuTimeMs(), resp.GetPeakMemoryMb())
	}
}

type streamingExecutionVMM struct{ *fakeVMM }

func (f *streamingExecutionVMM) ExecuteExecutionWithOutput(ctx context.Context, _ string, _ executionproto.Request, receive executionproto.OutputReceiver) (executionproto.Result, error) {
	if err := receive(ctx, "stdout", []byte("live\n")); err != nil {
		return executionproto.Result{}, err
	}
	return executionproto.Result{
		Status: api.ExecutionStatusSucceeded,
		Result: json.RawMessage(`{"ok":true}`),
		Stdout: []byte("live\n"),
	}, nil
}

func TestExecuteExecutionStreamSendsOutputBeforeMetadataOnlyTerminal(t *testing.T) {
	cli := newExecutionClient(t, &streamingExecutionVMM{fakeVMM: &fakeVMM{}})
	stream, err := cli.ExecuteExecutionStream(context.Background(), &vmmdpb.ExecuteExecutionRequest{
		Instance:       "exec-vm-1",
		Version:        uint32(executionproto.Version),
		ExecutionId:    "exec-1",
		Runtime:        string(api.ExecutionRuntimeNode22),
		Source:         "1 + 1",
		Input:          []byte(`{"value":1}`),
		TimeoutMs:      1000,
		MaxOutputBytes: 1024,
		NetworkMode:    string(api.ExecutionNetworkNone),
	})
	if err != nil {
		t.Fatalf("ExecuteExecutionStream: %v", err)
	}
	first, err := stream.Recv()
	if err != nil || first.GetOutput() == nil || first.GetOutput().GetStream() != "stdout" || string(first.GetOutput().GetChunk()) != "live\n" {
		t.Fatalf("first event = %v, %v", first, err)
	}
	second, err := stream.Recv()
	if err != nil || second.GetTerminal() == nil {
		t.Fatalf("terminal event = %v, %v", second, err)
	}
	if len(second.GetTerminal().GetStdout()) != 0 || string(second.GetTerminal().GetResult()) != `{"ok":true}` {
		t.Fatalf("terminal output = %q/%q", second.GetTerminal().GetStdout(), second.GetTerminal().GetResult())
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("stream end = %v, want io.EOF", err)
	}
}

const (
	testOutboundIntegrationID = "11111111-1111-4111-8111-111111111111"
	testOutboundAccountID     = "22222222-2222-4222-8222-222222222222"
	testOutboundExecutionID   = "33333333-3333-4333-8333-333333333333"
	testOutboundLeaseToken    = "44444444-4444-4444-8444-444444444444"
)

type brokerExecutionVMM struct {
	*fakeVMM
	response chan executionproto.OutboundResponse
}

func (f *brokerExecutionVMM) ExecutionOutboundIdentity(instance, integrationID string) (string, string, string, error) {
	if instance != "exec-vm-1" || integrationID != testOutboundIntegrationID {
		return "", "", "", errors.New("integration was not granted")
	}
	return testOutboundAccountID, testOutboundExecutionID, testOutboundLeaseToken, nil
}

func (f *brokerExecutionVMM) ExecuteExecutionWithBroker(ctx context.Context, _ string, _ executionproto.Request, _ executionproto.OutputReceiver, broker executionproto.OutboundCallFunc) (executionproto.Result, error) {
	response, err := broker(ctx, executionproto.OutboundRequest{
		ID: 1, IntegrationID: testOutboundIntegrationID, Method: "GET", Path: "/v1/issues?state=open",
	})
	if err != nil {
		return executionproto.Result{}, err
	}
	f.response <- response
	return executionproto.Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"ok":true}`)}, nil
}

func TestExecuteExecutionBrokerStreamMintsHostOnlyIdentityAndReturnsResponse(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const issuer = "https://vmmd.example.test"
	signer, err := workloadidentity.NewSigner(privateKey, issuer, "vmmd-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	vmm := &brokerExecutionVMM{fakeVMM: &fakeVMM{}, response: make(chan executionproto.OutboundResponse, 1)}
	srv := grpc.NewServer()
	vmmdgrpc.New(vmm, wire.NewOpsMetrics("vmmd_execution_broker_test"), "1.0.0", nil).
		WithExecutionIdentitySigner(signer).Register(srv)
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://brokerbuf",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := vmmdpb.NewVmmdClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.ExecuteExecutionBrokerStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&vmmdpb.ExecuteExecutionBrokerRequest{Frame: &vmmdpb.ExecuteExecutionBrokerRequest_Start{Start: &vmmdpb.ExecuteExecutionRequest{
		Instance: "exec-vm-1", Version: uint32(executionproto.Version), ExecutionId: "exec-1",
		Runtime: string(api.ExecutionRuntimeNode22), Source: "1 + 1", Input: []byte(`null`),
		TimeoutMs: 1000, MaxOutputBytes: 1024, NetworkMode: string(api.ExecutionNetworkNone), OutboundEnabled: true,
	}}}); err != nil {
		t.Fatal(err)
	}
	callEvent, err := stream.Recv()
	if err != nil || callEvent.GetOutboundCall() == nil {
		t.Fatalf("outbound event = %v, %v", callEvent, err)
	}
	call := callEvent.GetOutboundCall()
	if call.GetIntegrationId() != testOutboundIntegrationID || call.GetPath() != "/v1/issues?state=open" || call.GetExecutionIdentity() == "" {
		t.Fatalf("outbound call = %+v", call)
	}
	jwks, err := json.Marshal(signer.JWKS())
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := outbound.NewWorkloadIdentityVerifier(jwks, issuer)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := verifier.VerifyExecution(call.GetExecutionIdentity(), testOutboundIntegrationID)
	if err != nil {
		t.Fatalf("verify execution identity: %v", err)
	}
	if identity.AccountID != testOutboundAccountID || identity.ExecutionID != testOutboundExecutionID || identity.LeaseToken != testOutboundLeaseToken {
		t.Fatalf("execution identity = %+v", identity)
	}
	if err := stream.Send(&vmmdpb.ExecuteExecutionBrokerRequest{Frame: &vmmdpb.ExecuteExecutionBrokerRequest_OutboundResponse{OutboundResponse: &vmmdpb.ExecuteExecutionOutboundResponse{
		Id: call.GetId(), Status: 200, Headers: map[string]string{"content-type": "application/json"}, Body: []byte(`{"issues":[]}`),
	}}}); err != nil {
		t.Fatal(err)
	}
	terminal, err := stream.Recv()
	if err != nil || terminal.GetTerminal() == nil || string(terminal.GetTerminal().GetResult()) != `{"ok":true}` {
		t.Fatalf("terminal = %v, %v", terminal, err)
	}
	gotResponse := <-vmm.response
	if gotResponse.Status != 200 || string(gotResponse.Body) != `{"issues":[]}` || gotResponse.Headers["content-type"] != "application/json" {
		t.Fatalf("guest response = %+v", gotResponse)
	}
}

func TestExecuteExecution_RejectsInvalidRequestBeforeVMM(t *testing.T) {
	cli, _ := newServer(t, &fakeVMM{})
	_, err := cli.ExecuteExecution(context.Background(), &vmmdpb.ExecuteExecutionRequest{
		Instance:       "exec-vm-1",
		Version:        uint32(executionproto.Version),
		ExecutionId:    "exec-1",
		Runtime:        string(api.ExecutionRuntimeNode22),
		Source:         "",
		Input:          []byte(`null`),
		TimeoutMs:      1000,
		MaxOutputBytes: 1024,
		NetworkMode:    string(api.ExecutionNetworkNone),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
	}
}

type noExecutionVMM struct{ vmmdgrpc.VmmdAPI }

func TestExecuteExecution_ReportsCapabilityGap(t *testing.T) {
	cli := newExecutionClient(t, &noExecutionVMM{VmmdAPI: &fakeVMM{}})
	_, err := cli.ExecuteExecution(context.Background(), &vmmdpb.ExecuteExecutionRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("code = %v, want Unimplemented", status.Code(err))
	}
}

type executionFailureVMM struct{ *fakeVMM }

func (f *executionFailureVMM) ExecuteExecution(_ context.Context, _ string, _ executionproto.Request) (executionproto.Result, error) {
	return executionproto.Result{
		Status:         api.ExecutionStatusFailed,
		Result:         json.RawMessage(`null`),
		FailureCode:    "guest_stack_trace",
		FailureMessage: "source=customer-secret; path=/etc/shadow",
	}, nil
}

func TestExecuteExecution_SanitizesGuestFailureDetail(t *testing.T) {
	cli := newExecutionClient(t, &executionFailureVMM{fakeVMM: &fakeVMM{}})
	resp, err := cli.ExecuteExecution(context.Background(), &vmmdpb.ExecuteExecutionRequest{
		Instance:       "exec-vm-1",
		Version:        uint32(executionproto.Version),
		ExecutionId:    "exec-1",
		Runtime:        string(api.ExecutionRuntimeNode22),
		Source:         "throw new Error()",
		Input:          []byte(`null`),
		TimeoutMs:      1000,
		MaxOutputBytes: 1024,
		NetworkMode:    string(api.ExecutionNetworkNone),
	})
	if err != nil {
		t.Fatalf("ExecuteExecution: %v", err)
	}
	if resp.GetFailureCode() != "guest_error" || resp.GetFailureMessage() != "execution failed inside the isolated guest" {
		t.Fatalf("failure detail was not sanitized: %q/%q", resp.GetFailureCode(), resp.GetFailureMessage())
	}
}

func newExecutionClient(t *testing.T, vmm vmmdgrpc.VmmdAPI) vmmdpb.VmmdClient {
	t.Helper()
	srv := grpc.NewServer()
	vmmdgrpc.New(vmm, wire.NewOpsMetrics("vmmd_execution_test"), "1.0.0", nil).Register(srv)
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn)
}
