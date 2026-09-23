// adr: 230

package vmmdgrpc_test

import (
	"context"
	"errors"
	"io"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type appTaskRuntimeVMM struct {
	*fakeVMM
	wake    fcvm.AppTaskWakeRequest
	execute apptaskproto.Request
}

func (v *appTaskRuntimeVMM) WakeAppTask(_ context.Context, request fcvm.AppTaskWakeRequest) (*fcvm.Instance, error) {
	v.wake = request
	return &fcvm.Instance{Lease: fcvm.Lease{Instance: request.Instance, UID: 22001}, Method: fcvm.WakeColdBoot, AppTaskOnly: true}, nil
}

func (v *appTaskRuntimeVMM) ExecuteAppTask(_ context.Context, _ string, request apptaskproto.Request) (apptaskproto.Result, error) {
	v.execute = request
	exit := 0
	return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exit, Stdout: []byte("done\n")}, nil
}

func (v *appTaskRuntimeVMM) ExecuteAppTaskWithOutput(ctx context.Context, instance string, request apptaskproto.Request, receive apptaskproto.OutputReceiver) (apptaskproto.Result, error) {
	v.execute = request
	if err := receive(ctx, "stdout", []byte("live\n")); err != nil {
		return apptaskproto.Result{}, err
	}
	exit := 0
	return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exit, Stdout: []byte("live\n")}, nil
}

func TestAppTaskRestoreAndExecuteUseSeparateEnvelopes(t *testing.T) {
	vmm := &appTaskRuntimeVMM{fakeVMM: &fakeVMM{}}
	cli := newExecutionClient(t, vmm)
	restored, err := cli.RestoreAppTask(context.Background(), &vmmdpb.RestoreAppTaskRequest{
		Instance: "task-1", AccountId: "acct-1", DeploymentId: "dep-1", Plan: "pro",
		App: &vmmdpb.AppSpec{
			AppId: "app-1", Runtime: "node22", BaseKey: "base/node22.ext4", LayerKey: "apps/app-1/dep-1.ext4",
			VcpuCount: 2, MemSizeMib: 512, CpuMillicores: 500,
		},
	})
	if err != nil {
		t.Fatalf("RestoreAppTask: %v", err)
	}
	if restored.GetInstance() != "task-1" || vmm.wake.DeploymentID != "dep-1" || vmm.wake.AppID != "app-1" ||
		vmm.wake.LayerKey != "apps/app-1/dep-1.ext4" || vmm.execute.TaskID != "" {
		t.Fatalf("restore response/wake = %#v / %#v", restored, vmm.wake)
	}
	response, err := cli.ExecuteAppTask(context.Background(), validAppTaskProtoRequest())
	if err != nil {
		t.Fatalf("ExecuteAppTask: %v", err)
	}
	if response.GetTaskId() != "task-1" || response.GetStatus() != string(apptaskproto.StatusSucceeded) || string(response.GetStdout()) != "done\n" {
		t.Fatalf("response = %#v", response)
	}
	if len(vmm.execute.Command) != 2 || vmm.execute.Command[0] != "bin/migrate" {
		t.Fatalf("execute request = %#v", vmm.execute)
	}
}

func TestExecuteAppTaskStreamSendsOutputBeforeTerminal(t *testing.T) {
	cli := newExecutionClient(t, &appTaskRuntimeVMM{fakeVMM: &fakeVMM{}})
	stream, err := cli.ExecuteAppTaskStream(context.Background(), validAppTaskProtoRequest())
	if err != nil {
		t.Fatalf("ExecuteAppTaskStream: %v", err)
	}
	first, err := stream.Recv()
	if err != nil || first.GetOutput() == nil || string(first.GetOutput().GetChunk()) != "live\n" {
		t.Fatalf("first event = %#v, %v", first, err)
	}
	terminal, err := stream.Recv()
	if err != nil || terminal.GetTerminal() == nil || len(terminal.GetTerminal().GetStdout()) != 0 {
		t.Fatalf("terminal event = %#v, %v", terminal, err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("stream end = %v, want EOF", err)
	}
}

func TestExecuteAppTaskRejectsInvalidCommandBeforeVMM(t *testing.T) {
	vmm := &appTaskRuntimeVMM{fakeVMM: &fakeVMM{}}
	cli := newExecutionClient(t, vmm)
	request := validAppTaskProtoRequest()
	request.Command = nil
	_, err := cli.ExecuteAppTask(context.Background(), request)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
	}
	if vmm.execute.TaskID != "" {
		t.Fatal("invalid command reached VMM")
	}
}

func validAppTaskProtoRequest() *vmmdpb.ExecuteAppTaskRequest {
	return &vmmdpb.ExecuteAppTaskRequest{
		Instance: "task-1", Version: uint32(apptaskproto.Version), TaskId: "task-1",
		Command: []string{"bin/migrate", "--once"}, TimeoutSeconds: 30, MaxOutputBytes: 2048,
	}
}
