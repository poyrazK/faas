package vmmdgrpc_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type busyParentMountVMM struct{ *fakeVMM }

func (*busyParentMountVMM) MountParentExt4(context.Context, string) (string, error) {
	return "", fmt.Errorf("native owner: %w", vmmdmount.ErrMountCapacity)
}
func (*busyParentMountVMM) MaterializeParentExt4(context.Context, string, string) error {
	return fmt.Errorf("native owner: %w", vmmdmount.ErrMountCapacity)
}
func (*busyParentMountVMM) MountOverlayParent(context.Context, string, string, string, string) error {
	return fmt.Errorf("native owner: %w", vmmdmount.ErrMountCapacity)
}
func (*busyParentMountVMM) UmountParentExt4(context.Context, string) error {
	return fmt.Errorf("native owner: %w", vmmdmount.ErrMountBusy)
}
func (*busyParentMountVMM) UmountOverlayParent(context.Context, string) error {
	return fmt.Errorf("native owner: %w", vmmdmount.ErrMountBusy)
}

func TestParentMountWireRefusesCapacityAndActiveOwnership(t *testing.T) {
	s := vmmdgrpc.New(&busyParentMountVMM{fakeVMM: &fakeVMM{}}, wire.NewOpsMetrics("parent_mount_capacity_test"), "test", nil)
	for _, operation := range []string{"mount", "copy", "overlay", "unmount", "unmount overlay"} {
		t.Run(operation, func(t *testing.T) {
			var receipt any
			var err error
			want := codes.ResourceExhausted
			switch operation {
			case "mount":
				receipt, err = s.MountParentExt4ReadOnly(t.Context(), &vmmdpb.MountParentExt4ReadOnlyRequest{StorageKey: sched.BaseKeyForArch(sched.ParentBaseRuntime, "amd64")})
			case "copy":
				receipt, err = s.MaterializeParentExt4(t.Context(), &vmmdpb.MaterializeParentExt4Request{StorageKey: sched.BaseKeyForArch(sched.ParentBaseRuntime, "amd64"), TargetDir: "/dev/shm/faas-base-staging/target"})
			case "overlay":
				receipt, err = s.MountOverlayParent(t.Context(), &vmmdpb.MountOverlayParentRequest{Lowerdir: "lower", Upperdir: "upper", Workdir: "work", Merged: "merged"})
			case "unmount":
				want = codes.FailedPrecondition
				receipt, err = s.UmountParentExt4(t.Context(), &vmmdpb.UmountParentExt4Request{Mountpoint: "owned"})
			case "unmount overlay":
				want = codes.FailedPrecondition
				receipt, err = s.UmountOverlayParent(t.Context(), &vmmdpb.UmountOverlayParentRequest{Merged: "owned"})
			}
			if status.Code(err) != want {
				t.Fatalf("wire result: %v, want %v", err, want)
			}
			// Each concrete nil pointer has a non-nil interface representation.
			if receipt != nil && !reflect.ValueOf(receipt).IsNil() {
				t.Fatal("failed mount returned a receipt")
			}
		})
	}
}
