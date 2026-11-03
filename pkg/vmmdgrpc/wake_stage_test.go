// adr: 20261022184531
package vmmdgrpc_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

type failingWakeVMM struct {
	*fakeVMM
	err error
}

func (v *failingWakeVMM) Wake(context.Context, fcvm.WakeRequest) (*fcvm.Instance, error) {
	return nil, v.err
}

// A cold boot that failed before the VM started says so beside its Problem;
// any other failure does not.
func TestCreateColdBoot_MarksFailuresBeforeTheVMStarted(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		wantPre bool
	}{
		{"network setup", fmt.Errorf("wake i: network setup: %w", fcvm.ErrWakeNotStarted), true},
		{"after the guest started", errors.New("wake i: cold boot: guest kernel panic"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := vmmdgrpc.New(&failingWakeVMM{fakeVMM: &fakeVMM{}, err: tc.err}, wire.NewOpsMetrics("vmmd_test"), "1.17.0", nil)
			_, err := srv.CreateColdBoot(context.Background(), &vmmdpb.CreateColdBootRequest{
				Instance: "i", App: &vmmdpb.AppSpec{BaseKey: "base/base-amd64.ext4", LayerKey: "apps/a/layer.ext4", VcpuCount: 1, MemSizeMib: 256},
			})
			if err == nil {
				t.Fatal("CreateColdBoot succeeded")
			}
			stage, _ := grpcerr.MetadataValue(err, grpcerr.MetadataWakeStage)
			if (stage == grpcerr.WakeStagePreStart) != tc.wantPre {
				t.Fatalf("wake stage = %q, want pre-start %t", stage, tc.wantPre)
			}
		})
	}
}
