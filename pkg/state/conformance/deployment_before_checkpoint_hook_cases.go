package conformance

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// testDeploymentBeforeCheckpointHookSurvivesRead covers Hunt 32 H32-49: sched
// parks each deployment with the before_checkpoint hook imaged stamped into
// that deployment's guest manifest, so the recorded value, including an
// explicit "no hook", must be known on every read. A deployment that imaged
// never recorded stays unknown so parks keep the app's current hook.
func testDeploymentBeforeCheckpointHookSurvivesRead(t *testing.T, fx *Fixture) {
	t.Helper()
	legacy, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: fx.App.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:before-checkpoint-legacy", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := fx.Store.DeploymentByID(fx.Ctx, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.BeforeCheckpointHookKnown {
		t.Fatalf("unrecorded deployment reads as known (hook=%v)", stored.BeforeCheckpointHook)
	}
	for _, hook := range []bool{true, false} {
		dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
			AppID: fx.App.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:before-checkpoint-" + map[bool]string{true: "on", false: "off"}[hook], Status: state.DeployPending,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.Store.SetDeploymentBeforeCheckpointHook(fx.Ctx, dep.ID, hook); err != nil {
			t.Fatal(err)
		}
		stored, err := fx.Store.DeploymentByID(fx.Ctx, dep.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.BeforeCheckpointHook != hook || !stored.BeforeCheckpointHookKnown {
			t.Fatalf("hook %v read back as (%v, known=%v), want (%v, known=true)",
				hook, stored.BeforeCheckpointHook, stored.BeforeCheckpointHookKnown, hook)
		}
	}
	if err := fx.Store.SetDeploymentBeforeCheckpointHook(fx.Ctx, "00000000-0000-4000-8000-000000000049", true); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing deployment: err = %v, want ErrNotFound", err)
	}
}
