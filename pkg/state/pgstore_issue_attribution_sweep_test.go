package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgIssueAttributionSweepPrioritizesCurrentAndSpacesRetries(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	record := func() string {
		t.Helper()
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", RequestID: uuid.NewString()}
		_, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Environment: "application"}, Event: event, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return event.EventID
	}
	old, current := record(), record()
	if _, err := pool.Exec(ctx, `UPDATE issue_events SET verified_consumer_id=$1,verified_platform_tenant_id=$2,received_at=$3,attribution_checked_at=$3 WHERE app_id=$4 AND event_id=$5`, uuid.NewString(), uuid.NewString(), now.Add(-24*time.Hour), app.ID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE issue_events SET received_at=$1,attribution_checked_at=$1 WHERE app_id=$2 AND event_id=$3`, now.Add(-2*time.Minute), app.ID, current); err != nil {
		t.Fatal(err)
	}
	rows, err := sqlc.New().IssueUnattributedEvents(ctx, pool, sqlc.IssueUnattributedEventsParams{Before: state.NewPgtypeTime(now.Add(-time.Minute)), VerifiedBefore: state.NewPgtypeTime(now.Add(-30 * time.Minute)), WindowStart: state.NewPgtypeTime(now.Add(-time.Hour)), FullOldest: state.NewPgtypeTime(now.Add(-91 * time.Minute)), BatchLimit: 1})
	if err != nil || len(rows) != 1 || uuid.UUID(rows[0].EventID.Bytes).String() != current {
		t.Fatalf("old complete occurrence delayed current event: %+v %v", rows, err)
	}
	checked := func() time.Time {
		t.Helper()
		var at time.Time
		if err := pool.QueryRow(ctx, `SELECT attribution_checked_at FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, current).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	if err := s.MaintainIssues(ctx, now); err != nil {
		t.Fatal(err)
	}
	firstCheck := checked()
	if !firstCheck.Equal(now) {
		t.Fatalf("first check = %v, want %v", firstCheck, now)
	}
	if err := s.MaintainIssues(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := checked(); !got.Equal(firstCheck) {
		t.Fatalf("partial identity was retried too soon: %v", got)
	}
	if err := s.MaintainIssues(ctx, now.Add(31*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := checked(); !got.Equal(now.Add(31 * time.Minute)) {
		t.Fatalf("partial identity did not retry: %v", got)
	}
	// A formerly partial old event that just became complete still gets its
	// final recheck, unlike an untouched historical complete row.
	if _, err := pool.Exec(ctx, `UPDATE issue_events SET attribution_checked_at=$1 WHERE app_id=$2 AND event_id=$3`, now, app.ID, old); err != nil {
		t.Fatal(err)
	}
	rows, err = sqlc.New().IssueUnattributedEvents(ctx, pool, sqlc.IssueUnattributedEventsParams{Before: state.NewPgtypeTime(now.Add(30 * time.Minute)), VerifiedBefore: state.NewPgtypeTime(now.Add(time.Minute)), WindowStart: state.NewPgtypeTime(now.Add(-29 * time.Minute)), FullOldest: state.NewPgtypeTime(now.Add(-60 * time.Minute)), BatchLimit: 10})
	if err != nil || len(rows) != 1 || uuid.UUID(rows[0].EventID.Bytes).String() != old {
		t.Fatalf("recently checked old complete event lost its final recheck: %+v %v", rows, err)
	}

}
