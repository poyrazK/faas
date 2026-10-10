// adr: 958

package vmmdgrpc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// interactiveVMM echoes stdin upper-cased, records resizes, and exits 0 on
// stdin close. It blocks until ctx ends otherwise.
type interactiveVMM struct {
	*appTaskRuntimeVMM
	mu      sync.Mutex
	request apptaskproto.Request
	resizes []apptaskproto.InputEvent
	ended   chan error
}

func newInteractiveVMM() *interactiveVMM {
	return &interactiveVMM{appTaskRuntimeVMM: &appTaskRuntimeVMM{fakeVMM: &fakeVMM{}}, ended: make(chan error, 1)}
}

func (v *interactiveVMM) ExecuteInteractiveAppTask(ctx context.Context, _ string, req apptaskproto.Request, input <-chan apptaskproto.InputEvent, receive apptaskproto.OutputReceiver) (apptaskproto.Result, error) {
	v.mu.Lock()
	v.request = req
	v.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			v.ended <- ctx.Err()
			return apptaskproto.Result{}, ctx.Err()
		case event, ok := <-input:
			if !ok {
				<-ctx.Done()
				v.ended <- ctx.Err()
				return apptaskproto.Result{}, ctx.Err()
			}
			switch event.Kind {
			case apptaskproto.InputStdin:
				if err := receive(ctx, "stdout", bytes.ToUpper(event.Data)); err != nil {
					return apptaskproto.Result{}, err
				}
			case apptaskproto.InputResize:
				v.mu.Lock()
				v.resizes = append(v.resizes, event)
				v.mu.Unlock()
			case apptaskproto.InputStdinClose:
				exit := 0
				v.ended <- nil
				return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exit}, nil
			}
		}
	}
}

const testAttachToken = "attach-token-0123456789abcdef"

func interactiveProtoRequest(token string) *vmmdpb.ExecuteAppTaskRequest {
	digest := sha256.Sum256([]byte(token))
	return &vmmdpb.ExecuteAppTaskRequest{
		Instance: "task-1", Version: uint32(apptaskproto.Version), TaskId: "task-1",
		Command: []string{"/bin/sh"}, TimeoutSeconds: 30, MaxOutputBytes: 1024,
		Interactive: true, Tty: true, AttachTokenSha256: digest[:], AttachTimeoutSeconds: 5,
	}
}

type scheddSide struct {
	terminal chan *vmmdpb.ExecuteAppTaskResponse
	err      chan error
}

// startInteractive issues schedd's ExecuteAppTaskStream and collects its
// terminal frame. Output must never reach schedd.
func startInteractive(t *testing.T, ctx context.Context, cli vmmdpb.VmmdClient, req *vmmdpb.ExecuteAppTaskRequest) scheddSide {
	t.Helper()
	side := scheddSide{terminal: make(chan *vmmdpb.ExecuteAppTaskResponse, 1), err: make(chan error, 1)}
	stream, err := cli.ExecuteAppTaskStream(ctx, req)
	if err != nil {
		t.Fatalf("ExecuteAppTaskStream: %v", err)
	}
	go func() {
		for {
			event, err := stream.Recv()
			if err != nil {
				side.err <- err
				return
			}
			if event.GetOutput() != nil {
				side.err <- status.Error(codes.Internal, "interactive output leaked to schedd")
				return
			}
			if terminal := event.GetTerminal(); terminal != nil {
				side.terminal <- terminal
				return
			}
		}
	}()
	return side
}

