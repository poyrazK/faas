//go:build metal && linux

package builderd

import (
	"context"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"os"
	"path/filepath"
	"testing"
)

type completionVMClient struct {
	vmmdpb.VmmdClient
	destroys int
	stopped  *vmmdpb.StopInstanceRequest
}

func (c *completionVMClient) Destroy(context.Context, *vmmdpb.DestroyRequest, ...grpc.CallOption) (*vmmdpb.DestroyResponse, error) {
	c.destroys++
	return &vmmdpb.DestroyResponse{ExitCode: 1}, nil
}
func (c *completionVMClient) StopInstance(_ context.Context, req *vmmdpb.StopInstanceRequest, _ ...grpc.CallOption) (*vmmdpb.StopInstanceResponse, error) {
	c.stopped = req
	return &vmmdpb.StopInstanceResponse{}, nil
}
func TestVMMCancelUsesInterruptRPC(t *testing.T) {
	client := &completionVMClient{}
	d := &VMMDriver{cli: client}
	if err := d.Cancel(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	if client.destroys != 0 || client.stopped == nil || client.stopped.Instance != "build-test" || client.stopped.Signal != 9 {
		t.Fatalf("wrong cancellation RPC: %+v", client)
	}
}
func TestVMMCompletionPreservesTypedFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "build-done.json"), []byte(`{"exit_code":1,"failure_class":"FailureUserError","failure_code":"dep_install_failed","failure_pkg":"npm"}`), 0600); err != nil {
		t.Fatal(err)
	}
	client := &completionVMClient{}
	d := &VMMDriver{cli: client}
	got, err := d.WaitForCompletion(context.Background(), BuildHandle{BuildID: "test", Instance: "build-test", ExportDir: dir, TimeoutSec: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.FailureCode != "dep_install_failed" || got.FailurePkg != "npm" || got.FailureClass != "FailureUserError" {
		t.Fatalf("failure detail lost: %+v", got)
	}
}
