package vmmdgrpc_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type suspendRuntimeVMM struct {
	*fakeVMM
	mu                sync.Mutex
	instance, receipt string
	err               error
}

func (v *suspendRuntimeVMM) RehearseSuspendResume(_ context.Context, instance, receipt string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.instance, v.receipt = instance, receipt
	return v.err
}
func TestSuspendResumeRPCRequiresOriginalCapabilityAndAuthority(t *testing.T) {
	for _, mode := range []string{"complete", "unsupported", "owner_lost", "invalid_identity"} {
		t.Run(mode, func(t *testing.T) {
			v := &suspendRuntimeVMM{fakeVMM: &fakeVMM{}}
			if mode == "owner_lost" {
				v.err = errors.New("original owner lost")
			}
			client := newExecutionClient(t, v)
			if mode == "unsupported" {
				client = newExecutionClient(t, v.fakeVMM)
			}
			req := &vmmdpb.RehearseSuspendResumeRequest{Instance: uuid.NewString(), ReceiptId: uuid.NewString()}
			if mode == "invalid_identity" {
				req.ReceiptId = ""
			}
			r, err := client.RehearseSuspendResume(t.Context(), req)
			if mode == "complete" {
				if err != nil || r.GetInstance() != req.Instance || r.GetReceiptId() != req.ReceiptId {
					t.Fatal("lifecycle authority not acknowledged")
				}
			} else if err == nil {
				t.Fatal("unsupported or unproven lifecycle succeeded")
			}
			if mode == "unsupported" && status.Code(err) != codes.Unimplemented {
				t.Fatal(err)
			}
			if mode == "invalid_identity" && status.Code(err) != codes.InvalidArgument {
				t.Fatal(err)
			}
		})
	}
}
