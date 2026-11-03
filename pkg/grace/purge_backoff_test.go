package grace_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// refusingPurgeStore refuses DeleteAppPermanently with refuse, counting the
// attempts, and purges through the MemStore once refuse is nil.
type refusingPurgeStore struct {
	MemStore *state.MemStore
	refuse   error
	attempts int
}

func (s *refusingPurgeStore) DeleteAppPermanently(ctx context.Context, id string) error {
	s.attempts++
	if s.refuse != nil {
		return s.refuse
	}
	return s.MemStore.DeleteAppPermanently(ctx, id)
}

// A purge refused on a foreign key used to retry every minute, forever, and
// on 2026-10-24 deadlocked a migration adding a foreign key to apps. It now
// backs off per app; a retained-resource wait stays a cheap per-tick retry.
// Either way the reason is exported for alerting and cleared by the purge.
func TestRunAppsOnceBacksOffRefusedPurges(t *testing.T) {
	foreignKey := &state.AppPurgeBlockedError{Reason: state.AppPurgeBlockedForeignKey,
		Constraint: "managed_postgres_bindings_app_id_fkey", Table: "managed_postgres_bindings"}
	tests := []struct {
		name         string
		refuse       error
		reason       string // blocked_apps label; "" for a plain failure
		stage        string
		wantAttempts []int // cumulative attempts after the sweep at each minute
		wantWarnings int
	}{
		{name: "foreign key backs off", refuse: foreignKey, reason: state.AppPurgeBlockedForeignKey, stage: "foreign_key",
			// Retries 2 and then 4 minutes later.
			wantAttempts: []int{1, 1, 2, 2, 2, 2, 3}, wantWarnings: 3},
		{name: "live managed postgres binding waits every tick and warns once",
			refuse: &state.AppPurgeBlockedError{Reason: state.AppPurgeBlockedManagedPostgresBinding, Count: 1},
			reason: state.AppPurgeBlockedManagedPostgresBinding, stage: "retained_resources",
			wantAttempts: []int{1, 2, 3, 4, 5, 6, 7}, wantWarnings: 1},
		{name: "untyped conflict is a quiet retained-resource wait", refuse: state.ErrConflict,
			reason: "retained_resources", stage: "retained_resources",
			wantAttempts: []int{1, 2, 3, 4, 5, 6, 7}},
		{name: "other failure retries every tick", refuse: errors.New("conn reset"), stage: "delete_database",
			wantAttempts: []int{1, 2, 3, 4, 5, 6, 7}, wantWarnings: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mem := state.NewMemStore()
			account, err := mem.CreateAccount(ctx, "app-grace-backoff@example.com", "pro")
			if err != nil {
				t.Fatal(err)
			}
			app, err := mem.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "refused", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := mem.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			store := &refusingPurgeStore{MemStore: mem, refuse: tt.refuse}
			var logs strings.Builder
			registry := prometheus.NewRegistry()
			now := time.Now()
			g := grace.New(grace.Params{
				Store: statetest.NewMem(store), Registry: registry, Interval: time.Minute,
				Now: func() time.Time { return now },
				Log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
			})
			for minute, want := range tt.wantAttempts {
				if err := g.RunAppsOnce(ctx); err != nil {
					t.Fatal(err)
				}
				if store.attempts != want {
					t.Fatalf("minute %d: attempts = %d, want %d", minute, store.attempts, want)
				}
				now = now.Add(time.Minute)
			}
			if got := strings.Count(logs.String(), "level=WARN"); got != tt.wantWarnings {
				t.Fatalf("warnings = %d, want %d:\n%s", got, tt.wantWarnings, logs.String())
			}
			if tt.reason == state.AppPurgeBlockedForeignKey && !strings.Contains(logs.String(), "constraint=managed_postgres_bindings_app_id_fkey") {
				t.Fatalf("foreign-key warning does not name the constraint:\n%s", logs.String())
			}
			if got := labeledCounter(t, registry, "apid_app_grace_failures_total", "stage", tt.stage); got != float64(store.attempts) {
				t.Fatalf("failures{stage=%q} = %v, want %d", tt.stage, got, store.attempts)
			}
			for _, reason := range []string{state.AppPurgeBlockedForeignKey, state.AppPurgeBlockedManagedPostgresBinding, "retained_resources"} {
				want := 0.0
				if reason == tt.reason {
					want = 1
				}
				if got := labeledGauge(t, registry, "apid_app_grace_blocked_apps", "reason", reason); got != want {
					t.Fatalf("blocked_apps{reason=%q} = %v, want %v", reason, got, want)
				}
			}

			// Once nothing refuses, the next due attempt purges the app and
			// clears the reason.
			store.refuse = nil
			now = now.Add(24 * time.Hour)
			if err := g.RunAppsOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := mem.AppByID(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("app remains after the refusal cleared: %v", err)
			}
			if tt.reason != "" {
				if got := labeledGauge(t, registry, "apid_app_grace_blocked_apps", "reason", tt.reason); got != 0 {
					t.Fatalf("blocked_apps{reason=%q} = %v after the purge, want 0", tt.reason, got)
				}
			}
		})
	}
}

func labeledGauge(t *testing.T, registry *prometheus.Registry, name, label, value string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			for _, pair := range metric.GetLabel() {
				if pair.GetName() == label && pair.GetValue() == value {
					return metric.GetGauge().GetValue()
				}
			}
		}
	}
	t.Fatalf("metric %s{%s=%q} not found", name, label, value)
	return 0
}
