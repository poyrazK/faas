package imaged

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Deploy-path review P14: apid checked freezes only when it created a
// deployment, so a release still building when a freeze began went live
// inside it.
func TestReleaseFrozenSinceDeployIsRefusedAtGoLive(t *testing.T) {
	fx := seedReleaseGateWithIntent(t, false, false)
	fx.freezeSince(t, time.Now().UTC())
	frozen, err := fx.handler.refuseReleaseFrozenSinceDeploy(context.Background(), fx.candidate)
	if err != nil || !frozen {
		t.Fatalf("refuseReleaseFrozenSinceDeploy = (%v, %v), want (true, nil)", frozen, err)
	}
	dep := fx.status(t)
	if dep.Status != state.DeployFailed || dep.ErrorCode != api.CodeReleaseFrozen || !strings.Contains(dep.Error, "--override-freeze") {
		t.Fatalf("frozen release = %s %s %q, want failed %s with the override hint", dep.Status, dep.ErrorCode, dep.Error, api.CodeReleaseFrozen)
	}
}

// A freeze already in force when the release was deployed was checked (or
// overridden) then; it does not stop the release at go-live.
func TestReleaseFrozenBeforeDeployStillGoesLive(t *testing.T) {
	fx := seedReleaseGateWithIntent(t, false, false)
	fx.freezeSince(t, fx.candidate.CreatedAt.Add(-time.Minute))
	frozen, err := fx.handler.refuseReleaseFrozenSinceDeploy(context.Background(), fx.candidate)
	if err != nil || frozen {
		t.Fatalf("refuseReleaseFrozenSinceDeploy = (%v, %v), want (false, nil)", frozen, err)
	}
	if dep := fx.status(t); dep.Status == state.DeployFailed {
		t.Fatalf("release frozen before it was deployed was failed: %q", dep.Error)
	}
}
