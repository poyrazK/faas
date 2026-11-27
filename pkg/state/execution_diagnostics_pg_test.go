//go:build !no_pg

package state_test

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestPgEnvironmentDiagnosticFencesAndTail(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	exerciseEnvironmentDiagnostic(t, store)
}
func TestPgExecutionFinishedDelivery(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	exerciseExecutionFinishedDelivery(t, store)
}

func TestPgEnvironmentDiagnosticErasedByRetirement(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.CreateExecutionEnvironment(ctx, environmentFixture(account.ID))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimExecutionEnvironment(ctx, "build")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendExecutionEnvironmentEvent(ctx, claim, api.ExecutionEnvironmentEvent{Stage: "build", Code: "build_output", Message: "#1 installing"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishExecutionEnvironmentBuild(ctx, claim, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := store.RetireExecutionEnvironment(ctx, account.ID, env.ID, "", true); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteExecutionEnvironmentArtifacts(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM execution_environment_diagnostics WHERE environment_id=$1", env.ID).Scan(&retained); err != nil || retained != 0 {
		t.Fatal("retirement retained build output", retained, err)
	}
}

func TestPgExecutionCompletionDeliveryFailureRollsBackTerminalState(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	limits, _ := api.LimitsFor(acct.Plan)
	hook, err := store.CreateAccountReleaseWebhookIfUnderQuota(ctx, state.AppWebhook{AccountID: acct.ID, TargetURL: "https://example.com/rollback", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{api.ExecutionFinishedWebhookEvent}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run, err := store.CreateExecution(ctx, executionTestParams(t, acct.ID, now, 0, "payload"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "ALTER TABLE app_webhook_deliveries ADD CONSTRAINT reject_completion_fixture CHECK(false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RequestExecutionCancellation(ctx, acct.ID, run.ID, now.Add(time.Millisecond)); err == nil {
		t.Fatal("terminal update committed without notification")
	}
	current, err := store.ExecutionByID(ctx, acct.ID, run.ID)
	if err != nil || current.Status != api.ExecutionStatusQueued {
		t.Fatal("terminal state escaped transaction", current.Status, err)
	}
	if _, err = pool.Exec(ctx, "ALTER TABLE app_webhook_deliveries DROP CONSTRAINT reject_completion_fixture"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = store.RequestExecutionCancellation(ctx, acct.ID, run.ID, now.Add(2*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	deliveries, _, err := store.ListAccountReleaseWebhookDeliveries(ctx, acct.ID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 {
		t.Fatal("retry duplicated fanout", len(deliveries), err)
	}
}
