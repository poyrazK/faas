package state_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestMemStoreAppWebhookRetentionKeepsActiveAndRecentReplayable(t *testing.T) {
	m, ctx, acct, app := state.WebhookFixture(t)
	hook, err := m.CreateAppWebhook(ctx, state.MemSampleWebhook(acct.ID, app.ID))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cutoff := now.Add(-90 * 24 * time.Hour)
	seed := func(status state.AppWebhookDeliveryStatus, updated time.Time) state.AppWebhookDelivery {
		t.Helper()
		d := state.MemSampleDelivery(hook.ID, app.ID, acct.ID, "")
		d.Status = status
		d.CreatedAt = updated
		got, err := m.RecordAppWebhookDelivery(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	oldSucceeded := seed(state.AppWebhookDeliverySucceeded, cutoff.Add(-time.Hour))
	oldDead := seed(state.AppWebhookDeliveryDead, cutoff.Add(-time.Minute))
	recentDead := seed(state.AppWebhookDeliveryDead, cutoff.Add(time.Minute))
	oldPending := seed(state.AppWebhookDeliveryPending, cutoff.Add(-time.Hour))
	oldInFlight := seed(state.AppWebhookDeliveryInFlight, cutoff.Add(-time.Hour))
	(*m.AppWebhookDeliveryAttempts())[oldDead.ID] = []state.AppWebhookDeliveryAttempt{{DeliveryID: oldDead.ID}}
	(*m.AppWebhookReplayGenerations())[oldDead.ID] = 2
	deadLetterID := state.UnifiedDeadLetterEventID("webhook_delivery", oldDead.ID)
	(*m.DeadLetterPurged())[deadLetterID] = struct{}{}

	if got, err := m.PruneAppWebhookDeliveries(ctx, cutoff, 1); err != nil || got != 1 {
		t.Fatalf("first bounded prune = %d, %v", got, err)
	}
	if got, err := m.PruneAppWebhookDeliveries(ctx, cutoff, 500); err != nil || got != 1 {
		t.Fatalf("second prune = %d, %v", got, err)
	}
	for _, id := range []string{oldSucceeded.ID, oldDead.ID} {
		if _, err := m.AppWebhookDeliveryByID(ctx, id); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("old terminal delivery %s survived: %v", id, err)
		}
	}
	if _, ok := (*m.AppWebhookDeliveryAttempts())[oldDead.ID]; ok {
		t.Error("pruned attempt history survived")
	}
	if _, ok := (*m.AppWebhookReplayGenerations())[oldDead.ID]; ok {
		t.Error("pruned replay generation survived")
	}
	if _, ok := (*m.DeadLetterPurged())[deadLetterID]; ok {
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
