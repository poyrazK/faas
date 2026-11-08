package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgIssueCreationUsesCurrentOwnerLimits(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	issue, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", SourceKind: "exception"}, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO issue_ingest_tokens(account_id,app_id,deployment_id,environment,name,token_hash,expires_at)
		SELECT $1,$2,$3,'application','token-' || n,decode(md5(n::text)||md5(n::text),'hex'),now()+interval '1 hour'
		FROM generate_series(1,20) n`, account.ID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO app_issues(account_id,app_id,environment,fingerprint,grouping_version,title,first_seen_at,last_seen_at)
		SELECT $1,$2,'application',lpad(to_hex(n),64,'0'),1,'other',now(),now() FROM generate_series(1,199) n`, account.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAccountPlan(ctx, account.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateIssueToken(ctx, account.ID, app.ID, api.CreateIssueIngestTokenRequest{DeploymentID: dep.ID, Environment: "application", Name: "stale", ExpiresAt: now.Add(time.Hour)}, []byte(strings.Repeat("x", 32)), api.PlanPro.IssueLimits())
	var limit *state.IssueLimitError
	if !errors.As(err, &limit) || limit.Resource != "ingest_tokens" || limit.Limit != int64(api.PlanHobby.IssueLimits().TokensPerApp) {
		t.Fatalf("stale token quota: %v", err)
	}
	_, err = s.CreateIssueSplitRule(ctx, app.ID, issue.IssueID, account.ID, api.IssueSplitMatcher{ExceptionType: "Error"}, api.PlanPro.IssueLimits(), now)
	if !errors.As(err, &limit) || limit.Resource != "issues" || limit.Limit != int64(api.PlanHobby.IssueLimits().IssuesPerApp) {
		t.Fatalf("stale split quota: %v", err)
	}
	if err = s.UpdateAccountPlan(ctx, account.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateIssueToken(ctx, account.ID, app.ID, api.CreateIssueIngestTokenRequest{DeploymentID: dep.ID, Environment: "application", Name: "stale", ExpiresAt: now.Add(time.Hour)}, []byte(strings.Repeat("x", 32)), api.PlanPro.IssueLimits())
	if !errors.Is(err, state.ErrIssuePolicyChanged) {
		t.Fatalf("free owner accepted token: %v", err)
	}
}

func TestPgIssueTokenCreationWaitsForDowngrade(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `UPDATE accounts SET plan='free' WHERE id=$1`, account.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := s.CreateIssueToken(ctx, account.ID, app.ID, api.CreateIssueIngestTokenRequest{DeploymentID: dep.ID, Environment: "application", Name: "pending", ExpiresAt: time.Now().Add(time.Hour)}, []byte(strings.Repeat("y", 32)), api.PlanPro.IssueLimits())
		result <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%IssuePolicyLockAccount%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("token creation did not wait on owner policy")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if !errors.Is(err, state.ErrIssuePolicyChanged) {
			t.Fatalf("downgrade ignored: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("token creation did not finish")
	}
}

func TestPgIssueRetentionSkipsLockedUsageAndContinues(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	a1, app1 := seedPgAccountAndApp(t, s, ctx)
	a2, app2 := seedPgAccountAndApp(t, s, ctx)
	dep1, dep2 := seedPgDeployment(t, s, ctx, app1), seedPgDeployment(t, s, ctx, app2)
	now := time.Now().UTC()
	record := func(account state.Account, app state.App, dep state.Deployment) {
		t.Helper()
		_, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", SourceKind: "exception"}, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE issue_events SET received_at=now()-interval '31 days' WHERE app_id=$1`, app.ID); err != nil {
			t.Fatal(err)
		}
	}
	record(a1, app1, dep1)
	record(a2, app2, dep2)
	if _, err := pool.Exec(ctx, `UPDATE issue_events SET received_at=now()-interval '32 days' WHERE app_id=$1`, app1.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT app_id FROM issue_event_usage WHERE app_id=$1 FOR UPDATE`, app1.ID); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	purge := func(ctx context.Context) (int64, error) {
		return sqlc.New().IssuePurgePlanEvents(ctx, pool, sqlc.IssuePurgePlanEventsParams{Plan: string(api.PlanPro), Before: pgtype.Timestamptz{Time: now.AddDate(0, 0, -30), Valid: true}, BatchLimit: 1})
	}
	if _, err = purge(short); err != nil {
		t.Fatalf("locked usage stalled other cleanup: %v", err)
	}
	var one, two int
	if err = pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE app_id=$1),count(*) FILTER (WHERE app_id=$2) FROM issue_events`, app1.ID, app2.ID).Scan(&one, &two); err != nil {
		t.Fatal(err)
	}
	if one != 1 || two != 0 {
		t.Fatalf("locked app=%d other app=%d", one, two)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MaintainIssueRetention(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM issue_events WHERE app_id=$1`, app1.ID).Scan(&one); err != nil || one != 0 {
		t.Fatalf("released app not cleaned: %d %v", one, err)
	}
	// Older retained events can predate the lazily-created usage row.
	record(a2, app2, dep2)
	if _, err = pool.Exec(ctx, `DELETE FROM issue_event_usage WHERE app_id=$1`, app2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MaintainIssueRetention(ctx, now); err != nil {
		t.Fatalf("legacy usage cleanup: %v", err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM issue_events WHERE app_id=$1`, app2.ID).Scan(&two); err != nil || two != 0 {
		t.Fatalf("legacy event not cleaned: %d %v", two, err)
	}
	// The oldest app must also be skipped when its event, rather than its
	// usage row, is locked independently of its app row.
	record(a1, app1, dep1)
	record(a2, app2, dep2)
	if _, err = pool.Exec(ctx, `UPDATE issue_events SET received_at=now()-interval '32 days' WHERE app_id=$1`, app1.ID); err != nil {
		t.Fatal(err)
	}
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx2.Rollback(ctx) }()
	if _, err = tx2.Exec(ctx, `SELECT id FROM issue_events WHERE app_id=$1 FOR UPDATE`, app1.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := purge(ctx); err != nil || n != 1 {
		t.Fatalf("locked event stalled later app: deleted=%d err=%v", n, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM issue_events WHERE app_id=$1`, app2.ID).Scan(&two); err != nil || two != 0 {
		t.Fatalf("later app not cleaned: %d %v", two, err)
	}
}

func TestPgSlackPreflightDoesNotSpendDeliveryAttempts(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	team, err := s.PutIssueTeam(ctx, app.ID, account.ID, account.ID, "responders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{account.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueSlackDestination(ctx, app.ID, account.ID, state.PutIssueSlackDestination{TeamID: team.ID, Enabled: true, ChannelID: "C0123456789", TokenSealed: []byte("sealed")}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueApplicationSlackPolicy(ctx, app.ID, account.ID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created"}, FallbackTeamID: team.ID}); err != nil {
		t.Fatal(err)
	}
	_, err = s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", SourceKind: "exception"}, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < api.IssueSlackMaxAttempts+1; i++ {
		if _, err = pool.Exec(ctx, `UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE app_id=$1`, app.ID); err != nil {
			t.Fatal(err)
		}
		claim, ok, err := s.ClaimIssueSlackDelivery(ctx)
		if err != nil || !ok || claim.Status != "in_flight" || claim.Attempt != 1 {
			t.Fatalf("preflight claim %d: %+v %v %v", i, claim, ok, err)
		}
		if err = s.FinishIssueSlackDelivery(ctx, claim, state.IssueSlackResult{Status: "failed", Error: "token_unavailable", RetryAt: time.Now().Add(-time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	claim, ok, err := s.ClaimIssueSlackDelivery(ctx)
	if err != nil || !ok || claim.Status != "in_flight" {
		t.Fatalf("claim after preflight: %+v %v %v", claim, ok, err)
	}
	// A worker that dies during preflight must not spend an attempt either.
	if _, err = pool.Exec(ctx, `UPDATE issue_team_slack_deliveries SET claim_until=clock_timestamp()-interval '1 second' WHERE id=$1`, claim.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE issue_team_slack_destinations SET claim_until=clock_timestamp()-interval '1 second' WHERE id=$1`, claim.DestinationID); err != nil {
		t.Fatal(err)
	}
	replacement, ok, err := s.ClaimIssueSlackDelivery(ctx)
	if err != nil || !ok || replacement.Attempt != 1 || replacement.ClaimID == claim.ClaimID {
		t.Fatalf("preflight lease recovery: %+v %v %v", replacement, ok, err)
	}
	if err = s.BeginIssueSlackAttempt(ctx, claim); !errors.Is(err, state.ErrIssueSlackClaimLost) {
		t.Fatalf("expired claim began sending: %v", err)
	}
	claim = replacement
	ready, err := s.IssueSlackDeliveryReady(ctx, claim)
	if err != nil || !ready {
		t.Fatalf("pre-downgrade readiness: %v %v", ready, err)
	}
	if err = s.UpdateAccountPlan(ctx, account.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	if err = s.BeginIssueSlackAttempt(ctx, claim); !errors.Is(err, state.ErrIssuePolicyChanged) {
		t.Fatalf("downgraded owner began sending: %v", err)
	}
	if err = s.UpdateAccountPlan(ctx, account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE issue_application_slack_policies SET enabled=false WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.BeginIssueSlackAttempt(ctx, claim); !errors.Is(err, state.ErrIssueSlackClaimLost) {
		t.Fatalf("ineligible source began sending: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE issue_application_slack_policies SET enabled=true WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.BeginIssueSlackAttempt(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if err = s.BeginIssueSlackAttempt(ctx, claim); !errors.Is(err, state.ErrIssueSlackClaimLost) {
		t.Fatalf("attempt was double-counted: %v", err)
	}
	var attempt int
	if err = pool.QueryRow(ctx, `SELECT attempt FROM issue_team_slack_deliveries WHERE id=$1`, claim.ID).Scan(&attempt); err != nil || attempt != 0 {
		t.Fatalf("attempt=%d err=%v", attempt, err)
	}
	// A crash after the claim is prepared but before a transport call leaves
	// the budget intact when the lease is recovered.
	if _, err = pool.Exec(ctx, `UPDATE issue_team_slack_deliveries SET claim_until=clock_timestamp()-interval '1 second' WHERE id=$1`, claim.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE issue_team_slack_destinations SET claim_until=clock_timestamp()-interval '1 second' WHERE id=$1`, claim.DestinationID); err != nil {
		t.Fatal(err)
	}
	replacement, ok, err = s.ClaimIssueSlackDelivery(ctx)
	if err != nil || !ok || replacement.Attempt != 1 || replacement.ClaimID == claim.ClaimID {
		t.Fatalf("dispatch lease recovery: %+v %v %v", replacement, ok, err)
	}
	if err = s.BeginIssueSlackAttempt(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishIssueSlackDelivery(ctx, replacement, state.IssueSlackResult{Status: "failed", Error: "transport_error", Attempted: true}); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT attempt FROM issue_team_slack_deliveries WHERE id=$1`, claim.ID).Scan(&attempt); err != nil || attempt != 1 {
		t.Fatalf("completed transport attempt=%d err=%v", attempt, err)
	}
	for want := 2; want <= api.IssueSlackMaxAttempts; want++ {
		if _, err = pool.Exec(ctx, `UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE id=$1`, claim.DestinationID); err != nil {
			t.Fatal(err)
		}
		next, found, err := s.ClaimIssueSlackDelivery(ctx)
		if err != nil || !found || next.Status != "in_flight" || next.Attempt != want {
			t.Fatalf("attempt %d claim: %+v %v %v", want, next, found, err)
		}
		if err = s.BeginIssueSlackAttempt(ctx, next); err != nil {
			t.Fatal(err)
		}
		if err = s.FinishIssueSlackDelivery(ctx, next, state.IssueSlackResult{Status: "failed", Error: "transport_error", Attempted: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE id=$1`, claim.DestinationID); err != nil {
		t.Fatal(err)
	}
	exhausted, found, err := s.ClaimIssueSlackDelivery(ctx)
	if err != nil || !found || exhausted.Status != "dead" {
		t.Fatalf("completed attempts did not exhaust budget: %+v %v %v", exhausted, found, err)
	}
}
