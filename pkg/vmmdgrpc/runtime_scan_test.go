package vmmdgrpc_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/runtimescan"
	"github.com/onebox-faas/faas/pkg/scanview"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type runtimeScanWireOwner struct {
	*fakeVMM
	mode  string
	calls int
}

func (o *runtimeScanWireOwner) MaterializeRuntimeScan(ctx context.Context, r runtimescan.Request) (runtimescan.Receipt, error) {
	o.calls++
	if o.mode == "capacity" {
		return runtimescan.Receipt{}, vmmdmount.ErrMountCapacity
	}
	if o.mode == "failure" {
		return runtimescan.Receipt{}, errors.New("native cleanup failed")
	}
	if o.mode == "canceled" {
		return runtimescan.Receipt{}, context.Canceled
	}
	if o.mode == "deadline" {
		return runtimescan.Receipt{}, context.DeadlineExceeded
	}
	hash, _ := runtimeadmission.HashArtifactSources(r.Sources)
	tree := scanview.Tree{Version: 1, Digest: strings.Repeat("a", 64), ProjectionDigest: strings.Repeat("b", 64), Entries: 2, Bytes: 4}
	receipt := runtimescan.Receipt{Version: 1, InputHash: r.InputHash, SourcesHash: hash, TargetDir: r.TargetDir, Views: []runtimescan.View{{SourceTree: tree, ProjectionTree: tree}}}
	if o.mode == "mismatch" {
		receipt.InputHash = strings.Repeat("f", 64)
	}
	if o.mode == "incomplete" {
		receipt.Views = nil
	}
	return receipt, nil
}

func TestRuntimeScanRPCRefusesUnsupportedIncompleteAndFailedOwners(t *testing.T) {
	for _, mode := range []string{"complete", "legacy", "unknown", "nested unknown", "missing base", "mismatch", "incomplete", "failure", "capacity", "canceled", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			owner := &runtimeScanWireOwner{fakeVMM: &fakeVMM{}, mode: mode}
			var backend vmmdgrpc.VmmdAPI = owner
			if mode == "legacy" {
				backend = &fakeVMM{}
			}
			s := vmmdgrpc.New(backend, wire.NewOpsMetrics("runtime_scan_wire"), "fixture", nil)
			req := runtimescan.Request{Version: 1, InputHash: strings.Repeat("a", 64), TargetDir: t.TempDir(), Sources: []runtimeadmission.ArtifactSource{
				{Kind: "base-image", StorageKey: "base/test.ext4", Digest: "sha256:" + strings.Repeat("1", 64), Bytes: 4},
				{Kind: "app-layer", StorageKey: "apps/test.ext4", Digest: "sha256:" + strings.Repeat("2", 64), Bytes: 4},
			}}.ToProto()
			want := codes.FailedPrecondition
			switch mode {
			case "complete":
				want = codes.OK
			case "legacy":
				want = codes.Unimplemented
			case "unknown":
				req.ProtoReflect().SetUnknown([]byte{0x28, 1})
				want = codes.InvalidArgument
			case "nested unknown":
				req.Sources[0].ProtoReflect().SetUnknown([]byte{0x30, 1})
				want = codes.InvalidArgument
			case "missing base":
				req.Sources = req.Sources[1:]
				want = codes.InvalidArgument
			case "capacity":
				want = codes.ResourceExhausted
			case "canceled":
				want = codes.Canceled
			case "deadline":
				want = codes.DeadlineExceeded
			}
			res, err := s.MaterializeRuntimeScan(t.Context(), req)
			if status.Code(err) != want || (want != codes.OK && res != nil) {
				t.Fatal("invalid native receipt crossed wire", mode, err)
			}
			if want == codes.InvalidArgument && owner.calls != 0 {
				t.Fatal("invalid source set reached native owner")
			}
		})
	}
}
