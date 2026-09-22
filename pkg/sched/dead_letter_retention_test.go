// spec: §17 — bounded Failed Events projection retention.
package sched

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeadLetterRetentionPurgesOldProjectionOnly(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, app, _ := seedApp(t, store, api.PlanPro, 256, 5)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

	old, err := store.EnqueueInvocation(ctx, state.Invocation{
		AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue,
		State: state.InvocationDeadLetter, CreatedAt: now.Add(-48 * time.Hour), DueAt: now,
	})
	if err != nil {
		t.Fatalf("old invocation: %v", err)
	}
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{
		AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue,
		State: state.InvocationDeadLetter, CreatedAt: now, DueAt: now,
	}); err != nil {
		t.Fatalf("recent invocation: %v", err)
	}

	oldEvents, err := store.ListDeadLetterEvents(ctx, app.ID, 20, "")
	if err != nil || len(oldEvents) != 2 {
		t.Fatalf("initial events = %d, %v; want 2", len(oldEvents), err)
	}
	var oldEvent state.DeadLetterEvent
	for _, event := range oldEvents {
		if event.SourceID == old.ID {
			oldEvent = event
		}
	}
	if oldEvent.ID == "" {
		t.Fatal("old event not found")
	}
	// Replayed projections are retained in the same ledger until this sweep;
	// their replay timestamp must not make the old failure disappear early.
	if _, err := store.ReplayDeadLetterEvent(ctx, account.ID, app.ID, oldEvent.ID); err != nil {
		t.Fatalf("replay old event: %v", err)
	}

	retention := NewDeadLetterRetention(store, slog.Default()).
		WithRetention(24 * time.Hour).
		WithClock(func() time.Time { return now })
	deleted, err := retention.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	remaining, err := store.ListDeadLetterEvents(ctx, app.ID, 20, "")
	if err != nil {
		t.Fatalf("remaining events: %v", err)
	}
	if len(remaining) != 1 || remaining[0].SourceID == old.ID {
		t.Fatalf("remaining events = %+v, want only recent event", remaining)
	}
	// The source row remains authoritative and its replay transition is still
	// observable even though its dashboard projection was pruned.
	if replayed, err := store.InvocationByID(ctx, old.ID); err != nil || replayed.State != state.InvocationPending {
		t.Fatalf("source after purge = %+v, %v; want pending source row", replayed, err)
	}
	if _, err := store.DeadLetterEventByID(ctx, app.ID, oldEvent.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("purged projection lookup = %v, want ErrNotFound", err)
	}
}
