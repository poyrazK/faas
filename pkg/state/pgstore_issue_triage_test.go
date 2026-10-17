package state_test

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_IssueReleaseComparisonAndAtomicTriage(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	baseline := seedPgDeployment(t, s, ctx, app)
	candidate := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i, d := range []state.Deployment{baseline, candidate} {
		if _, err := pool.Exec(ctx, "UPDATE deployments SET created_at=$2 WHERE id=$1", d.ID, now.Add(time.Duration(i-2)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	record := func(dep state.Deployment, key string, at time.Time) api.IssueEventResponse {
		t.Helper()
		e := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: at, ExceptionType: key, Message: key, SourceKind: "exception"}
		out, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: e, Fingerprint: strings.Repeat(key, 64), Title: key, PayloadHash: strings.Repeat("c", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: at})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	alpha := record(baseline, "a", now.Add(-20*time.Minute))
	if _, err := s.ActOnIssue(ctx, app.ID, alpha.IssueID, account.ID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: baseline.ID}, now.Add(-19*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if recur := record(candidate, "a", now.Add(-10*time.Minute)); !recur.Regressed {
		t.Fatal("fixture did not regress")
	}
	beta := record(candidate, "b", now.Add(-9*time.Minute))
	otherEnv := record(candidate, "d", now.Add(-8*time.Minute))
	if _, err := pool.Exec(ctx, "UPDATE app_issues SET environment='staging' WHERE id=$1", otherEnv.IssueID); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		dep            string
		status, weight int
		at             time.Time
	}{{baseline.ID, 200, 1000, now.Add(-time.Minute)}, {baseline.ID, 503, 100, now.Add(-time.Minute)}, {candidate.ID, 200, 1, now.Add(-time.Minute)}, {candidate.ID, 500, 3, now.Add(-time.Minute)}, {candidate.ID, 500, 999, now.Add(-25 * time.Hour)}, {candidate.ID, 500, 999, now.Add(time.Hour)}} {
		if _, err := pool.Exec(ctx, "INSERT INTO request_telemetry(account_id,app_id,deployment_id,route,method,status,latency_ms,count,received_at) VALUES($1,$2,$3,'/','GET',$4,1,$5,$6)", account.ID, app.ID, row.dep, row.status, row.weight, row.at); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.CompareIssueReleases(ctx, account.ID, app.ID, baseline.ID, candidate.ID, "application", now)
	if err != nil {
		t.Fatal(err)
	}
	if out.Baseline.ObservedRequests != 1100 || out.Candidate.ObservedRequests != 4 || out.Candidate.HTTP5xxRequests != 3 || out.Candidate.HTTP5xxRate == nil || *out.Candidate.HTTP5xxRate != .75 || out.HTTP5xxRateDelta == nil || math.Abs(*out.HTTP5xxRateDelta-(.75-100.0/1100)) > 1e-12 {
		t.Fatalf("request weights/window: %+v", out)
	}
	if out.Candidate.ObservedIssueEvents != 2 || out.Candidate.ObservedIssueGroups != 2 || out.Candidate.NewIssueGroups != 1 || out.Candidate.RegressedIssueGroups != 1 || out.Candidate.UnattributedEvents != 2 {
		t.Fatalf("issue signals/environment: %+v", out.Candidate)
	}
	missingTraffic := seedPgDeployment(t, s, ctx, app)
	unknown, err := s.CompareIssueReleases(ctx, account.ID, app.ID, baseline.ID, missingTraffic.ID, "application", now)
	if err != nil || unknown.Candidate.HTTP5xxRate != nil || unknown.HTTP5xxRateDelta != nil || unknown.Candidate.RequestCoverage != "unknown" {
		t.Fatalf("invented zero health: %+v %v", unknown, err)
	}
	if _, err = s.CompareIssueReleases(ctx, uuid.NewString(), app.ID, baseline.ID, candidate.ID, "application", now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign owner: %v", err)
	}
	if _, err = s.CompareIssueReleases(ctx, account.ID, app.ID, baseline.ID, strings.ToUpper(baseline.ID), "application", now); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("same UUID: %v", err)
	}
	batch := api.BulkIssueActionRequest{IssueIDs: []string{alpha.IssueID, beta.IssueID}, Action: api.IssueActionRequest{Action: "assign", AssigneeAccountID: account.ID}}
	if updated, err := s.BulkActOnIssues(ctx, app.ID, account.ID, batch, now); err != nil || len(updated.Items) != 2 {
		t.Fatalf("bulk assign: %+v %v", updated, err)
	}
	batch.Action = api.IssueActionRequest{Action: "resolve", FixedDeploymentID: candidate.ID}
	batch.IssueIDs[1] = uuid.NewString()
	if _, err = s.BulkActOnIssues(ctx, app.ID, account.ID, batch, now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing issue accepted: %v", err)
	}
	detail, err := s.GetIssueDetail(ctx, app.ID, alpha.IssueID, now.Add(-time.Hour), now, state.IssueDetailCursors{})
	if err != nil || detail.Issue.State != "open" || detail.Issue.AssigneeAccountID != account.ID {
		t.Fatalf("partial batch committed: %+v %v", detail.Issue, err)
	}
	if _, err = s.ActOnIssue(ctx, app.ID, beta.IssueID, account.ID, api.IssueActionRequest{Action: "merge", TargetIssueID: alpha.IssueID}, now); err != nil {
		t.Fatal(err)
	}
	batch.IssueIDs = []string{alpha.IssueID, beta.IssueID}
	batch.Action = api.IssueActionRequest{Action: "assign"}
	if updated, err := s.BulkActOnIssues(ctx, app.ID, account.ID, batch, now); err != nil || len(updated.Items) != 1 || updated.Items[0].ID != alpha.IssueID || updated.Items[0].AssigneeAccountID != "" {
		t.Fatalf("aliases acted on twice: %+v %v", updated, err)
	}
	out, err = s.CompareIssueReleases(ctx, account.ID, app.ID, baseline.ID, candidate.ID, "application", now.Add(time.Second))
	if err != nil || out.Candidate.ObservedIssueGroups != 1 || out.Candidate.ObservedIssueEvents != 2 {
		t.Fatalf("merged grouping count: %+v %v", out.Candidate, err)
	}
}
