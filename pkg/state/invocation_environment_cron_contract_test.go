package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStageHTTPCronInvocation(t *testing.T) {
	testStageHTTPCronInvocation(t, state.NewMemStore())
}

// ADR-20261104120000: an environment HTTP cron's request is stage work. The
// store stamps it with the cron's environment, it resolves and claims only in
// that environment, a production cron cannot name a stage, and deleting the
// stage drains the cron's idle requests.
func testStageHTTPCronInvocation(t *testing.T, store invocationWorkEnvironmentTestStore) {
	t.Helper()
	ctx := t.Context()
	f := seedInvocationWorkEnvironment(t, store)
	limits := api.MustLimitsFor(api.PlanPro)
	stageCron, err := store.CreateCronIfUnderQuotaWithOptions(ctx, f.app.ID, "* * * * *", "/tick", true, limits, state.CronOptions{DeploymentScope: "staging"})
	if err != nil {
		t.Fatalf("stage HTTP cron: %v", err)
	}
	prodCron, err := store.CreateCronIfUnderQuotaWithOptions(ctx, f.app.ID, "* * * * *", "/tick", true, limits, state.CronOptions{})
	if err != nil {
		t.Fatal(err)
	}
	headers, _ := json.Marshal(map[string]string{"x-faas-cron": "true"})
	now := time.Now().UTC().Truncate(time.Microsecond)

	scheduled, ok := store.(state.ScheduledCronInvocationStore)
	if !ok {
		t.Fatalf("%T has no scheduled cron occurrences", store)
	}
	occurrence, _, created, err := scheduled.CreateScheduledCronInvocationOccurrence(ctx, stageCron.ID, nil, now,
		state.CronScheduledOccurrenceOptions{ScheduledFor: now, ScheduleRevision: stageCron.ScheduleRevision},
		state.Invocation{Method: "POST", Path: "/tick", Headers: headers, DeploymentScope: "staging"})
	if err != nil || !created {
		t.Fatalf("scheduled stage occurrence = %v, %v", created, err)
	}
	stored, err := store.InvocationByID(ctx, occurrence.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.EnvironmentID == "" || stored.DeploymentScope != "staging" {
		t.Fatalf("scheduled stage request environment = %q scope %q", stored.EnvironmentID, stored.DeploymentScope)
	}
	if _, version, err := state.ResolveInvocationVersion(ctx, store, stored); err != nil || version.Scope != "staging" || version.DeploymentID != f.stage.ID {
		t.Fatalf("scheduled stage request resolves to %+v, %v; want the staging deployment %s", version, err, f.stage.ID)
	}
	if _, err := store.ClaimInvocation(ctx, stored.ID, "", 30); err != nil {
		t.Fatalf("claim scheduled stage request: %v", err)
	}
	if err := store.CompleteInvocation(ctx, stored.ID, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}

	request := func(cron state.Cron, scope, environmentID string) state.Invocation {
		cronID := cron.ID
		return state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationCron, CronID: &cronID,
			Method: "POST", Path: "/tick", Headers: headers, DueAt: now, DeploymentScope: scope, EnvironmentID: environmentID}
	}
	manual, err := store.EnqueueInvocation(ctx, request(stageCron, "staging", ""))
	if err != nil {
		t.Fatalf("manual stage request: %v", err)
	}
	if manual.EnvironmentID != stored.EnvironmentID {
		t.Fatalf("manual stage request environment = %q, want %q", manual.EnvironmentID, stored.EnvironmentID)
	}
	if _, err := store.EnqueueInvocation(ctx, request(stageCron, "production", "")); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
		t.Fatalf("stage cron request in production: err = %v", err)
	}
	if _, err := store.EnqueueInvocation(ctx, request(prodCron, "", stored.EnvironmentID)); err == nil {
		t.Fatal("production cron request named the stage")
	}

	retireInvocationEnvironmentDeployments(t, store, f, "staging")
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "staging"); err != nil {
		t.Fatalf("delete stage with cron requests: %v", err)
	}
	for _, id := range []string{stored.ID, manual.ID} {
		if _, err := store.InvocationByID(ctx, id); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("stage cron request %s remains after delete: %v", id, err)
		}
	}
	if _, err := store.CronByID(ctx, stageCron.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stage cron remains after delete: %v", err)
	}
	if _, err := store.CronByID(ctx, prodCron.ID); err != nil {
		t.Fatalf("production cron gone with the stage: %v", err)
	}
}
