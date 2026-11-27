package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// ListEventsByKindSince returns one kind's rows at or after since, newest
// first, capped at limit, the same on both stores (ADR-20261121200409).
func TestEventsByKindSinceStores(t *testing.T) {
	for _, backend := range []string{"mem", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			mem := state.NewMemStore()
			var s state.EventsByKindReader = mem
			appendEvent := mem.AppendEventAt
			if backend == "postgres" {
				pg := state.NewPgStore(pgtest.OpenMigrated(t))
				s, appendEvent = pg, pg.AppendEventAt
			}
			at := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
			add := func(kind string, when time.Time) {
				t.Helper()
				if err := appendEvent(context.Background(), "schedd", kind, nil, []byte(`{"n":"`+when.Format(time.RFC3339)+`"}`), when); err != nil {
					t.Fatal(err)
				}
			}
			add("wake.boot_completed", at.Add(-2*time.Hour))
			add("wake.boot_completed", at.Add(-30*time.Minute))
			add("wake.boot_completed", at.Add(-10*time.Minute))
			add("wake.boot_completed", at)
			add("wake.boot_failed", at)
			add("wake.boot_completed_extra", at)

			rows, err := s.ListEventsByKindSince(t.Context(), "wake.boot_completed", at.Add(-time.Hour), 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 3 || !rows[0].At.Equal(at) || !rows[2].At.Equal(at.Add(-30*time.Minute)) {
				t.Fatalf("rows = %+v", rows)
			}
			for _, r := range rows {
				if r.Kind != "wake.boot_completed" || len(r.Data) == 0 {
					t.Fatalf("row = %+v", r)
				}
			}
			if rows, err := s.ListEventsByKindSince(t.Context(), "wake.boot_completed", at.Add(-time.Hour), 2); err != nil || len(rows) != 2 || !rows[1].At.Equal(at.Add(-10*time.Minute)) {
				t.Fatalf("limited rows = %+v err=%v", rows, err)
			}
			for _, bad := range []struct {
				kind  string
				limit int
			}{{"", 1}, {"wake.boot_completed", 0}} {
				if _, err := s.ListEventsByKindSince(t.Context(), bad.kind, at, bad.limit); err == nil {
					t.Fatalf("%+v: want error", bad)
				}
			}
		})
	}
}
