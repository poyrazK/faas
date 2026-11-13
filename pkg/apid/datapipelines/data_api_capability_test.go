package datapipelines

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDataAPISetupRevocationStopsNewResourcesAndBuilds(t *testing.T) {
	setup, store, job := apiSetupFixture(t)
	ctx := t.Context()
	released := false
	setup.DataAPIReleased = func(state.Account) bool { return released }
	bindings := setup.Bindings.(*setupBindings)
	if _, _, err := setup.Prepare(ctx, job); !errors.Is(err, datapipeline.ErrUnavailable) {
		t.Fatal("queued setup bypassed capability revocation", err)
	}
	if _, err := store.AppByID(ctx, job.DataAPIPlan.AppID); !errors.Is(err, state.ErrNotFound) || bindings.creates != 0 {
		t.Fatal("revoked setup created an app or binding", bindings.creates, err)
	}
	released = true
	binding, ready, err := setup.Prepare(ctx, job)
	if err != nil || !ready {
		t.Fatal(binding, ready, err)
	}
	job.DataAPIPlan.BindingID = binding
	released = false
	if _, _, err = setup.Prepare(ctx, job); !errors.Is(err, datapipeline.ErrUnavailable) || bindings.creates != 1 {
		t.Fatal("existing app bypassed the preparation gate", bindings.creates, err)
	}
	admissions := 0
	setup.AdmitDeploy = func(context.Context, state.Account, state.App) error { admissions++; return nil }
	if _, _, err = setup.Deploy(ctx, job); !errors.Is(err, datapipeline.ErrUnavailable) || admissions != 0 {
		t.Fatal("revoked setup charged admission or queued a build", admissions, err)
	}
	deployment, build := apidsource.IntentIDs(job.DataAPIPlan.IntentID, job.DataAPIPlan.AppID)
	if _, err = store.DeploymentByID(ctx, deployment); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("revoked setup persisted a new deployment", err)
	}
	if _, err = store.BuildByID(ctx, build); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("revoked setup persisted a new build", err)
	}
	released = true
	accepted, acceptedBuild, err := setup.Deploy(ctx, job)
	if err != nil || accepted != deployment || acceptedBuild != build || admissions != 1 {
		t.Fatal("releasing the capability lost the saved intent", accepted, acceptedBuild, admissions, err)
	}
	// A lost accepted response remains recoverable after the switch is off.
	released = false
	replayed, replayedBuild, err := setup.Deploy(ctx, job)
	if err != nil || replayed != accepted || replayedBuild != acceptedBuild || admissions != 1 {
		t.Fatal("revocation stranded accepted work", replayed, replayedBuild, admissions, err)
	}
	job.DataAPIPlan.DeploymentID, job.DataAPIPlan.BuildID = accepted, acceptedBuild
	if err = store.MarkDeploymentLive(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	if status, endpoint, err := setup.State(ctx, job); err != nil || status != "live" || endpoint == "" {
		t.Fatal("revocation prevented reading accepted deployment status", status, endpoint, err)
	}
}
