package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261103131742 — egress overage is off until a subject turns it on,
// and every change is kept so a delivery can tell whether an hour was used
// while it was on.
func exerciseEgressOverage(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "egress-overage-"+time.Now().Format("150405.000000")+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if on, err := store.EgressOverageEnabled(ctx, acct.ID); err != nil || on {
		t.Fatalf("EgressOverageEnabled before any choice = %v, %v; want off", on, err)
	}
	hour := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	on, off := hour.Add(-2*time.Hour), hour.Add(20*time.Minute)
	if err := store.SetEgressOverage(ctx, acct.ID, true, on); err != nil {
		t.Fatalf("SetEgressOverage on: %v", err)
	}
	subjects, err := store.EgressOverageEnabledSubjects(ctx)
	if err != nil || !subjects[acct.ID] {
		t.Fatalf("EgressOverageEnabledSubjects = %v, %v; want %s", subjects, err, acct.ID)
	}
	if err := store.SetEgressOverage(ctx, acct.ID, false, off); err != nil {
		t.Fatalf("SetEgressOverage off: %v", err)
	}
	if on, err := store.EgressOverageEnabled(ctx, acct.ID); err != nil || on {
		t.Fatalf("EgressOverageEnabled after turning off = %v, %v; want off", on, err)
	}
	if subjects, err := store.EgressOverageEnabledSubjects(ctx); err != nil || subjects[acct.ID] {
		t.Fatalf("EgressOverageEnabledSubjects after turning off = %v, %v", subjects, err)
	}
	choices, err := store.EgressOverageChoices(ctx, acct.ID, hour, hour.Add(time.Hour))
	if err != nil {
		t.Fatalf("EgressOverageChoices: %v", err)
	}
	if len(choices) != 2 || !choices[0].ChangedAt.Equal(on) || !choices[0].Enabled || !choices[1].ChangedAt.Equal(off) || choices[1].Enabled {
		t.Fatalf("choices for the hour = %+v, want the earlier on and the off within it", choices)
	}
	if !state.EgressOverageOnDuring(choices, hour, hour.Add(time.Hour)) {
		t.Fatal("hour turned off at :20 was on during its first 20 minutes")
	}
	later, err := store.EgressOverageChoices(ctx, acct.ID, hour.Add(time.Hour), hour.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("EgressOverageChoices later: %v", err)
	}
	if state.EgressOverageOnDuring(later, hour.Add(time.Hour), hour.Add(2*time.Hour)) {
		t.Fatalf("hour after turning off counted as on: %+v", later)
	}
}

func TestMemStoreEgressOverage(t *testing.T) {
	exerciseEgressOverage(t, state.NewMemStore())
}

func TestPgStoreEgressOverage(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseEgressOverage(t, state.NewPgStore(pool))
}

func TestEgressOverageOnDuring(t *testing.T) {
	start := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	at := func(minutes int, enabled bool) state.EgressOverageChoice {
		return state.EgressOverageChoice{ChangedAt: start.Add(time.Duration(minutes) * time.Minute), Enabled: enabled}
	}
	tests := []struct {
		name    string
		choices []state.EgressOverageChoice
		want    bool
	}{
		{"never chosen", nil, false},
		{"on before the hour", []state.EgressOverageChoice{at(-30, true)}, true},
		{"off before the hour", []state.EgressOverageChoice{at(-60, true), at(-30, false)}, false},
		{"turned on within the hour", []state.EgressOverageChoice{at(30, true)}, true},
		{"turned off within the hour", []state.EgressOverageChoice{at(-30, true), at(10, false)}, true},
		{"on and off within the hour", []state.EgressOverageChoice{at(10, true), at(20, false)}, true},
		{"turned on at the hour's start", []state.EgressOverageChoice{at(0, true)}, true},
		{"turned off at the hour's start", []state.EgressOverageChoice{at(-30, true), at(0, false)}, false},
		{"turned on at the hour's end", []state.EgressOverageChoice{at(60, true)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := state.EgressOverageOnDuring(tt.choices, start, end); got != tt.want {
				t.Fatalf("EgressOverageOnDuring = %v, want %v", got, tt.want)
			}
		})
	}
}
