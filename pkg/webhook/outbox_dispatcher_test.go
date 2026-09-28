package webhook

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDispatcherCycleRelaysCommittedWebhookEventWithoutDeliverySlots(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID, consumerID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{
		AccountID: accountID, AppID: appID, TargetURL: "https://example.com/statements",
		EventFilter: []string{string(state.AppWebhookEventUsageStatementFinalized)}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	statement, _, err := store.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumerID,
		PeriodStart: start, PeriodEnd: start.Add(time.Hour), AsOf: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumerID, statement.ID); err != nil || !changed {
		t.Fatalf("finalize changed=%v err=%v", changed, err)
	}
	d := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.Cap = 0 // exercise relay even when dispatch cannot reserve worker slots
	d.cycle(ctx)
	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, appID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("relayed deliveries = %+v, %v", deliveries, err)
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 10); err != nil || n != 0 {
		t.Fatalf("outbox after cycle = %d, %v", n, err)
	}
}
