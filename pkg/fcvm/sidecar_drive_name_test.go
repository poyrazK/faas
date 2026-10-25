package fcvm

import (
	"path/filepath"
	"testing"
)

// adr: 069
// A restore must stage each companion drive under the jail name cold boot
// gave it, because the Firecracker snapshot records that name. Staging by
// the source basename made every restore of an app with a companion fail
// ("Block: ... No such file or directory (os error 2) sidecar-1.ext4") and
// fall back to cold boot.
func TestRestoreSidecarDriveNameMatchesColdBoot(t *testing.T) {
	cfg := BuildColdBootConfig(ColdBootSpec{KernelKey: "k", BaseKey: "base.ext4", VcpuCount: 2, MemSizeMiB: 256, Tap: "tap0",
		Workloads: []WorkloadSpec{{StorageKey: "cache/main.ext4"}, {StorageKey: "cache/one.ext4"}, {StorageKey: "cache/two.ext4"}}}, 1)
	if len(cfg.Drives) != 4 {
		t.Fatalf("drives = %d, want base, main and two sidecars", len(cfg.Drives))
	}
	seen := map[string]bool{}
	for i := 2; i < len(cfg.Drives); i++ {
		d := cfg.Drives[i]
		cold := coldBootReadOnlyDriveName(i, d)
		if got := restoreSidecarDriveName(i - 1); got != cold {
			t.Fatalf("workload %d: restore stages %q, cold boot staged %q", i-1, got, cold)
		}
		if cold == filepath.Base(d.PathOnHost) || seen[cold] {
			t.Fatalf("drive %d: jail name %q must be stable per slot and unique", i, cold)
		}
		seen[cold] = true
	}
}
