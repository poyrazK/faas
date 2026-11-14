package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/receivers"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgIssuePolicySerializesWithDowngrade(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	in := state.RecordIssueParams{
		Credential:  state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Environment: "application"},
		Event:       api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", SourceKind: "exception"},
		Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1,
		Limits: api.PlanPro.IssueLimits(), Now: now, EnforceResourcePolicy: true,
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, `UPDATE accounts SET plan='free' WHERE id=$1`, account.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := s.RecordIssue(ctx, in); result <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%IssuePolicyLockAccount%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("capture did not wait for the owner-policy update")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, state.ErrIssuePolicyChanged) {
		t.Fatalf("capture used pre-downgrade policy: %v", err)
	}
}

func TestPgIssueEnforcesCurrentOwnerPolicy(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	in := state.RecordIssueParams{
		Credential:  state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Environment: "application"},
		Event:       api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", SourceKind: "exception"},
		Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1,
		Limits: api.PlanPro.IssueLimits(), Now: now, EnforceResourcePolicy: true,
	}
	if _, err := s.RecordIssue(ctx, in); err != nil {
		t.Fatalf("active paid owner could not report: %v", err)
	}
	in.Event.EventID = uuid.NewString()
	if err := s.UpdateAccountPlan(ctx, account.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordIssue(ctx, in); !errors.Is(err, state.ErrIssuePolicyChanged) {
		t.Fatalf("stale paid policy admitted after downgrade: %v", err)
	}
	if err := s.UpdateAccountPlan(ctx, account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccountStatus(ctx, account.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordIssue(ctx, in); !errors.Is(err, state.ErrIssuePolicyChanged) {
		t.Fatalf("stale active policy admitted after suspension: %v", err)
	}
}

func TestPgHTTPRetryDoesNotDoubleCountAnAmbiguousLegacyWrite(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	receiver := receivers.NewAppErrorsReceiver(s, nil, true)
	req := &apidpb.IncrementAppErrorRequest{AccountId: account.ID, AppId: app.ID, DeploymentId: deployment.ID, RequestId: uuid.NewString(), IssueEventId: uuid.NewString(), RouteTemplate: "GET /orders", HttpStatus: 500, ErrorClass: "unhandled", Fingerprint: strings.Repeat("a", 64), ReceivedAtUnixMs: time.Now().UnixMilli(), Count: 3, RequestRowOmitted: true}
	if out := receiver.HandleOne(ctx, req); out.GetOutcome() != receivers.OutcomeInserted {
		t.Fatalf("legacy write: %s", out.GetOutcome())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, req.IssueEventId).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial issue was not recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	result, err := receiver.RecordHTTPIssue(ctx, req)
	if err != nil || result.GetOutcome() != "duplicate" {
		t.Fatalf("retry = %+v, %v", result, err)
	}
	var occurrences, legacy int64
	if err = pool.QueryRow(ctx, `SELECT (SELECT sum(occurrence_count) FROM issue_events WHERE app_id=$1), (SELECT sum(count) FROM app_errors WHERE app_id=$1)`, app.ID).Scan(&occurrences, &legacy); err != nil {
		t.Fatal(err)
	}
	if occurrences != 3 || legacy != 3 {
		t.Fatalf("retry changed counts: issues=%d legacy=%d", occurrences, legacy)
	}
}

func TestPgIssueEnforcesCompanyOwnerPolicy(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	account, err := s.CreateAccount(ctx, "issue-company-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := s.CreateOrg(ctx, state.Org{Slug: "issues-" + uuid.NewString()[:8], Name: "Issues", Plan: api.PlanFree, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateOrgPlan(ctx, org.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if err = s.AddOrgMember(ctx, org.ID, account.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, OrgID: org.ID, Slug: "issues-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	deployment := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	in := state.RecordIssueParams{
		Credential:  state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Environment: "application"},
		Event:       api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", SourceKind: "exception"},
		Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1,
		Limits: api.PlanPro.IssueLimits(), Now: now, EnforceResourcePolicy: true,
	}
	if err = s.UpdateAccountStatus(ctx, account.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordIssue(ctx, in); err != nil {
		t.Fatalf("active company incorrectly inherited creator suspension: %v", err)
	}
	in.Event.EventID = uuid.NewString()
	if err = s.UpdateOrgStatus(ctx, org.ID, state.OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordIssue(ctx, in); !errors.Is(err, state.ErrIssuePolicyChanged) {
		t.Fatalf("suspended company admitted stale capture: %v", err)
	}
}
