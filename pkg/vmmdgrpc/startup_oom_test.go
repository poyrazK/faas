package vmmdgrpc

import (
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// TestWakeResponseCarriesStartupOOM: a wake whose app the kernel OOM-killed
// before readiness reports it, so gateways keep the wake out of the platform
// wake SLO.
func TestWakeResponseCarriesStartupOOM(t *testing.T) {
	for _, oom := range []bool{true, false} {
		resp := wakeResponseFromInstance("i", fcvm.WakeRequest{}, &fcvm.Instance{Method: fcvm.WakeColdBoot, StartupOOM: oom}, vmmdpb.WakeMethod_WAKE_COLD_BOOT)
		if resp.GetStartupOom() != oom {
			t.Fatalf("startup_oom = %v, want %v", resp.GetStartupOom(), oom)
		}
	}
}
