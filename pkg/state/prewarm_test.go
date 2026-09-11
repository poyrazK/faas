package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemStorePrewarmClaimIsSingleFlight(t *testing.T) {
	m := NewMemStore()
	app := App{ID: "app-1", AccountID: "acct-1", Status: AppActive}
	m.apps[app.ID] = app
	now := time.Now().UTC()
	intent, err := m.CreatePrewarmIntent(context.Background(), app.ID, app.AccountID, 2, now.Add(time.Minute), now.Add(5*time.Minute), PrewarmTriggerCalendar)
	if err != nil {
		t.Fatal(err)
	}
	due, err := m.ListDuePrewarmIntents(context.Background(), now.Add(2*time.Minute), now, 10)
	if err != nil || len(due) != 1 || due[0].ID != intent.ID {
		t.Fatalf("due = %#v, err = %v", due, err)
	}
	if _, claimed, err := m.ClaimPrewarmIntent(context.Background(), intent.ID, now.Add(2*time.Minute)); err != nil || !claimed {
		t.Fatalf("first claim = claimed %v, err %v", claimed, err)
	}
	if _, claimed, err := m.ClaimPrewarmIntent(context.Background(), intent.ID, now.Add(2*time.Minute)); err != nil || claimed {
		t.Fatalf("second claim = claimed %v, err %v", claimed, err)
	}
	if err := m.CompletePrewarmIntent(context.Background(), intent.ID, now.Add(2*time.Minute), 2, "admitted:2"); err != nil {
		t.Fatal(err)
	}
	got, err := m.PrewarmIntentByID(context.Background(), intent.ID)
	if err != nil || got.Status != PrewarmStatusSucceeded || got.Outcome != "admitted:2" {
		t.Fatalf("intent = %#v, err = %v", got, err)
	}
}

