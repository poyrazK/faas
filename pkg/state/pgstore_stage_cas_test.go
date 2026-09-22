package state_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Duplicate notifications may enter different imaged processes together.
// Exactly one writer may close each stage, even when they all read the same
// starting state before the update.
func TestPgDeploymentStageConcurrentReplay(t *testing.T) {
	store, ctx := pgStore(t)
	acct, err := store.CreateAccount(ctx, "stage-cas@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "stage-cas", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}

	const workers = 16
	run := func(call func() error) {
		t.Helper()
		start := make(chan struct{})
		results := make(chan error, workers)
		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results <- call()
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		successes := 0
		for err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, state.ErrNotFound):
			default:
				t.Errorf("concurrent stage mutation: %v", err)
			}
		}
		if successes != 1 {
			t.Errorf("successful stage mutations = %d, want 1", successes)
		}
	}

	at := time.Now().UTC()
	run(func() error {
		_, err := store.AppendDeploymentStage(ctx, dep.ID, state.StageSourceDownload, state.StageReadiness, at, "")
		return err
	})
	run(func() error {
		_, err := store.CloseDeploymentStage(ctx, dep.ID, state.StageReadiness, at.Add(time.Second))
		return err
	})

	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stages state.StageState
	if err := json.Unmarshal(got.StageState, &stages); err != nil {
		t.Fatal(err)
	}
	if stages.Current != "" || len(stages.History) != 2 || stages.History[0].Name != state.StageSourceDownload || stages.History[1].Name != state.StageReadiness {
		t.Fatalf("replayed stage timeline: %+v", stages)
	}

	failed, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	run(func() error {
		_, err := store.MarkDeploymentStageFailed(ctx, failed.ID, at, "build failed")
		return err
	})
	got, err = store.DeploymentByID(ctx, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got.StageState, &stages); err != nil {
		t.Fatal(err)
	}
	if stages.Current != "" || len(stages.History) != 1 || stages.History[0].Status != "failed" {
		t.Fatalf("replayed failure stage: %+v", stages)
	}
}
