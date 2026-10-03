package state

import (
	"errors"
	"testing"
	"time"
)

func TestMemStoreAppWebhookRetentionKeepsActiveAndRecentReplayable(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	hook, err := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cutoff := now.Add(-90 * 24 * time.Hour)
	seed := func(status AppWebhookDeliveryStatus, updated time.Time) AppWebhookDelivery {
		t.Helper()
		d := memSampleDelivery(hook.ID, app.ID, acct.ID, "")
		d.Status = status
		d.CreatedAt = updated
		got, err := m.RecordAppWebhookDelivery(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	oldSucceeded := seed(AppWebhookDeliverySucceeded, cutoff.Add(-time.Hour))
	oldDead := seed(AppWebhookDeliveryDead, cutoff.Add(-time.Minute))
	recentDead := seed(AppWebhookDeliveryDead, cutoff.Add(time.Minute))
	oldPending := seed(AppWebhookDeliveryPending, cutoff.Add(-time.Hour))
	oldInFlight := seed(AppWebhookDeliveryInFlight, cutoff.Add(-time.Hour))
	m.appWebhookDeliveryAttempts[oldDead.ID] = []AppWebhookDeliveryAttempt{{DeliveryID: oldDead.ID}}
	m.appWebhookReplayGenerations[oldDead.ID] = 2
	deadLetterID := unifiedDeadLetterEventID("webhook_delivery", oldDead.ID)
	m.deadLetterPurged[deadLetterID] = struct{}{}

	if got, err := m.PruneAppWebhookDeliveries(ctx, cutoff, 1); err != nil || got != 1 {
		t.Fatalf("first bounded prune = %d, %v", got, err)
	}
	if got, err := m.PruneAppWebhookDeliveries(ctx, cutoff, 500); err != nil || got != 1 {
		t.Fatalf("second prune = %d, %v", got, err)
	}
	for _, id := range []string{oldSucceeded.ID, oldDead.ID} {
		if _, err := m.AppWebhookDeliveryByID(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("old terminal delivery %s survived: %v", id, err)
		}
	}
	if _, ok := m.appWebhookDeliveryAttempts[oldDead.ID]; ok {
		t.Error("pruned attempt history survived")
	}
	if _, ok := m.appWebhookReplayGenerations[oldDead.ID]; ok {
		t.Error("pruned replay generation survived")
	}
	if _, ok := m.deadLetterPurged[deadLetterID]; ok {
		t.Error("pruned dead-letter tombstone survived")
	}
	for _, id := range []string{recentDead.ID, oldPending.ID, oldInFlight.ID} {
		if _, err := m.AppWebhookDeliveryByID(ctx, id); err != nil {
			t.Errorf("retained delivery %s missing: %v", id, err)
		}
	}
	if err := m.ResetAppWebhookDeliveryFromDead(ctx, recentDead.ID, hook.ID, acct.ID, now); err != nil {
		t.Fatalf("recent dead delivery cannot be replayed: %v", err)
	}
}
