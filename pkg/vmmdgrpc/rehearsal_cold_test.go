package vmmdgrpc_test

import (
	"context"
	"errors"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sync"
	"testing"
)

type coldRuntimeVMM struct {
	*fakeVMM
	mu                    sync.Mutex
	instance, preparation string
	err                   error
}

func (v *coldRuntimeVMM) RetireRehearsalCold(_ context.Context, instance, prep string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.instance, v.preparation = instance, prep
	return v.err
}

func TestColdStartRPCRequiresExactAuthorityAndNeverFallsBack(t *testing.T) {
	for _, mode := range []string{"complete", "owner_lost", "unsupported", "conflicting_authority", "ordinary"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			ordinary := 0
			base := &fakeVMM{destroyWithExport: func(context.Context, string, string) (int, error) { mu.Lock(); ordinary++; mu.Unlock(); return 0, nil }}
			v := &coldRuntimeVMM{fakeVMM: base}
			if mode == "owner_lost" {
				v.err = errors.New("original collector unavailable")
			}
			cli := newExecutionClient(t, v)
			if mode == "unsupported" {
				cli = newExecutionClient(t, base)
			}
			req := &vmmdpb.DestroyRequest{Instance: "22222222-2222-4222-8222-222222222222", RehearsalColdPreparationId: "33333333-3333-4333-8333-333333333333"}
			if mode == "conflicting_authority" {
				req.RehearsalWorkerFaultId = req.RehearsalColdPreparationId
			}
			if mode == "ordinary" {
				req.RehearsalColdPreparationId = ""
			}
			response, err := cli.Destroy(t.Context(), req)
			valid := mode == "complete" || mode == "ordinary"
			if (err == nil) != valid {
				t.Fatalf("RPC: %v", err)
			}
			if mode == "unsupported" && status.Code(err) != codes.Unimplemented {
				t.Fatal(err)
			}
			if mode == "conflicting_authority" && status.Code(err) != codes.InvalidArgument {
				t.Fatal(err)
			}
			if valid && (response.GetInstance() != req.Instance || response.GetRehearsalColdPreparationId() != req.RehearsalColdPreparationId) {
				t.Fatal("RPC did not echo authority")
			}
			mu.Lock()
			calls := ordinary
			mu.Unlock()
			if (calls == 1) != (mode == "ordinary") {
				t.Fatal("cold retirement used ordinary destruction")
			}
		})
	}
}
