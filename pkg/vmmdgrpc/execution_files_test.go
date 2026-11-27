package vmmdgrpc_test

import (
	"context"
	"sync/atomic"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fileDispatchGuard struct {
	*fakeVMM
	dispatches atomic.Int32
}

func (f *fileDispatchGuard) ExecuteExecution(context.Context, string, executionproto.Request) (executionproto.Result, error) {
	f.dispatches.Add(1)
	return executionproto.Result{}, nil
}

func (f *fileDispatchGuard) ExecuteExecutionWithOutput(context.Context, string, executionproto.Request, executionproto.OutputReceiver) (executionproto.Result, error) {
	f.dispatches.Add(1)
	return executionproto.Result{}, nil
}

func TestExecutionFilesLegacyEndpointsRejectBeforeDispatch(t *testing.T) {
	vmm := &fileDispatchGuard{fakeVMM: &fakeVMM{}}
	client := newExecutionClient(t, vmm)
	req := &vmmdpb.ExecuteExecutionRequest{
		Instance: "file-vm", ExecutionId: "file-run", Version: uint32(executionproto.FileVersion),
		Runtime: "node24", Source: "export default ()=>42", Input: []byte("null"),
		TimeoutMs: 1000, MaxOutputBytes: 4096, NetworkMode: "none",
		PrivateFileManifest: []byte(`{"account_id":"12345678-1234-4234-8234-123456789abc"}`),
	}
	_, err := client.ExecuteExecution(t.Context(), req)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("legacy unary accepted file protocol: %v", err)
	}
	stream, err := client.ExecuteExecutionStream(t.Context(), req)
	if err == nil {
		_, err = stream.Recv()
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("legacy stream accepted file protocol: %v", err)
	}
	// A v4 call without configured file storage also fails before dispatch.
	files, err := client.ExecuteExecutionFiles(t.Context(), req)
	if err == nil {
		_, err = files.Recv()
	}
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("missing file capability did not fail closed: %v", err)
	}
	if got := vmm.dispatches.Load(); got != 0 {
		t.Fatalf("dispatched %d requests through an incompatible transport", got)
	}
}
