package sched

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// PreverifyPrimeLayer verifies a deployment's layer when imaged announces it
// published, before the deployment's prime asks for it (ADR-038 amendment
// 2026-11-17). The verifier caches a success and shares a hash already
// running, so the prime finds the layer verified, or joins the hash, instead
// of reading and hashing the whole layer while it holds the app lock.
//
// Only the schedd that owns the app verifies, as only it primes the app. The
// check is the one the prime runs, and nothing trusts this call's result: a
// failure is returned for the caller to log, and the prime verifies again and
// fails closed.
func (e *Engine) PreverifyPrimeLayer(ctx context.Context, appID, deploymentID string) error {
	if e.verifier == nil {
		return nil
	}
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return fmt.Errorf("sched: preverify layer: load app: %w", err)
	}
	if !e.OwnsApp(app) {
		return nil
	}
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("sched: preverify layer: load deployment: %w", err)
	}
	// A held environment candidate is not primed until its graph is released.
	if dep.AppID != appID || dep.RootfsKey == "" || dep.Status.IsTerminal() || dep.Status == state.DeployLive || dep.EnvironmentWorkloadHeld() {
		return nil
	}
	key := layerKey(dep.RootfsKey, dep.ID)
	return e.verifier.Verify(ctx, key, "sigs/"+key+".sig")
}
