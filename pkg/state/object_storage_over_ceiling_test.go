// adr: 20261111123000

package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectStorageOverCeilingClockMem(t *testing.T) {
	objectStorageOverCeilingSuite(t, state.NewMemStore())
}

func TestObjectStorageOverCeilingClockPG(t *testing.T) {
	st, _ := pgStore(t)
	objectStorageOverCeilingSuite(t, st)
}

// The first observation over the ceiling fixes the clock and schedules
// deletion 30 days later; later observations keep the dates and refresh the
// bytes; clearing removes it and a later breach starts a new clock.
func objectStorageOverCeilingSuite(t *testing.T, st contractTestStore) {
	t.Helper()
	ctx := context.Background()
	subject := uuid.NewString()
	if _, err := st.GetObjectStorageOverCeiling(ctx, subject); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("clock before any observation", err)
	}
	start := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	c, err := st.ObserveObjectStorageOverCeiling(ctx, subject, 30<<30, 20<<30, start)
	if err != nil {
		t.Fatal(err)
	}
	want := start.AddDate(0, 0, api.ObjectStorageOverCeilingGraceDays)
	if !c.Started(start) || !c.OverCeilingSince.Equal(start) || !c.DeletionScheduledAt.Equal(want) || c.StoredBytes != 30<<30 || c.CeilingBytes != 20<<30 {
		t.Fatalf("first observation = %+v", c)
	}
	later := start.Add(26 * time.Hour)
	c, err = st.ObserveObjectStorageOverCeiling(ctx, subject, 25<<30, 20<<30, later)
	if err != nil {
		t.Fatal(err)
	}
	if c.Started(later) || !c.OverCeilingSince.Equal(start) || !c.DeletionScheduledAt.Equal(want) || c.StoredBytes != 25<<30 || !c.ObservedAt.Equal(later) {
		t.Fatalf("a later observation moved the clock: %+v", c)
	}
	got, err := st.GetObjectStorageOverCeiling(ctx, subject)
	if err != nil || got != c {
		t.Fatalf("read back %+v, %v; want %+v", got, err, c)
	}
	for _, bad := range []struct{ stored, ceiling int64 }{{10, 10}, {10, 20}, {0, 0}, {10, -1}} {
		if _, err := st.ObserveObjectStorageOverCeiling(ctx, subject, bad.stored, bad.ceiling, later); err == nil {
			t.Fatalf("stored %d within ceiling %d recorded as over", bad.stored, bad.ceiling)
		}
	}
	if cleared, err := st.ClearObjectStorageOverCeiling(ctx, subject); err != nil || !cleared {
		t.Fatal("clear", cleared, err)
	}
	if cleared, err := st.ClearObjectStorageOverCeiling(ctx, subject); err != nil || cleared {
		t.Fatal("second clear", cleared, err)
	}
	again := later.Add(time.Hour)
	if c, err = st.ObserveObjectStorageOverCeiling(ctx, subject, 30<<30, 20<<30, again); err != nil || !c.Started(again) || !c.DeletionScheduledAt.Equal(again.AddDate(0, 0, api.ObjectStorageOverCeilingGraceDays)) {
		t.Fatal("a new breach must start a new clock", c, err)
	}
}