// attach retries while vmmd has not registered the rendezvous yet.
func attach(t *testing.T, ctx context.Context, cli vmmdpb.VmmdClient, token string, rows, cols uint32) (vmmdpb.Vmmd_AttachAppTaskClient, *vmmdpb.AttachAppTaskEvent, error) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		stream, err := cli.AttachAppTask(ctx)
		if err != nil {
			t.Fatalf("AttachAppTask: %v", err)
		}
		if err := stream.Send(&vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_Start{
			Start: &vmmdpb.AttachAppTaskStart{Instance: "task-1", AttachToken: token, Rows: rows, Cols: cols},
		}}); err != nil {
			t.Fatalf("send start: %v", err)
		}
		first, err := stream.Recv()
		if status.Code(err) == codes.NotFound && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		return stream, first, err
	}
}

func TestAttachAppTaskRoundTrip(t *testing.T) {
	vmm := newInteractiveVMM()
	cli := newExecutionClient(t, vmm)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	side := startInteractive(t, ctx, cli, interactiveProtoRequest(testAttachToken))

	stream, first, err := attach(t, ctx, cli, testAttachToken, 30, 100)
	if err != nil || first.GetAttached() == nil {
		t.Fatalf("first attach event = %#v, %v", first, err)
	}
	send := func(frame *vmmdpb.AttachAppTaskRequest) {
		t.Helper()
		if err := stream.Send(frame); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	send(&vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_Stdin{Stdin: []byte("hi")}})
	output, err := stream.Recv()
	if err != nil || string(output.GetOutput().GetChunk()) != "HI" || output.GetOutput().GetStream() != "stdout" {
		t.Fatalf("output event = %#v, %v", output, err)
	}
	send(&vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_Resize{Resize: &vmmdpb.AttachAppTaskResize{Rows: 50, Cols: 5000}}})
	send(&vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_StdinClose{StdinClose: &vmmdpb.AttachAppTaskStdinClose{}}})
	terminal, err := stream.Recv()
	if err != nil || terminal.GetTerminal().GetStatus() != string(apptaskproto.StatusSucceeded) || terminal.GetTerminal().GetExitCode().GetValue() != 0 {
		t.Fatalf("client terminal = %#v, %v", terminal, err)
	}
	select {
	case got := <-side.terminal:
		if got.GetStatus() != string(apptaskproto.StatusSucceeded) || len(got.GetStdout()) != 0 {
			t.Fatalf("schedd terminal = %#v", got)
		}
	case err := <-side.err:
		t.Fatalf("schedd stream: %v", err)
	}

	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	req := vmm.request
	if req.Version != apptaskproto.InteractiveVersion || !req.Interactive || !req.TTY || req.Rows != 30 || req.Cols != 100 ||
		req.MaxOutputBytes != 0 || req.TimeoutSeconds < 1 || req.TimeoutSeconds > 30 {
		t.Fatalf("guest request = %#v", req)
	}
	if len(vmm.resizes) != 1 || vmm.resizes[0].Rows != 50 || vmm.resizes[0].Cols != apptaskproto.MaxTerminalCols {
		t.Fatalf("resizes = %#v", vmm.resizes)
	}
}

func TestAttachAppTaskTokenIsCheckedAndSingleUse(t *testing.T) {
	vmm := newInteractiveVMM()
	cli := newExecutionClient(t, vmm)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	side := startInteractive(t, ctx, cli, interactiveProtoRequest(testAttachToken))

	if _, _, err := attach(t, ctx, cli, "wrong-token", 0, 0); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("wrong token = %v, want PermissionDenied", err)
	}
	stream, first, err := attach(t, ctx, cli, testAttachToken, 0, 0)
	if err != nil || first.GetAttached() == nil {
		t.Fatalf("valid attach after a rejected one = %#v, %v", first, err)
	}
	second, err := cli.AttachAppTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Send(&vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_Start{
		Start: &vmmdpb.AttachAppTaskStart{Instance: "task-1", AttachToken: testAttachToken},
	}})
	if _, err := second.Recv(); status.Code(err) != codes.NotFound {
		t.Fatalf("reused token = %v, want NotFound", err)
	}
	_ = stream.Send(&vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_StdinClose{StdinClose: &vmmdpb.AttachAppTaskStdinClose{}}})
	select {
	case <-side.terminal:
	case err := <-side.err:
		t.Fatalf("schedd stream: %v", err)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if vmm.request.Rows != 24 || vmm.request.Cols != 80 {
		t.Fatalf("default terminal size = %dx%d", vmm.request.Rows, vmm.request.Cols)
	}
}

