//go:build !no_pg

// adr: 1662
package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgIssueCompoundSuppressionAndRegressionReset(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	next := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	rule := api.IssueCompoundAlertRule{Key: "impact", Enabled: true, MinimumEvents: 1, MinimumCustomers: 1, WindowSeconds: 60, CooldownSeconds: 60}
	zero := int64(0)
	if _, err := s.SetIssueCompoundAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &zero, Rules: []api.IssueCompoundAlertRule{rule}}); err != nil {
		t.Fatal(err)
	}
	params := func(deployment string, at time.Time) state.RecordIssueParams {
		return state.RecordIssueParams{Credential: state.IssueCredential{AccountID: owner.ID, AppID: app.ID, DeploymentID: deployment, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: at, ExceptionType: "SuppressionError", SourceKind: "exception"}, Fingerprint: strings.Repeat("d", 64), Title: "suppression test", PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: at}
	}
	first, err := s.RecordIssue(ctx, params(dep.ID, now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE issue_events SET verified_consumer_id=$1 WHERE app_id=$2", uuid.NewString(), app.ID); err != nil {
		t.Fatal(err)
	}
	snooze := func() {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO issue_application_triage(issue_id,revision,snoozed_until,snoozed_by,snooze_reason) VALUES($1,1,$2,$3,'test snooze') ON CONFLICT(issue_id) DO UPDATE SET snoozed_until=excluded.snoozed_until,snoozed_by=excluded.snoozed_by,snooze_reason=excluded.snooze_reason`, first.IssueID, now.Add(5*time.Minute), owner.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	activityCount := func(want int) {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM issue_activity WHERE issue_id=$1 AND action='compound_threshold_reached'", first.IssueID).Scan(&count); err != nil || count != want {
			t.Fatal(count, want, err)
		}
	}
	snooze()
	preview, err := s.PreviewIssueCompoundAlert(ctx, app.ID, owner.ID, api.PreviewIssueCompoundAlertRequest{IssueID: first.IssueID, Rule: rule}, now)
	if err != nil || !preview.Suppressed || !preview.EventsMatched || !preview.CustomersMatched {
		t.Fatal(preview, err)
	}
	if _, err = s.RecordIssue(ctx, params(dep.ID, now)); err != nil {
		t.Fatal(err)
	}
	activityCount(0)
	if _, err = pool.Exec(ctx, "UPDATE issue_application_triage SET snoozed_until=NULL,snoozed_by=NULL,snooze_reason='' WHERE issue_id=$1", first.IssueID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ActOnIssue(ctx, app.ID, first.IssueID, owner.ID, api.IssueActionRequest{Action: "ignore"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordIssue(ctx, params(dep.ID, now)); err != nil {
		t.Fatal(err)
	}
	activityCount(0)
	if _, err = s.ActOnIssue(ctx, app.ID, first.IssueID, owner.ID, api.IssueActionRequest{Action: "reopen"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ActOnIssue(ctx, app.ID, first.IssueID, owner.ID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, now); err != nil {
		t.Fatal(err)
	}
	snooze()
	regressed, err := s.RecordIssue(ctx, params(next.ID, now.Add(time.Second)))
	if err != nil || !regressed.Regressed {
		t.Fatal(regressed, err)
	}
	activityCount(1)
	preview, err = s.PreviewIssueCompoundAlert(ctx, app.ID, owner.ID, api.PreviewIssueCompoundAlertRequest{IssueID: first.IssueID, Rule: rule}, now.Add(time.Second))
	if err != nil || preview.Suppressed || preview.Reason != "cooldown" {
		t.Fatal("old incident snooze leaked", preview, err)
	}
}
