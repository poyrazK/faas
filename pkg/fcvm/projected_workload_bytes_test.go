//go:build !metal

// adr: 223

// Whitebox tests for the pre-marshal byte projection helpers
// (issue #463 / ADR-069 / PR-B review finding #7). The cap is
// enforced BEFORE json.Marshal so a malicious or buggy wire
// payload with an unbounded Name field can't allocate without
// bound during marshalling. The projection is deliberately
// conservative (overestimates) — a false positive just rejects
// a payload that wouldn't have hit the cap anyway, so the
// false-positive rate is zero.

package fcvm

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestProjectedWorkloadManifestBytes_Bounded pins the helper's
// contract: a normal workload spec projects to a small number
// of bytes (well under api.MaxExportedLayerBytes), and the
// projection scales linearly with Name length. A regression
// that forgets to multiply by the escape factor would still
// pass this test; the unbounded-Name test below catches that.
func TestProjectedWorkloadManifestBytes_Bounded(t *testing.T) {
	normal := WorkloadSpec{Name: "metrics", Type: "sidecar", RamMB: 64, Port: 9090, Essential: true}
	got := projectedWorkloadManifestBytes(normal)
	if got <= 0 || got > 1024 {
		t.Errorf("normal projection = %d, want 0 < x <= 1024", got)
	}
}

// TestProjectedWorkloadManifestBytes_RejectsUnboundedName pins
// the security-critical case: an attacker-controlled Name that
// projects to MORE than api.MaxExportedLayerBytes must be
// REJECTED before json.Marshal allocates. The cap is
// api.MaxExportedLayerBytes (4 GiB); a Name of 3 GiB projects
// to 6 GiB and must trip the cap. A regression that drops the
// *2 escape multiplier or the +64 overhead would still pass for
// small inputs but fail here.
func TestProjectedWorkloadManifestBytes_RejectsUnboundedName(t *testing.T) {
	huge := WorkloadSpec{Name: strings.Repeat("a", 3*1024*1024*1024), Type: "sidecar", RamMB: 64, Port: 9090}
	got := projectedWorkloadManifestBytes(huge)
	if got <= api.MaxExportedLayerBytes {
		t.Errorf("3 GiB Name projected = %d bytes, want > %d (cap)", got, api.MaxExportedLayerBytes)
	}
}

// TestProjectedWorkloadRosterBytes_SumsWorkloads pins the
// roster projection: it's the sum of per-workload projections
// plus the wrapper overhead. The projection naturally includes every helper.
func TestProjectedWorkloadRosterBytes_SumsWorkloads(t *testing.T) {
	main := WorkloadSpec{Name: "main", Type: "main", RamMB: 256, Port: 8080, Essential: true}
	sidecars := []WorkloadSpec{
		{Name: "metrics", Type: "sidecar", RamMB: 64, Port: 9090, Essential: true},
		{Name: "logger", Type: "sidecar", RamMB: 32, Port: 9091, Essential: false},
		{Name: "proxy", Type: "sidecar", RamMB: 48, Port: 9092, Essential: true},
		{Name: "tracer", Type: "sidecar", RamMB: 16, Port: 9093, Essential: true},
		{Name: "migrator", Type: "init", RamMB: 64, Essential: true},
	}
	got := projectedWorkloadRosterBytes(main, sidecars)
	want := projectedWorkloadManifestBytes(main) + 64
	for _, sidecar := range sidecars {
		want += projectedWorkloadManifestBytes(sidecar)
	}
	if got != want {
		t.Errorf("roster projection = %d, want %d", got, want)
	}
	if got > api.MaxExportedLayerBytes {
		t.Errorf("normal roster projection = %d, want <= %d (cap)", got, api.MaxExportedLayerBytes)
	}
}

func TestMarshalWorkloadRosterEnforcesCompanionCardinality(t *testing.T) {
	main := WorkloadSpec{Name: "main", Type: "main", RamMB: 256, Port: 8080, Essential: true}
	valid := []WorkloadSpec{
		{Name: "migrator", Type: "init"},
		{Name: "metrics", Type: "sidecar"},
		{Name: "logger", Type: "sidecar"},
		{Name: "proxy", Type: "sidecar"},
		{Name: "tracer", Type: "sidecar"},
	}
	if _, err := marshalWorkloadRoster(main, valid); err != nil {
		t.Fatalf("marshal maximum roster: %v", err)
	}

	overTotal := append(append([]WorkloadSpec(nil), valid...), WorkloadSpec{Name: "audit", Type: "sidecar"})
	if _, err := marshalWorkloadRoster(main, overTotal); err == nil || !strings.Contains(err.Error(), "cap is 5") {
		t.Fatalf("six helpers error = %v, want total cap rejection", err)
	}

	overType := []WorkloadSpec{
		{Name: "one", Type: "sidecar"}, {Name: "two", Type: "sidecar"},
		{Name: "three", Type: "sidecar"}, {Name: "four", Type: "sidecar"},
		{Name: "five", Type: "sidecar"},
	}
	if _, err := marshalWorkloadRoster(main, overType); err == nil || !strings.Contains(err.Error(), "cap is 4") {
		t.Fatalf("five long-running helpers error = %v, want per-type cap rejection", err)
	}
}