func TestInteractiveAppTaskAttachTimeout(t *testing.T) {
	vmm := newInteractiveVMM()
	cli := newExecutionClient(t, vmm)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := interactiveProtoRequest(testAttachToken)
	req.AttachTimeoutSeconds = 1
	side := startInteractive(t, ctx, cli, req)
	select {
	case got := <-side.terminal:
		if got.GetStatus() != string(apptaskproto.StatusFailed) || got.GetFailureCode() != "attach_timeout" {
			t.Fatalf("terminal = %#v", got)
		}
	case err := <-side.err:
		t.Fatalf("schedd stream: %v", err)
	}
	if _, _, err := attach(t, ctx, cli, testAttachToken, 0, 0); status.Code(err) != codes.NotFound {
		t.Fatalf("late attach = %v, want NotFound", err)
	}
}

func TestInteractiveAppTaskClientDisconnect(t *testing.T) {
	vmm := newInteractiveVMM()
	cli := newExecutionClient(t, vmm)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	side := startInteractive(t, ctx, cli, interactiveProtoRequest(testAttachToken))
	clientCtx, detach := context.WithCancel(ctx)
	_, first, err := attach(t, clientCtx, cli, testAttachToken, 24, 80)
	if err != nil || first.GetAttached() == nil {
		t.Fatalf("attach = %#v, %v", first, err)
	}
	detach()
	select {
	case got := <-side.terminal:
		if got.GetFailureCode() != "client_disconnected" {
			t.Fatalf("terminal = %#v", got)
		}
	case err := <-side.err:
		t.Fatalf("schedd stream: %v", err)
	}
	if err := <-vmm.ended; err == nil {
		t.Fatal("session context was not cancelled")
	}
}

func TestInteractiveAppTaskRejectsInvalidEnvelopes(t *testing.T) {
	cli := newExecutionClient(t, newInteractiveVMM())
	ctx := context.Background()
	if _, err := cli.ExecuteAppTask(ctx, interactiveProtoRequest(testAttachToken)); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unary interactive = %v, want InvalidArgument", err)
	}
	for name, mutate := range map[string]func(*vmmdpb.ExecuteAppTaskRequest){
		"short digest":  func(r *vmmdpb.ExecuteAppTaskRequest) { r.AttachTokenSha256 = []byte("short") },
		"no command":    func(r *vmmdpb.ExecuteAppTaskRequest) { r.Command = nil },
		"no timeout":    func(r *vmmdpb.ExecuteAppTaskRequest) { r.TimeoutSeconds = 0 },
		"no instance":   func(r *vmmdpb.ExecuteAppTaskRequest) { r.Instance = "" },
		"neg. attach":   func(r *vmmdpb.ExecuteAppTaskRequest) { r.AttachTimeoutSeconds = -1 },
		"shell 2 words": func(r *vmmdpb.ExecuteAppTaskRequest) { r.CommandShell = true; r.Command = []string{"a", "b"} },
	} {
		req := interactiveProtoRequest(testAttachToken)
		mutate(req)
		stream, err := cli.ExecuteAppTaskStream(ctx, req)
		if err == nil {
			_, err = stream.Recv()
		}
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s: code = %v, want InvalidArgument", name, status.Code(err))
		}
	}
}

func TestAttachAppTaskRequiresStartFrame(t *testing.T) {
	cli := newExecutionClient(t, newInteractiveVMM())
	stream, err := cli.AttachAppTask(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Send(&vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_Stdin{Stdin: []byte("x")}})
	if _, err := stream.Recv(); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
	}
}
