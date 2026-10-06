package sched_test

// adr: 642
import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRegionalRuntimeRetirementIndependentFencesAndLostReply(t *testing.T) {
	f := newRetirementCell(t, true)
	hostFence, cellFence := uuid.NewString(), uuid.NewString()
	v := &retirementWireVMM{fakeVMM: &fakeVMM{}, store: f.store, firstFence: hostFence}
	var lost atomic.Bool
	interceptor := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err == nil && !lost.Swap(true) {
			return nil, status.Error(codes.Unavailable, "reply lost after native stop")
		}
		return resp, err
	}
	router, _ := newRetirementWire(t, f, v, "schedd.faas", "native-a", false, interceptor)
	owner := sched.RegionalRuntimeRetirement{Authority: f.authority, Signer: f.signer, Router: router}
	if _, err := owner.Retire(t.Context(), f.p.InstanceID, f.p, cellFence); err == nil {
		t.Fatal("lost stop response released capacity")
	}
	retained, err := f.store.RegionalRuntimeCapacity(t.Context(), f.p.Grant.Binding.RegionalAppID, f.p.ID)
	if err != nil || retained.FenceID != cellFence || retained.ReleasedAt != nil || retained.Stop != nil {
		t.Fatal("lost reply changed original hold", err)
	}
	if err = f.store.DeleteApp(t.Context(), f.p.Grant.Binding.RegionalAppID); err != nil {
		t.Fatal(err)
	}
	closed, err := owner.Retire(t.Context(), f.p.InstanceID, f.p, uuid.NewString())
	if err != nil || closed.StopEnvelope == nil || closed.Stop == nil || closed.ReleasedAt == nil || closed.FenceID != cellFence || v.calls.Load() != 2 {
		t.Fatal("independent first fences were not reconciled", err)
	}
	stop, err := f.authority.Cells.VerifyCapacityStop(*closed.StopEnvelope)
	if err != nil || stop.Permit != f.p || stop.FenceID != cellFence || stop.HostFenceID != hostFence || v.firstFence != hostFence {
		t.Fatal("signed relation lost original ownership or fence", err)
	}
	replay, err := owner.Retire(t.Context(), f.p.InstanceID, f.p, uuid.NewString())
	if err != nil || !reflect.DeepEqual(closed, replay) || v.calls.Load() != 2 {
		t.Fatal("durable relation was not replayed without native work", err)
	}
}
