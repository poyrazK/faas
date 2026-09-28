package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// testWebhookDeliveryAttemptsHealthRetentionAndStorage pins the shared
// delivery ledger behavior used by customer history and fleet operations.
func testWebhookDeliveryAttemptsHealthRetentionAndStorage(t *testing.T, fx *Fixture) {
	t.Helper()
	hook, err := fx.Store.CreateAppWebhook(fx.Ctx, state.AppWebhook{
		AppID: fx.App.ID, AccountID: fx.Account.ID,
		TargetURL:    "https://example.test/webhook-" + uuid.NewString(),
		SecretSealed: []byte("sealed"),
		EventFilter:  []string{string(state.AppWebhookEventAppDeployed)},
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("CreateAppWebhook: %v", err)
	}

	// Use a future timeline so the case stays deterministic even if CI is
	// delayed between creating a row and claiming its next retry.
	base := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	record := func(nextAttempt time.Time) state.AppWebhookDelivery {
		t.Helper()
		d, err := fx.Store.RecordAppWebhookDelivery(fx.Ctx, state.AppWebhookDelivery{
			WebhookID: hook.ID, AppID: fx.App.ID, AccountID: fx.Account.ID,
			Event: state.AppWebhookEventAppDeployed, Payload: []byte(`{"v":1}`),
			Status: state.AppWebhookDeliveryPending, NextAttemptAt: nextAttempt,
		})
		if err != nil {
			t.Fatalf("RecordAppWebhookDelivery: %v", err)
		}
		return d
	}
	claim := func(id string, at time.Time) state.AppWebhookDelivery {
		t.Helper()
		rows, err := fx.Store.ClaimDueAppWebhookDeliveries(fx.Ctx, 1, at)
		if err != nil {
			t.Fatalf("ClaimDueAppWebhookDeliveries: %v", err)
		}
		if len(rows) != 1 || rows[0].ID != id {
			t.Fatalf("claimed rows = %+v, want only %s", rows, id)
		}
		return rows[0]
	}

	// One row gets a transient failure and then succeeds. Its history must
	// retain both attempts in newest-first order, with a working cursor.
	retried := record(base.Add(-time.Minute))
	firstClaim := claim(retried.ID, base)
	retryAt := base.Add(time.Minute)
	if err := fx.Store.MarkAppWebhookDeliveryFailed(fx.Ctx, retried.ID, 503,
		firstClaim.Attempt, firstClaim.NextAttemptAt, "temporarily unavailable", retryAt,
		state.AppWebhookAttemptMetadata{StartedAt: base.Add(-time.Second), FinishedAt: base}); err != nil {
		t.Fatalf("MarkAppWebhookDeliveryFailed: %v", err)
	}
	secondClaim := claim(retried.ID, retryAt.Add(time.Second))
	succeededAt := retryAt.Add(5 * time.Second)
	if err := fx.Store.MarkAppWebhookDeliverySucceeded(fx.Ctx, retried.ID, 204,
		secondClaim.Attempt, secondClaim.NextAttemptAt, succeededAt,
		state.AppWebhookAttemptMetadata{StartedAt: retryAt.Add(time.Second), FinishedAt: succeededAt}); err != nil {
		t.Fatalf("MarkAppWebhookDeliverySucceeded: %v", err)
	}

	dead := record(base.Add(2 * time.Minute))
	deadClaimAt := base.Add(3 * time.Minute)
	deadClaim := claim(dead.ID, deadClaimAt)
	deadFinishedAt := deadClaimAt.Add(time.Second)
	if err := fx.Store.MarkAppWebhookDeliveryDead(fx.Ctx, dead.ID,
		deadClaim.Attempt, deadClaim.NextAttemptAt, "receiver rejected",
		state.AppWebhookAttemptMetadata{
			ResponseCode: 410,
			StartedAt:    deadClaimAt,
			FinishedAt:   deadFinishedAt,
		}); err != nil {
		t.Fatalf("MarkAppWebhookDeliveryDead: %v", err)
	}

	oldestDue := base.Add(-5 * time.Minute)
	record(oldestDue)
	record(base.Add(30 * time.Minute)) // Future work counts as pending but is not overdue.
	healthAt := base.Add(4 * time.Minute)
	health, err := fx.Store.AppWebhookDeliveryHealth(fx.Ctx, hook.ID, fx.Account.ID, healthAt)
	if err != nil {
		t.Fatalf("AppWebhookDeliveryHealth: %v", err)
	}
	if health.ReceiverState != state.AppWebhookReceiverReady || health.PendingCount != 2 ||
		health.InFlightCount != 0 || health.DeadCount != 1 ||
		health.RecentSucceededCount != 1 || health.RecentDeadCount != 1 ||
		health.OldestOverdueAt == nil || !health.OldestOverdueAt.Equal(oldestDue) {
		t.Fatalf("delivery health = %+v; want ready, pending=2, dead=1, recent success/dead=1, oldest=%s",
			health, oldestDue)
	}
	if _, err := fx.Store.AppWebhookDeliveryHealth(fx.Ctx, hook.ID, uuid.NewString(), healthAt); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign webhook health = %v, want ErrNotFound", err)
	}

	page, next, err := fx.Store.ListAppWebhookDeliveryAttempts(fx.Ctx,
		retried.ID, hook.ID, fx.Account.ID, 1, "")
	if err != nil || len(page) != 1 || next == "" ||
		page[0].ReplayGeneration != 0 || page[0].AttemptNumber != 2 ||
		page[0].Outcome != "succeeded" || page[0].ResponseCode != 204 {
		t.Fatalf("newest attempt page = %+v, next=%q, err=%v", page, next, err)
	}
	older, end, err := fx.Store.ListAppWebhookDeliveryAttempts(fx.Ctx,
		retried.ID, hook.ID, fx.Account.ID, 1, next)
	if err != nil || len(older) != 1 || end != "" || older[0].AttemptNumber != 1 ||
		older[0].Outcome != "retrying" || older[0].ResponseCode != 503 ||
		older[0].NextAttemptAt == nil || !older[0].NextAttemptAt.Equal(retryAt) {
		t.Fatalf("older attempt page = %+v, end=%q, err=%v", older, end, err)
	}
	foreign, _, err := fx.Store.ListAppWebhookDeliveryAttempts(fx.Ctx,
		retried.ID, hook.ID, uuid.NewString(), 10, "")
	if err != nil || len(foreign) != 0 {
		t.Fatalf("foreign attempt history = %+v, err=%v; want empty", foreign, err)
	}
	if _, _, err := fx.Store.ListAppWebhookDeliveryAttempts(fx.Ctx,
		retried.ID, hook.ID, fx.Account.ID, 10, "invalid"); !errors.Is(err, state.ErrInvalidAppWebhookAttemptPageToken) {
		t.Fatalf("invalid attempt cursor = %v, want ErrInvalidAppWebhookAttemptPageToken", err)
	}

	fleet, err := fx.Store.AppWebhookFleetQueueHealth(fx.Ctx, healthAt)
	if err != nil || fleet.HeldDueCount != 0 || fleet.OldestHeldAt != nil ||
		fleet.OldestClaimableAt == nil || !fleet.OldestClaimableAt.Equal(oldestDue) {
		t.Fatalf("fleet queue health = %+v, err=%v; want one claimable oldest delivery at %s",
			fleet, err, oldestDue)
	}
	oldest, err := fx.Store.OldestOverdueAppWebhookDeliveryAt(fx.Ctx, healthAt)
	if err != nil || oldest == nil || !oldest.Equal(oldestDue) {
		t.Fatalf("fleet oldest overdue = %v, err=%v; want %s", oldest, err, oldestDue)
	}

	// Retention removes terminal rows in bounded batches and leaves pending
	// work alone. The succeeded row is older than the dead row, so it is first.
	cutoff := base.Add(10 * time.Minute)
	if pruned, err := fx.Store.PruneAppWebhookDeliveries(fx.Ctx, cutoff, 1); err != nil || pruned != 1 {
		t.Fatalf("first bounded prune = %d, %v; want 1", pruned, err)
	}
	if attempts, _, err := fx.Store.ListAppWebhookDeliveryAttempts(fx.Ctx,
		retried.ID, hook.ID, fx.Account.ID, 10, ""); err != nil || len(attempts) != 0 {
		t.Fatalf("pruned delivery attempts = %+v, err=%v; want cascade deletion", attempts, err)
	}
	if pruned, err := fx.Store.PruneAppWebhookDeliveries(fx.Ctx, cutoff, 1); err != nil || pruned != 1 {
		t.Fatalf("second bounded prune = %d, %v; want 1", pruned, err)
	}
	if pruned, err := fx.Store.PruneAppWebhookDeliveries(fx.Ctx, cutoff, 1); err != nil || pruned != 0 {
		t.Fatalf("prune after terminal rows = %d, %v; want 0 (pending rows remain)", pruned, err)
	}

	bytes, err := fx.Store.AppWebhookDeliveryStorageBytes(fx.Ctx)
	if err != nil || bytes < 0 {
		t.Fatalf("AppWebhookDeliveryStorageBytes = %d, %v; want a non-negative size", bytes, err)
	}
}
