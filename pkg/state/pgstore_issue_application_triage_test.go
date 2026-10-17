package state_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-1492: native issue response lifecycle, authorization and delivery fencing.
func TestPg_ApplicationIssueResponseLifecycleAndSlack(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Native issue response follows owner entitlement.
	exec("UPDATE accounts SET plan='pro' WHERE id=$1", account.ID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	org, err := s.CreateOrg(ctx, state.Org{Slug: "triage-" + uuid.NewString()[:8], Name: "Responders", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.CreateAccount(ctx, "triage-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := s.CreateAccount(ctx, "outside-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{account.ID, member.ID, outsider.ID} {
		if err = s.AddOrgMember(ctx, org.ID, id, state.OrgRoleDeveloper, nil); err != nil {
			t.Fatal(err)
		}
	}
	exec("UPDATE apps SET org_id=$2 WHERE id=$1", app.ID, org.ID)
	team, err := s.PutIssueTeam(ctx, app.ID, account.ID, account.ID, "responders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{member.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := s.PutIssueTeam(ctx, app.ID, account.ID, account.ID, "fallback", api.PutIssueTeamRequest{Name: "Fallback", MemberAccountIDs: []string{outsider.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, tm := range []api.IssueTeam{team, fallback} {
		if _, err = s.SetIssueSlackDestination(ctx, app.ID, account.ID, state.PutIssueSlackDestination{TeamID: tm.ID, Enabled: true, ChannelID: "C0123456789", TokenSealed: []byte("sealed-test")}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.SetIssueApplicationSlackPolicy(ctx, app.ID, account.ID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created", "regressed", "frequency_threshold_reached"}, FallbackTeamID: fallback.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueOwnershipRules(ctx, app.ID, account.ID, []api.IssueOwnershipRule{{ExceptionType: "ResponseError", AssigneeTeamID: team.ID}}); err != nil {
		t.Fatal(err)
	}
	record := func(key string) api.IssueEventResponse {
		t.Helper()
		out, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "ResponseError", SourceKind: "exception"}, Fingerprint: strings.Repeat(key, 64), Title: key, PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	issue := record("a").IssueID
	get := func(actor string) state.IssueApplicationTriage {
		t.Helper()
		v, err := s.GetIssueApplicationTriage(ctx, app.ID, account.ID, actor, issue)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	update := func(actor string, rev int64, req state.IssueApplicationTriageAction) error {
		return s.UpdateIssueApplicationTriage(ctx, app.ID, account.ID, actor, issue, rev, req)
	}
	act := func(action string) {
		t.Helper()
		_, err := s.ActOnIssue(ctx, app.ID, issue, account.ID, api.IssueActionRequest{Action: action, FixedDeploymentID: dep.ID}, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	initial := get(member.ID)
	if !initial.Active || initial.Revision != 0 || initial.TeamID != team.ID {
		t.Fatalf("initial %+v", initial)
	}
	for _, who := range []string{outsider.ID, uuid.NewString()} {
		if _, err = s.GetIssueApplicationTriage(ctx, app.ID, account.ID, who, issue); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("unrelated actor", err)
		}
	}
	if _, err = s.GetIssueApplicationTriage(ctx, app.ID, member.ID, member.ID, issue); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross owner", err)
	}
	if err = update(member.ID, 0, state.IssueApplicationTriageAction{Action: "acknowledge"}); err != nil {
		t.Fatal(err)
	}
	ack := get(account.ID)
	if ack.AcknowledgedBy != member.ID || ack.AcknowledgedAt == nil || ack.Revision != 1 || len(ack.History) != 1 {
		t.Fatal("ack", ack)
	}
	if err = update(account.ID, 0, state.IssueApplicationTriageAction{Action: "acknowledge"}); err == nil {
		t.Fatal("stale update")
	}
	if err = update(account.ID, ack.Revision, state.IssueApplicationTriageAction{Action: "acknowledge"}); err != nil || get(account.ID).Revision != ack.Revision {
		t.Fatal("ack not idempotent", err)
	}
	until := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	for _, req := range []state.IssueApplicationTriageAction{{Action: "snooze"}, {Action: "claim"}, {Action: "acknowledge", Reason: "invalid"}, {Action: "snooze", SnoozedUntil: &now}, {Action: "snooze", SnoozedUntil: &until, Reason: strings.Repeat("x", api.IssueApplicationSnoozeReasonMaxBytes+1)}} {
		if err = update(member.ID, 1, req); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("invalid request", req, err)
		}
	}
	claimed, ok, err := s.ClaimIssueSlackDelivery(ctx)
	if err != nil || !ok || claimed.Status != "in_flight" {
		t.Fatal("claim", claimed, ok, err)
	}

	// Failed audit must roll back cancellation, lease release and response state.
	exec(`CREATE FUNCTION reject_native_snooze() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='snooze' THEN RAISE EXCEPTION 'injected triage audit failure'; END IF; RETURN NEW; END $$`)
	exec(`CREATE TRIGGER reject_native_snooze BEFORE INSERT ON issue_application_triage_activity FOR EACH ROW EXECUTE FUNCTION reject_native_snooze()`)
	if err = update(member.ID, 1, state.IssueApplicationTriageAction{Action: "snooze", SnoozedUntil: &until}); err == nil {
		t.Fatal("audit failure accepted")
	}
	if v := get(account.ID); v.Revision != 1 || v.SnoozedUntil != nil {
		t.Fatal("failed write persisted state", v)
	}
	if ready, err := s.IssueSlackDeliveryReady(ctx, claimed); err != nil || !ready {
		t.Fatal("failed write canceled delivery", ready, err)
	}
	exec("DROP TRIGGER reject_native_snooze ON issue_application_triage_activity")
	exec("DROP FUNCTION reject_native_snooze()")
	if err = update(member.ID, 1, state.IssueApplicationTriageAction{Action: "snooze", SnoozedUntil: &until, Reason: "<script>investigate</script>"}); err != nil {
		t.Fatal(err)
	}
	snooze := get(account.ID)
	if !snooze.Snoozed || snooze.SnoozedBy != member.ID || snooze.AcknowledgedBy != member.ID {
		t.Fatal(snooze)
	}
	if ready, err := s.IssueSlackDeliveryReady(ctx, claimed); err != nil || ready {
		t.Fatal("snooze did not fence ready", ready, err)
	}
	if err = s.FinishIssueSlackDelivery(ctx, claimed, state.IssueSlackResult{Status: "succeeded", MessageTS: "123.456789"}); !errors.Is(err, state.ErrIssueSlackClaimLost) {
		t.Fatal("stale completion", err)
	}
	if err = update(member.ID, snooze.Revision, state.IssueApplicationTriageAction{Action: "snooze", SnoozedUntil: &until, Reason: snooze.SnoozeReason}); err != nil || get(account.ID).Revision != snooze.Revision {
		t.Fatal("snooze no-op", err)
	}
	// Snooze does not block ingestion or generic frequency activity, only native admission.
	if _, err = s.SetIssueFrequencyAlertPolicy(ctx, app.ID, account.ID, api.UpdateIssueFrequencyAlertPolicyRequest{ExpectedRevision: new(int64), Rules: []api.IssueFrequencyAlertRule{{Key: "responses", Enabled: true, MinimumEvents: 2, WindowSeconds: 60, CooldownSeconds: 60}}}); err != nil {
		t.Fatal(err)
	}
	record("a")
	rows, err := s.ListIssueSlackDeliveries(ctx, app.ID, account.ID)
	if err != nil || len(rows) != 1 || rows[0].Status != "canceled" {
		t.Fatal("snoozed source queued", rows, err)
	}
	detail, err := s.GetIssueDetail(ctx, app.ID, issue, now.Add(-time.Hour), now.Add(time.Hour), state.IssueDetailCursors{})
	if err != nil || detail.Issue.EventCount != 2 {
		t.Fatal("capture suppressed", detail, err)
	}
	found := false
	for _, a := range detail.Activity {
		found = found || a.Action == "frequency_threshold_reached"
	}
	if !found {
		t.Fatal("source activity suppressed")
	}
	exec("UPDATE issue_application_triage SET snoozed_until=clock_timestamp()-interval '1 second' WHERE issue_id=$1", issue)
	if v := get(member.ID); v.Snoozed || !v.Resurfaced || v.Revision != snooze.Revision {
		t.Fatal("expiry", v)
	}
	if err = update(member.ID, snooze.Revision, state.IssueApplicationTriageAction{Action: "unsnooze"}); err != nil {
		t.Fatal(err)
	}
	if v := get(account.ID); v.SnoozedUntil != nil || v.Resurfaced {
		t.Fatal("clear", v)
	}
	rows, _ = s.ListIssueSlackDeliveries(ctx, app.ID, account.ID)
	if len(rows) != 1 {
		t.Fatal("expiry replayed")
	}
	// Reopening/regression renews the response without recycling revisions.
	before := get(account.ID)
	act("resolve")
	if get(account.ID).Active {
		t.Fatal("resolved active")
	}
	if err = update(member.ID, before.Revision, state.IssueApplicationTriageAction{Action: "snooze", SnoozedUntil: &until}); err == nil {
		t.Fatal("resolved update accepted")
	}
	act("reopen")
	fresh := get(account.ID)
	if !fresh.Active || fresh.AcknowledgedAt != nil || fresh.SnoozedUntil != nil || fresh.IncidentID == before.IncidentID || fresh.Revision <= before.Revision {
		t.Fatal("reopened inherited response", fresh)
	}
	if err = update(member.ID, fresh.Revision, state.IssueApplicationTriageAction{Action: "acknowledge"}); err != nil {
		t.Fatal(err)
	}
	act("resolve")
	dep = seedPgDeployment(t, s, ctx, app)
	now = now.Add(time.Second)
	if !record("a").Regressed {
		t.Fatal("fixture did not regress")
	}
	if v := get(member.ID); v.AcknowledgedAt != nil || v.IncidentID == fresh.IncidentID {
		t.Fatal("regression did not reset", v)
	}

	rows, err = s.ListIssueSlackDeliveries(ctx, app.ID, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	regressionQueued := false
	for _, d := range rows {
		regressionQueued = regressionQueued || (d.Kind == "regressed" && d.Status == "pending")
	}
	if !regressionQueued {
		t.Fatal("fresh regression suppressed", rows)
	}
	// Even an untouched issue advances revision when reopened; old revision-zero forms are fenced.
	untouched := record("c").IssueID
	for _, action := range []string{"resolve", "reopen"} {
		if _, err = s.ActOnIssue(ctx, app.ID, untouched, account.ID, api.IssueActionRequest{Action: action, FixedDeploymentID: dep.ID}, now); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.UpdateIssueApplicationTriage(ctx, app.ID, account.ID, account.ID, untouched, 0, state.IssueApplicationTriageAction{Action: "acknowledge"}); err == nil {
		t.Fatal("pre-reopen revision-zero form accepted")
	}
	// Only one concurrent form may win.
	revision := get(account.ID).Revision
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			results <- update(member.ID, revision, state.IssueApplicationTriageAction{Action: "snooze", SnoozedUntil: &until})
		})
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatal("concurrent winners", wins)
	}
	// Current membership is rechecked, and fallback responders follow the live route.
	exec("UPDATE org_memberships SET removed_at=clock_timestamp() WHERE org_id=$1 AND account_id=$2", org.ID, member.ID)
	if _, err = s.GetIssueApplicationTriage(ctx, app.ID, account.ID, member.ID, issue); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("removed member retained access", err)
	}
	if _, err = s.GetIssueApplicationTriage(ctx, app.ID, account.ID, outsider.ID, issue); err != nil {
		t.Fatal("fallback inaccessible", err)
	}
	if err = update(member.ID, get(account.ID).Revision, state.IssueApplicationTriageAction{Action: "acknowledge"}); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("removed write", err)
	}
	exec("UPDATE accounts SET plan='free' WHERE id=$1", account.ID)
	if _, err = s.GetIssueApplicationTriage(ctx, app.ID, account.ID, account.ID, issue); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("entitlement", err)
	}
	exec("UPDATE accounts SET plan='pro' WHERE id=$1", account.ID)
	// Grouping does not transfer source response state; split opens a fresh response.
	other := record("b").IssueID
	if _, err = s.ActOnIssue(ctx, app.ID, issue, account.ID, api.IssueActionRequest{Action: "merge", TargetIssueID: other}, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetIssueApplicationTriage(ctx, app.ID, account.ID, account.ID, issue); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("alias triage", err)
	}
	if _, err = s.ActOnIssue(ctx, app.ID, other, account.ID, api.IssueActionRequest{Action: "split", MemberIssueID: issue}, now); err != nil {
		t.Fatal(err)
	}
	if v := get(account.ID); v.SnoozedUntil != nil || v.AcknowledgedAt != nil {
		t.Fatal("split inherited state", v)
	}
	// Audit is bounded across lifecycle resets; repeated no-ops add nothing.
	for n := 0; n < api.IssueApplicationTriageHistoryMax+2; n++ {
		v := get(account.ID)
		action := "snooze"
		req := state.IssueApplicationTriageAction{Action: action, SnoozedUntil: &until}
		if v.Snoozed {
			req = state.IssueApplicationTriageAction{Action: "unsnooze"}
		}
		if err = update(account.ID, v.Revision, req); err != nil {
			t.Fatal(err)
		}
	}
	if len(get(account.ID).History) != api.IssueApplicationTriageHistoryMax {
		t.Fatal("history unbounded")
	}
}
