package vmmdgrpc_test

// adr: 1790 — image runs boot with the image process and -u/-w overrides.

import (
	"context"
	"strings"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestJobColdBootImageProcessEchoAndPassThrough(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		imageProcess         bool
		user, workdir        string
		wantUser, wantWorkdr string
	}{
		{name: "ordinary job"},
		{name: "image defaults", imageProcess: true},
		{name: "overrides", imageProcess: true, user: "app:staff", workdir: "/srv", wantUser: "app:staff", wantWorkdr: "/srv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vmm := newInteractiveJobVMM()
			cli := newExecutionClient(t, vmm)
			req := interactiveJobBootRequest("job-process", testAttachToken)
			req.Interactive = nil
			req.ImageProcess, req.ProcessUser, req.ProcessWorkdir = tc.imageProcess, tc.user, tc.workdir
			resp, err := cli.JobColdBoot(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetImageProcess() != tc.imageProcess {
				t.Fatalf("image_process echo = %t, want %t", resp.GetImageProcess(), tc.imageProcess)
			}
			vmm.mu.Lock()
			boot := vmm.boots[0]
			vmm.mu.Unlock()
			if boot.ImageProcess != tc.imageProcess || boot.ProcessUser != tc.wantUser || boot.ProcessWorkdir != tc.wantWorkdr {
				t.Fatalf("boot request = image %t user %q workdir %q", boot.ImageProcess, boot.ProcessUser, boot.ProcessWorkdir)
			}
		})
	}
}

func TestJobColdBootRejectsInvalidImageProcessBeforeBoot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*vmmdpb.JobColdBootRequest)
	}{
		{name: "user without image process", mutate: func(r *vmmdpb.JobColdBootRequest) { r.ProcessUser = "app" }},
		{name: "workdir without image process", mutate: func(r *vmmdpb.JobColdBootRequest) { r.ProcessWorkdir = "/srv" }},
		{name: "user with whitespace", mutate: func(r *vmmdpb.JobColdBootRequest) { r.ImageProcess, r.ProcessUser = true, "a b" }},
		{name: "user with two groups", mutate: func(r *vmmdpb.JobColdBootRequest) { r.ImageProcess, r.ProcessUser = true, "a:b:c" }},
		{name: "user too long", mutate: func(r *vmmdpb.JobColdBootRequest) {
			r.ImageProcess, r.ProcessUser = true, strings.Repeat("u", 257)
		}},
		{name: "relative workdir", mutate: func(r *vmmdpb.JobColdBootRequest) { r.ImageProcess, r.ProcessWorkdir = true, "srv" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vmm := newInteractiveJobVMM()
			cli := newExecutionClient(t, vmm)
			req := interactiveJobBootRequest("job-bad-process", testAttachToken)
			req.Interactive = nil
			tc.mutate(req)
			if _, err := cli.JobColdBoot(context.Background(), req); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("JobColdBoot = %v, want InvalidArgument", err)
			}
			if vmm.bootCount() != 0 {
				t.Fatal("an invalid process booted a VM")
			}
		})
	}
}
