package vmmdgrpc_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestSnapshotsRefusePatchedInstances(t *testing.T) {
	var parked, destroyed []string
	fake := &fakeVMM{
		parkFn: func(_ context.Context, instance string, _ fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
			parked = append(parked, instance)
			return fcvm.SnapshotInfo{MemBytes: 1}, nil
		},
		destroy: func(_ context.Context, instance string) error {
			destroyed = append(destroyed, instance)
			return nil
		},
	}
	diverged := vmmdgrpc.NewDivergedInstances()
	srv := vmmdgrpc.New(fake, wire.NewOpsMetrics("vmmd_diverged_test"), "test", slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithDivergedInstances(diverged)
	ctx := context.Background()
	park := func(instance string) error {
		_, err := srv.PauseAndSnapshot(ctx, &vmmdpb.PauseAndSnapshotRequest{Instance: instance, StorageKey: "mem", VmstateStorageKey: "state"})
		return err
	}

	if err := park("clean"); err != nil || len(parked) != 1 {
		t.Fatalf("unpatched park err=%v parked=%v, want a normal snapshot", err, parked)
	}

	diverged.Mark("patched")
	if _, err := srv.WarmSnapshot(ctx, &vmmdpb.WarmSnapshotRequest{Instance: "patched", StorageKey: "mem", VmstateStorageKey: "state"}); !isDiverged(err) {
		t.Fatalf("warm snapshot of a patched instance = %v, want dev_source_diverged", err)
	}
	if len(destroyed) != 0 {
		t.Fatal("warm refusal destroyed the VM; the scheduler owns that cleanup")
	}
	if err := park("patched"); !isDiverged(err) {
		t.Fatalf("park of a patched instance = %v, want dev_source_diverged", err)
	}
	if len(parked) != 1 || len(destroyed) != 1 || destroyed[0] != "patched" {
		t.Fatalf("patched park snapshotted=%v destroyed=%v, want destroy without snapshot", parked, destroyed)
	}
	if diverged.Has("patched") {
		t.Fatal("destroyed instance stayed in the diverged registry")
	}
}

func isDiverged(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() != codes.OK && strings.Contains(st.Message(), "developer live patch")
}
