package vmmdgrpc_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type workerFaultRuntimeVMM struct {
	*fakeVMM
	mu              sync.Mutex
	instance, fault string
	err             error
}

func (v *workerFaultRuntimeVMM) TerminateRehearsalWorker(_ context.Context, instance, fault string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.instance, v.fault = instance, fault
	return v.err
}

func TestWorkerTerminationRPCNeverFallsBackToOrdinaryDestroy(t *testing.T) {
	for _, mode := range []string{"complete", "owner_lost", "unsupported", "ordinary"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			ordinary := 0
			base := &fakeVMM{destroyWithExport: func(context.Context, string, string) (int, error) { mu.Lock(); ordinary++; mu.Unlock(); return 0, nil }}
			v := &workerFaultRuntimeVMM{fakeVMM: base}
			if mode == "owner_lost" {
				v.err = errors.New("original owner is gone")
			}
			instance, fault := "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
			cli := newExecutionClient(t, v)
			if mode == "unsupported" {
				cli = newExecutionClient(t, base)
			}
			requested := fault
			if mode == "ordinary" {
				requested = ""
			}
			response, err := cli.Destroy(t.Context(), &vmmdpb.DestroyRequest{Instance: instance, RehearsalWorkerFaultId: requested})
			valid := mode == "complete" || mode == "ordinary"
			if (err == nil) != valid {
				t.Fatalf("%s: %v", mode, err)
			}
			if mode == "unsupported" && status.Code(err) != codes.Unimplemented {
				t.Fatalf("unsupported fault: %v", err)
			}
			if valid && (response.GetInstance() != instance || response.GetRehearsalWorkerFaultId() != requested) {
				t.Fatal("RPC lost fault identity")
			}
			mu.Lock()
			calls := ordinary
			mu.Unlock()
			if (calls == 1) != (mode == "ordinary") {
				t.Fatal("fault invoked ordinary destruction")
			}
			v.mu.Lock()
			seenInstance, seenFault := v.instance, v.fault
			v.mu.Unlock()
			if mode == "complete" || mode == "owner_lost" {
				if seenInstance != instance || seenFault != fault {
					t.Fatal("fault authority was not forwarded")
				}
			}
		})
	}
}
