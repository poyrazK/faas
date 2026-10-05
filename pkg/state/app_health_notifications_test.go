package state_test

// adr: 595

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func observedHealthNotificationHook(t *testing.T, s state.Store, account state.Account, app state.App, name string, filter []string, enabled bool) state.AppWebhook {
	t.Helper()
	hook, err := s.CreateAppWebhook(t.Context(), state.AppWebhook{AccountID: account.ID, AppID: app.ID, TargetURL: "https://" + name + ".example.test/health", SecretSealed: []byte("sealed-test-secret"), EventFilter: filter, Enabled: enabled})
	if err != nil {
		t.Fatal(err)
	}
	return hook
}

func healthNotifications(t *testing.T, s state.Store, app state.App, hook state.AppWebhook) []api.AppHealthChangedWebhookPayload {
	t.Helper()
	outbox := s.(state.AppWebhookEventOutboxStore)
	if _, err := outbox.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil {
		t.Fatal(err)
	}
	deliveries, _, err := s.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	var out []api.AppHealthChangedWebhookPayload
	for _, d := range deliveries {
		if d.Event != state.AppWebhookEventAppHealthChanged {
			continue
		}
		var payload api.AppHealthChangedWebhookPayload
		if err := json.Unmarshal(d.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, payload)
	}
	// Relay can create several deliveries in one batch. Their ledger IDs do
	// not express the ordering of the original health observations.
	slices.SortFunc(out, func(a, b api.AppHealthChangedWebhookPayload) int {
		left, _ := time.Parse(time.RFC3339Nano, a.QueuedAt)
		right, _ := time.Parse(time.RFC3339Nano, b.QueuedAt)
		return right.Compare(left)
	})
	return out
}

// ADR-595: only explicit, eligible subscriptions receive coalesced status changes.
func TestAppHealthNotificationsOptInAndCoalescing(t *testing.T) {
	healthNotificationStores(t, func(t *testing.T, store state.Store, s state.AppHealthHistoryStore) {
		account, app := healthNotificationFixture(t, store)
		event := []string{string(state.AppWebhookEventAppHealthChanged)}
		hook := observedHealthNotificationHook(t, store, account, app, "explicit", event, true)
		wildcard := observedHealthNotificationHook(t, store, account, app, "wildcard", nil, true)
		disabled := observedHealthNotificationHook(t, store, account, app, "disabled", event, false)
		other, err := store.CreateAccount(t.Context(), uuid.NewString()+"@foreign-health.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		foreign := observedHealthNotificationHook(t, store, other, app, "foreign", event, true)
		now := time.Now().UTC().Truncate(time.Second)
		recordHealthNotificationAssessment(t, s, app.ID, "healthy", now)
		if got := healthNotifications(t, store, app, hook); len(got) != 0 {
			t.Fatal("initial observation emitted", got)
		}
		recordHealthNotificationAssessment(t, s, app.ID, "unhealthy", now.Add(31*time.Second))
		got := healthNotifications(t, store, app, hook)
		if len(got) != 1 || got[0].PreviousStatus != "healthy" || got[0].Status != "unhealthy" || got[0].Change != "worsened" || got[0].Coalesced {
			t.Fatalf("first transition %+v", got)
		}
		page, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 20, "", now.Add(32*time.Second))
		if err != nil || got[0].TransitionID != page.Entries[0].ID || got[0].HistoryPath != "/v1/apps/"+app.Slug+"/health/history" {
			t.Fatal("notification lost saved provenance", got, err)
		}
		recordHealthNotificationAssessment(t, s, app.ID, "degraded", now.Add(62*time.Second))
		late := observedHealthNotificationHook(t, store, account, app, "late", event, true)
		// Moving observations can flush the pending change after cooldown even
		// though no new status transition/history entry was created at flush.
		for seconds := 93; seconds <= 341; seconds += 31 {
			recordHealthNotificationAssessment(t, s, app.ID, "degraded", now.Add(time.Duration(seconds)*time.Second))
		}
		got = healthNotifications(t, store, app, hook)
		if len(got) != 2 || got[0].PreviousStatus != "unhealthy" || got[0].Status != "degraded" || got[0].Change != "improved" || !got[0].Coalesced || got[0].EvaluatedAt == got[0].TransitionObservedAt {
			t.Fatalf("deferred latest status %+v", got)
		}
		for _, quiet := range []state.AppWebhook{wildcard, disabled, late, foreign} {
			if got := healthNotifications(t, store, app, quiet); len(got) != 0 {
				t.Fatalf("unexpected recipient %s %+v", quiet.TargetURL, got)
			}
		}
		recordHealthNotificationAssessment(t, s, app.ID, "healthy", now.Add(372*time.Second))
		recordHealthNotificationAssessment(t, s, app.ID, "degraded", now.Add(403*time.Second))
		for seconds := 434; seconds <= 682; seconds += 31 {
			recordHealthNotificationAssessment(t, s, app.ID, "degraded", now.Add(time.Duration(seconds)*time.Second))
		}
		if got := healthNotifications(t, store, app, hook); len(got) != 2 {
			t.Fatal("net-zero flap emitted", got)
		}
	})
}

