package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestAppendDeploymentStageKeepsACompletedStageNote: the builder-slot wait
// booked to source_download needs a note on the closed stage (Hunt 12 H12-5).
func TestAppendDeploymentStageKeepsACompletedStageNote(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "stage-note@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "stage-note", Type: state.AppTypeApp, RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.AppendDeploymentStage(ctx, dep.ID, state.StageSourceDownload, state.StageImageBuild, time.Now(), "waited 3m12s for a builder slot")
	if err != nil {
		t.Fatal(err)
	}
	var ss state.StageState
	if err := json.Unmarshal(got.StageState, &ss); err != nil || len(ss.History) != 1 || ss.History[0].Name != state.StageSourceDownload || ss.History[0].Reason != "waited 3m12s for a builder slot" || ss.History[0].Status != "completed" {
		t.Fatalf("history=%+v err=%v", ss.History, err)
	}
}
