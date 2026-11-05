// adr: 20261103112845
package reconcile

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// A built Compose service keeps its ports: target as its serving port; a
// built service from another detector is unchanged.
func TestComposeBuiltServiceKeepsServingPort(t *testing.T) {
	compose := reposcan.Workload{Name: "api", Dockerfile: "Dockerfile", Class: reposcan.ClassHTTP, Ports: []int{3000},
		DetectedBy: reposcan.Detection{Detector: "compose"}}
	app := state.App{WorkloadName: "api", WorkloadClass: workloadClassFromScan(compose)}
	applyProjectImage(&app.Manifest, compose)
	if app.Manifest.ProjectImagePort != 3000 {
		t.Fatalf("port = %d, want 3000", app.Manifest.ProjectImagePort)
	}
	if changed := diffFieldsChanged(app, compose, resolveStartCommand(compose)); slices.Contains(changed, "image_port") {
		t.Fatalf("matching port reported changed: %v", changed)
	}
	compose.Ports = []int{8000}
	if changed := diffFieldsChanged(app, compose, resolveStartCommand(compose)); !slices.Contains(changed, "image_port") {
		t.Fatalf("port change not reported: %v", changed)
	}
	folder := reposcan.Workload{Name: "web", Class: reposcan.ClassHTTP, Ports: []int{3000},
		DetectedBy: reposcan.Detection{Detector: "folder"}}
	other := state.App{}
	applyProjectImage(&other.Manifest, folder)
	if other.Manifest.ProjectImagePort != 0 {
		t.Fatalf("non-Compose built service got port %d", other.Manifest.ProjectImagePort)
	}
}
