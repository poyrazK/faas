//go:build !no_pg

// adr: 20261105104500
package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// A dependency whose newest release failed, or was rolled back, still serves
// its older release; a dependent deploy pins that release and goes live
// instead of failing on the dead one.
func TestProjectDependencyGatePinsTheServingReleaseAfterAFailedDeploy(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newDependencyGateFixture(t, backend)
			if err := f.store.UpdateDeploymentStatus(f.ctx, f.dependency.ID, state.DeployFailed, "build failed"); err != nil {
				t.Fatal(err)
			}
			candidate, err := f.store.CreateDeployment(f.ctx, state.Deployment{AppID: f.web.ID, Kind: state.DeploymentKindImage, ImageDigest: "docker.io/library/nginx:1.27"})
			if err != nil {
				t.Fatalf("admission refused a dependent of a serving dependency: %v", err)
			}
			if err := f.store.UpdateDeploymentStatus(f.ctx, candidate.ID, state.DeploySnapshotting, ""); err != nil {
				t.Fatal(err)
			}
			gate, err := f.reader.CheckDeploymentDependencies(f.ctx, candidate.ID, time.Now())
			if err != nil || gate.Status != "ready" {
				t.Fatalf("gate = %+v, %v; want ready on the serving release", gate, err)
			}
			if pin := gate.Dependencies[0].DeploymentID; pin == f.dependency.ID {
				t.Fatalf("gate pinned the failed release %s", pin)
			}
		})
	}
}
