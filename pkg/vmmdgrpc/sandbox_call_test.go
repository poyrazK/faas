// adr: 20261027090013

package vmmdgrpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// sandboxVMM serves one session, sbx-1 on instance sbx-vm, with
// echoSandbox as its guest.
type sandboxVMM struct {
	*fakeVMM
}

func (v *sandboxVMM) DialSandboxSession(_ context.Context, instance, sandboxID string) (net.Conn, error) {
	if instance != "sbx-vm" || sandboxID != "sbx-1" {
		return nil, fcvm.ErrSandboxNotFound
	}
	host, guest := net.Pipe()
	go func() { _ = sandboxproto.Serve(context.Background(), guest, echoSandbox) }()
	return host, nil
}

// echoSandbox echoes a command's argv on stdout and counts an upload.
func echoSandbox(_ context.Context, req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
	switch req.Op {
	case sandboxproto.OpExec:
		_, _ = call.Stdout().Write([]byte(strings.Join(req.Argv, " ")))
		_, _ = call.Stderr().Write([]byte("warn"))
		return sandboxproto.ExecResult{ExitCode: 3, WallTimeMillis: 1500}, nil
	case sandboxproto.OpFileWrite:
		var buf bytes.Buffer
		n, err := call.ReadUpload(&buf, 1<<20)
		if err != nil {
			return nil, err
		}
		return sandboxproto.TransferResult{Bytes: n}, nil
	}
	return nil, &sandboxproto.GuestError{Code: sandboxproto.CodeNotFound, Message: req.Path + " does not exist"}
}

func sandboxStart(t *testing.T, sandboxID string, req sandboxproto.Request) *vmmdpb.SandboxCallRequest {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return &vmmdpb.SandboxCallRequest{Message: &vmmdpb.SandboxCallRequest_Start{Start: &vmmdpb.SandboxCallStart{
		Instance: "sbx-vm", SandboxId: sandboxID, Request: body}}}
}

func sandboxFrame(frameType uint32, body []byte) *vmmdpb.SandboxCallRequest {
	return &vmmdpb.SandboxCallRequest{Message: &vmmdpb.SandboxCallRequest_Frame{Frame: &vmmdpb.SandboxFrame{Type: frameType, Body: body}}}
}

// sandboxCall sends msgs, ends the caller's side and collects the events up
// to the stream's end.
func sandboxCall(t *testing.T, cli vmmdpb.VmmdClient, msgs ...*vmmdpb.SandboxCallRequest) ([]*vmmdpb.SandboxFrame, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	stream, err := cli.SandboxCall(ctx)
	if err != nil {
		t.Fatalf("SandboxCall: %v", err)
	}
	for _, m := range msgs {
		if err := stream.Send(m); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	_ = stream.CloseSend()
	var frames []*vmmdpb.SandboxFrame
	for {
		ev, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return frames, nil
			}
			return frames, err
		}
		frames = append(frames, ev.GetFrame())
	}
}

func TestSandboxCallRelaysACommand(t *testing.T) {
	cli := newExecutionClient(t, &sandboxVMM{fakeVMM: &fakeVMM{}})
	frames, err := sandboxCall(t, cli, sandboxStart(t, "sbx-1", sandboxproto.Request{Op: sandboxproto.OpExec, Argv: []string{"echo", "hi"}, TimeoutMillis: 1000, MaxOutputBytes: 1024}))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if len(frames) != 3 || frames[0].GetType() != sandboxproto.FrameStdout || string(frames[0].GetBody()) != "echo hi" ||
		frames[1].GetType() != sandboxproto.FrameStderr || frames[2].GetType() != sandboxproto.FrameResult {
		t.Fatalf("frames = %v", frames)
	}
	var res sandboxproto.ExecResult
	if err := json.Unmarshal(frames[2].GetBody(), &res); err != nil || res.ExitCode != 3 || res.WallTimeMillis != 1500 {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

func TestSandboxCallRelaysAnUpload(t *testing.T) {
	cli := newExecutionClient(t, &sandboxVMM{fakeVMM: &fakeVMM{}})
	frames, err := sandboxCall(t, cli,
		sandboxStart(t, "sbx-1", sandboxproto.Request{Op: sandboxproto.OpFileWrite, Path: "a.txt"}),
		sandboxFrame(sandboxproto.FrameData, []byte("hello ")),
		// A frame only the guest sends is dropped, not relayed.
		sandboxFrame(sandboxproto.FrameResult, []byte(`{"bytes":99}`)),
		sandboxFrame(sandboxproto.FrameData, []byte("world")),
		sandboxFrame(sandboxproto.FrameDataEnd, nil))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if len(frames) != 1 || frames[0].GetType() != sandboxproto.FrameResult || string(frames[0].GetBody()) != `{"bytes":11}` {
		t.Fatalf("frames = %v", frames)
	}
}

func TestSandboxCallRelaysTheGuestRefusal(t *testing.T) {
	cli := newExecutionClient(t, &sandboxVMM{fakeVMM: &fakeVMM{}})
	frames, err := sandboxCall(t, cli, sandboxStart(t, "sbx-1", sandboxproto.Request{Op: sandboxproto.OpFileStat, Path: "missing"}))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var e sandboxproto.ErrorFrame
	if len(frames) != 1 || frames[0].GetType() != sandboxproto.FrameError || json.Unmarshal(frames[0].GetBody(), &e) != nil ||
		e.Code != sandboxproto.CodeNotFound {
		t.Fatalf("frames = %v", frames)
	}
}

func TestSandboxCallRefusals(t *testing.T) {
	exec := sandboxproto.Request{Op: sandboxproto.OpExec, Argv: []string{"true"}, TimeoutMillis: 1000, MaxOutputBytes: 1024}
	for _, tc := range []struct {
		name string
		cli  vmmdpb.VmmdClient
		msg  *vmmdpb.SandboxCallRequest
		want codes.Code
	}{
		{"another session", newExecutionClient(t, &sandboxVMM{fakeVMM: &fakeVMM{}}), sandboxStart(t, "sbx-2", exec), codes.NotFound},
		{"no start frame", newExecutionClient(t, &sandboxVMM{fakeVMM: &fakeVMM{}}), sandboxFrame(sandboxproto.FrameData, nil), codes.InvalidArgument},
		{"invalid request", newExecutionClient(t, &sandboxVMM{fakeVMM: &fakeVMM{}}),
			sandboxStart(t, "sbx-1", sandboxproto.Request{Op: sandboxproto.OpExec}), codes.InvalidArgument},
		{"no sandbox support", newExecutionClient(t, &fakeVMM{}), sandboxStart(t, "sbx-1", exec), codes.Unimplemented},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sandboxCall(t, tc.cli, tc.msg)
			if status.Code(err) != tc.want {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
		})
	}
}
