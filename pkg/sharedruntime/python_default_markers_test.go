package sharedruntime

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/markers"
)

// TestPythonBuildDefaultMatchesMarkers keeps the version the deploy plan
// reports for a lower-bound requires-python equal to the base the build uses
// when no version is pinned (Hunt 32 H32-9).
func TestPythonBuildDefaultMatchesMarkers(t *testing.T) {
	if got := PythonRuntimeForSpecs([]string{markers.PythonBuildDefault}); got != PythonRuntimeForSpecs(nil) {
		t.Fatalf("markers.PythonBuildDefault %q maps to %q, the build default is %q", markers.PythonBuildDefault, got, PythonRuntimeForSpecs(nil))
	}
}
