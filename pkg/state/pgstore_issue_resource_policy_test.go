package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgIssueIntegrationCompanyOwnerEligibility(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	creator, err := s.CreateAccount(ctx, "issue-company-owner-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := s.CreateOrg(ctx, state.Org{Slug: "issue-owner-" + uuid.NewString()[:8], Name: "Issues", Plan: api.PlanFree, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateOrgPlan(ctx, org.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if err = s.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "issue-app-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	args := sqlc.IssueSlackOAuthOwnerReadyParams{ID: state.NewPgtypeUUID(uuid.MustParse(app.ID)), AccountID: state.NewPgtypeUUID(uuid.MustParse(creator.ID)), EntitledPlans: []string{"hobby", "pro", "scale"}}
	check := func(want bool) {
		t.Helper()
		got, err := sqlc.New().IssueSlackOAuthOwnerReady(ctx, pool, args)
		if err != nil || got != want {
			t.Fatalf("company eligibility = %v, want %v: %v", got, want, err)
		}
	}
	if err = s.UpdateAccountStatus(ctx, creator.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err = s.UpdateAccountStatus(ctx, creator.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateOrgStatus(ctx, org.ID, state.OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err = s.UpdateOrgStatus(ctx, org.ID, state.OrgStatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE orgs SET abuse_hold_at=now() WHERE id=$1`, org.ID); err != nil {
		t.Fatal(err)
	}
	check(false)
	if _, err = pool.Exec(ctx, `UPDATE orgs SET abuse_hold_at=NULL WHERE id=$1`, org.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateOrgPlan(ctx, org.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	check(false)
}

func TestPgIssueIntegrationLegacyCompanyOwnerEligibility(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	creator, err := s.CreateAccount(ctx, "issue-legacy-owner-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	// Paid organizations without an adopted billing identity retain the
	// creator-owned policy until resource ownership is enabled.
	org, err := s.CreateOrg(ctx, state.Org{Slug: "issue-legacy-" + uuid.NewString()[:8], Name: "Issues", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if org.ResourceOwnershipEnabled {
		t.Fatal("legacy fixture unexpectedly adopted resource ownership")
	}
	if err = s.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "issue-legacy-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	args := sqlc.IssueSlackOAuthOwnerReadyParams{ID: state.NewPgtypeUUID(uuid.MustParse(app.ID)), AccountID: state.NewPgtypeUUID(uuid.MustParse(creator.ID)), EntitledPlans: []string{"hobby", "pro", "scale"}}
	check := func(want bool) {
		t.Helper()
		got, err := sqlc.New().IssueSlackOAuthOwnerReady(ctx, pool, args)
		if err != nil || got != want {
			t.Fatalf("legacy company eligibility = %v, want %v: %v", got, want, err)
		}
	}
	check(true)
	if err = s.UpdateOrgStatus(ctx, org.ID, state.OrgStatusPastDue); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err = s.UpdateOrgStatus(ctx, org.ID, state.OrgStatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE orgs SET abuse_hold_at=now() WHERE id=$1`, org.ID); err != nil {
		t.Fatal(err)
	}
	check(false)
}

func TestPgIssueCompanySlackAndReminderUseResourceOwner(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	creator, err := s.CreateAccount(ctx, "issue-company-slack-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := s.CreateOrg(ctx, state.Org{Slug: "issue-slack-" + uuid.NewString()[:8], Name: "Issues", Plan: api.PlanFree, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateOrgPlan(ctx, org.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if err = s.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	responder, err := s.CreateAccount(ctx, "issue-company-responder-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddOrgMember(ctx, org.ID, responder.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "issue-slack-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	deployment := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	team, err := s.PutIssueTeam(ctx, app.ID, creator.ID, creator.ID, "responders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{creator.ID, responder.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueSlackDestination(ctx, app.ID, creator.ID, state.PutIssueSlackDestination{TeamID: team.ID, Enabled: true, ChannelID: "C0123456789", TokenSealed: []byte("sealed-test")}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueApplicationSlackPolicy(ctx, app.ID, creator.ID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created"}, FallbackTeamID: team.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueApplicationReminderPolicy(ctx, app.ID, creator.ID, state.IssueApplicationReminderPolicy{Enabled: true, IntervalMinutes: 5, RepeatLimit: 1}); err != nil {
		t.Fatal(err)
	}
	out, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: creator.ID, AppID: app.ID, DeploymentID: deployment.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", SourceKind: "exception"}, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE issue_application_reminder_policies SET updated_at=clock_timestamp()-interval '1 day' WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE issue_application_triage SET reminder_started_at=clock_timestamp()-interval '1 day',last_reminded_at=NULL WHERE issue_id=$1`, out.IssueID); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAccountStatus(ctx, creator.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetIssueApplicationTriage(ctx, app.ID, creator.ID, responder.ID, out.IssueID); err != nil {
		t.Fatalf("company response followed suspended creator: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE orgs SET plan=$2 WHERE id=$1`, org.ID, string(api.PlanFree)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetIssueApplicationTriage(ctx, app.ID, creator.ID, responder.ID, out.IssueID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("free company retained response access: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE orgs SET plan=$2 WHERE id=$1`, org.ID, string(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	claim, ok, err := s.ClaimIssueSlackDelivery(ctx)
	if err != nil || !ok || claim.Status != "in_flight" {
		t.Fatalf("company Slack claim: %+v %v %v", claim, ok, err)
	}
	ready, err := s.IssueSlackDeliveryReady(ctx, claim)
	if err != nil || !ready {
		t.Fatalf("company Slack readiness: %v %v", ready, err)
	}
	if ok, err = s.EnqueueIssueApplicationReminder(ctx); err != nil || !ok {
		t.Fatalf("company reminder: %v %v", ok, err)
	}
	if err = s.UpdateOrgStatus(ctx, org.ID, state.OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	ready, err = s.IssueSlackDeliveryReady(ctx, claim)
	if err != nil || ready {
		t.Fatalf("suspended company Slack readiness: %v %v", ready, err)
	}
}

func TestPgIssueRetentionDrainsExpiredEventsAfterDowngrade(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	out, err := s.RecordIssue(ctx, state.RecordIssueParams{
		Credential:  state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Environment: "application"},
		Event:       api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", SourceKind: "exception"},
		Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1,
		Limits: api.PlanPro.IssueLimits(), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO issue_events(app_id,deployment_id,event_id,issue_id,payload_hash,payload,occurred_at,received_at)
		SELECT $1,$2,gen_random_uuid(),$3,repeat('b',64),'{}'::jsonb,now()-interval '8 days',now()-interval '8 days'
		FROM generate_series(1,$4)`, app.ID, deployment.ID, out.IssueID, api.IssueMaintenanceBatch+1)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAccountPlan(ctx, account.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	more, err := s.MaintainIssueRetention(ctx, time.Now())
	if err != nil || !more {
		t.Fatalf("first batch: more=%v err=%v", more, err)
	}
	var remaining int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM issue_events WHERE app_id=$1`, app.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 2 {
		t.Fatalf("remaining events = %d, want 1 expired and 1 fresh", remaining)
	}
	more, err = s.MaintainIssueRetention(ctx, time.Now())
	if err != nil || more {
		t.Fatalf("last batch: more=%v err=%v", more, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM issue_events WHERE app_id=$1`, app.ID).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("fresh event lost: count=%d err=%v", remaining, err)
	}
}

func TestPgIssueRetentionDrainsExpiredTokensInBatches(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	_, err := pool.Exec(ctx, `INSERT INTO issue_ingest_tokens(account_id,app_id,deployment_id,environment,name,token_hash,expires_at)
		SELECT $1,$2,$3,'application','expired-' || n,decode(md5(n::text) || md5(n::text),'hex'),now()-interval '1 hour'
		FROM generate_series(1,$4) n`, account.ID, app.ID, deployment.ID, api.IssueMaintenanceBatch+1)
	if err != nil {
		t.Fatal(err)
	}
	more, err := s.MaintainIssueRetention(ctx, time.Now())
	if err != nil || !more {
		t.Fatalf("first batch: more=%v err=%v", more, err)
	}
	var remaining int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM issue_ingest_tokens WHERE app_id=$1`, app.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("remaining expired tokens = %d, want 1", remaining)
	}
	more, err = s.MaintainIssueRetention(ctx, time.Now())
	if err != nil || more {
		t.Fatalf("final batch: more=%v err=%v", more, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM issue_ingest_tokens WHERE app_id=$1`, app.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("tokens remaining: %d %v", remaining, err)
	}
}
