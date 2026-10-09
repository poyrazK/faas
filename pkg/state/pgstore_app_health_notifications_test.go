package state_test

// adr: 735

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-735: assessment, history, cooldown state and outbox intent commit together.
func TestAppHealthNotificationsPgAtomicRestartAndConcurrentRelay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	account, app := healthNotificationFixture(t, s)
	hook := observedHealthNotificationHook(t, s, account, app, "atomic", []string{string(state.AppWebhookEventAppHealthChanged)}, true)
	now := time.Now().UTC().Truncate(time.Second)
	recordHealthNotificationAssessment(t, s, app.ID, "healthy", now)
	claim, err := s.ClaimAppHealth(t.Context(), uuid.NewString(), now.Add(31*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	a := healthNotificationAssessment(app.ID, "unhealthy", now.Add(31*time.Second+time.Millisecond))
	if _, err := pool.Exec(t.Context(), "ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT reject_health_test CHECK (false)"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAppHealth(t.Context(), claim, a, now.Add(31*time.Second+time.Millisecond)); err == nil {
		t.Fatal("outbox failure committed assessment")
	}
	page, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 20, "", now.Add(32*time.Second))
	if err != nil || len(page.Entries) != 1 || page.Latest.Status != "healthy" {
		t.Fatal("assessment/history escaped rollback", err, page)
	}
	if _, err := pool.Exec(t.Context(), "ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT reject_health_test"); err != nil {
		t.Fatal(err)
	}
	// The same still-valid claim can complete after the failed transaction.
	if err := s.FinishAppHealth(t.Context(), claim, a, now.Add(31*time.Second+2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	late := observedHealthNotificationHook(t, restarted, account, app, "after-commit", []string{string(state.AppWebhookEventAppHealthChanged)}, true)
	var wg sync.WaitGroup
	processed := make(chan int, 2)
	errs := make(chan error, 2)
	for _, store := range []*state.PgStore{s, restarted} {
		wg.Add(1)
		go func(store *state.PgStore) {
			defer wg.Done()
			n, err := store.DrainAppWebhookEventOutbox(t.Context(), 32)
			processed <- n
			errs <- err
		}(store)
	}
	wg.Wait()
	if n := <-processed + <-processed; n != 1 {
		t.Fatal("concurrent relay duplicated source", n)
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := healthNotifications(t, restarted, app, hook); len(got) != 1 || got[0].Status != "unhealthy" {
		t.Fatal("restart lost committed notification", got)
	}
	if got := healthNotifications(t, restarted, app, late); len(got) != 0 {
		t.Fatal("late receiver backfilled committed event", got)
	}
	// Restart also preserves cooldown and the pending recipient snapshot.
	recordHealthNotificationAssessment(t, restarted, app.ID, "healthy", now.Add(62*time.Second))
	for seconds := 93; seconds <= 341; seconds += 31 {
		current := state.NewPgStore(pool)
		recordHealthNotificationAssessment(t, current, app.ID, "healthy", now.Add(time.Duration(seconds)*time.Second))
	}
	if got := healthNotifications(t, restarted, app, hook); len(got) != 2 || !got[0].Coalesced {
		t.Fatal("restart lost deferred notification", got)
	}
}
