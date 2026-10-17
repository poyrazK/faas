package state_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-1495: filter and page only authorized current responses, without evidence.
func TestPg_NativeApplicationResponseInbox(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	org, err := s.CreateOrg(ctx, state.Org{Slug: "inbox-" + uuid.NewString()[:8], Name: "Inbox", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.CreateAccount(ctx, "inbox-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := s.CreateAccount(ctx, "outside-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{owner.ID, member.ID} {
		if err = s.AddOrgMember(ctx, org.ID, id, state.OrgRoleDeveloper, nil); err != nil {
			t.Fatal(err)
		}
	}
	exec("UPDATE apps SET org_id=$2 WHERE id=$1", app.ID, org.ID)
	team, err := s.PutIssueTeam(ctx, app.ID, owner.ID, owner.ID, "responders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{member.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for n := range api.IssuePageSize + 3 {
		r, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: owner.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "InboxError", SourceKind: "exception"}, Fingerprint: fmt.Sprintf("%064x", n+1), Title: "private-title", PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.IssueID)
	}
	list := func(actor, status string, mine bool, after string) state.IssueApplicationResponseInbox {
		t.Helper()
		v, e := s.ListIssueApplicationResponses(ctx, app.ID, owner.ID, actor, state.IssueApplicationResponseFilter{Status: status, MyTeams: mine, AfterID: after})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	first := list(owner.ID, "all", false, "")
	if len(first.Items) != api.IssuePageSize || first.NextID == "" {
		t.Fatal("bounded page", len(first.Items), first.NextID)
	}
	second := list(owner.ID, "all", false, first.NextID)
	if len(second.Items) != 3 || second.NextID != "" {
		t.Fatal("second page", second)
	}
	seen := map[string]bool{}
	for _, r := range append(first.Items, second.Items...) {
		if seen[r.IssueID] {
			t.Fatal("duplicate cursor row")
		}
		seen[r.IssueID] = true
	}
	if len(list(member.ID, "all", false, "").Items) != 0 || len(list(outsider.ID, "all", false, "").Items) != 0 {
		t.Fatal("unassigned access")
	}
	if err = s.AddOrgMember(ctx, org.ID, outsider.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	if len(list(outsider.ID, "all", false, "").Items) != 0 {
		t.Fatal("unrelated organization member gained access")
	}
	for _, id := range ids[:6] {
		exec("UPDATE app_issues SET assignee_team_id=$2 WHERE id=$1", id, team.ID)
	}
	if len(list(member.ID, "all", true, "").Items) != 6 {
		t.Fatal("my teams")
	}
	for _, id := range ids[:6] {
		if _, err = s.GetIssueApplicationTriage(ctx, app.ID, owner.ID, member.ID, id); err != nil {
			t.Fatal("scope parity", err)
		}
	}
	if err = s.UpdateIssueApplicationTriage(ctx, app.ID, owner.ID, member.ID, ids[0], 0, state.IssueApplicationTriageAction{Action: "acknowledge"}); err != nil {
		t.Fatal(err)
	}
	until := now.Add(time.Hour)
	if err = s.UpdateIssueApplicationTriage(ctx, app.ID, owner.ID, member.ID, ids[1], 0, state.IssueApplicationTriageAction{Action: "snooze", SnoozedUntil: &until}); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateIssueApplicationTriage(ctx, app.ID, owner.ID, member.ID, ids[2], 0, state.IssueApplicationTriageAction{Action: "snooze", SnoozedUntil: &until}); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE issue_application_triage SET snoozed_until=clock_timestamp()-interval '1 minute' WHERE issue_id=$1", ids[2])
	if len(list(member.ID, "unacknowledged", false, "").Items) != 4 {
		t.Fatal("awake filter")
	}
	if v := list(member.ID, "snoozed", false, ""); len(v.Items) != 1 || v.Items[0].IssueID != ids[1] {
		t.Fatal("snoozed", v)
	}
	if v := list(member.ID, "resurfaced", false, ""); len(v.Items) != 1 || v.Items[0].IssueID != ids[2] {
		t.Fatal("resurfaced", v)
	}
	// A backup grant is independent of assignment; reset or removal revokes it.
	exec("INSERT INTO issue_application_triage(issue_id,revision,escalated_at,escalation_team_id) VALUES($1,1,clock_timestamp(),$2)", ids[6], team.ID)
	if v := list(member.ID, "escalated", true, ""); len(v.Items) != 1 || v.Items[0].ResponsibleTeamID != team.ID {
		t.Fatal("backup inbox", v)
	}
	exec("UPDATE issue_application_triage SET escalated_at=NULL,escalation_team_id=NULL WHERE issue_id=$1", ids[6])
	if len(list(member.ID, "escalated", false, "").Items) != 0 {
		t.Fatal("reset retained backup grant")
	}
	// Enabled schedules use the same latest-policy/snooze clocks as workers.
	_, err = s.SetIssueSlackDestination(ctx, app.ID, owner.ID, state.PutIssueSlackDestination{TeamID: team.ID, Enabled: true, ChannelID: "C0123456789", TokenSealed: []byte("sealed-test")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetIssueApplicationSlackPolicy(ctx, app.ID, owner.ID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetIssueApplicationReminderPolicy(ctx, app.ID, owner.ID, state.IssueApplicationReminderPolicy{Enabled: true, IntervalMinutes: 60, RepeatLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetIssueApplicationEscalationPolicy(ctx, app.ID, owner.ID, state.IssueApplicationEscalationPolicy{Enabled: true, DelayMinutes: 15, TeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	exec("UPDATE issue_application_triage SET reminder_started_at=clock_timestamp()-interval '1 day',first_reminded_at=clock_timestamp()-interval '1 day' WHERE issue_id=$1", ids[2])
	v := list(member.ID, "resurfaced", false, "")
	if len(v.Items) != 1 || v.Items[0].ReminderDueAt == nil || v.Items[0].EscalationDueAt == nil || !v.Items[0].ReminderDueAt.After(now) || !v.Items[0].EscalationDueAt.After(now) {
		t.Fatal("due clocks", v)
	}
	// A current-response failed admission is visible, while old responses are excluded.
	exec("INSERT INTO issue_application_reminders(id,issue_id,response_id,sequence,policy_revision,outcome) SELECT gen_random_uuid(),issue_id,incident_id,1,1,'unrouted' FROM issue_application_triage WHERE issue_id=$1", ids[2])
	if v := list(member.ID, "delivery_failed", false, ""); len(v.Items) != 1 || v.Items[0].Outcome != "unrouted" {
		t.Fatal("delivery failure", v)
	}
	exec("UPDATE issue_application_triage SET incident_id=gen_random_uuid() WHERE issue_id=$1", ids[2])
	if len(list(member.ID, "delivery_failed", false, "").Items) != 0 {
		t.Fatal("old response failure leaked")
	}
	exec("DELETE FROM app_issue_team_members WHERE team_id=$1 AND account_id=$2", team.ID, member.ID)
	if len(list(member.ID, "all", false, "").Items) != 0 {
		t.Fatal("removed team member retained access")
	}
	exec("INSERT INTO app_issue_team_members(team_id,account_id) VALUES($1,$2)", team.ID, member.ID)
	exec("UPDATE org_memberships SET removed_at=clock_timestamp() WHERE org_id=$1 AND account_id=$2", org.ID, member.ID)
	if len(list(member.ID, "all", false, "").Items) != 0 {
		t.Fatal("removed membership")
	}
	exec("UPDATE accounts SET abuse_hold_at=clock_timestamp(),abuse_hold_reason='operator' WHERE id=$1", owner.ID)
	if len(list(owner.ID, "all", false, "").Items) != 0 {
		t.Fatal("abuse hold")
	}
	for _, f := range []state.IssueApplicationResponseFilter{{Status: "bad"}, {AfterID: "invalid"}} {
		if _, err = s.ListIssueApplicationResponses(ctx, app.ID, owner.ID, owner.ID, f); err == nil {
			t.Fatal("invalid filter")
		}
	}
}
