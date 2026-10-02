package vmmdgrpc_test

// adr: 429

import (
	"context"
	"errors"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Wire capability tests only; no fake response is native filesystem evidence.
type receiptContractVMM struct {
	*fakeVMM
	mode  string
	calls int
}

func (m *receiptContractVMM) MaterializeVerifiedParentExt4(_ context.Context, in imagechain.ParentMaterialization) (imagechain.ParentMaterialization, error) {
	m.calls++
	if m.mode == "failure" {
		return imagechain.ParentMaterialization{}, errors.New("copy failed")
	}
	if m.mode == "mismatch" {
		in.Artifact.Bytes++
	}
	return in, nil
}
func TestVerifiedParentCapabilityRefusals(t *testing.T) {
	for _, mode := range []string{"legacy", "unknown", "wrong key", "failure", "mismatch", "complete"} {
		t.Run(mode, func(t *testing.T) {
			owner := &receiptContractVMM{fakeVMM: &fakeVMM{}, mode: mode}
			var backend vmmdgrpc.VmmdAPI = owner
			if mode == "legacy" {
				backend = &fakeVMM{}
			}
			s := vmmdgrpc.New(backend, wire.NewOpsMetrics("verified_parent_test"), "test", nil)
			req := &vmmdpb.MaterializeVerifiedParentExt4Request{StorageKey: sched.BaseKeyForArch(sched.ParentBaseRuntime, "amd64"), TargetDir: "/dev/shm/faas-base-staging/test", ArtifactDigest: imagechain.Digest([]byte("actual bytes")), ArtifactBytes: 12}
			expectedCode := codes.FailedPrecondition
			switch mode {
			case "legacy":
				expectedCode = codes.Unimplemented
			case "unknown":
				req.ProtoReflect().SetUnknown([]byte{0x28, 1})
				expectedCode = codes.InvalidArgument
			case "wrong key":
				req.StorageKey = "apps/customer.ext4"
				expectedCode = codes.InvalidArgument
			case "complete":
				expectedCode = codes.OK
			}
			res, err := s.MaterializeVerifiedParentExt4(t.Context(), req)
			if status.Code(err) != expectedCode {
				t.Fatalf("capability result: %v", err)
			}
			if expectedCode != codes.OK && res != nil {
				t.Fatal("failed capability returned receipt")
			}
			if mode == "unknown" || mode == "wrong key" {
				if owner.calls != 0 {
					t.Fatal("invalid request reached owner")
				}
			}
			if res != nil && (res.ArtifactDigest != req.ArtifactDigest || res.ArtifactBytes != req.ArtifactBytes) {
				t.Fatal("wire lost actual identity")
			}
		})
	}
}
