// adr: 1330

package vmmdgrpc_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// interactiveJobVMM boots job VMs with a complete runtime identity and runs
// ADR-1330 sessions: stdin is echoed back as stdout, stdin close returns
// result (or err).
type interactiveJobVMM struct {
	*fakeVMM
	mu       sync.Mutex
	boots    []fcvm.JobBootRequest
	bootErr  error
	identity bool
	instance string
	request  apptaskproto.Request
	result   apptaskproto.Result
	err      error
	ended    chan error
}

func newInteractiveJobVMM() *interactiveJobVMM {
	exit := 7
	v := &interactiveJobVMM{identity: true, ended: make(chan error, 1),
		result: apptaskproto.Result{Status: apptaskproto.StatusFailed, ExitCode: &exit, FailureCode: "command_failed"}}
	v.fakeVMM = &fakeVMM{jobBootFn: v.boot}
	return v
}

func (v *interactiveJobVMM) boot(_ context.Context, req fcvm.JobBootRequest) (*fcvm.Instance, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.boots = append(v.boots, req)
	if v.bootErr != nil {
		return nil, v.bootErr
	}
	inst := &fcvm.Instance{Lease: fcvm.Lease{Instance: req.Instance}, IsJob: true, JobInteractive: req.Interactive != nil}
	if v.identity {
		inst.Lease.Netns, inst.Lease.HostIP, inst.Lease.UID = "fc-"+req.Instance, netip.MustParseAddr("10.100.0.9"), 22009
	}
	return inst, nil
}

func (v *interactiveJobVMM) bootCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.boots)
}

func (v *interactiveJobVMM) ExecuteInteractiveJob(ctx context.Context, instance string, req apptaskproto.Request, input <-chan apptaskproto.InputEvent, receive apptaskproto.OutputReceiver) (apptaskproto.Result, error) {
	v.mu.Lock()
	v.instance, v.request = instance, req
	result, resultErr := v.result, v.err
	v.mu.Unlock()
	if resultErr != nil {
		return apptaskproto.Result{}, resultErr
	}
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
				if err := receive(ctx, "stdout", event.Data); err != nil {
					return apptaskproto.Result{}, err
				}
			case apptaskproto.InputStdinClose:
				v.ended <- nil
				return result, nil
			}
		}
	}
}

func interactiveJobBootRequest(instance, token string) *vmmdpb.JobColdBootRequest {
	digest := sha256.Sum256([]byte(token))
	return &vmmdpb.JobColdBootRequest{
		Instance: instance, AccountId: "acct-1", NodeId: "node-1", Plan: "pro",
		RunId: "run-1", ImageRef: "image", KernelKey: "kernel/1", BaseKey: "base/base.ext4",
		Command: []string{"/bin/sh"}, VcpuCount: 1, MemSizeMib: 256, TaskTimeoutSec: 30, LeaseToken: "lease-1",
		Interactive: &vmmdpb.JobInteractiveAttach{Tty: true, AttachTokenSha256: digest[:], AttachTimeoutSeconds: 5},
	}
}

// attachJob presents token for instance once; vmmd registers the
// rendezvous before JobColdBoot returns, so no retry is needed.
func attachJob(t *testing.T, ctx context.Context, cli vmmdpb.VmmdClient, instance, token string, rows, cols uint32) (vmmdpb.Vmmd_AttachAppTaskClient, *vmmdpb.AttachAppTaskEvent, error) {
	t.Helper()
	stream, err := cli.AttachAppTask(ctx)
	if err != nil {
		t.Fatalf("AttachAppTask: %v", err)
	}
	if err := stream.Send(&vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_Start{
		Start: &vmmdpb.AttachAppTaskStart{Instance: instance, AttachToken: token, Rows: rows, Cols: cols},
	}}); err != nil {
		t.Fatalf("send start: %v", err)
	}
	first, err := stream.Recv()
	return stream, first, err
}

