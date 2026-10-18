package state_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-1494: once-per-response escalation transfers reminder responsibility only
// on admission, with live authorization, transactionality and delivery fencing.
func TestPg_NativeApplicationEscalationLifecycle(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	org, err := s.CreateOrg(ctx, state.Org{Slug: "escalation-" + uuid.NewString()[:8], Name: "Responders", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.CreateAccount(ctx, "backup-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{owner.ID, member.ID} {
		if err = s.AddOrgMember(ctx, org.ID, id, state.OrgRoleDeveloper, nil); err != nil {
			t.Fatal(err)
		}
	}
	exec("UPDATE apps SET org_id=$2 WHERE id=$1", app.ID, org.ID)
	primary, err := s.PutIssueTeam(ctx, app.ID, owner.ID, owner.ID, "primary", api.PutIssueTeamRequest{Name: "Primary", MemberAccountIDs: []string{owner.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := s.PutIssueTeam(ctx, app.ID, owner.ID, owner.ID, "backup", api.PutIssueTeamRequest{Name: "Backup", MemberAccountIDs: []string{member.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	for i, team := range []api.IssueTeam{primary, backup} {
		if _, err = s.SetIssueSlackDestination(ctx, app.ID, owner.ID, state.PutIssueSlackDestination{TeamID: team.ID, Enabled: true, ChannelID: fmt.Sprintf("C012345678%d", i), TokenSealed: []byte("sealed-test")}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.SetIssueOwnershipRules(ctx, app.ID, owner.ID, []api.IssueOwnershipRule{{ExceptionType: "EscalationError", AssigneeTeamID: primary.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueApplicationSlackPolicy(ctx, app.ID, owner.ID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created", "regressed"}, FallbackTeamID: primary.ID}); err != nil {
		t.Fatal(err)
	}
	_, err = s.SetIssueApplicationReminderPolicy(ctx, app.ID, owner.ID, state.IssueApplicationReminderPolicy{Enabled: true, IntervalMinutes: 1440, RepeatLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.GetIssueApplicationEscalationPolicy(ctx, app.ID, owner.ID)
	if err != nil || policy.Enabled || policy.Revision != 0 || policy.DelayMinutes != api.IssueApplicationEscalationDefaultMinutes {
		t.Fatal(policy, err)
	}
	for _, bad := range []state.IssueApplicationEscalationPolicy{{Enabled: true, DelayMinutes: 15}, {Enabled: true, DelayMinutes: 14, TeamID: backup.ID}, {Enabled: true, DelayMinutes: 10081, TeamID: backup.ID}, {DelayMinutes: 15, TeamID: "invalid"}, {Revision: -1, DelayMinutes: 15}} {
		if _, err = s.SetIssueApplicationEscalationPolicy(ctx, app.ID, owner.ID, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("invalid policy", bad, err)
		}
	}
	if _, err = s.SetIssueApplicationEscalationPolicy(ctx, app.ID, member.ID, state.IssueApplicationEscalationPolicy{DelayMinutes: 15}); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("member settings", err)
	}
	_, foreign := seedPgAccountAndApp(t, s, ctx)
	foreignTeam, err := s.PutIssueTeam(ctx, foreign.ID, foreign.AccountID, foreign.AccountID, "foreign", api.PutIssueTeamRequest{Name: "Foreign", MemberAccountIDs: []string{foreign.AccountID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueApplicationEscalationPolicy(ctx, app.ID, owner.ID, state.IssueApplicationEscalationPolicy{Enabled: true, DelayMinutes: 15, TeamID: foreignTeam.ID}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("cross-app backup", err)
	}
	policy, err = s.SetIssueApplicationEscalationPolicy(ctx, app.ID, owner.ID, state.IssueApplicationEscalationPolicy{Enabled: true, DelayMinutes: 15, TeamID: backup.ID})
	if err != nil {
		t.Fatal(err)
	}
	record := func(key string) string {
		t.Helper()
		r, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: owner.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "EscalationError", SourceKind: "exception"}, Fingerprint: strings.Repeat(key, 64), Title: "private-evidence", PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return r.IssueID
	}
	get := func(id string) state.IssueApplicationTriage {
		t.Helper()
		r, err := s.GetIssueApplicationTriage(ctx, app.ID, owner.ID, owner.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	remDue := func(id string) {
		exec("UPDATE issue_application_reminder_policies SET updated_at=clock_timestamp()-interval '2 days' WHERE app_id=$1", app.ID)
		exec("UPDATE issue_application_triage SET reminder_started_at=clock_timestamp()-interval '2 days',last_reminded_at=NULL WHERE issue_id=$1", id)
	}
	escDue := func(id string) {
		exec("UPDATE issue_application_escalation_policies SET updated_at=clock_timestamp()-interval '2 days' WHERE app_id=$1", app.ID)
		exec("UPDATE issue_application_reminder_policies SET updated_at=clock_timestamp()-interval '2 days' WHERE app_id=$1", app.ID)
		exec("UPDATE issue_application_triage SET first_reminded_at=CASE WHEN first_reminded_at IS NOT NULL THEN clock_timestamp()-interval '2 days' END WHERE issue_id=$1", id)
	}
	enqueueRem := func(want bool) {
		t.Helper()
		ok, err := s.EnqueueIssueApplicationReminder(ctx)
		if err != nil || ok != want {
			t.Fatal("reminder", ok, err)
		}
	}
	enqueueEsc := func(want bool) {
		t.Helper()
		ok, err := s.EnqueueIssueApplicationEscalation(ctx)
		if err != nil || ok != want {
			t.Fatal("escalation", ok, err)
		}
	}
	var requests []map[string]any
	var mu sync.Mutex
	rateNext := false
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(v)
		if strings.Contains(string(raw), "private-evidence") {
			t.Error("private evidence leaked")
		}
		mu.Lock()
		requests = append(requests, v)
		n := len(requests)
		rate := rateNext
		rateNext = false
		mu.Unlock()
		if rate {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
			return
		}
		fmt.Fprintf(w, `{"ok":true,"channel":%q,"ts":"123.%06d"}`, v["channel"], n)
	}))
	defer fake.Close()
	worker := issueslack.Worker{Store: s, DashboardBase: "https://gregale.dev", OpenToken: func([]byte) (string, error) { return "xoxb-test", nil }, Client: issueslack.NewClient(&http.Client{Transport: nativeReminderTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "slack.com" {
			return nil, errors.New("unexpected host")
		}
		copy := r.Clone(r.Context())
		copy.URL.Scheme = "http"
		copy.URL.Host = strings.TrimPrefix(fake.URL, "http://")
		return http.DefaultTransport.RoundTrip(copy)
	})})}
	forceSend := func() {
		exec("UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE app_id=$1", app.ID)
	}
	drain := func() {
		t.Helper()
		for range 30 {
			forceSend()
			ok, err := worker.Step(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				return
			}
		}
		t.Fatal("queue did not drain")
	}
	firstReminder := func(id string) { t.Helper(); remDue(id); enqueueRem(true); drain() }
	id := record("a")
	escDue(id)
	enqueueEsc(false) // A selected alert alone is not a reminder.
	if _, err = s.GetIssueApplicationTriage(ctx, app.ID, owner.ID, member.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("backup access before handoff", err)
	}
	firstReminder(id)
	enqueueEsc(false)
	same, err := s.SetIssueApplicationEscalationPolicy(ctx, app.ID, owner.ID, policy)
	if err != nil || same.Revision != policy.Revision {
		t.Fatal("no-op policy", same, err)
	}
	until := time.Now().UTC().Add(time.Hour)
	v := get(id)
	if err = s.UpdateIssueApplicationTriage(ctx, app.ID, owner.ID, owner.ID, id, v.Revision, state.IssueApplicationTriageAction{Action: "snooze", SnoozedUntil: &until}); err != nil {
		t.Fatal(err)
	}
	escDue(id)
	enqueueEsc(false)
	exec("UPDATE issue_application_triage SET snoozed_until=clock_timestamp()-interval '1 minute' WHERE issue_id=$1", id)
	enqueueEsc(false)
	exec("UPDATE issue_application_triage SET snoozed_until=clock_timestamp()-interval '16 minutes' WHERE issue_id=$1", id)
	exec(`CREATE FUNCTION reject_native_escalation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='escalation' THEN RAISE EXCEPTION 'injected escalation failure'; END IF; RETURN NEW; END $$`)
	exec(`CREATE TRIGGER reject_native_escalation BEFORE INSERT ON issue_application_reminders FOR EACH ROW EXECUTE FUNCTION reject_native_escalation()`)
	if _, err = s.EnqueueIssueApplicationEscalation(ctx); err == nil {
		t.Fatal("partial escalation accepted")
	}
	if v := get(id); len(v.Escalations) != 0 || v.EscalatedAt != nil {
		t.Fatal("partial handoff", v)
	}
	exec("DROP TRIGGER reject_native_escalation ON issue_application_reminders")
	exec("DROP FUNCTION reject_native_escalation()")
	v = get(id)
	if err = s.UpdateIssueApplicationTriage(ctx, app.ID, owner.ID, owner.ID, id, v.Revision, state.IssueApplicationTriageAction{Action: "unsnooze"}); err != nil {
		t.Fatal(err)
	}
	remDue(id)
	enqueueRem(true)
	forceSend()
	oldClaim, ok, err := s.ClaimIssueSlackDelivery(ctx)
	if err != nil || !ok || oldClaim.Kind != "application_reminder" {
		t.Fatal(oldClaim, err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { ok, err := s.EnqueueIssueApplicationEscalation(ctx); results <- ok; errs <- err })
	}
	wg.Wait()
	close(results)
	close(errs)
	wins := 0
	for ok := range results {
		if ok {
			wins++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal("duplicate escalation", wins)
	}
	v = get(id)
	if v.EscalatedAt == nil || v.ResponsibleTeamID != backup.ID || v.TeamID != primary.ID || len(v.Escalations) != 1 || v.Escalations[0].Outcome != "queued" {
		t.Fatal("handoff", v)
	}
	if ready, err := s.IssueSlackDeliveryReady(ctx, oldClaim); err != nil || ready {
		t.Fatal("primary reminder still ready", ready, err)
	}
	if err = s.FinishIssueSlackDelivery(ctx, oldClaim, state.IssueSlackResult{Status: "succeeded", MessageTS: "123.999999"}); !errors.Is(err, state.ErrIssueSlackClaimLost) {
		t.Fatal("stale primary receipt", err)
	}
	if _, err = s.GetIssueApplicationTriage(ctx, app.ID, owner.ID, member.ID, id); err != nil {
		t.Fatal("backup cannot respond", err)
	}
	// Escalation starts/retries a backup-team thread; later reminders reply there.
	mu.Lock()
	rateNext = true
	mu.Unlock()
	forceSend()
	if processed, err := worker.Step(ctx); err != nil || !processed {
		t.Fatal(processed, err)
	}
	if _, ok, err := s.ClaimIssueSlackDelivery(ctx); err != nil || ok {
		t.Fatal("rate limit bypassed", ok, err)
	}
	exec("UPDATE issue_team_slack_deliveries SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE status='failed' AND reminder_id IS NOT NULL")
	drain()
	enqueueRem(false)
	remDue(id)
	enqueueRem(true)
	drain()
	mu.Lock()
	escFirst, escRetry, last := requests[2], requests[3], requests[len(requests)-1]
	mu.Unlock()
	if escFirst["client_msg_id"] != escRetry["client_msg_id"] || escRetry["thread_ts"] != nil || escRetry["channel"] != "C0123456781" || last["thread_ts"] != "123.000004" || last["channel"] != "C0123456781" {
		t.Fatal("backup threading", escFirst, escRetry, last)
	}
	if r := get(id).Reminders[0]; r.Sequence != 3 || r.TeamName != "Backup" || r.DeliveryStatus != "succeeded" {
		t.Fatal("post-handoff reminder", r)
	}
	escDue(id)
	enqueueEsc(false)
	// Policy saves cancel only queued escalation transport, preserving responsibility.
	b := record("b")
	firstReminder(b)
	escDue(b)
	enqueueEsc(true)
	forceSend()
	claim, ok, err := s.ClaimIssueSlackDelivery(ctx)
	if err != nil || !ok || claim.Kind != "application_escalation" {
		t.Fatal(claim, err)
	}
	policy.DelayMinutes = 16
	policy, err = s.SetIssueApplicationEscalationPolicy(ctx, app.ID, owner.ID, policy)
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := s.IssueSlackDeliveryReady(ctx, claim); err != nil || ready {
		t.Fatal("policy fence", ready, err)
	}
	if err = s.FinishIssueSlackDelivery(ctx, claim, state.IssueSlackResult{Status: "succeeded", MessageTS: "123.999999"}); !errors.Is(err, state.ErrIssueSlackClaimLost) {
		t.Fatal("stale escalation receipt", err)
	}
	if _, err = s.SetIssueApplicationEscalationPolicy(ctx, app.ID, owner.ID, state.IssueApplicationEscalationPolicy{Revision: 0, DelayMinutes: 15}); err == nil {
		t.Fatal("stale policy")
	}
	if get(b).ResponsibleTeamID != backup.ID {
		t.Fatal("policy rewound responsibility")
	}
	escDue(b)
	enqueueEsc(false)
	// Acknowledgment and resolution suppress unattempted escalation.
	c := record("c")
	firstReminder(c)
	escDue(c)
	v = get(c)
	if err = s.UpdateIssueApplicationTriage(ctx, app.ID, owner.ID, owner.ID, c, v.Revision, state.IssueApplicationTriageAction{Action: "acknowledge"}); err != nil {
		t.Fatal(err)
	}
	enqueueEsc(false)
	d := record("d")
	firstReminder(d)
	escDue(d)
	if _, err = s.ActOnIssue(ctx, app.ID, d, owner.ID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, now); err != nil {
		t.Fatal(err)
	}
	enqueueEsc(false)
	// Current organization membership gates backup access and captured transport.
	exec("UPDATE org_memberships SET removed_at=clock_timestamp() WHERE org_id=$1 AND account_id=$2", org.ID, member.ID)
	if _, err = s.GetIssueApplicationTriage(ctx, app.ID, owner.ID, member.ID, b); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("removed backup retained access", err)
	}
	remDue(b)
	enqueueRem(true)
	drain()
	if r := get(b).Reminders[0]; r.TeamName != "Primary" {
		t.Fatal("missing backup fallback", r)
	}
	e := record("e")
	firstReminder(e)
	escDue(e)
	enqueueEsc(true)
	v = get(e)
	if v.EscalatedAt != nil || len(v.Escalations) != 1 || v.Escalations[0].Outcome != "unrouted" || v.Escalations[0].TeamName != "Backup" {
		t.Fatal("unrouted escalation", v)
	}
	escDue(e)
	enqueueEsc(false)
	exec("UPDATE org_memberships SET removed_at=NULL WHERE org_id=$1 AND account_id=$2", org.ID, member.ID)
	// Regression starts a fresh response and revokes the old backup grant.
	previous := get(id).IncidentID
	if _, err = s.ActOnIssue(ctx, app.ID, id, owner.ID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, now); err != nil {
		t.Fatal(err)
	}
	dep = seedPgDeployment(t, s, ctx, app)
	now = now.Add(time.Second)
	record("a")
	v = get(id)
	if v.IncidentID == previous || v.EscalatedAt != nil || v.ResponsibleTeamID != "" {
		t.Fatal("regression inherited escalation", v)
	}
	if _, err = s.GetIssueApplicationTriage(ctx, app.ID, owner.ID, member.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("old response grant survived", err)
	}
	firstReminder(id)
	escDue(id)
	enqueueEsc(true)
	drain()
	if len(get(id).Escalations) != 2 {
		t.Fatal("response history lost")
	}
	v = get(id)
	if err = s.UpdateIssueApplicationTriage(ctx, app.ID, owner.ID, member.ID, id, v.Revision, state.IssueApplicationTriageAction{Action: "acknowledge"}); err != nil {
		t.Fatal("backup acknowledgment", err)
	}
	if get(id).AcknowledgedBy != member.ID {
		t.Fatal("backup handling actor missing")
	}
	// An already-responsible target consumes an attempt without another message.
	policy.TeamID = primary.ID
	policy, err = s.SetIssueApplicationEscalationPolicy(ctx, app.ID, owner.ID, policy)
	if err != nil {
		t.Fatal(err)
	}
	f := record("f")
	firstReminder(f)
	escDue(f)
	enqueueEsc(true)
	v = get(f)
	if v.EscalatedAt != nil || v.Escalations[0].Outcome != "already_responsible" {
		t.Fatal("duplicate responsible route", v)
	}
	// Saturated admission, owner plan and abuse holds cannot cause extra sends.
	policy.TeamID = backup.ID
	policy, err = s.SetIssueApplicationEscalationPolicy(ctx, app.ID, owner.ID, policy)
	if err != nil {
		t.Fatal(err)
	}
	g := record("1")
	firstReminder(g)
	escDue(g)
	exec("UPDATE accounts SET plan='free' WHERE id=$1", owner.ID)
	enqueueEsc(false)
	exec("UPDATE accounts SET plan='pro',abuse_hold_at=clock_timestamp(),abuse_hold_reason='operator' WHERE id=$1", owner.ID)
	enqueueEsc(false)
	exec("UPDATE accounts SET abuse_hold_at=NULL,abuse_hold_reason=NULL WHERE id=$1", owner.ID)
	exec(`WITH activities AS (INSERT INTO issue_activity(id,issue_id,action,created_at,details)
 SELECT gen_random_uuid(),$1,'created',clock_timestamp(),'{}'::jsonb FROM generate_series(1,$2) RETURNING id,issue_id)
 INSERT INTO issue_team_slack_deliveries(id,destination_id,destination_revision,activity_id,incident_id,channel_id,application_policy_revision,recipient_account_ids,assigned_team_id)
 SELECT gen_random_uuid(),dst.id,dst.revision,act.id,act.issue_id,dst.channel_id,p.revision,ARRAY[$3::uuid],$4
 FROM activities act CROSS JOIN issue_team_slack_destinations dst JOIN issue_application_slack_policies p ON p.app_id=dst.app_id WHERE dst.team_id=$4`, g, api.IssueApplicationSlackMaxDeliveries, owner.ID, primary.ID)
	enqueueEsc(true)
	v = get(g)
	if v.EscalatedAt != nil || v.Escalations[0].Outcome != "queue_full" {
		t.Fatal("capacity escalation", v)
	}
	enqueueEsc(false)
	missed, err := s.GetIssueApplicationSlackPolicy(ctx, app.ID, owner.ID)
	if err != nil || missed.UnroutedAlerts < 1 || missed.SkippedDeliveries < 1 {
		t.Fatal("missed counters", missed, err)
	}
}
