package vmmdgrpc_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
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
