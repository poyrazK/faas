// adr: 20261022184531
package vmmdgrpc_test

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

type baseReportingVMM struct {
	*fakeVMM
	report []fcvm.BaseGenerationReadiness
}

func (v *baseReportingVMM) BaseGenerationReadiness(context.Context) []fcvm.BaseGenerationReadiness {
	return v.report
}

func TestPing_ReportsBaseGenerationReadiness(t *testing.T) {
	refused := fcvm.BaseGenerationReadiness{BaseKey: "base/base-minimal-amd64.ext4", Generation: "ghcr.io/p/base-minimal@sha256:db529ed",
		Reason: "shared storage holds generation ghcr.io/p/base-minimal@sha256:5ca69ea"}
	for _, tc := range []struct {
		name string
		vmm  vmmdgrpc.VmmdAPI
		want []*vmmdpb.BaseGenerationReadiness
	}{
		{name: "a manager without the report sends none", vmm: &fakeVMM{}},
		{name: "a refused generation crosses the wire", vmm: &baseReportingVMM{fakeVMM: &fakeVMM{}, report: []fcvm.BaseGenerationReadiness{refused}},
			want: []*vmmdpb.BaseGenerationReadiness{{BaseKey: refused.BaseKey, Generation: refused.Generation, Reason: refused.Reason}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := vmmdgrpc.New(tc.vmm, wire.NewOpsMetrics("vmmd_test"), "1.17.0", nil).Ping(context.Background(), &vmmdpb.PingRequest{})
			if err != nil {
				t.Fatal(err)
			}
			got := resp.GetBaseGenerations()
			if len(got) != len(tc.want) {
				t.Fatalf("base generations = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i].GetBaseKey() != tc.want[i].GetBaseKey() || got[i].GetGeneration() != tc.want[i].GetGeneration() ||
					got[i].GetAdoptable() != tc.want[i].GetAdoptable() || got[i].GetReason() != tc.want[i].GetReason() {
					t.Fatalf("base generation %d = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
