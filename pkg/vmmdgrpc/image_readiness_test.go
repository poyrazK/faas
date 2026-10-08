// adr:683
package vmmdgrpc

import (
	"log/slog"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/wire"
)

type imageReadinessResumeVMM struct {
	*migrationHandlerVMM
	required bool
}

func (v *imageReadinessResumeVMM) ImageHealthcheckRequiredFor(string) bool { return v.required }

func TestImageHealthcheckWarmResumeAcknowledgesStoredContract(t *testing.T) {
	for _, required := range []bool{false, true} {
		vmm := &imageReadinessResumeVMM{migrationHandlerVMM: &migrationHandlerVMM{}, required: required}
		server := New(vmm, wire.NewOpsMetrics("image-readiness-test"), "1.10.0", slog.Default())
		response, err := server.ResumeWarmInstance(t.Context(), &vmmdpb.ResumeWarmInstanceRequest{Instance: "image", ImageHealthcheckRequired: true})
		if !required {
			if err == nil || vmm.resumes != 0 {
				t.Fatal("new flag upgraded an older unverified instance")
			}
		} else if err != nil || !response.GetImageHealthcheckVerified() || !response.GetSupportsImageHealthcheckMonitoring() || vmm.resumes != 1 {
			t.Fatalf("stored contract not acknowledged: %+v %v", response, err)
		}
	}
}

func TestImageHealthcheckWakeResponseUsesInstanceEvidence(t *testing.T) {
	for _, tc := range []struct{ required, paused, want bool }{{false, false, false}, {true, true, false}, {true, false, true}} {
		response := wakeResponseFromInstance("image", fcvm.WakeRequest{ImageHealthcheckRequired: true}, &fcvm.Instance{ImageHealthcheckRequired: tc.required, Paused: tc.paused}, vmmdpb.WakeMethod_WAKE_COLD_BOOT)
		if response.GetImageHealthcheckVerified() != tc.want {
			t.Fatalf("response verified an unqualified instance: %+v", tc)
		}
		if !response.GetSupportsImageHealthcheckMonitoring() {
			t.Fatal("runtime monitor capability omitted")
		}
	}
}
