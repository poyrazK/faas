package state_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgUsageStatementWebhookOutboxRestartAndConcurrentRelay(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	consumer, err := store.CreateAPIConsumer(ctx, accountID, appID, "outbox-consumer", "Outbox consumer")
	if err != nil {
		t.Fatal(err)
	}
	hookInput := pgSampleWebhook(accountID, appID)
	hookInput.EventFilter = []string{string(state.AppWebhookEventUsageStatementFinalized)}
	hook, err := store.CreateAppWebhook(ctx, hookInput)
	if err != nil {
		t.Fatal(err)
	}
	createStatement := func(start time.Time) state.APIConsumerUsageStatement {
		t.Helper()
		statement, created, createErr := store.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
			AccountID: accountID, AppID: appID, ConsumerID: consumer.ID,
			PeriodStart: start, PeriodEnd: start.Add(time.Hour), Currency: "EUR",
			BillableUnits: 1, AmountMillicents: 25, Priced: true, AsOf: time.Now().UTC(),
			Buckets: []state.APIConsumerUsageStatementBucket{{
				WindowStart: start, BillableUnits: 1, RateCardID: uuid.NewString(),
				Currency: "EUR", PriceMillicentsPerUnit: 25, AmountMillicents: 25,
			}},
		})
		if createErr != nil || !created {
			t.Fatalf("create statement = %+v, %v, %v", statement, created, createErr)
		}
		return statement
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	statement := createStatement(start)
	if _, changed, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, statement.ID); err != nil || !changed {
		t.Fatalf("finalize changed=%v err=%v", changed, err)
	}
	var pending int
	if err := pool.QueryRow(ctx, `select count(*) from app_webhook_event_outbox where source_id = $1`, statement.ID).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("committed outbox events = %d, %v", pending, err)
	}
	health, err := store.AppWebhookEventOutboxHealth(ctx)
	if err != nil || health.PendingCount != 1 || health.OldestPendingAt == nil {
		t.Fatalf("pending event outbox health = %+v, %v", health, err)
	}
	lateInput := pgSampleWebhook(accountID, appID)
	lateInput.EventFilter = []string{string(state.AppWebhookEventUsageStatementFinalized)}
	lateHook, err := store.CreateAppWebhook(ctx, lateInput)
	if err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	if n, err := restarted.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 1 {
		t.Fatalf("relay after restart = %d, %v", n, err)
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 0 {
		t.Fatalf("repeat relay = %d, %v", n, err)
	}
	cleared, err := store.AppWebhookEventOutboxHealth(ctx)
	if err != nil || cleared.PendingCount != 0 || cleared.OldestPendingAt != nil {
		t.Fatalf("empty event outbox health = %+v, %v", cleared, err)
	}
	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, appID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("snapshot recipient deliveries = %+v, %v", deliveries, err)
	}
	var payload api.APIConsumerUsageStatementFinalizedWebhookPayload
	if err := json.Unmarshal(deliveries[0].Payload, &payload); err != nil ||
		payload.StatementID != statement.ID || payload.AmountMillicents != 25 || len(payload.Buckets) != 1 {
		t.Fatalf("relayed payload = %+v, %v", payload, err)
	}
	lateDeliveries, _, err := store.ListAppWebhookDeliveries(ctx, appID, lateHook.ID, 10, "")
	if err != nil || len(lateDeliveries) != 0 {
		t.Fatalf("late subscription received old event = %+v, %v", lateDeliveries, err)
	}
	if _, changed, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, statement.ID); err != nil || changed {
		t.Fatalf("repeat finalize changed=%v err=%v", changed, err)
	}

	second := createStatement(start.Add(2 * time.Hour))
	if _, changed, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, second.ID); err != nil || !changed {
		t.Fatalf("second finalize changed=%v err=%v", changed, err)
	}
	startRelay := make(chan struct{})
	results := make(chan int, 2)
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, relayer := range []*state.PgStore{store, restarted} {
		go func(relayer *state.PgStore) {
			ready.Done()
			<-startRelay
			n, relayErr := relayer.DrainAppWebhookEventOutbox(ctx, 16)
			results <- n
			errs <- relayErr
		}(relayer)
	}
	ready.Wait()
	close(startRelay)
	if n := <-results + <-results; n != 1 {
		t.Fatalf("concurrent relays processed %d events, want one", n)
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	var secondDeliveries int
	if err := pool.QueryRow(ctx, `select count(*) from app_webhook_deliveries where event = 'usage_statement.finalized' and payload->>'statement_id' = $1`, second.ID).Scan(&secondDeliveries); err != nil || secondDeliveries != 2 {
		t.Fatalf("concurrent fanout deliveries = %d, %v; want one per snapshot recipient", secondDeliveries, err)
	}
}

func TestPgUsageStatementWebhookOutboxFailureRollsBackFinalization(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	consumer, err := store.CreateAPIConsumer(ctx, accountID, appID, "rollback-consumer", "Rollback consumer")
	if err != nil {
		t.Fatal(err)
	}
	hookInput := pgSampleWebhook(accountID, appID)
	hookInput.EventFilter = []string{string(state.AppWebhookEventUsageStatementFinalized)}
	if _, err := store.CreateAppWebhook(ctx, hookInput); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	statement, _, err := store.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID,
		PeriodStart: start, PeriodEnd: start.Add(time.Hour), Currency: "EUR", AsOf: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `alter table app_webhook_event_outbox add constraint reject_outbox_insert_test check (false)`); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, statement.ID); err == nil || changed {
		t.Fatalf("finalization with failed outbox write: changed=%v err=%v", changed, err)
	}
	got, err := store.GetAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, statement.ID)
	if err != nil || got.Status != state.APIConsumerUsageStatementDraft || got.FinalizedAt != nil {
		t.Fatalf("finalization escaped rollback = %+v, %v", got, err)
	}
	if _, err := pool.Exec(ctx, `alter table app_webhook_event_outbox drop constraint reject_outbox_insert_test`); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, statement.ID); err != nil || !changed {
		t.Fatalf("finalization after recovery: changed=%v err=%v", changed, err)
	}
}