func TestValidatePrewarmIntentRejectsExpiredWindow(t *testing.T) {
	now := time.Now().UTC()
	if err := ValidatePrewarmIntent(1, now.Add(time.Minute), now, now, PrewarmTriggerCalendar); err == nil {
		t.Fatal("expected expires_at validation error")
	}
	for name, tc := range map[string]struct {
		count             int
		wakeAt, expiresAt time.Time
		trigger           string
	}{
		"count":   {0, now.Add(time.Minute), now.Add(2 * time.Minute), PrewarmTriggerCalendar},
		"wake":    {1, now, now.Add(time.Minute), PrewarmTriggerCalendar},
		"trigger": {1, now.Add(time.Minute), now.Add(2 * time.Minute), " "},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidatePrewarmIntent(tc.count, tc.wakeAt, tc.expiresAt, now, tc.trigger); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestMemStoreActivePrewarmFloorExpires(t *testing.T) {
	m := NewMemStore()
	app := App{ID: "app-1", AccountID: "acct-1", Status: AppActive}
	m.apps[app.ID] = app
	now := time.Now().UTC()
	intent, err := m.CreatePrewarmIntent(context.Background(), app.ID, app.AccountID, 3, now.Add(time.Minute), now.Add(5*time.Minute), PrewarmTriggerCalendar)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := m.ClaimPrewarmIntent(context.Background(), intent.ID, now.Add(2*time.Minute)); err != nil || !claimed {
		t.Fatalf("claim = %v, err = %v", claimed, err)
	}
	if got, err := m.ActivePrewarmFloor(context.Background(), app.ID, now.Add(3*time.Minute)); err != nil || got != 3 {
		t.Fatalf("active floor = %d, err = %v", got, err)
	}
	if got, err := m.ActivePrewarmFloor(context.Background(), app.ID, now.Add(6*time.Minute)); err != nil || got != 0 {
		t.Fatalf("expired floor = %d, err = %v", got, err)
	}
}

func TestMemStorePrewarmRunningIntentCannotBeCancelled(t *testing.T) {
	m := NewMemStore()
	app := App{ID: "app-1", AccountID: "acct-1", Status: AppActive}
	m.apps[app.ID] = app
	now := time.Now().UTC()
	intent, err := m.CreatePrewarmIntent(context.Background(), app.ID, app.AccountID, 1, now.Add(time.Minute), now.Add(5*time.Minute), PrewarmTriggerCalendar)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := m.ClaimPrewarmIntent(context.Background(), intent.ID, now); err != nil || !claimed {
		t.Fatalf("claim = %v, err = %v", claimed, err)
	}
	if err := m.CancelPrewarmIntent(context.Background(), intent.ID, app.AccountID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel running = %v, want ErrNotFound", err)
	}
	got, err := m.PrewarmIntentByID(context.Background(), intent.ID)
	if err != nil || got.Status != PrewarmStatusRunning {
		t.Fatalf("intent = %#v, err = %v", got, err)
	}
}

func TestMemStorePrewarmListAndFail(t *testing.T) {
	m := NewMemStore()
	app := App{ID: "app-1", AccountID: "acct-1", Status: AppActive}
	other := App{ID: "app-2", AccountID: "acct-1", Status: AppActive}
	m.apps[app.ID] = app
	m.apps[other.ID] = other
	now := time.Now().UTC()
	first, err := m.CreatePrewarmIntent(context.Background(), app.ID, app.AccountID, 1, now.Add(2*time.Minute), now.Add(5*time.Minute), PrewarmTriggerCalendar)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.CreatePrewarmIntent(context.Background(), app.ID, app.AccountID, 2, now.Add(time.Minute), now.Add(6*time.Minute), PrewarmTriggerCalendar)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreatePrewarmIntent(context.Background(), other.ID, other.AccountID, 1, now.Add(3*time.Minute), now.Add(7*time.Minute), PrewarmTriggerCalendar); err != nil {
		t.Fatal(err)
	}
	rows, err := m.ListPrewarmIntentsForApp(context.Background(), app.ID, 1)
	if err != nil || len(rows) != 1 || rows[0].ID != second.ID {
		t.Fatalf("limited rows = %#v, err = %v", rows, err)
	}
	rows, err = m.ListPrewarmIntentsForApp(context.Background(), app.ID, 0)
	if err != nil || len(rows) != 2 || rows[0].ID != second.ID || rows[1].ID != first.ID {
		t.Fatalf("sorted rows = %#v, err = %v", rows, err)
	}
	if _, claimed, err := m.ClaimPrewarmIntent(context.Background(), first.ID, now); err != nil || !claimed {
		t.Fatalf("claim = %v, err = %v", claimed, err)
	}
	if err := m.FailPrewarmIntent(context.Background(), first.ID, now.Add(time.Minute), "capacity unavailable"); err != nil {
		t.Fatal(err)
	}
	failed, err := m.PrewarmIntentByID(context.Background(), first.ID)
	if err != nil || failed.Status != PrewarmStatusFailed || failed.LastError != "capacity unavailable" || failed.FiredAt == nil {
		t.Fatalf("failed intent = %#v, err = %v", failed, err)
	}
	if err := m.FailPrewarmIntent(context.Background(), second.ID, now, "ignored"); err != nil {
		t.Fatal(err)
	}
	if err := m.FailPrewarmIntent(context.Background(), "missing", now, "ignored"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing fail = %v, want ErrNotFound", err)
	}
	if _, err := m.CreatePrewarmIntent(context.Background(), "missing-app", app.AccountID, 1, now.Add(time.Minute), now.Add(2*time.Minute), PrewarmTriggerCalendar); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing app create = %v, want ErrNotFound", err)
	}
	if err := m.CancelPrewarmIntent(context.Background(), second.ID, "wrong-account"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-account cancel = %v, want ErrNotFound", err)
	}
}

func TestMemStorePrewarmDueFiltersExpiredAndUsesDefaultLimit(t *testing.T) {
	m := NewMemStore()
	app := App{ID: "app-1", AccountID: "acct-1", Status: AppActive}
	m.apps[app.ID] = app
	now := time.Now().UTC()
	if _, err := m.CreatePrewarmIntent(context.Background(), app.ID, app.AccountID, 1, now.Add(time.Minute), now.Add(2*time.Minute), PrewarmTriggerCalendar); err != nil {
		t.Fatal(err)
	}
	m.prewarmIntents["expired"] = PrewarmIntent{
		ID: "expired", AppID: app.ID, AccountID: app.AccountID, Count: 1,
		WakeAt: now.Add(-time.Minute), ExpiresAt: now, Trigger: PrewarmTriggerCalendar,
		Status: PrewarmStatusPending, CreatedAt: now.Add(-2 * time.Minute),
	}
	rows, err := m.ListDuePrewarmIntents(context.Background(), now.Add(3*time.Minute), now, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("due rows = %#v, err = %v", rows, err)
	}
}

func TestMemStoreActivePrewarmFloorUsesAdmittedCount(t *testing.T) {
	m := NewMemStore()
	app := App{ID: "app-1", AccountID: "acct-1", Status: AppActive}
	m.apps[app.ID] = app
	now := time.Now().UTC()
	intent, err := m.CreatePrewarmIntent(context.Background(), app.ID, app.AccountID, 4, now.Add(time.Minute), now.Add(5*time.Minute), PrewarmTriggerCalendar)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := m.ClaimPrewarmIntent(context.Background(), intent.ID, now); err != nil || !claimed {
		t.Fatalf("claim = %v, err = %v", claimed, err)
	}
	if err := m.CompletePrewarmIntent(context.Background(), intent.ID, now, 2, "partial:2:capacity"); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ActivePrewarmFloor(context.Background(), app.ID, now.Add(time.Minute)); err != nil || got != 2 {
		t.Fatalf("active floor = %d, err = %v", got, err)
	}
}

func TestMemStorePrewarmExpiryTerminalizesOnlyExpiredPending(t *testing.T) {
	m := NewMemStore()
	app := App{ID: "app-1", AccountID: "acct-1", Status: AppActive}
	m.apps[app.ID] = app
	now := time.Now().UTC()
	if _, err := m.CreatePrewarmIntent(context.Background(), app.ID, app.AccountID, 0,
		now.Add(time.Minute), now.Add(2*time.Minute), PrewarmTriggerCalendar); err == nil {
		t.Fatal("invalid prewarm creation succeeded")
	}

	expiredID := newID()
	// CreatePrewarmIntent correctly rejects a past wake window, so seed the
	// expired row directly to exercise the reaper's storage behavior.
	m.prewarmIntents[expiredID] = PrewarmIntent{
		ID: expiredID, AppID: app.ID, AccountID: app.AccountID, Count: 1,
		WakeAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(-time.Minute),
		Trigger: PrewarmTriggerCalendar, Status: PrewarmStatusPending, CreatedAt: now,
	}
	active, err := m.CreatePrewarmIntent(context.Background(), app.ID, app.AccountID, 1,
		now.Add(time.Minute), now.Add(2*time.Minute), PrewarmTriggerCalendar)
	if err != nil {
		t.Fatal(err)
	}

	rows, err := m.ListExpiredPrewarmIntents(context.Background(), now, 0)
	if err != nil || len(rows) != 1 || rows[0].ID != expiredID {
		t.Fatalf("expired rows = %#v, err = %v", rows, err)
	}
	if changed, err := m.ExpirePrewarmIntent(context.Background(), expiredID, now); err != nil || !changed {
		t.Fatalf("ExpirePrewarmIntent = changed=%v, err=%v", changed, err)
	}
	if changed, err := m.ExpirePrewarmIntent(context.Background(), expiredID, now); err != nil || changed {
		t.Fatalf("duplicate ExpirePrewarmIntent = changed=%v, err=%v", changed, err)
	}
	got, err := m.PrewarmIntentByID(context.Background(), expiredID)
	if err != nil || got.Status != PrewarmStatusFailed || got.Outcome != "expired" || got.LastError != "expired" || got.FiredAt == nil {
		t.Fatalf("expired intent = %#v, err = %v", got, err)
	}
	rows, err = m.ListExpiredPrewarmIntents(context.Background(), now, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("expired rows after terminalization = %#v, err = %v", rows, err)
	}
	if changed, err := m.ExpirePrewarmIntent(context.Background(), active.ID, now); err != nil || changed {
		t.Fatalf("active ExpirePrewarmIntent = changed=%v, err=%v", changed, err)
	}
	if changed, err := m.ExpirePrewarmIntent(context.Background(), "missing", now); !errors.Is(err, ErrNotFound) || changed {
		t.Fatalf("missing ExpirePrewarmIntent = changed=%v, err=%v", changed, err)
	}
}
