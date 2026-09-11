package sched

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
}
