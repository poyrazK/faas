package vmmdgrpc_test

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// backingCheckVMM adds the backing check to the shared fake.
type backingCheckVMM struct {
	*fakeVMM
	check func(context.Context, *fcvm.Snapshot, string) (fcvm.SnapshotBackingVerdict, error)
}

func (f backingCheckVMM) CheckSnapshotBackingCurrent(ctx context.Context, snap *fcvm.Snapshot, baseKey string) (fcvm.SnapshotBackingVerdict, error) {
	return f.check(ctx, snap, baseKey)
}

// backingServer calls the handler directly: newServer takes only *fakeVMM.
func backingServer(vmm vmmdgrpc.VmmdAPI) *vmmdgrpc.Server {
	return vmmdgrpc.New(vmm, wire.NewOpsMetrics("vmmd_backing_test"), "1.0.0", nil)
}

// adr: 20261106214916 — schedd asks vmmd whether a capture is stale.
func TestSnapshotBackingCurrent_MapsVerdicts(t *testing.T) {
	const (
		key  = "snap/0b5f5ae4-0f0e-4c51-9d55-1b1d0b8e6a11/captures/3c8f2b1e-9d4a-4c6b-8f0e-2a7d5c9b1e33/v2/mem"
		base = "base/releases/runner-node22-amd64-r1.ext4"
	)
	var gotSnap *fcvm.Snapshot
	var gotBase string
	verdict, failure := fcvm.SnapshotBackingCurrent, error(nil)
	f := backingCheckVMM{fakeVMM: &fakeVMM{}, check: func(_ context.Context, snap *fcvm.Snapshot, baseKey string) (fcvm.SnapshotBackingVerdict, error) {
		gotSnap, gotBase = snap, baseKey
		return verdict, failure
	}}
	cli := backingServer(f)
	req := &vmmdpb.SnapshotBackingCurrentRequest{
		Snapshot: &vmmdpb.SnapshotRef{DeploymentId: "d1", StorageKey: key, FcVersion: "1.17.0", MemBytes: 256 << 20},
		BaseKey:  base,
	}
	for _, tc := range []struct {
		verdict fcvm.SnapshotBackingVerdict
		err     error
		want    vmmdpb.SnapshotBackingVerdict
	}{
		{verdict: fcvm.SnapshotBackingCurrent, want: vmmdpb.SnapshotBackingVerdict_SNAPSHOT_BACKING_CURRENT},
		{verdict: fcvm.SnapshotBackingStale, err: fmt.Errorf("%w: kernel moved", fcvm.ErrSnapshotBackingChanged), want: vmmdpb.SnapshotBackingVerdict_SNAPSHOT_BACKING_STALE},
		{verdict: fcvm.SnapshotBackingUnknown, err: fmt.Errorf("base unreadable"), want: vmmdpb.SnapshotBackingVerdict_SNAPSHOT_BACKING_UNKNOWN},
	} {
		verdict, failure = tc.verdict, tc.err
		resp, err := cli.SnapshotBackingCurrent(context.Background(), req)
		if err != nil {
			t.Fatalf("SnapshotBackingCurrent: %v", err)
		}
		if resp.GetVerdict() != tc.want || (resp.GetDetail() == "") != (tc.err == nil) {
			t.Fatalf("response = %+v, want verdict %s with detail %v", resp, tc.want, tc.err)
		}
	}
	if gotBase != base || gotSnap == nil || gotSnap.StorageKey != key || gotSnap.FCVersion != "1.17.0" || gotSnap.MemBytes != 256<<20 {
		t.Fatalf("checker got snapshot %+v base %q", gotSnap, gotBase)
	}
}

// Only a capture key and a base key reach the check, so the RPC cannot read
// other storage through vmmd.
func TestSnapshotBackingCurrent_RefusesOtherKeys(t *testing.T) {
	called := false
	f := backingCheckVMM{fakeVMM: &fakeVMM{}, check: func(context.Context, *fcvm.Snapshot, string) (fcvm.SnapshotBackingVerdict, error) {
		called = true
		return fcvm.SnapshotBackingCurrent, nil
	}}
	cli := backingServer(f)
	const capture = "snap/0b5f5ae4-0f0e-4c51-9d55-1b1d0b8e6a11/captures/3c8f2b1e-9d4a-4c6b-8f0e-2a7d5c9b1e33/v2/mem"
	for name, req := range map[string]*vmmdpb.SnapshotBackingCurrentRequest{
		"no snapshot":       {BaseKey: "base/runtime-node22.ext4"},
		"not a capture key": {Snapshot: &vmmdpb.SnapshotRef{StorageKey: "apps/a/d1.ext4"}, BaseKey: "base/runtime-node22.ext4"},
		"no base":           {Snapshot: &vmmdpb.SnapshotRef{StorageKey: capture}},
		"not a base":        {Snapshot: &vmmdpb.SnapshotRef{StorageKey: capture}, BaseKey: "apps/a/d1.ext4"},
		"base traversal":    {Snapshot: &vmmdpb.SnapshotRef{StorageKey: capture}, BaseKey: "base/../secrets/x"},
	} {
		_, err := cli.SnapshotBackingCurrent(context.Background(), req)
		if code := status.Code(err); code != codes.InvalidArgument {
			t.Fatalf("%s: code = %v, want InvalidArgument", name, code)
		}
	}
	if called {
		t.Fatal("the backing check ran for a refused request")
	}
}

func TestSnapshotBackingCurrent_UnimplementedWithoutChecker(t *testing.T) {
	cli := backingServer(&fakeVMM{})
	_, err := cli.SnapshotBackingCurrent(context.Background(), &vmmdpb.SnapshotBackingCurrentRequest{
		Snapshot: &vmmdpb.SnapshotRef{StorageKey: "snap/0b5f5ae4-0f0e-4c51-9d55-1b1d0b8e6a11/captures/3c8f2b1e-9d4a-4c6b-8f0e-2a7d5c9b1e33/v2/mem"},
		BaseKey:  "base/runtime-node22.ext4",
	})
	if code := status.Code(err); code != codes.Unimplemented {
		t.Fatalf("code = %v, want Unimplemented", code)
	}
}
