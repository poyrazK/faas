package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDoctorNodesSkipsInactiveSyntheticDefaultLocal reproduces the
// production-us rc.239 rollout: migration 00024 seeds an inactive
// default-local row in every database, apid refuses to retire it, and
// fleet_verify's `gregalectl doctor` failed control-plane convergence with
// "compute_nodes row has empty release_id" for it. A single-box install's
// active default-local must still be checked.
func TestDoctorNodesSkipsInactiveSyntheticDefaultLocal(t *testing.T) {
	release := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name      string
		active    bool
		wantError bool
	}{
		{name: "multi-node inactive seed row", active: false, wantError: false},
		{name: "single-box active default-local", active: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := &doctorDeps{
				store: &joinDoctorScopeStore{nodes: []releaseinstall.ComputeNodeRow{
					{Name: state.DefaultLocalNodeName, Active: tc.active, Lifecycle: "unavailable"},
					{Name: "fsn-2.faas", Active: true, Lifecycle: "active", ReleaseID: release},
				}},
				bundlesBySHA: map[string]releaseinstall.BundleRow{release: {GitSHA: release}},
			}
			findings, err := checkNodes(t.Context(), deps)
			if err != nil {
				t.Fatal(err)
			}
			gotError := false
			for _, f := range findings {
				if f.Target == state.DefaultLocalNodeName && f.Severity == doctorSeverityError {
					gotError = true
				}
			}
			if gotError != tc.wantError {
				t.Fatalf("default-local error = %v, want %v; findings=%+v", gotError, tc.wantError, findings)
			}
		})
	}
}
