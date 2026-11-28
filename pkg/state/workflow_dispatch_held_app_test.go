package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Workflow schedules skipped deleted apps and held accounts, but the run
// dispatcher claimed their pending runs anyway, so a deleted app's runs and
// an abuse-held account's runs kept executing steps (async assessment
// 2026-11-28). They now wait, and resume once the app is restored or the
// hold is lifted.
func TestWorkflowDispatchSkipsDeletedAppsAndHeldAccounts(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		app, _ := seedWorkflowScheduleWithContext(t, ctx, store, "allow")
		spec := api.WorkflowSpec{Name: "held", Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/main"}}}
		snapshot, _ := json.Marshal(spec)
		newRun := func() *WorkflowRun {
			t.Helper()
			run := &WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, Input: json.RawMessage(`{}`), DefinitionSnapshot: snapshot}
			if err := store.CreateWorkflowRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			return run
		}
		mustNotClaim := func(why string) {
			t.Helper()
			if got, err := store.ClaimNextDueWorkflowRun(ctx); !errors.Is(err, ErrNotFound) {
				t.Fatalf("%s: claimed %+v, err %v; want ErrNotFound", why, got, err)
			}
		}
		mustClaim := func(run *WorkflowRun, why string) {
			t.Helper()
			got, err := store.ClaimNextDueWorkflowRun(ctx)
			if err != nil || got == nil || got.ID != run.ID {
				t.Fatalf("%s: claimed %+v, err %v; want run %s", why, got, err, run.ID)
			}
		}

		deleted := newRun()
		if _, err := store.ScheduleAppDeletion(ctx, app.ID, time.Time{}); err != nil {
			t.Fatal(err)
		}
		mustNotClaim("deleted app")
		if _, err := store.RestoreApp(ctx, app.ID, api.MustLimitsFor(api.PlanHobby)); err != nil {
			t.Fatal(err)
		}
		mustClaim(deleted, "restored app")

		holds, ok := store.(AccountAbuseHoldStore)
		if !ok {
			t.Skip("store has no abuse hold")
		}
		held := newRun()
		if _, err := holds.SetAccountAbuseHold(ctx, app.AccountID, AccountAbuseHoldOperator, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		mustNotClaim("abuse-held account")
		if _, err := holds.ReleaseAccountAbuseHold(ctx, app.AccountID); err != nil {
			t.Fatal(err)
		}
		mustClaim(held, "released hold")
	})
}
