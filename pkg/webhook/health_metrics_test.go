package webhook

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
	parkOldest := now.Add(-7 * time.Minute)
	wakeOldest := now.Add(-30 * time.Second)
	outboxOldest := now.Add(-8 * time.Minute)
	m.setFleetQueueHealth(now, state.AppWebhookFleetQueueHealth{
		OldestClaimableAt: &oldest, OldestHeldAt: &held, HeldDueCount: 8,
	})
	m.setLifecycleTransitionHealth(now, state.AppLifecycleTransitionHealth{
		ParkPendingCount: 2, ParkOldestPendingAt: &parkOldest,
		WakePendingCount: 1, WakeOldestPendingAt: &wakeOldest,
	})
	m.setEventOutboxHealth(now, state.AppWebhookEventOutboxHealth{PendingCount: 3, OldestPendingAt: &outboxOldest})
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
	if got := testutil.ToFloat64(m.lifecycleTransitionPendingCount.WithLabelValues("park")); got != 2 {
		t.Fatalf("pending park transitions = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.lifecycleTransitionOldestAge.WithLabelValues("park")); got != 420 {
		t.Fatalf("oldest park transition age = %v, want 420", got)
	}
	if got := testutil.ToFloat64(m.lifecycleTransitionPendingCount.WithLabelValues("wake")); got != 1 {
		t.Fatalf("pending wake transitions = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.lifecycleTransitionOldestAge.WithLabelValues("wake")); got != 30 {
		t.Fatalf("oldest wake transition age = %v, want 30", got)
	}
	if got := testutil.ToFloat64(m.lifecycleTransitionPollSuccess); got != 1 {
		t.Fatalf("lifecycle transition poll success = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.eventOutboxPendingCount); got != 3 {
		t.Fatalf("event outbox pending count = %v, want 3", got)
	}
	if got := testutil.ToFloat64(m.eventOutboxOldestAge); got != 480 {
		t.Fatalf("oldest event outbox age = %v, want 480", got)
	}
	if got := testutil.ToFloat64(m.eventOutboxPollSuccess); got != 1 {
		t.Fatalf("event outbox poll success = %v, want 1", got)
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
	m.markLifecycleTransitionPollFailed()
	if got := testutil.ToFloat64(m.lifecycleTransitionPollSuccess); got != 0 {
		t.Fatalf("failed lifecycle transition poll = %v, want 0", got)
	}
	m.markEventOutboxPollFailed()
	if got := testutil.ToFloat64(m.eventOutboxPollSuccess); got != 0 {
		t.Fatalf("failed event outbox poll = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.heldDueCount); got != 8 {
		t.Fatalf("failed poll changed held count to %v", got)
	}
	m.setFleetQueueHealth(now, state.AppWebhookFleetQueueHealth{})
	m.setLifecycleTransitionHealth(now, state.AppLifecycleTransitionHealth{})
	m.setEventOutboxHealth(now, state.AppWebhookEventOutboxHealth{})
	if got := testutil.ToFloat64(m.heldDueCount); got != 0 {
		t.Fatalf("cleared held count = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.heldDueSeconds); got != 0 {
		t.Fatalf("cleared held age = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.lifecycleTransitionPendingCount.WithLabelValues("park")); got != 0 {
		t.Fatalf("cleared pending park transitions = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.lifecycleTransitionOldestAge.WithLabelValues("wake")); got != 0 {
		t.Fatalf("cleared oldest wake age = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.eventOutboxPendingCount); got != 0 {
		t.Fatalf("cleared event outbox pending count = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.eventOutboxOldestAge); got != 0 {
		t.Fatalf("cleared event outbox age = %v, want 0", got)
	}
	families, err := reg.Gather()
	if err != nil || len(families) != 18 {
		t.Fatalf("fleet metrics = %d families, err=%v", len(families), err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			if family.GetName() == "schedd_app_lifecycle_transition_pending_count" ||
				family.GetName() == "schedd_app_lifecycle_transition_oldest_pending_seconds" {
				if len(metric.Label) != 1 || metric.Label[0].GetName() != "kind" {
					t.Errorf("metric %s labels = %+v, want only kind", family.GetName(), metric.Label)
				}
			} else if len(metric.Label) != 0 {
				t.Errorf("metric %s has labels: %+v", family.GetName(), metric.Label)
			}
		}
	}
}

func TestDispatcherRefreshesAppLifecycleTransitionHealth(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "transition-health@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "transition-health", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	parked := state.AppEvictedCold
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}
	transition, changed, err := store.BeginAppWakeTransition(ctx, app.ID)
	if err != nil || !changed {
		t.Fatalf("begin wake transition = %+v changed=%v err=%v", transition, changed, err)
	}
	reg := prometheus.NewRegistry()
	metrics := NewDeliveryHealthMetrics(reg, "schedd")
	dispatcher := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	dispatcher.HealthMetrics = metrics
	now := time.Now().Add(time.Minute)
	dispatcher.Now = func() time.Time { return now }
	dispatcher.refreshHealth(ctx)
	if got := testutil.ToFloat64(metrics.lifecycleTransitionPendingCount.WithLabelValues("wake")); got != 1 {
		t.Fatalf("pending wake transitions = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.lifecycleTransitionOldestAge.WithLabelValues("wake")); got < 59 {
		t.Fatalf("oldest wake age = %v, want at least 59 seconds", got)
	}
	if got := testutil.ToFloat64(metrics.lifecycleTransitionPollSuccess); got != 1 {
		t.Fatalf("lifecycle transition poll success = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.eventOutboxPendingCount); got != 0 {
		t.Fatalf("event outbox pending count = %v, want 0", got)
	}
	if got := testutil.ToFloat64(metrics.eventOutboxPollSuccess); got != 1 {
		t.Fatalf("event outbox poll success = %v, want 1", got)
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
