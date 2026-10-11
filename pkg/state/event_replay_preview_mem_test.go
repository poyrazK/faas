package state

// adr: 624

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemEventReplayPreviewCursorBindingRevisionAndLegacy(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "preview-cursor@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: account.ID, Slug: "preview-cursor"})
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := m.UpsertEventSubscription(ctx, account.ID, app.ID, "*", "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	accepted := time.Now().UTC().Add(-time.Hour)
	for _, id := range []string{"one", "two", "three"} {
		if err := m.AppendEvent(ctx, "apid", "event.published", &account.ID, []byte(`{"id":"`+id+`","source":"orders","type":"created","data":{}}`)); err != nil {
			t.Fatal(err)
		}
	}
	m.mu.Lock()
	for _, work := range m.eventFanout {
		work.CreatedAt = accepted // tied acceptance timestamps must page by ID
		work.SnapshotCaptured = false
		work.RecipientSnapshot = nil
	}
	m.mu.Unlock()
	q := EventReplayPreviewQuery{AppID: app.ID, SubscriptionID: sub.ID, EventReplayPreviewOptions: api.EventReplayPreviewOptions{From: accepted, Until: accepted.Add(2 * time.Hour), Limit: 1}}
	first, err := m.PreviewEventReplay(ctx, account.ID, q)
	if err != nil || first.MatchedCount != 1 || first.NextAfter == "" || first.Matches[0].OriginalRecipient != "unknown" {
		t.Fatalf("legacy=%+v %v", first, err)
	}
	q.After = first.NextAfter
	second, err := m.PreviewEventReplay(ctx, account.ID, q)
	if err != nil || second.MatchedCount != 1 || second.Matches[0].EventID == first.Matches[0].EventID || second.NextAfter == "" {
		t.Fatalf("tied cursor=%+v %v", second, err)
	}
	for _, mutate := range []func(*EventReplayPreviewQuery){func(q *EventReplayPreviewQuery) { q.From = q.From.Add(-time.Minute) }, func(q *EventReplayPreviewQuery) { q.Until = q.Until.Add(time.Minute) }, func(q *EventReplayPreviewQuery) { q.After = "erp1.invalid" }, func(q *EventReplayPreviewQuery) { q.Limit = -1 }} {
		changed := q
		mutate(&changed)
		if _, err := m.PreviewEventReplay(ctx, account.ID, changed); !errors.Is(err, ErrEventReplayPreviewQuery) {
			t.Fatalf("cursor mutation=%v", err)
		}
	}
	setSubscription := func(update func(*EventSubscription)) {
		m.mu.Lock()
		defer m.mu.Unlock()
		for key, s := range m.eventSubscriptions {
			if s.ID == sub.ID {
				update(&s)
				m.eventSubscriptions[key] = s
			}
		}
	}
	setSubscription(func(s *EventSubscription) { s.Filter = json.RawMessage(`{"data":{"amount":1}}`) })
	if _, err := m.PreviewEventReplay(ctx, account.ID, q); !errors.Is(err, ErrEventReplayPreviewChanged) {
		t.Fatalf("changed filter=%v", err)
	}
	setSubscription(func(s *EventSubscription) { s.Enabled = false })
	if _, err := m.PreviewEventReplay(ctx, account.ID, q); !errors.Is(err, ErrEventReplayPreviewDisabled) {
		t.Fatalf("disabled=%v", err)
	}
	setSubscription(func(s *EventSubscription) {
		s.Enabled = true
		s.Filter = sub.Filter
		s.UpdatedAt = s.UpdatedAt.Add(time.Second)
	})
	if _, err := m.PreviewEventReplay(ctx, account.ID, q); !errors.Is(err, ErrEventReplayPreviewChanged) {
		t.Fatalf("re-enabled revision=%v", err)
	}
	m.mu.Lock()
	m.eventWorkBindings[sub.ID] = EventWorkBinding{SubscriptionID: sub.ID, AppID: app.ID, PolicyName: "keyed"}
	m.mu.Unlock()
	if _, err := m.PreviewEventReplay(ctx, account.ID, q); !errors.Is(err, ErrEventReplayPreviewUnsupported) {
		t.Fatalf("work-bound=%v", err)
	}
}
