package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowActionConcurrencySharedAcrossRuns(t *testing.T) {
	state.WorkflowGuardStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "action-cap-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256})
		if err != nil {
			t.Fatal(err)
		}
		spec := api.WorkflowSpec{
			Name:                 "send-notifications",
			MaxConcurrentActions: 2,
			Steps:                []api.WorkflowStepSpec{{Name: "send", Run: "send"}},
		}
		snapshot, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		const runCount = 8
		runs := make([]*state.WorkflowRun, runCount)
		for index := range runs {
			run := &state.WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, Input: json.RawMessage(`{}`), DefinitionSnapshot: snapshot}
			if err := store.CreateWorkflowRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send", Status: state.WorkflowStepStatusPending, Input: json.RawMessage(`{}`)}}); err != nil {
				t.Fatal(err)
			}
			runs[index] = run
		}

		started := make(chan int, runCount)
		blocked := make(chan int, runCount)
		var wg sync.WaitGroup
		for index := range runs {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				_, err := store.StartWorkflowStep(ctx, runs[index].ID, "send", 1, json.RawMessage(`{}`))
				switch {
				case err == nil:
					started <- index
				case errors.Is(err, state.ErrWorkflowActionConcurrencyLimit):
					blocked <- index
				default:
					t.Errorf("StartWorkflowStep(%d): %v", index, err)
				}
			}(index)
		}
		wg.Wait()
		close(started)
		close(blocked)

		var startedRuns, blockedRuns []int
		for index := range started {
			startedRuns = append(startedRuns, index)
		}
		for index := range blocked {
			blockedRuns = append(blockedRuns, index)
		}
		if len(startedRuns) != spec.MaxConcurrentActions || len(blockedRuns) != runCount-spec.MaxConcurrentActions {
			t.Fatalf("cross-run action cap admitted %d and blocked %d; want %d and %d", len(startedRuns), len(blockedRuns), spec.MaxConcurrentActions, runCount-spec.MaxConcurrentActions)
		}

		for _, index := range startedRuns {
			if err := store.MarkWorkflowStepAttemptStatus(ctx, runs[index].ID, "send", state.WorkflowStepStatusSucceeded, 1, nil, json.RawMessage(`true`), nil); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.StartWorkflowStep(ctx, runs[blockedRuns[0]].ID, "send", 1, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("released action slot was not reusable: %v", err)
		}
		if err := store.MarkWorkflowStepAttemptStatus(ctx, runs[blockedRuns[0]].ID, "send", state.WorkflowStepStatusSucceeded, 1, nil, json.RawMessage(`true`), nil); err != nil {
			t.Fatal(err)
		}
	})
}
