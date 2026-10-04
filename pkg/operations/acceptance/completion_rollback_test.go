package acceptance_test

// ADR-521: business success, terminal event and completion outbox cannot
// partially commit when delivery persistence fails.
import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestPgOperationCompletionRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	ctx, acct, app, definition, alice, _ := operationFixture(t, store)
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{AccountID: acct.ID, AppID: app.ID, TargetURL: "https://receiver.example.test/finished", SecretSealed: []byte("sealed"), EventFilter: []string{"operation.finished"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	spec := operationSpec()
	spec.Name = "with-delivery"
	spec.Path = "/with-delivery"
	spec.CompletionWebhookID = hook.ID
	definition, err = store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: definition.DeploymentID, Scope: definition.Scope, Spec: spec}})
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, PlatformTenantID: alice.ID, DefinitionID: definition.ID, IdempotencyKey: "atomic-completion", Input: json.RawMessage(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimInvocation(ctx, op.CurrentInvocationID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `CREATE FUNCTION reject_completion_delivery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected outbox failure'; END $$;
CREATE TRIGGER reject_completion_delivery BEFORE INSERT ON app_webhook_deliveries FOR EACH ROW EXECUTE FUNCTION reject_completion_delivery()`)
	if err != nil {
		t.Fatal(err)
	}
	result := json.RawMessage(`{"file":"exports/confirmed.csv"}`)
	if err := store.CompleteKeyedInvocation(ctx, claim.ID, claim.Attempts, result); err == nil {
		t.Fatal("ignored outbox failure")
	}
	current, err := store.OperationByID(ctx, acct.ID, alice.ID, op.ID)
	if err != nil || current.State != api.OperationRunning || current.LatestSequence != 2 || current.CompletionDelivery.State != "awaiting_outcome" {
		t.Fatalf("partial business commit: %+v %v", current, err)
	}
	invocation, err := store.InvocationByID(ctx, claim.ID)
	if err != nil || invocation.State != state.InvocationDispatching {
		t.Fatalf("partial execution commit: %+v %v", invocation, err)
	}
	events, err := store.OperationEvents(ctx, acct.ID, alice.ID, op.ID, 0, 100)
	if err != nil || len(events.Events) != 2 {
		t.Fatalf("partial terminal event: %+v %v", events, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_completion_delivery ON app_webhook_deliveries`); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteKeyedInvocation(ctx, claim.ID, claim.Attempts, result); err != nil {
		t.Fatal(err)
	}
	deliveries, err := store.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now().Add(time.Second))
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("outbox after successful retry: %+v %v", deliveries, err)
	}
	current, err = store.OperationByID(ctx, acct.ID, alice.ID, op.ID)
	if err != nil || current.State != api.OperationSucceeded || current.CompletionDelivery.DeliveryID != deliveries[0].ID {
		t.Fatalf("missing atomic business result: %+v %v", current, err)
	}
}