// ADR-595: a subscription revision fences deferred notifications.
func TestAppHealthNotificationsDeferredOptOut(t *testing.T) {
	healthNotificationStores(t, func(t *testing.T, store state.Store, s state.AppHealthHistoryStore) {
		account, app := healthNotificationFixture(t, store)
		event := []string{string(state.AppWebhookEventAppHealthChanged)}
		hook := observedHealthNotificationHook(t, store, account, app, "opt-out", event, true)
		now := time.Now().UTC().Truncate(time.Second)
		recordHealthNotificationAssessment(t, s, app.ID, "healthy", now)
		recordHealthNotificationAssessment(t, s, app.ID, "unhealthy", now.Add(31*time.Second))
		recordHealthNotificationAssessment(t, s, app.ID, "healthy", now.Add(62*time.Second))
		empty := []string{}
		if _, err := store.UpdateAppWebhook(t.Context(), hook.ID, state.UpdateAppWebhookParams{EventFilter: &empty}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateAppWebhook(t.Context(), hook.ID, state.UpdateAppWebhookParams{EventFilter: &event}); err != nil {
			t.Fatal(err)
		}
		// Even opting out and back in between collections changes the captured
		// subscription revision; that old pending transition must stay quiet.
		recordHealthNotificationAssessment(t, s, app.ID, "healthy", now.Add(93*time.Second))
		for seconds := 124; seconds <= 341; seconds += 31 {
			recordHealthNotificationAssessment(t, s, app.ID, "healthy", now.Add(time.Duration(seconds)*time.Second))
		}
		if got := healthNotifications(t, store, app, hook); len(got) != 1 {
			t.Fatal("opt-out/rejoin received old pending transition", got)
		}
	})
}

// ADR-595: missing evidence cannot imply an outage or recovery.
func TestAppHealthNotificationsGapsUnknownAndLateOptIn(t *testing.T) {
	healthNotificationStores(t, func(t *testing.T, store state.Store, s state.AppHealthHistoryStore) {
		account, app := healthNotificationFixture(t, store)
		now := time.Now().UTC().Truncate(time.Second)
		recordHealthNotificationAssessment(t, s, app.ID, "healthy", now)
		recordHealthNotificationAssessment(t, s, app.ID, "unhealthy", now.Add(31*time.Second))
		hook := observedHealthNotificationHook(t, store, account, app, "opt-in-late", []string{string(state.AppWebhookEventAppHealthChanged)}, true)
		recordHealthNotificationAssessment(t, s, app.ID, "unhealthy", now.Add(62*time.Second))
		if got := healthNotifications(t, store, app, hook); len(got) != 0 {
			t.Fatal("late opt-in backfilled", got)
		}
		recordHealthNotificationAssessment(t, s, app.ID, "unknown", now.Add(93*time.Second))
		got := healthNotifications(t, store, app, hook)
		if len(got) != 1 || got[0].Change != "unconfirmed" {
			t.Fatal("missing evidence became confirmed outage/recovery", got)
		}
		// A pending known result cannot survive an expired-evidence gap.
		recordHealthNotificationAssessment(t, s, app.ID, "healthy", now.Add(124*time.Second))
		recordHealthNotificationAssessment(t, s, app.ID, "healthy", now.Add(500*time.Second))
		if got := healthNotifications(t, store, app, hook); len(got) != 1 {
			t.Fatal("gap falsely resumed/recovered", got)
		}
		recordHealthNotificationAssessment(t, s, app.ID, "unknown", now.Add(531*time.Second))
		// Unknown-to-known is confirmation, not an inferred recovery.
		recordHealthNotificationAssessment(t, s, app.ID, "healthy", now.Add(562*time.Second))
		for seconds := 593; seconds <= 841; seconds += 31 {
			recordHealthNotificationAssessment(t, s, app.ID, "healthy", now.Add(time.Duration(seconds)*time.Second))
		}
		got = healthNotifications(t, store, app, hook)
		if len(got) != 3 || got[0].Change != "confirmed" || got[0].PreviousStatus != "unknown" {
			t.Fatal("known result mislabeled recovery", got)
		}
	})
}

// ADR-595: the maximum cohort fits durable JSON state; overflow rolls back.
func TestAppHealthNotificationsRecipientBoundIsAtomic(t *testing.T) {
	healthNotificationStores(t, func(t *testing.T, store state.Store, s state.AppHealthHistoryStore) {
		account, app := healthNotificationFixture(t, store)
		event := []string{string(state.AppWebhookEventAppHealthChanged)}
		var hook state.AppWebhook
		for i := range api.AppHealthNotificationRecipients {
			hook = observedHealthNotificationHook(t, store, account, app, fmt.Sprintf("bounded-%d", i), event, true)
		}
		now := time.Now().UTC().Truncate(time.Second)
		recordHealthNotificationAssessment(t, s, app.ID, "healthy", now)
		recordHealthNotificationAssessment(t, s, app.ID, "unhealthy", now.Add(31*time.Second))
		recordHealthNotificationAssessment(t, s, app.ID, "healthy", now.Add(62*time.Second))
		if got := healthNotifications(t, store, app, hook); len(got) != 1 {
			t.Fatal("maximum recipient cohort did not commit", got)
		}
		observedHealthNotificationHook(t, store, account, app, "overflow", event, true)
		at := now.Add(93 * time.Second)
		claim, err := s.ClaimAppHealth(t.Context(), uuid.NewString(), at)
		if err != nil {
			t.Fatal(err)
		}
		assessment := healthNotificationAssessment(app.ID, "degraded", at.Add(time.Millisecond))
		if err := s.FinishAppHealth(t.Context(), claim, assessment, at.Add(time.Millisecond)); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("recipient overflow accepted", err)
		}
		page, err := s.ListAppHealthHistory(t.Context(), account.ID, app.ID, 20, "", at)
		if err != nil || len(page.Entries) != 3 || page.Latest.Status != "healthy" {
			t.Fatal("recipient overflow partially committed health", page, err)
		}
	})
}
