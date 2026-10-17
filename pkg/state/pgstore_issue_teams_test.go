package state_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/pressly/goose/v3"
)

func TestPg_IssueTeamsRoutingInboxAndGroupLifecycle(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	team, err := s.PutIssueTeam(ctx, app.ID, account.ID, account.ID, "payments", api.PutIssueTeamRequest{Name: "Payments", MemberAccountIDs: []string{account.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	replaced, err := s.PutIssueTeam(ctx, app.ID, account.ID, account.ID, "payments", api.PutIssueTeamRequest{Name: "Payments renamed", MemberAccountIDs: []string{account.ID}}, now.Add(time.Minute))
	if err != nil || replaced.ID != team.ID || !replaced.CreatedAt.Equal(team.CreatedAt) {
		t.Fatalf("stable identity: %+v %v", replaced, err)
	}
	if _, err := s.PutIssueTeam(ctx, app.ID, account.ID, account.ID, "foreign", api.PutIssueTeamRequest{Name: "Foreign", MemberAccountIDs: []string{uuid.NewString()}}, now); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("ineligible member: %v", err)
	}
	if _, err := s.GetIssueTeam(ctx, app.ID, uuid.NewString(), "payments"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross owner read: %v", err)
	}
	_, foreignApp := seedPgAccountAndApp(t, s, ctx)
	if _, err := s.SetIssueOwnershipRules(ctx, foreignApp.ID, foreignApp.AccountID, []api.IssueOwnershipRule{{SourceKind: "exception", AssigneeTeamID: team.ID}}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("cross app route: %v", err)
	}
	if _, err := s.SetIssueOwnershipRules(ctx, app.ID, account.ID, []api.IssueOwnershipRule{{SourceKind: "exception", AssigneeTeamID: team.ID}}); err != nil {
		t.Fatal(err)
	}
	record := func(key string) api.IssueEventResponse {
		t.Helper()
		out, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: key, SourceKind: "exception"}, Fingerprint: strings.Repeat(key, 64), Title: key, PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	a, b := record("a"), record("b")
	for _, sort := range []string{"recent", "impact"} {
		list, err := s.ListIssues(ctx, app.ID, state.IssueListFilter{TeamID: team.ID, Sort: sort}, state.IssueCursor{})
		if err != nil || len(list.Items) != 2 || list.Items[0].AssigneeTeamID != team.ID || list.Items[0].AssigneeAccountID != "" {
			t.Fatalf("team %s inbox: %+v %v", sort, list, err)
		}
	}
	unassigned, err := s.ListIssues(ctx, app.ID, state.IssueListFilter{Unassigned: true}, state.IssueCursor{})
	if err != nil || len(unassigned.Items) != 0 {
		t.Fatal("team issues reported as unassigned", err)
	}
	if _, err := s.ActOnIssue(ctx, app.ID, a.IssueID, account.ID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, now); err != nil {
		t.Fatal(err)
	}
	dep = seedPgDeployment(t, s, ctx, app)
	now = now.Add(time.Second)
	if out := record("a"); !out.Regressed {
		t.Fatal("fixture did not regress")
	}
	detail, err := s.GetIssueDetail(ctx, app.ID, a.IssueID, now.Add(-time.Hour), now.Add(time.Hour), state.IssueDetailCursors{})
	if err != nil || detail.Issue.AssigneeTeamID != team.ID {
		t.Fatal("regression lost team", err)
	}
	if _, err := s.ActOnIssue(ctx, app.ID, b.IssueID, account.ID, api.IssueActionRequest{Action: "merge", TargetIssueID: a.IssueID}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BulkActOnIssues(ctx, app.ID, account.ID, api.BulkIssueActionRequest{IssueIDs: []string{a.IssueID, b.IssueID}, Action: api.IssueActionRequest{Action: "assign", AssigneeAccountID: account.ID}}, now); err != nil {
		t.Fatal(err)
	}
	detached, err := s.ActOnIssue(ctx, app.ID, a.IssueID, account.ID, api.IssueActionRequest{Action: "split", MemberIssueID: b.IssueID}, now)
	if err != nil || detached.AssigneeTeamID != "" || detached.AssigneeAccountID != account.ID {
		t.Fatalf("split preserved stale team: %+v %v", detached, err)
	}
	if _, err := s.BulkActOnIssues(ctx, app.ID, account.ID, api.BulkIssueActionRequest{IssueIDs: []string{a.IssueID, b.IssueID}, Action: api.IssueActionRequest{Action: "assign", AssigneeTeamID: team.ID}}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteIssueTeam(ctx, app.ID, account.ID, "payments"); !errors.Is(err, state.ErrIssueTeamInUse) {
		t.Fatalf("in-use delete: %v", err)
	}
	// Removed members cannot attract new issues; the first route stays authoritative.
	if _, err := pool.Exec(ctx, "DELETE FROM app_issue_team_members WHERE team_id=$1", team.ID); err != nil {
		t.Fatal(err)
	}
	c := record("c")
	detail, err = s.GetIssueDetail(ctx, app.ID, c.IssueID, now.Add(-time.Hour), now.Add(time.Hour), state.IssueDetailCursors{})
	if err != nil || detail.Issue.AssigneeTeamID != "" {
		t.Fatal("empty team attracted new group", err)
	}
	_, err = s.ActOnIssue(ctx, app.ID, c.IssueID, account.ID, api.IssueActionRequest{Action: "assign", AssigneeTeamID: team.ID}, now)
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("empty team manual target: %v", err)
	}
	// Explicit manual unassignment is preserved by recurrence.
	if _, err := s.ActOnIssue(ctx, app.ID, a.IssueID, account.ID, api.IssueActionRequest{Action: "assign"}, now); err != nil {
		t.Fatal(err)
	}
	record("a")
	detail, err = s.GetIssueDetail(ctx, app.ID, a.IssueID, now.Add(-time.Hour), now.Add(time.Hour), state.IssueDetailCursors{})
	if err != nil || detail.Issue.AssigneeTeamID != "" {
		t.Fatal("recurrence rerouted unassigned group", err)
	}
}

func TestPg_IssueTeamsQuotaRollbackAndSavedInbox(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	now := time.Now().UTC()
	in := api.PutIssueTeamRequest{Name: "Team", MemberAccountIDs: []string{account.ID}}
	var team api.IssueTeam
	for i := 0; i < api.IssueTeamsMax-1; i++ {
		var err error
		team, err = s.PutIssueTeam(ctx, app.ID, account.ID, account.ID, fmt.Sprintf("team-%d", i), in, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []string{"last-a", "last-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, err := s.PutIssueTeam(ctx, app.ID, account.ID, account.ID, key, in, now)
			results <- err
		}(key)
	}
	wg.Wait()
	close(results)
	success, quota := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, state.ErrIssueQuota) {
			quota++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || quota != 1 {
		t.Fatal("team quota raced")
	}
	view, err := s.PutIssueSavedView(ctx, app.ID, account.ID, account.ID, "team", api.PutIssueSavedViewRequest{Name: "Inbox", Filters: api.IssueSavedViewFilters{Team: team.ID, State: "open"}}, now)
	if err != nil || view.Filters.Team != team.ID {
		t.Fatal("saved team filter lost", err)
	}
	if _, err := s.PutIssueSavedView(ctx, app.ID, account.ID, account.ID, "bad", api.PutIssueSavedViewRequest{Name: "Bad", Filters: api.IssueSavedViewFilters{Team: uuid.NewString()}}, now); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("foreign team view accepted", err)
	}
	migration, err := migrations.FS.ReadFile("20261014090001001_issue_teams.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := strings.SplitN(string(migration), "-- +goose Down", 2)[1]
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, down)
	_ = tx.Rollback(ctx)
	if err == nil || !strings.Contains(err.Error(), "remove team assignments") {
		t.Fatalf("rollback erased teams: %v", err)
	}
	if err := s.DeleteIssueTeam(ctx, app.ID, account.ID, team.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetIssueTeam(ctx, app.ID, account.ID, team.Key); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("deleted team survived", err)
	}
}

func TestPg_IssueTeamRevokedOrganizationMembersAndSplitRules(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	member, _ := seedPgAccountAndApp(t, s, ctx)
	now := time.Now().UTC().Truncate(time.Microsecond)
	org, err := s.CreateOrg(ctx, state.Org{Slug: "team-org", Name: "Team Org", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddOrgMember(ctx, org.ID, member.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE apps SET org_id=$2 WHERE id=$1", app.ID, org.ID); err != nil {
		t.Fatal(err)
	}
	team, err := s.PutIssueTeam(ctx, app.ID, account.ID, account.ID, "org-team", api.PutIssueTeamRequest{Name: "Org Team", MemberAccountIDs: []string{member.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	dep := seedPgDeployment(t, s, ctx, app)
	if _, err := s.SetIssueOwnershipRules(ctx, app.ID, account.ID, []api.IssueOwnershipRule{{SourceKind: "exception", AssigneeTeamID: team.ID}, {SourceKind: "exception", AssigneeAccountID: account.ID}}); err != nil {
		t.Fatal(err)
	}
	record := func(key, code string) api.IssueEventResponse {
		t.Helper()
		out, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "PaymentError", ErrorCode: code, SourceKind: "exception"}, Fingerprint: strings.Repeat(key, 64), Title: key, PayloadHash: strings.Repeat("e", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	a := record("a", "A")
	record("a", "B")
	rule, err := s.CreateIssueSplitRule(ctx, app.ID, a.IssueID, account.ID, api.IssueSplitMatcher{ErrorCode: "A"}, api.PlanPro.IssueLimits(), now)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := s.GetIssueDetail(ctx, app.ID, rule.TargetIssueID, now.Add(-time.Hour), now.Add(time.Hour), state.IssueDetailCursors{})
	if err != nil || branch.Issue.AssigneeTeamID != team.ID {
		t.Fatal("occurrence split lost team", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE org_memberships SET removed_at=$3 WHERE org_id=$1 AND account_id=$2", org.ID, member.ID, now); err != nil {
		t.Fatal(err)
	}
	b := record("b", "C")
	detail, err := s.GetIssueDetail(ctx, app.ID, b.IssueID, now.Add(-time.Hour), now.Add(time.Hour), state.IssueDetailCursors{})
	if err != nil || detail.Issue.AssigneeTeamID != "" || detail.Issue.AssigneeAccountID != "" {
		t.Fatal("revoked roster routed or fell through to later account", err)
	}
	existing, err := s.GetIssueTeam(ctx, app.ID, account.ID, "org-team")
	if err != nil || len(existing.MemberAccountIDs) != 1 {
		t.Fatal("revocation erased roster audit", err)
	}
	if _, err := s.PutIssueTeam(ctx, app.ID, account.ID, account.ID, "org-team", api.PutIssueTeamRequest{Name: "Org Team", MemberAccountIDs: []string{member.ID}}, now); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("revoked member accepted in new roster", err)
	}
}

func TestPg_IssueTeamsRollbackGuardsSavedFiltersAndSupportsEmptyRoundTrip(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	now := time.Now().UTC()
	team, err := s.PutIssueTeam(ctx, app.ID, account.ID, account.ID, "payments", api.PutIssueTeamRequest{Name: "Payments", MemberAccountIDs: []string{account.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutIssueSavedView(ctx, app.ID, account.ID, account.ID, "inbox", api.PutIssueSavedViewRequest{Name: "Inbox", Filters: api.IssueSavedViewFilters{Team: team.ID}}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteIssueTeam(ctx, app.ID, account.ID, "payments"); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261014090001001_issue_teams.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, parts[1])
	_ = tx.Rollback(ctx)
	if err == nil || !strings.Contains(err.Error(), "saved team filters") {
		t.Fatalf("rollback widened a saved team inbox: %v", err)
	}
	if err := s.DeleteIssueSavedView(ctx, app.ID, account.ID, "inbox"); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, parts[1])
	_ = tx.Rollback(ctx)
	var dependency *pgconn.PgError
	if !errors.As(err, &dependency) || dependency.Code != "2BP01" {
		t.Fatalf("team rollback must retain later response views and triggers: %v", err)
	}

	// A migration's empty round trip belongs at its own schema boundary.
	// Later response views and inbound triggers depend on the team column and
	// must be rolled back first; the migration deliberately does not cascade.
	roundTripPool := pgtest.OpenDatabase(t)
	sqlDB := stdlib.OpenDBFromPool(roundTripPool)
	defer func() { _ = sqlDB.Close() }()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 20261014090001001); err != nil {
		t.Fatal(err)
	}
	tx, err = roundTripPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, parts[1]); err != nil {
		t.Fatalf("empty down failed: %v", err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatalf("empty up roundtrip failed: %v", err)
	}
}
