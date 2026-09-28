package webhook

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDeliveryHealthMetrics_FleetOnly(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewDeliveryHealthMetrics(reg, "schedd")
	now := time.Now()
	oldest := now.Add(-20 * time.Minute)
	held := now.Add(-2 * time.Hour)
	m.setFleetQueueHealth(now, state.AppWebhookFleetQueueHealth{
		OldestClaimableAt: &oldest, OldestHeldAt: &held, HeldDueCount: 8,
	})
	m.markDead()
	if got := testutil.ToFloat64(m.overdueSeconds); got != 1200 {
		t.Fatalf("overdue age = %v, want 1200", got)
	}
	if got := testutil.ToFloat64(m.heldDueCount); got != 8 {
		t.Fatalf("held due count = %v, want 8", got)
	}
	if got := testutil.ToFloat64(m.heldDueSeconds); got != 7200 {
		t.Fatalf("oldest held age = %v, want 7200", got)
	}
	if got := testutil.ToFloat64(m.deadTotal); got != 1 {
		t.Fatalf("dead count = %v, want 1", got)
	}
	m.markPollFailed()
	if got := testutil.ToFloat64(m.pollSuccess); got != 0 {
		t.Fatalf("poll success = %v, want 0", got)
	}
	m.setOutboxRelaySuccess(false)
	if got := testutil.ToFloat64(m.outboxSuccess); got != 0 {
		t.Fatalf("failed outbox relay = %v, want 0", got)
	}
	m.setOutboxRelaySuccess(true)
	if got := testutil.ToFloat64(m.outboxSuccess); got != 1 {
		t.Fatalf("recovered outbox relay = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.heldDueCount); got != 8 {
		t.Fatalf("failed poll changed held count to %v", got)
	}
	m.setFleetQueueHealth(now, state.AppWebhookFleetQueueHealth{})
	if got := testutil.ToFloat64(m.heldDueCount); got != 0 {
		t.Fatalf("cleared held count = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.heldDueSeconds); got != 0 {
		t.Fatalf("cleared held age = %v, want 0", got)
	}
	families, err := reg.Gather()
	if err != nil || len(families) != 12 {
		t.Fatalf("fleet metrics = %d families, err=%v", len(families), err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			if len(metric.Label) != 0 {
				t.Errorf("metric %s has labels: %+v", family.GetName(), metric.Label)
			}
		}
	}
}

func TestDeliveryHealthMetrics_OnlyCommittedDeadOutcomesCount(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{
		AccountID: "account", AppID: "app", TargetURL: "https://example.com/hook",
		SecretSealed: []byte("sealed"), Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := store.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
		WebhookID: hook.ID, AccountID: "account", AppID: "app", Event: "cron.fired",
	})
	if err != nil {
		t.Fatal(err)
	}
	reg := prometheus.NewRegistry()
	metrics := NewDeliveryHealthMetrics(reg, "schedd")
	dispatcher := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	dispatcher.HealthMetrics = metrics
	now := time.Now().Add(2 * time.Minute)
	dispatcher.Now = func() time.Time { return now }
	dispatcher.refreshHealth(ctx)
	if got := testutil.ToFloat64(metrics.overdueSeconds); got < 119 {
		t.Fatalf("fleet overdue age = %v, want at least 119", got)
	}
	claims, err := store.ClaimDueAppWebhookDeliveries(ctx, 1, now)
	if err != nil || len(claims) != 1 || claims[0].ID != delivery.ID {
		t.Fatalf("claims = %+v, err=%v", claims, err)
	}
	if !dispatcher.markDead(ctx, claims[0], "receiver rejected") {
		t.Fatal("first dead outcome was not recorded")
	}
	if dispatcher.markDead(ctx, claims[0], "stale worker") {
		t.Fatal("stale dead outcome was recorded")
	}
	if got := testutil.ToFloat64(metrics.deadTotal); got != 1 {
		t.Fatalf("dead counter = %v, want 1", got)
	}
}
