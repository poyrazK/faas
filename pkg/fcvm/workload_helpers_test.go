//go:build !metal

// adr: 223

// Whitebox tests for the workload-helper surface (issue #463 /
// ADR-069 / PR-B review finding #3 — reject sidecar named "main").
// The helpers are package-internal; a whitebox test pins their
// behaviour without dragging in the full Manager / Jailer
// machinery the metal suite exercises. Mirrors the
// `whitebox-test-file-pattern` discipline: narrow scope, no
// unexported fixtures beyond the helper under test.

package fcvm

import "testing"

// TestBuildWorkloadsForColdBoot_RejectsSidecarNamedMain pins
// the load-bearing rejection: a sidecar whose Name == "main"
// must be silently dropped (it would collide with the main
// workload's reserved leaf in the cgroup scope and the
// characterize probe's filter key). The other sidecars in the
// same request must survive — apid's API gate is the
// user-facing surface, this is the host-side defence-in-depth
// for older apid or hand-crafted WakeRequests in metal tests.
func TestBuildWorkloadsForColdBoot_RejectsSidecarNamedMain(t *testing.T) {
	req := WakeRequest{
		LayerKey:   "apps/main.ext4",
		VcpuCount:  2,
		MemSizeMiB: 256,
		Port:       8080,
		Sidecars: []WorkloadSpec{
			{Name: "metrics", Type: "sidecar", StorageKey: "apps/metrics.ext4", DriveID: "layer-sidecar-0", RamMB: 64, Port: 9090, Essential: true},
			{Name: "main", Type: "sidecar", StorageKey: "apps/evil.ext4", DriveID: "layer-sidecar-1", RamMB: 32, Port: 9091, Essential: false},
			{Name: "logger", Type: "sidecar", StorageKey: "apps/logger.ext4", DriveID: "layer-sidecar-2", RamMB: 32, Port: 9092, Essential: true},
		},
	}
	got := buildWorkloadsForColdBoot(req)
	if len(got) != 3 {
		t.Fatalf("got %d workloads, want 3 (main + metrics + logger; 'main'-named sidecar dropped)", len(got))
	}
	if got[0].Name != "main" {
		t.Errorf("got[0].Name = %q, want %q", got[0].Name, "main")
	}
	if got[0].Type != "main" {
		t.Errorf("got[0].Type = %q, want %q", got[0].Type, "main")
	}
	if got[1].Name != "metrics" {
		t.Errorf("got[1].Name = %q, want %q", got[1].Name, "metrics")
	}
	if got[2].Name != "logger" {
		t.Errorf("got[2].Name = %q, want %q", got[2].Name, "logger")
	}
}

// TestBuildWorkloadsForColdBoot_LegacySingleWorkload pins the
// no-sidecar fallback: an empty Sidecars slice must return nil
// so BootColdBoot's "Workloads empty → resolve LayerKey" branch
// runs unchanged. Roster validation and the main-name rejection
// are only exercised on the new path; a regression that drops
// them on the new path while keeping the legacy path unchanged must
// not affect this test.
func TestBuildWorkloadsForColdBoot_LegacySingleWorkload(t *testing.T) {
	req := WakeRequest{
		LayerKey:   "apps/legacy.ext4",
		VcpuCount:  2,
		MemSizeMiB: 256,
		Port:       8080,
	}
	if got := buildWorkloadsForColdBoot(req); got != nil {
		t.Errorf("empty Sidecars: got %v, want nil (legacy single-workload path)", got)
	}
}

// TestBuildWorkloadsForRestore_MatchesColdBoot pins the restore
// twin's contract: buildWorkloadsForRestore is currently a
// one-line alias of buildWorkloadsForColdBoot and must stay in
// lockstep (the same rejection runs on the restore path). A
// future PR-C extension that diverges the two helpers must
// update this test.
func TestBuildWorkloadsForRestore_MatchesColdBoot(t *testing.T) {
	req := WakeRequest{
		LayerKey:   "apps/main.ext4",
		VcpuCount:  2,
		MemSizeMiB: 256,
		Port:       8080,
		Sidecars: []WorkloadSpec{
			{Name: "main", Type: "sidecar", StorageKey: "apps/evil.ext4", DriveID: "layer-sidecar-0", RamMB: 32, Essential: false},
		},
	}
	cold := buildWorkloadsForColdBoot(req)
	restore := buildWorkloadsForRestore(req)
	if len(cold) != len(restore) {
		t.Fatalf("cold-boot len %d != restore len %d (helpers diverged)", len(cold), len(restore))
	}
	for i := range cold {
		if cold[i].Name != restore[i].Name {
			t.Errorf("[%d] cold.Name=%q restore.Name=%q (helpers diverged)", i, cold[i].Name, restore[i].Name)
		}
	}
}
