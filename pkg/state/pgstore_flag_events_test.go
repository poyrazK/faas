//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR 20261115163113 §2–§4: runtime conversion events keep only active
// customers of the account and a bounded set of names, take from the
// monthly quota, fold into one row per customer, event and day, and are
// swept with the plan's request telemetry retention.
func TestPg_FlagEventsIngestAndSweep(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, appID, _ := seedLiveDeploy(t, s, ctx)
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct, Slug: "flags-events", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET project_id = $1 WHERE id = $2`, project.ID, appID); err != nil {
		t.Fatalf("attach app: %v", err)
	}
	env, err := s.ProjectEnvironmentBySlug(ctx, acct, project.ID, "production")
	if err != nil {
		t.Fatalf("environment: %v", err)
	}
	pilot, _, err := s.CreatePlatformTenant(ctx, acct, "pilot", "Pilot", 100)
	if err != nil {
		t.Fatalf("CreatePlatformTenant: %v", err)
	}
	scope := state.FeatureFlagScope{AccountID: acct, ProjectID: project.ID, EnvironmentID: env.ID}
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	ev := func(name string, customer string, at time.Time, value int64) state.FlagConversionEvent {
		return state.FlagConversionEvent{Name: name, CustomerID: customer, Value: value, At: at}
	}

	res, err := s.IngestFlagEvents(ctx, state.FlagEventIngest{Scope: scope, Quota: 4, MaxNames: 2, Now: now, Events: []state.FlagConversionEvent{
		ev("order_completed", pilot.ID, now, 1200),
		ev("order_completed", pilot.ID, now.Add(-time.Hour), 800),
		ev("order_completed", "6f1c1d8e-0000-4000-8000-000000000001", now, 0), // not a tenant of the account
		ev("signup", pilot.ID, now, 0),
		ev("third_name", pilot.ID, now, 0), // over the two-name bound
		ev("order_completed", pilot.ID, now.Add(-25*time.Hour), 0),
		ev("signup", pilot.ID, now, 0), // over the quota of four
	}})
	if err != nil {
		t.Fatalf("IngestFlagEvents: %v", err)
	}
	want := state.FlagEventIngestResult{Accepted: 4, DroppedQuota: 1, DroppedUnknownCustomer: 1, DroppedNameLimit: 1, MonthAccepted: 4, MonthDropped: 1}
	if res != want {
		t.Fatalf("result = %+v; want %+v", res, want)
	}
	var count, value int64
	var first, last time.Time
	if err := pool.QueryRow(ctx, `SELECT count, value_sum, first_at, last_at FROM flag_conversion_daily
		WHERE environment_id = $1 AND event_name = 'order_completed' AND day = '2026-11-15'`, env.ID).Scan(&count, &value, &first, &last); err != nil {
		t.Fatalf("daily row: %v", err)
	}
	if count != 2 || value != 2000 || !first.Equal(now.Add(-time.Hour)) || !last.Equal(now) {
		t.Fatalf("folded row = count %d value %d first %v last %v", count, value, first, last)
	}

	res, err = s.IngestFlagEvents(ctx, state.FlagEventIngest{Scope: scope, Quota: 4, MaxNames: 2, Now: now, Events: []state.FlagConversionEvent{ev("signup", pilot.ID, now, 0)}})
	if err != nil || res.Accepted != 0 || res.DroppedQuota != 1 || res.MonthAccepted != 4 || res.MonthDropped != 2 {
		t.Fatalf("over quota = %+v, %v", res, err)
	}
	res, err = s.IngestFlagEvents(ctx, state.FlagEventIngest{Scope: scope, Quota: 4, MaxNames: 2, Now: now.AddDate(0, 1, 0), Events: []state.FlagConversionEvent{ev("signup", pilot.ID, now.AddDate(0, 1, 0), 0)}})
	if err != nil || res.Accepted != 1 || res.MonthAccepted != 1 {
		t.Fatalf("next month = %+v, %v", res, err)
	}

	// Retention 1 day on 2026-11-16 keeps the 15th, drops the 14th.
	deleted, err := s.SweepFlagConversions(ctx, now.Add(24*time.Hour), func(string) int { return 1 }, 10)
	if err != nil || deleted != 1 {
		t.Fatalf("sweep deleted %d, %v; want the 2026-11-14 row", deleted, err)
	}
}
