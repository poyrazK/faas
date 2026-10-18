package imaged

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-1500 (H10-140): schedd boots a shared-runtime source deployment on its
// runner base, so imaged stages that base rather than the minimal one the app
// row alone would select.
func TestEnsureDeploymentRuntimeBaseStagesSharedRunnerBase(t *testing.T) {
	hs := newBaseHarness(t, newTwoLayerPuller(t), &callCountingBuilder{})
	hs.h.runtimeBaseStagingEnabled = true
	app := state.App{Slug: "node-source", Type: state.AppTypeApp}
	dep := state.Deployment{ID: "dep-1", Kind: state.DeploymentKindTarball, RuntimeBase: RuntimeNode22}

	if err := hs.h.ensureDeploymentRuntimeBaseForDeployment(context.Background(), app, dep); err != nil {
		t.Fatalf("ensureDeploymentRuntimeBaseForDeployment: %v", err)
	}
	key := sched.BaseKeyForArch(RuntimeNode22, oci.ImageArchitecture)
	if _, err := hs.be.Get(context.Background(), key); err != nil {
		t.Fatalf("runner base not staged at %s: %v", key, err)
	}
}
