package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreAppWebhookRetentionBoundsDeletionAndCascadesAttempts(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)
	hook, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	cutoff := now.Add(-90 * 24 * time.Hour)
	seed := func(status state.AppWebhookDeliveryStatus, updated time.Time) state.AppWebhookDelivery {
		t.Helper()
		d, err := s.RecordAppWebhookDelivery(ctx, pgSampleDelivery(hook.ID, app, acct))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `update app_webhook_deliveries set status = $1, updated_at = $2 where id = $3`, status, updated, d.ID); err != nil {
			t.Fatal(err)
		}
		return d
	}
	oldSucceeded := seed(state.AppWebhookDeliverySucceeded, cutoff.Add(-time.Hour))
	oldDead := seed(state.AppWebhookDeliveryDead, cutoff.Add(-time.Minute))
	recentDead := seed(state.AppWebhookDeliveryDead, cutoff.Add(time.Minute))
	oldPending := seed(state.AppWebhookDeliveryPending, cutoff.Add(-time.Hour))
	oldInFlight := seed(state.AppWebhookDeliveryInFlight, cutoff.Add(-time.Hour))
	if _, err := pool.Exec(ctx, `
		insert into app_webhook_delivery_attempts
			(delivery_id, replay_generation, attempt_number, outcome, started_at, finished_at)
		values ($1, 0, 1, 'dead', $2, $2)
	`, oldDead.ID, cutoff.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	lockTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lockTx.Rollback(ctx) }()
	var lockedID string
	if err := lockTx.QueryRow(ctx, `select id from dead_letter_events where source = 'webhook_delivery' and source_id = $1 for update`, oldDead.ID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	pruneCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if got, err := s.PruneAppWebhookDeliveries(pruneCtx, cutoff, 1); err != nil || got != 1 {
		t.Fatalf("first bounded prune = %d, %v", got, err)
	}
	if _, err := s.AppWebhookDeliveryByID(ctx, oldDead.ID); err != nil {
		t.Fatalf("dead delivery was pruned while its projection was locked: %v", err)
	}
	if err := lockTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := s.PruneAppWebhookDeliveries(ctx, cutoff, 500); err != nil || got != 1 {
		t.Fatalf("second prune = %d, %v", got, err)
	}
	for _, id := range []string{oldSucceeded.ID, oldDead.ID} {
		if _, err := s.AppWebhookDeliveryByID(ctx, id); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("old terminal delivery %s survived: %v", id, err)
		}
	}
	var attempts int
	if err := pool.QueryRow(ctx, `select count(*) from app_webhook_delivery_attempts where delivery_id = $1`, oldDead.ID).Scan(&attempts); err != nil || attempts != 0 {
		t.Errorf("pruned attempt count = %d, %v", attempts, err)
	}
	var oldProjection, recentProjection int
	if err := pool.QueryRow(ctx, `select count(*) from dead_letter_events where source = 'webhook_delivery' and source_id = $1`, oldDead.ID).Scan(&oldProjection); err != nil || oldProjection != 0 {
		t.Errorf("pruned dead-letter projection count = %d, %v", oldProjection, err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from dead_letter_events where source = 'webhook_delivery' and source_id = $1`, recentDead.ID).Scan(&recentProjection); err != nil || recentProjection != 1 {
		t.Errorf("recent dead-letter projection count = %d, %v", recentProjection, err)
	}
	for _, id := range []string{recentDead.ID, oldPending.ID, oldInFlight.ID} {
		if _, err := s.AppWebhookDeliveryByID(ctx, id); err != nil {
			t.Errorf("retained delivery %s missing: %v", id, err)
		}
	}
	if err := s.ResetAppWebhookDeliveryFromDead(ctx, recentDead.ID, hook.ID, acct, now); err != nil {
		t.Fatalf("recent dead delivery cannot be replayed: %v", err)
	}
	if bytes, err := s.AppWebhookDeliveryStorageBytes(ctx); err != nil || bytes <= 0 {
		t.Fatalf("delivery storage bytes = %d, %v", bytes, err)
	}
}
