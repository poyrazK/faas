// adr: 427 — preflight consumes the real inventory without hiding stale residents.
package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingCheckConsumesInventoryAfterGuestVerificationAndRuntimeRefresh(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, deployment := seedAppTaskDeployment(t, e, "bindings-preflight")
	ctx := context.Background()
	old, err := e.store.CreateInstance(ctx, app.ID, deployment.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	e.store.BackdateForTest(old.ID, time.Now().Add(-time.Hour))
	readyVerificationBinding(t, e, app)
	readyObjectStorageVerificationBinding(t, e, app, "default")
	if err := e.store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	for _, probe := range []struct{ command, selector, output string }{
		{api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL", passedPostgresVerification},
		{api.AppTaskObjectStorageBindingProbeCommand, "ASSETS", passedObjectStorageVerification},
	} {
		task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{probe.command, probe.selector}, MaxOutputBytes: 4096})
		completeVerificationTask(t, e, beginVerificationTask(t, e, task.ID), probe.output)
	}
	read := func() bindingcheck.Report {
		t.Helper()
		inventory := decodeBindingInventory(t, e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings", nil, nil))
		// Existing task fixtures finish at CreatedAt+2s; use an explicit clock
		// after those completions without sleeping or changing production clocks.
		report, err := bindingcheck.Evaluate(inventory, bindingcheck.Policy{App: app.Slug, MaxVerificationAge: time.Minute}, inventory.GeneratedAt.Add(3*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	blocked := read()
	if blocked.Passed || len(blocked.Blockers) != 1 || blocked.Blockers[0].Code != "runtime_stale" || blocked.Runtime[0].Serving.Stale != 1 {
		t.Fatalf("passed probes hid stale serving instance: %+v", blocked)
	}
	if _, err := e.store.CreateInstance(ctx, app.ID, deployment.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateInstanceState(ctx, old.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	passed := read()
	if !passed.Passed || passed.Coverage != "complete" || len(passed.Bindings) != 2 || passed.Runtime[0].Serving.Current != 1 || len(passed.Blockers) != 0 {
		t.Fatalf("refreshed runtime did not pass with durable evidence: %+v", passed)
	}
}
