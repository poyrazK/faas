// adr: 20261116182615 §3 — a browse task runs gregale-browser fetch with the
// URL as an argument after --, never as shell text, only in a browser
// session on web egress and within the plan; its end comes back on the
// session's stream.

package vmmdgrpc_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

// taskVMM is a sandboxRunVMM that runs sandbox tasks.
type taskVMM struct {
	*sandboxRunVMM
	tmu     sync.Mutex
	argv    []string
	timeout time.Duration
	max     int64
}

func (v *taskVMM) RunSandboxTask(_ context.Context, instance, _ string, argv []string, timeout time.Duration, maxOutput int64) (fcvm.SandboxTaskResult, error) {
	v.tmu.Lock()
	defer v.tmu.Unlock()
	v.argv, v.timeout, v.max = argv, timeout, maxOutput
	return fcvm.SandboxTaskResult{ExitCode: 0, Output: []byte(`{"title":"E"}`)}, nil
}

func taskStart(mutate func(*vmmdpb.RunSandboxStart)) *vmmdpb.RunSandboxRequest {
	r := sandboxRunStart()
	s := r.GetStart()
	s.Browser, s.NetworkMode = true, api.SandboxNetworkWeb
	s.Task = &vmmdpb.RunSandboxTask{Kind: "browse", Url: "https://example.com/a?b=1", MaxChars: 500, TimeoutMs: 60_000,
		MaxOutputBytes: int64(api.SandboxTaskOutputMaxBytes)}
	if mutate != nil {
		mutate(s)
	}
	return r
}

func TestRunSandboxRunsItsTask(t *testing.T) {
	vmm := &taskVMM{sandboxRunVMM: newSandboxRunVMM()}
	cli := newSandboxRunClient(t, vmm, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	stream, err := cli.RunSandbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(taskStart(nil)); err != nil {
		t.Fatal(err)
	}
	ev := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetTaskDone() != nil })
	if done := ev.GetTaskDone(); done.GetExitCode() != 0 || string(done.GetOutput()) != `{"title":"E"}` {
		t.Fatalf("task done = %+v", done)
	}
	vmm.tmu.Lock()
	defer vmm.tmu.Unlock()
	want := []string{"gregale-browser", "fetch", "--max-chars", "500", "--timeout", "60", "--", "https://example.com/a?b=1"}
	if !slices.Equal(vmm.argv, want) || vmm.timeout != time.Minute || vmm.max != int64(api.SandboxTaskOutputMaxBytes) {
		t.Fatalf("ran %q, %v, %d", vmm.argv, vmm.timeout, vmm.max)
	}
}

func TestRunSandboxTaskRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		noTask bool
		mutate func(*vmmdpb.RunSandboxStart)
		want   codes.Code
	}{
		{"an unknown kind", false, func(s *vmmdpb.RunSandboxStart) { s.Task.Kind = "shell" }, codes.InvalidArgument},
		{"no browser", false, func(s *vmmdpb.RunSandboxStart) { s.Browser = false }, codes.InvalidArgument},
		{"registries egress", false, func(s *vmmdpb.RunSandboxStart) { s.NetworkMode = api.SandboxNetworkRegistries }, codes.InvalidArgument},
		{"a private host", false, func(s *vmmdpb.RunSandboxStart) { s.Task.Url = "http://169.254.169.254/" }, codes.InvalidArgument},
		{"a flag as the URL", false, func(s *vmmdpb.RunSandboxStart) { s.Task.Url = "--remote-debugging-port=1" }, codes.InvalidArgument},
		{"too many chars", false, func(s *vmmdpb.RunSandboxStart) { s.Task.MaxChars = int32(api.SandboxBrowseMaxChars + 1) }, codes.InvalidArgument},
		{"past the plan's timeout", false, func(s *vmmdpb.RunSandboxStart) { s.Task.TimeoutMs = 121_000 }, codes.InvalidArgument},
		{"too much output", false, func(s *vmmdpb.RunSandboxStart) { s.Task.MaxOutputBytes = int64(api.SandboxTaskOutputMaxBytes + 1) }, codes.InvalidArgument},
		{"a vmm without tasks", true, nil, codes.Unimplemented},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &taskVMM{sandboxRunVMM: newSandboxRunVMM()}
			var vmm vmmdgrpc.VmmdAPI = run
			if tc.noTask {
				vmm = run.sandboxRunVMM
			}
			stream, err := newSandboxRunClient(t, vmm, true).RunSandbox(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(taskStart(tc.mutate)); err != nil && !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			if _, err := stream.Recv(); status.Code(err) != tc.want {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
			if woken, _, _, _ := run.state(); woken != 0 {
				t.Fatal("a refused task booted a VM")
			}
		})
	}
}
