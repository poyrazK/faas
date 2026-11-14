package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ChronoLake finding 28: `webhook retry` left the delivery's unified
// dead-letter event active after it succeeded, and deleting the webhook
// orphaned its events (listed, but 404 on replay).
func TestPgStoreAppWebhookRetryAndDeleteResolveDeadLetters(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)
	hook, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	kill := func() state.AppWebhookDelivery {
		t.Helper()
		d, err := s.RecordAppWebhookDelivery(ctx, pgSampleDelivery(hook.ID, app, acct))
		if err != nil {
			t.Fatal(err)
		}
		// The capture trigger projects the transition to dead.
		if _, err := pool.Exec(ctx, `update app_webhook_deliveries set status = 'dead' where id = $1`, d.ID); err != nil {
			t.Fatal(err)
		}
		return d
	}
	projection := func(id string) (exists bool, replayed bool) {
		t.Helper()
		var replayedAt *time.Time
		err := pool.QueryRow(ctx, `select replayed_at from dead_letter_events where source = 'webhook_delivery' and source_id = $1`, id).Scan(&replayedAt)
		if err != nil {
			return false, false
		}
		return true, replayedAt != nil
	}

	retried := kill()
	if exists, replayed := projection(retried.ID); !exists || replayed {
		t.Fatalf("dead delivery not projected as an active failed event: exists=%v replayed=%v", exists, replayed)
	}
	if err := s.ResetAppWebhookDeliveryFromDead(ctx, retried.ID, hook.ID, acct, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if exists, replayed := projection(retried.ID); !exists || !replayed {
		t.Fatalf("webhook retry left the failed event active: exists=%v replayed=%v", exists, replayed)
	}
	// Retrying a delivery that is no longer dead is still a conflict.
	if err := s.ResetAppWebhookDeliveryFromDead(ctx, retried.ID, hook.ID, acct, time.Now().UTC()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("second retry = %v, want ErrConflict", err)
	}

	orphan := kill()
	if err := s.DeleteAppWebhook(ctx, hook.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{retried.ID, orphan.ID} {
		if exists, _ := projection(id); exists {
			t.Fatalf("deleted webhook left dead-letter event for delivery %s", id)
		}
	}
	if err := s.DeleteAppWebhook(ctx, hook.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}
