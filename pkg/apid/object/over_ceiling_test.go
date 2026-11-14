package object

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type overCeilingHost struct {
	storageControlHost
	mode string
}

func (h *overCeilingHost) ObjectStorageOverCeilingMode() string { return h.mode }

// TestOverCeilingClock pins ADR-20261111123000 decisions 1, 2 and 9: the
// sweep starts a subject's clock when it first stores more than its ceiling,
// keeps it while it stays over, reports it due after 30 days, and clears it
// once the subject fits; customers see it only from notify on.
func TestOverCeilingClock(t *testing.T) {
	ctx := context.Background()
	st := state.NewMemStore()
	host := &overCeilingHost{storageControlHost: storageControlHost{store: st}, mode: api.ObjectStorageOverCeilingShadow}
	s := &Service{Host: host}
	subject := uuid.NewString()
	start := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	const gib = int64(1 << 30)

	var sweep OverCeilingSweep
	if err := s.observeOverCeiling(ctx, st, subject, 30*gib, 20*gib, host.mode, start, &sweep); err != nil {
		t.Fatal(err)
	}
	if sweep.Over != 1 || sweep.Started != 1 || sweep.Due != 0 {
		t.Fatalf("first sweep = %+v", sweep)
	}
	// Shadow records the clock but customers do not see it.
	if d, err := s.OverCeilingDeletion(ctx, subject); err != nil || d != nil {
		t.Fatal("shadow clock visible to the customer", d, err)
	}
	host.mode = api.ObjectStorageOverCeilingNotify
	d, err := s.OverCeilingDeletion(ctx, subject)
	if err != nil || d == nil || !d.DeletionScheduledAt.Equal(start.AddDate(0, 0, 30)) || d.ExcessBytes != 10*gib {
		t.Fatalf("notify usage = %+v, %v", d, err)
	}

	sweep = OverCeilingSweep{}
	due := start.AddDate(0, 0, api.ObjectStorageOverCeilingGraceDays)
	if err := s.observeOverCeiling(ctx, st, subject, 25*gib, 20*gib, host.mode, due, &sweep); err != nil {
		t.Fatal(err)
	}
	if sweep.Over != 1 || sweep.Started != 0 || sweep.Due != 1 {
		t.Fatalf("sweep at the deletion date = %+v", sweep)
	}

	// Getting back under the ceiling, or losing the ceiling, clears it.
	for _, tc := range []struct{ stored, ceiling int64 }{{20 * gib, 20 * gib}, {30 * gib, 0}} {
		if _, err := st.ObserveObjectStorageOverCeiling(ctx, subject, 30*gib, 20*gib, due); err != nil {
			t.Fatal(err)
		}
		sweep = OverCeilingSweep{}
		if err := s.observeOverCeiling(ctx, st, subject, tc.stored, tc.ceiling, host.mode, due.Add(time.Hour), &sweep); err != nil {
			t.Fatal(err)
		}
		if sweep.Cleared != 1 || sweep.Over != 0 {
			t.Fatalf("stored %d ceiling %d: sweep = %+v", tc.stored, tc.ceiling, sweep)
		}
		if d, err := s.OverCeilingDeletion(ctx, subject); err != nil || d != nil {
			t.Fatal("cleared clock still shown", d, err)
		}
	}
}

func TestOverCeilingSweepOffDoesNothing(t *testing.T) {
	st := state.NewMemStore()
	for _, mode := range []string{api.ObjectStorageOverCeilingOff, "", "delete-everything"} {
		s := &Service{Host: &overCeilingHost{storageControlHost: storageControlHost{store: st}, mode: mode}}
		if got := s.ObjectStorageOverCeilingMode(); got != api.ObjectStorageOverCeilingOff {
			t.Fatalf("mode %q read as %q", mode, got)
		}
		if sweep, err := s.SweepObjectStorageOverCeiling(context.Background(), time.Now()); err != nil || sweep != (OverCeilingSweep{}) {
			t.Fatalf("mode %q swept: %+v %v", mode, sweep, err)
		}
	}
}
