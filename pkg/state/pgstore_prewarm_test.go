//go:build !no_pg

package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 160
// TestPgStorePrewarmLifecycle pins the durable scheduled-prewarm contract
// against the real SQL store (ADR-160). The MemStore mirror covers the same
// state transitions without a database; this test keeps the hand-written SQL
// and nullable timestamp scanning exercised in CI's PostgreSQL shard.
func TestPgStorePrewarmLifecycle(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "prewarm", "intent")
	now := time.Now().UTC()

	intent, err := s.CreatePrewarmIntent(ctx, appID, accountID, 3,
		now.Add(time.Minute), now.Add(5*time.Minute), state.PrewarmTriggerCalendar)
	if err != nil {
		t.Fatalf("CreatePrewarmIntent: %v", err)
	}
	if intent.Status != state.PrewarmStatusPending || intent.ClaimedAt != nil || intent.FiredAt != nil {
		t.Fatalf("created intent = %+v", intent)
	}

	got, err := s.PrewarmIntentByID(ctx, intent.ID)
	if err != nil {
		t.Fatalf("PrewarmIntentByID: %v", err)
	}
	if got.ID != intent.ID || got.Trigger != state.PrewarmTriggerCalendar {
		t.Fatalf("loaded intent = %+v", got)
	}

	rows, err := s.ListPrewarmIntentsForApp(ctx, appID, 201)
	if err != nil || len(rows) != 1 || rows[0].ID != intent.ID {
		t.Fatalf("ListPrewarmIntentsForApp = %#v, err = %v", rows, err)
	}
	due, err := s.ListDuePrewarmIntents(ctx, now.Add(2*time.Minute), now, 0)
	if err != nil || len(due) != 1 || due[0].ID != intent.ID {
		t.Fatalf("ListDuePrewarmIntents = %#v, err = %v", due, err)
	}
	if floor, err := s.ActivePrewarmFloor(ctx, appID, now); err != nil || floor != 0 {
		t.Fatalf("initial ActivePrewarmFloor = %d, err = %v", floor, err)
	}

	claimed, didClaim, err := s.ClaimPrewarmIntent(ctx, intent.ID, now)
	if err != nil || !didClaim || claimed.Status != state.PrewarmStatusRunning || claimed.ClaimedAt == nil {
		t.Fatalf("ClaimPrewarmIntent = %+v, claimed=%v, err=%v", claimed, didClaim, err)
	}
	if _, didClaim, err := s.ClaimPrewarmIntent(ctx, intent.ID, now); err != nil || didClaim {
		t.Fatalf("second ClaimPrewarmIntent = claimed=%v, err=%v", didClaim, err)
	}
	if floor, err := s.ActivePrewarmFloor(ctx, appID, now); err != nil || floor != 3 {
		t.Fatalf("running ActivePrewarmFloor = %d, err = %v", floor, err)
	}

	if err := s.CompletePrewarmIntent(ctx, intent.ID, now, 2, "admitted:2"); err != nil {
		t.Fatalf("CompletePrewarmIntent: %v", err)
	}
	if err := s.CompletePrewarmIntent(ctx, intent.ID, now, 2, "duplicate"); err != nil {
		t.Fatalf("duplicate CompletePrewarmIntent: %v", err)
	}
	got, err = s.PrewarmIntentByID(ctx, intent.ID)
	if err != nil || got.Status != state.PrewarmStatusSucceeded || got.AdmittedCount != 2 || got.FiredAt == nil {
		t.Fatalf("completed intent = %+v, err = %v", got, err)
	}
	if floor, err := s.ActivePrewarmFloor(ctx, appID, now); err != nil || floor != 2 {
		t.Fatalf("completed ActivePrewarmFloor = %d, err = %v", floor, err)
	}
	if err := s.FailPrewarmIntent(ctx, intent.ID, now, "ignored"); err != nil {
		t.Fatalf("duplicate FailPrewarmIntent: %v", err)
	}

	failed, err := s.CreatePrewarmIntent(ctx, appID, accountID, 1,
		now.Add(2*time.Minute), now.Add(6*time.Minute), state.PrewarmTriggerPattern)
	if err != nil {
		t.Fatalf("Create failed intent: %v", err)
	}
	if _, didClaim, err := s.ClaimPrewarmIntent(ctx, failed.ID, now); err != nil || !didClaim {
		t.Fatalf("claim failed intent = %v, err = %v", didClaim, err)
	}
	if err := s.FailPrewarmIntent(ctx, failed.ID, now, strings.Repeat("x", 4096)); err != nil {
		t.Fatalf("FailPrewarmIntent: %v", err)
	}
	failed, err = s.PrewarmIntentByID(ctx, failed.ID)
	if err != nil || failed.Status != state.PrewarmStatusFailed || len(failed.LastError) != 2048 {
		t.Fatalf("failed intent = %+v, err = %v", failed, err)
	}

	cancelled, err := s.CreatePrewarmIntent(ctx, appID, accountID, 1,
		now.Add(3*time.Minute), now.Add(7*time.Minute), state.PrewarmTriggerWebhook)
	if err != nil {
		t.Fatalf("Create cancelled intent: %v", err)
	}
	if err := s.CancelPrewarmIntent(ctx, cancelled.ID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("wrong-account cancel = %v, want ErrNotFound", err)
	}
	if err := s.CancelPrewarmIntent(ctx, cancelled.ID, accountID); err != nil {
		t.Fatalf("CancelPrewarmIntent: %v", err)
	}
	cancelled, err = s.PrewarmIntentByID(ctx, cancelled.ID)
	if err != nil || cancelled.Status != state.PrewarmStatusCancelled {
		t.Fatalf("cancelled intent = %+v, err = %v", cancelled, err)
	}

	missing := uuid.NewString()
	if _, err := s.CreatePrewarmIntent(ctx, missing, accountID, 1, now.Add(time.Minute), now.Add(2*time.Minute), state.PrewarmTriggerCalendar); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing app create = %v, want ErrNotFound", err)
	}
	if _, err := s.PrewarmIntentByID(ctx, missing); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing get = %v, want ErrNotFound", err)
	}
	if _, didClaim, err := s.ClaimPrewarmIntent(ctx, missing, now); !errors.Is(err, state.ErrNotFound) || didClaim {
		t.Fatalf("missing claim = claimed=%v, err=%v", didClaim, err)
	}
	if err := s.CompletePrewarmIntent(ctx, missing, now, 1, "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing complete = %v, want ErrNotFound", err)
	}
	if err := s.FailPrewarmIntent(ctx, missing, now, "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing fail = %v, want ErrNotFound", err)
	}
	if err := s.CancelPrewarmIntent(ctx, missing, accountID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing cancel = %v, want ErrNotFound", err)
	}
}