func TestJobColdBootInteractiveSessionRoundTrip(t *testing.T) {
	vmm := newInteractiveJobVMM()
	cli := newExecutionClient(t, vmm)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := cli.JobColdBoot(ctx, interactiveJobBootRequest("job-1", testAttachToken))
	if err != nil {
		t.Fatalf("JobColdBoot: %v", err)
	}
	if !resp.GetInteractive() || resp.GetStartHeld() || resp.GetNetns() != "fc-job-1" {
		t.Fatalf("JobColdBoot response = %+v", resp)
	}
	vmm.mu.Lock()
	boot := vmm.boots[0]
	vmm.mu.Unlock()
	if boot.Interactive == nil || !boot.Interactive.TTY || boot.Interactive.AttachTimeoutSec != 5 || boot.StartHeld {
		t.Fatalf("BootJob request interactive = %+v", boot.Interactive)
	}

	if _, _, err := attachJob(t, ctx, cli, "job-1", "wrong-token", 0, 0); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("wrong token = %v, want PermissionDenied", err)
	}
	stream, first, err := attachJob(t, ctx, cli, "job-1", testAttachToken, 30, 100)
	if err != nil || first.GetAttached() == nil {
		t.Fatalf("first attach event = %#v, %v", first, err)
	}
	if err := stream.Send(&vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_Stdin{Stdin: []byte("hi\n")}}); err != nil {
		t.Fatal(err)
	}
	output, err := stream.Recv()
	if err != nil || string(output.GetOutput().GetChunk()) != "hi\n" || output.GetOutput().GetStream() != "stdout" {
		t.Fatalf("output event = %#v, %v", output, err)
	}
	if err := stream.Send(&vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_StdinClose{StdinClose: &vmmdpb.AttachAppTaskStdinClose{}}}); err != nil {
		t.Fatal(err)
	}
	terminal, err := stream.Recv()
	if err != nil || terminal.GetTerminal().GetStatus() != string(apptaskproto.StatusFailed) ||
		terminal.GetTerminal().GetExitCode().GetValue() != 7 || terminal.GetTerminal().GetFailureCode() != "command_failed" {
		t.Fatalf("terminal = %#v, %v", terminal, err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("attach stream stayed open after the terminal event")
	}

	vmm.mu.Lock()
	instance, req := vmm.instance, vmm.request
	vmm.mu.Unlock()
	if instance != "job-1" || req.Version != apptaskproto.InteractiveVersion || req.TaskID != "job-1" ||
		len(req.Command) != 1 || req.Command[0] != "job" || req.TimeoutSeconds != 30 || !req.Interactive ||
		!req.TTY || req.Rows != 30 || req.Cols != 100 || req.MaxOutputBytes != 0 {
		t.Fatalf("guest request for %q = %#v", instance, req)
	}

	// The token opens exactly one session.
	if _, _, err := attachJob(t, ctx, cli, "job-1", testAttachToken, 0, 0); status.Code(err) != codes.NotFound {
		t.Fatalf("second attach = %v, want NotFound", err)
	}
}

func TestJobColdBootOrdinaryJobIsNotInteractive(t *testing.T) {
	vmm := newInteractiveJobVMM()
	cli := newExecutionClient(t, vmm)
	req := interactiveJobBootRequest("job-plain", testAttachToken)
	req.Interactive = nil
	resp, err := cli.JobColdBoot(context.Background(), req)
	if err != nil || resp.GetInteractive() {
		t.Fatalf("ordinary JobColdBoot = %+v, %v", resp, err)
	}
	if _, _, err := attachJob(t, context.Background(), cli, "job-plain", testAttachToken, 0, 0); status.Code(err) != codes.NotFound {
		t.Fatalf("attach to an ordinary job = %v, want NotFound", err)
	}
}

func TestJobColdBootInteractiveRejectedBeforeBoot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		held   bool
		mutate func(*vmmdpb.JobColdBootRequest)
		want   codes.Code
	}{
		{name: "vmm without interactive jobs", want: codes.Unimplemented},
		{name: "held start", held: true, want: codes.InvalidArgument},
		{name: "short digest", mutate: func(r *vmmdpb.JobColdBootRequest) { r.Interactive.AttachTokenSha256 = []byte("short") }, want: codes.InvalidArgument},
		{name: "negative window", mutate: func(r *vmmdpb.JobColdBootRequest) { r.Interactive.AttachTimeoutSeconds = -1 }, want: codes.InvalidArgument},
		{name: "timeout past session limit", mutate: func(r *vmmdpb.JobColdBootRequest) { r.TaskTimeoutSec = 3601 }, want: codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cli vmmdpb.VmmdClient
			var boots func() int
			if tc.want == codes.Unimplemented {
				plain := &interactiveJobVMM{identity: true}
				plain.fakeVMM = &fakeVMM{jobBootFn: plain.boot}
				cli = newExecutionClient(t, plain.fakeVMM) // no ExecuteInteractiveJob
				boots = plain.bootCount
			} else {
				vmm := newInteractiveJobVMM()
				cli = newExecutionClient(t, vmm)
				boots = vmm.bootCount
			}
			req := interactiveJobBootRequest("job-1", testAttachToken)
			if tc.mutate != nil {
				tc.mutate(req)
			}
			var err error
			if tc.held {
				_, err = cli.JobColdBootHeld(context.Background(), req)
			} else {
				_, err = cli.JobColdBoot(context.Background(), req)
			}
			if status.Code(err) != tc.want {
				t.Fatalf("code = %v (%v), want %v", status.Code(err), err, tc.want)
			}
			if n := boots(); n != 0 {
				t.Fatalf("booted %d VMs for a rejected interactive job", n)
			}
		})
	}
}

