package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemStoreTriggerConsumerHealthPreservesLastError(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	first := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Second)
	lagMessages := int64(4)
	lagAge := 1.25

	if err := store.RecordTriggerConsumerHealth(ctx, "trigger-1", TriggerConsumerHealthObservation{
		LastPollAt: first,
		Success:    false,
		Error:      "broker unavailable",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTriggerConsumerHealth(ctx, "trigger-1", TriggerConsumerHealthObservation{
		LastPollAt:    second,
		Success:       true,
		LagMessages:   &lagMessages,
		LagAgeSeconds: &lagAge,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := store.TriggerConsumerHealth(ctx, "trigger-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastPollAt == nil || !got.LastPollAt.Equal(second) {
		t.Fatalf("last poll = %v, want %v", got.LastPollAt, second)
	}
	if got.LastSuccessAt == nil || !got.LastSuccessAt.Equal(second) {
		t.Fatalf("last success = %v, want %v", got.LastSuccessAt, second)
	}
	if got.LastErrorAt == nil || !got.LastErrorAt.Equal(first) || got.LastError != "broker unavailable" {
		t.Fatalf("last error = %v/%q, want %v/%q", got.LastErrorAt, got.LastError, first, "broker unavailable")
	}
	if got.LagMessages == nil || *got.LagMessages != lagMessages || got.LagAgeSeconds == nil || *got.LagAgeSeconds != lagAge {
		t.Fatalf("lag = %v/%v, want %d/%v", got.LagMessages, got.LagAgeSeconds, lagMessages, lagAge)
	}

	if _, err := store.TriggerConsumerHealth(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing health error = %v, want ErrNotFound", err)
	}
}
