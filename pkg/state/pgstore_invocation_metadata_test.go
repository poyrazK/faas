//go:build !no_pg

// adr: 375
package state_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// SQLC powers the scheduler and environment queue paths while the public row
// reader uses a scanner. Both must preserve admitted identity and policy facts.
func TestPgInvocationSQLCReadersRetainDurableMetadata(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	_, appID, _ := seedLiveDeploy(t, store, ctx, "invocation-metadata-"+uuid.NewString(), "metadata-"+uuid.NewString()[:8])
	app, err := store.AppByID(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, app.AccountID, "reader", "Reader", 250)
	if err != nil {
		t.Fatal(err)
	}
	rules := &workpolicy.FailureRules{Version: workpolicy.Version, UnmatchedFailure: "retry", UncertainOutcome: "hold"}
	cron, err := store.CreateCronWithOptions(ctx, appID, "* * * * *", "/sync", true, state.CronOptions{
		SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "allow", StartDeadlineSeconds: 120, MissedRuns: "skip"},
		FailureRules:   rules,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	scheduled, occurrence, created, err := store.CreateScheduledCronInvocationOccurrence(ctx, cron.ID, nil, now,
		state.CronScheduledOccurrenceOptions{ScheduledFor: now, ScheduleRevision: cron.ScheduleRevision}, state.Invocation{
			Method: "POST", Path: "/sync",
		})
	if err != nil || !created || scheduled.OccurrenceID != occurrence.ID || scheduled.StartDeadlineAt == nil {
		t.Fatalf("scheduled metadata: %+v, created=%v, %v", scheduled, created, err)
	}
	deadline := now.Add(time.Minute)
	queued, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: app.AccountID,
		PlatformTenantID: tenant.ID, Source: state.InvocationQueue, DueAt: now, Method: "POST", Path: "/queue",
		StartDeadlineAt: &deadline, FailureRules: rules,
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := workpolicy.Decision{Classification: "transport", Action: "retry", Reason: "connection_lost", PolicyVersion: workpolicy.Version}
	raw, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{scheduled.ID, queued.ID} {
		if _, err := pool.Exec(ctx, `update invocations set work_decision=$2::jsonb,outcome_code='connection_lost' where id=$1::uuid`, id, raw); err != nil {
			t.Fatal(err)
		}
	}
	assertMetadata := func(t *testing.T, row state.Invocation) {
		t.Helper()
		canonical, err := store.InvocationByID(ctx, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		if (row.ID == queued.ID && row.PlatformTenantID != tenant.ID) || row.PlatformTenantID != canonical.PlatformTenantID || row.OccurrenceID != canonical.OccurrenceID ||
			!reflect.DeepEqual(row.StartDeadlineAt, canonical.StartDeadlineAt) ||
			!reflect.DeepEqual(row.FailureRules, rules) || !reflect.DeepEqual(row.WorkDecision, &decision) || row.OutcomeCode != "connection_lost" {
			t.Fatalf("SQLC reader lost durable identity/policy: %+v; canonical %+v", row, canonical)
		}
	}
	for _, read := range []struct {
		name string
		fn   func() ([]state.Invocation, error)
	}{
		{"due", func() ([]state.Invocation, error) { return store.ListDueInvocations(ctx, now.Add(time.Second), 10) }},
		{"due_after", func() ([]state.Invocation, error) {
			return store.ListDueInvocationsAfter(ctx, now.Add(time.Second), state.InvocationDueCursor{}, 10)
		}},
	} {
		t.Run(read.name, func(t *testing.T) {
			rows, err := read.fn()
			if err != nil || len(rows) != 2 {
				t.Fatalf("due rows: %d, %v", len(rows), err)
			}
			for _, row := range rows {
				assertMetadata(t, row)
			}
		})
	}
	rows, err := store.QueuePeek(ctx, appID, 10, "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("queue rows: %d, %v", len(rows), err)
	}
	assertMetadata(t, rows[0])
	row, err := store.ProductionQueueInvocationByID(ctx, queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertMetadata(t, row)
}
