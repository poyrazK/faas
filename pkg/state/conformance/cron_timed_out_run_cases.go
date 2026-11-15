package conformance

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// testCronTimedOutRunCountsWhileInstanceRuns pins prod CLI e2e 2026-11-15
// #13: an HTTP cron run that timed out may still be running in the app, so
// skip_if_running counts it while the instance that served it runs, and
// stops counting it once that instance parks.
func testCronTimedOutRunCountsWhileInstanceRuns(t *testing.T, fx *Fixture) {
	c, err := fx.Store.CreateCron(fx.Ctx, fx.App.ID, "* * * * *", "/cron", true)
	if err != nil {
		t.Fatalf("CreateCron: %v", err)
	}
	ins, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID,
		string(state.StateRunning), 256, fx.Node.ID, uuid.NewString())
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	cronID := c.ID
	now := time.Now().UTC()
	inv, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{
		AppID: fx.App.ID, AccountID: fx.Account.ID, Source: state.InvocationCron,
		Method: "POST", Path: "/cron", CronID: &cronID, DueAt: now, CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	count := func(label string, want int) {
		t.Helper()
		got, err := fx.Store.CountActiveCronInvocations(fx.Ctx, cronID)
		if err != nil || got != want {
			t.Fatalf("%s: CountActiveCronInvocations = %d, %v; want %d", label, got, err, want)
		}
	}
	count("pending", 1)
	if _, err := fx.Store.ClaimInvocation(fx.Ctx, inv.ID, ins.ID, 30); err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}
	if err := fx.Store.FailInvocation(fx.Ctx, inv.ID, "request timeout", 0, 0, state.WithOutcome(state.OutcomeTimeout)); err != nil {
		t.Fatalf("FailInvocation: %v", err)
	}
	stored, err := fx.Store.InvocationByID(fx.Ctx, inv.ID)
	if err != nil || stored.State == state.InvocationPending || stored.State == state.InvocationDispatching {
		t.Fatalf("after timeout: %+v, %v; want a finished row", stored.State, err)
	}
	count("timed out, instance running", 1)
	if err := fx.Store.UpdateInstanceState(fx.Ctx, ins.ID, string(state.StateParked)); err != nil {
		t.Fatalf("UpdateInstanceState: %v", err)
	}
	count("timed out, instance parked", 0)
}
