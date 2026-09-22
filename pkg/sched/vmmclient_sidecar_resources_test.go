package sched

// adr: 175

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

func TestAppSpecToProtoCarriesSidecarResourceIsolation(t *testing.T) {
	proto := (AppSpec{Sidecars: []fcvm.WorkloadSpec{{
		Name:          "metrics",
		Type:          "sidecar",
		ScratchMB:     192,
		DiskIOProfile: string(api.SidecarDiskIOProfileHigh),
		StartupProbe:  &api.AppManifestHealthcheck{Test: []string{"CMD", "/ready"}, IntervalS: 5, TimeoutS: 2, Retries: 3, StartPeriodS: 10},
	}}}).toProto()
	if len(proto.GetSidecars()) != 1 {
		t.Fatalf("sidecars = %d, want 1", len(proto.GetSidecars()))
	}
	sc := proto.GetSidecars()[0]
	if sc.GetScratchMb() != 192 {
		t.Fatalf("scratch_mb = %d, want 192", sc.GetScratchMb())
	}
	if sc.GetDiskIoProfile() != string(api.SidecarDiskIOProfileHigh) {
		t.Fatalf("disk_io_profile = %q, want high", sc.GetDiskIoProfile())
	}
	if len(sc.GetStartupProbeTest()) != 2 || sc.GetStartupProbeTest()[1] != "/ready" || sc.GetStartupProbeIntervalS() != 5 || sc.GetStartupProbeTimeoutS() != 2 || sc.GetStartupProbeRetries() != 3 || sc.GetStartupProbeStartPeriodS() != 10 {
		t.Fatalf("startup probe = test=%v interval=%d timeout=%d retries=%d start_period=%d", sc.GetStartupProbeTest(), sc.GetStartupProbeIntervalS(), sc.GetStartupProbeTimeoutS(), sc.GetStartupProbeRetries(), sc.GetStartupProbeStartPeriodS())
	}
}