func TestJobColdBootInteractiveFailedBootReleasesRendezvous(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*interactiveJobVMM)
	}{
		{name: "boot error", setup: func(v *interactiveJobVMM) { v.bootErr = errors.New("no image") }},
		{name: "incomplete runtime identity", setup: func(v *interactiveJobVMM) { v.identity = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vmm := newInteractiveJobVMM()
			tc.setup(vmm)
			cli := newExecutionClient(t, vmm)
			if _, err := cli.JobColdBoot(context.Background(), interactiveJobBootRequest("job-1", testAttachToken)); err == nil {
				t.Fatal("JobColdBoot succeeded")
			}
			if _, _, err := attachJob(t, context.Background(), cli, "job-1", testAttachToken, 0, 0); status.Code(err) != codes.NotFound {
				t.Fatalf("attach after failed boot = %v, want NotFound", err)
			}
			// The instance id is free for a later attempt.
			vmm.mu.Lock()
			vmm.bootErr, vmm.identity = nil, true
			vmm.mu.Unlock()
			if resp, err := cli.JobColdBoot(context.Background(), interactiveJobBootRequest("job-1", testAttachToken)); err != nil || !resp.GetInteractive() {
				t.Fatalf("retry JobColdBoot = %+v, %v", resp, err)
			}
		})
	}
}

func TestJobColdBootInteractiveRejectsDuplicateInstance(t *testing.T) {
	vmm := newInteractiveJobVMM()
	cli := newExecutionClient(t, vmm)
	if _, err := cli.JobColdBoot(context.Background(), interactiveJobBootRequest("job-1", testAttachToken)); err != nil {
		t.Fatalf("first JobColdBoot: %v", err)
	}
	_, err := cli.JobColdBoot(context.Background(), interactiveJobBootRequest("job-1", "another-token-0123456789"))
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate = %v, want AlreadyExists", err)
	}
	if n := vmm.bootCount(); n != 1 {
		t.Fatalf("boots = %d, want 1", n)
	}
	// The first registration is untouched.
	if _, first, err := attachJob(t, context.Background(), cli, "job-1", testAttachToken, 0, 0); err != nil || first.GetAttached() == nil {
		t.Fatalf("attach with the first token = %#v, %v", first, err)
	}
}

func TestInteractiveJobSessionEndedWhenVMIsGone(t *testing.T) {
	vmm := newInteractiveJobVMM()
	vmm.err = fcvm.ErrJobSessionInstanceNotFound
	cli := newExecutionClient(t, vmm)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.JobColdBoot(ctx, interactiveJobBootRequest("job-1", testAttachToken)); err != nil {
		t.Fatal(err)
	}
	stream, first, err := attachJob(t, ctx, cli, "job-1", testAttachToken, 0, 0)
	if err != nil || first.GetAttached() == nil {
		t.Fatalf("attach = %#v, %v", first, err)
	}
	terminal, err := stream.Recv()
	if err != nil || terminal.GetTerminal().GetFailureCode() != "session_ended" ||
		terminal.GetTerminal().GetStatus() != string(apptaskproto.StatusFailed) || terminal.GetTerminal().GetExitCode() != nil {
		t.Fatalf("terminal = %#v, %v", terminal, err)
	}
}

func TestInteractiveJobClientDisconnectCancelsSession(t *testing.T) {
	vmm := newInteractiveJobVMM()
	cli := newExecutionClient(t, vmm)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.JobColdBoot(ctx, interactiveJobBootRequest("job-1", testAttachToken)); err != nil {
		t.Fatal(err)
	}
	clientCtx, detach := context.WithCancel(ctx)
	_, first, err := attachJob(t, clientCtx, cli, "job-1", testAttachToken, 0, 0)
	if err != nil || first.GetAttached() == nil {
		t.Fatalf("attach = %#v, %v", first, err)
	}
	detach()
	select {
	case err := <-vmm.ended:
		if err == nil {
			t.Fatal("session ended without its context being cancelled")
		}
	case <-ctx.Done():
		t.Fatal("client disconnect did not cancel the session")
	}
}
