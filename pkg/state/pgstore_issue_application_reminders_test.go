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

type nativeReminderTransport func(*http.Request) (*http.Response, error)

func (f nativeReminderTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// ADR-1493: native reminders are bounded per response and share safe Slack delivery.
func TestPg_NativeApplicationReminderLifecycle(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	policy, err := s.GetIssueApplicationReminderPolicy(ctx, app.ID, account.ID)
	if err != nil || policy.Enabled || policy.Revision != 0 || policy.IntervalMinutes != api.IssueApplicationReminderDefaultMinutes || policy.RepeatLimit != api.IssueApplicationRemindersMax {
		t.Fatal(policy, err)
	}
	team, err := s.PutIssueTeam(ctx, app.ID, account.ID, account.ID, "primary", api.PutIssueTeamRequest{Name: "Primary", MemberAccountIDs: []string{account.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := s.PutIssueTeam(ctx, app.ID, account.ID, account.ID, "fallback", api.PutIssueTeamRequest{Name: "Fallback", MemberAccountIDs: []string{account.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []api.IssueTeam{team, backup} {
		if _, err = s.SetIssueSlackDestination(ctx, app.ID, account.ID, state.PutIssueSlackDestination{TeamID: entry.ID, Enabled: true, ChannelID: "C0123456789", TokenSealed: []byte("test-sealed")}); err != nil {
			t.Fatal(err)
		}
	}
	slackPolicy, err := s.SetIssueApplicationSlackPolicy(ctx, app.ID, account.ID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created", "regressed"}, FallbackTeamID: backup.ID})
	if err != nil {
		t.Fatal(err)
	}
	record := func(key string) string {
		t.Helper()
		out, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "NativeReminderError", SourceKind: "exception"}, Fingerprint: strings.Repeat(key, 64), Title: "private-title", PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return out.IssueID
	}
	old := record("a")
	for _, bad := range []state.IssueApplicationReminderPolicy{{Enabled: true, IntervalMinutes: 0, RepeatLimit: 1}, {IntervalMinutes: 60, RepeatLimit: 4}, {IntervalMinutes: 1441, RepeatLimit: 1}, {Revision: -1, IntervalMinutes: 60, RepeatLimit: 1}} {
		if _, err = s.SetIssueApplicationReminderPolicy(ctx, app.ID, account.ID, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("invalid policy", err)
		}
	}
	if _, err = s.SetIssueApplicationReminderPolicy(ctx, app.ID, uuid.NewString(), state.IssueApplicationReminderPolicy{IntervalMinutes: 60, RepeatLimit: 1}); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross owner", err)
	}
	policy, err = s.SetIssueApplicationReminderPolicy(ctx, app.ID, account.ID, state.IssueApplicationReminderPolicy{Enabled: true, IntervalMinutes: 5, RepeatLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	get := func(id string) state.IssueApplicationTriage {
		t.Helper()
		v, err := s.GetIssueApplicationTriage(ctx, app.ID, account.ID, account.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	makeDue := func(id string) {
		exec("UPDATE issue_application_reminder_policies SET updated_at=clock_timestamp()-interval '1 day' WHERE app_id=$1", app.ID)
		exec("UPDATE issue_application_triage SET reminder_started_at=clock_timestamp()-interval '1 day',last_reminded_at=NULL WHERE issue_id=$1", id)
	}
	enqueue := func(want bool) {
		t.Helper()
		ok, err := s.EnqueueIssueApplicationReminder(ctx)
		if err != nil || ok != want {
			t.Fatal("enqueue", ok, err)
		}
	}
	makeDue(old)
	enqueue(false) // Opt-in does not replay historical alerts.
	id := record("b")
	enqueue(false)
	var requests []map[string]any
	rateNext := false
	var mu sync.Mutex
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(v)
		if strings.Contains(string(raw), "private-title") {
			t.Error("private event content leaked")
		}
		mu.Lock()
		requests = append(requests, v)
		n := len(requests)
		rate := rateNext && v["text"] == "Gregale Issues · Unacknowledged issue reminder"
		if rate {
			rateNext = false
		}
		mu.Unlock()
		if rate {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
			return
		}
		fmt.Fprintf(w, `{"ok":true,"channel":"C0123456789","ts":"123.%06d"}`, n)
	}))
	defer fake.Close()
	worker := issueslack.Worker{Store: s, DashboardBase: "https://gregale.dev", OpenToken: func([]byte) (string, error) { return "xoxb-test", nil }, Client: issueslack.NewClient(&http.Client{Transport: nativeReminderTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "slack.com" {
			return nil, errors.New("unexpected Slack host")
		}
		copy := r.Clone(r.Context())
		base := strings.TrimPrefix(fake.URL, "http://")
		copy.URL.Scheme = "http"
		copy.URL.Host = base
		return http.DefaultTransport.RoundTrip(copy)
	})})}
	drain := func() {
		t.Helper()
		for range 20 {
			exec("UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE app_id=$1", app.ID)
			processed, err := worker.Step(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !processed {
				return
			}
		}
		t.Fatal("queue did not drain")
	}
	drain() // Establish the native issue thread before its reminder.
	var sourceActivityCount int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM issue_activity WHERE issue_id=$1", id).Scan(&sourceActivityCount); err != nil {
		t.Fatal(err)
	}
	makeDue(id)
	// Scheduling/receipt is transactional even if ledger storage fails.
	exec(`CREATE FUNCTION reject_native_reminder() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected reminder failure'; END $$`)
	exec(`CREATE TRIGGER reject_native_reminder BEFORE INSERT ON issue_application_reminders FOR EACH ROW EXECUTE FUNCTION reject_native_reminder()`)
	if _, err = s.EnqueueIssueApplicationReminder(ctx); err == nil {
		t.Fatal("ledger failure accepted")
	}
	if len(get(id).Reminders) != 0 {
		t.Fatal("partial reminder history")
	}
	exec("DROP TRIGGER reject_native_reminder ON issue_application_reminders")
	exec("DROP FUNCTION reject_native_reminder()")
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { ok, err := s.EnqueueIssueApplicationReminder(ctx); results <- ok; errs <- err })
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
		t.Fatal("duplicate scheduling", wins)
	}
	enqueue(false)
	mu.Lock()
	rateNext = true
	mu.Unlock()
	if processed, err := worker.Step(ctx); err != nil || !processed {
		t.Fatal("rate-limited step", processed, err)
	}
	if r := get(id).Reminders; r[0].DeliveryStatus != "failed" || len(r) != 1 {
		t.Fatal("retry consumed reminder slot", r)
	}
	enqueue(false)
	if _, ok, err := s.ClaimIssueSlackDelivery(ctx); err != nil || ok {
		t.Fatal("rate-limit cooldown bypassed", ok, err)
	}
	exec("UPDATE issue_team_slack_deliveries SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE reminder_id IS NOT NULL AND status='failed'")
	exec("UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE app_id=$1", app.ID)
	drain()
	mu.Lock()
	firstID, retryID := requests[2]["client_msg_id"], requests[3]["client_msg_id"]
	mu.Unlock()
	if firstID != retryID {
		t.Fatal("retry changed reminder identity")
	}
	var afterActivityCount int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM issue_activity WHERE issue_id=$1", id).Scan(&afterActivityCount); err != nil || afterActivityCount != sourceActivityCount {
		t.Fatal("reminder changed generic activity", afterActivityCount, sourceActivityCount, err)
	}
	v := get(id)
	if len(v.Reminders) != 1 || v.Reminders[0].Sequence != 1 || v.Reminders[0].DeliveryStatus != "succeeded" {
		t.Fatal(v.Reminders)
	}
	mu.Lock()
	last := requests[len(requests)-1]
	mu.Unlock()
	if last["thread_ts"] != "123.000002" || last["text"] != "Gregale Issues · Unacknowledged issue reminder" {
		t.Fatal("unthreaded reminder", last)
	}
	// Snooze pauses scheduling and permits a full interval after expiry.
	until := time.Now().UTC().Add(time.Hour)
	if err = s.UpdateIssueApplicationTriage(ctx, app.ID, account.ID, account.ID, id, v.Revision, state.IssueApplicationTriageAction{Action: "snooze", SnoozedUntil: &until}); err != nil {
		t.Fatal(err)
	}
	makeDue(id)
	enqueue(false)
	exec("UPDATE issue_application_triage SET snoozed_until=clock_timestamp()-interval '1 minute' WHERE issue_id=$1", id)
	enqueue(false)
	exec("UPDATE issue_application_triage SET snoozed_until=clock_timestamp()-interval '6 minutes' WHERE issue_id=$1", id)
	enqueue(true)
	// Identical policy saves preserve pending deliveries and revision.
	same, err := s.SetIssueApplicationReminderPolicy(ctx, app.ID, account.ID, policy)
	if err != nil || same.Revision != policy.Revision {
		t.Fatal("no-op policy", same, err)
	}
	claimed, ok, err := s.ClaimIssueSlackDelivery(ctx)
	if err != nil || !ok || claimed.Kind != "application_reminder" {
		t.Fatal("claim", claimed, err)
	}
	v = get(id)
	if err = s.UpdateIssueApplicationTriage(ctx, app.ID, account.ID, account.ID, id, v.Revision, state.IssueApplicationTriageAction{Action: "acknowledge"}); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.IssueSlackDeliveryReady(ctx, claimed); err != nil || ready {
		t.Fatal("acknowledgment did not fence", ready, err)
	}
	if err = s.FinishIssueSlackDelivery(ctx, claimed, state.IssueSlackResult{Status: "succeeded", MessageTS: "123.999999"}); !errors.Is(err, state.ErrIssueSlackClaimLost) {
		t.Fatal("stale receipt", err)
	}
	makeDue(id)
	enqueue(false)
	// Regression renews response/count and resumes only after a new native alert.
	if _, err = s.ActOnIssue(ctx, app.ID, id, account.ID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, now); err != nil {
		t.Fatal(err)
	}
	dep = seedPgDeployment(t, s, ctx, app)
	now = now.Add(time.Second)
	record("b")
	makeDue(id)
	enqueue(true)
	drain()
	// Current assignment determines the next route, including explicit fallback.
	if _, err = s.ActOnIssue(ctx, app.ID, id, account.ID, api.IssueActionRequest{Action: "assign", AssigneeTeamID: team.ID}, now); err != nil {
		t.Fatal(err)
	}
	makeDue(id)
	enqueue(true)
	drain()
	if r := get(id).Reminders[0]; r.TeamName != "Primary" || r.Sequence != 2 {
		t.Fatal("current team routing", r)
	}
	makeDue(id)
	enqueue(true)
	drain()
	makeDue(id)
	enqueue(false)
	// Another active response proves policy changes fence claims and preserve counts.
	next := record("c")
	drain()
	makeDue(next)
	enqueue(true)
	c, ok, err := s.ClaimIssueSlackDelivery(ctx)
	if err != nil || !ok || c.Kind != "application_reminder" {
		t.Fatal(c, err)
	}
	policy.IntervalMinutes = 6
	policy, err = s.SetIssueApplicationReminderPolicy(ctx, app.ID, account.ID, policy)
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := s.IssueSlackDeliveryReady(ctx, c); err != nil || ready {
		t.Fatal("policy fence", ready, err)
	}
	if err = s.FinishIssueSlackDelivery(ctx, c, state.IssueSlackResult{Status: "succeeded", MessageTS: "123.999999"}); !errors.Is(err, state.ErrIssueSlackClaimLost) {
		t.Fatal("policy stale receipt", err)
	}
	if _, err = s.SetIssueApplicationReminderPolicy(ctx, app.ID, account.ID, state.IssueApplicationReminderPolicy{Revision: 0, IntervalMinutes: 5, RepeatLimit: 1}); err == nil {
		t.Fatal("stale settings")
	}
	enqueue(false)
	makeDue(next)
	enqueue(true)
	drain()
	// Disabling native alerts clears schedules; re-enable alone does not replay.
	slackPolicy.Enabled = false
	slackPolicy, err = s.SetIssueApplicationSlackPolicy(ctx, app.ID, account.ID, state.PutIssueApplicationSlackPolicy{Enabled: false, Revision: slackPolicy.Revision, Events: slackPolicy.Events, FallbackTeamID: backup.ID})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(next)
	enqueue(false)
	if _, err = s.SetIssueApplicationSlackPolicy(ctx, app.ID, account.ID, state.PutIssueApplicationSlackPolicy{Enabled: true, Revision: slackPolicy.Revision, Events: slackPolicy.Events, FallbackTeamID: backup.ID}); err != nil {
		t.Fatal(err)
	}
	enqueue(false)

	// Missing routes and a saturated shared queue consume visible, spaced slots.
	blocked := record("d")
	drain()
	exec("UPDATE issue_team_slack_destinations SET enabled=false,revision=revision+1 WHERE app_id=$1", app.ID)
	makeDue(blocked)
	enqueue(true)
	if r := get(blocked).Reminders[0]; r.Outcome != "unrouted" || r.Sequence != 1 || r.DeliveryStatus != "" {
		t.Fatal("missing route history", r)
	}
	exec("UPDATE issue_team_slack_destinations SET enabled=true,revision=revision+1 WHERE app_id=$1", app.ID)
	exec(`WITH activities AS (
 INSERT INTO issue_activity(id,issue_id,action,created_at,details)
 SELECT gen_random_uuid(),$1,'created',clock_timestamp(),'{"queue_fixture":"full"}'::jsonb FROM generate_series(1,$2) RETURNING id,issue_id)
 INSERT INTO issue_team_slack_deliveries(id,destination_id,destination_revision,activity_id,incident_id,channel_id,application_policy_revision,recipient_account_ids)
 SELECT gen_random_uuid(),dst.id,dst.revision,act.id,act.issue_id,dst.channel_id,p.revision,ARRAY[$3::uuid]
 FROM activities act CROSS JOIN issue_team_slack_destinations dst JOIN issue_application_slack_policies p ON p.app_id=dst.app_id
 WHERE dst.team_id=$4`, blocked, api.IssueApplicationSlackMaxDeliveries, account.ID, backup.ID)
	makeDue(blocked)
	enqueue(true)
	if r := get(blocked).Reminders[0]; r.Outcome != "queue_full" || r.Sequence != 2 {
		t.Fatal("full queue history", r)
	}
	missed, err := s.GetIssueApplicationSlackPolicy(ctx, app.ID, account.ID)
	if err != nil || missed.UnroutedAlerts < 1 || missed.SkippedDeliveries < 1 {
		t.Fatal("missed counters", missed, err)
	}
	exec("UPDATE accounts SET plan='free' WHERE id=$1", account.ID)
	makeDue(blocked)
	enqueue(false)
	exec("UPDATE accounts SET plan='pro',abuse_hold_at=clock_timestamp(),abuse_hold_reason='operator' WHERE id=$1", account.ID)
	enqueue(false)
	exec("UPDATE accounts SET abuse_hold_at=NULL,abuse_hold_reason=NULL WHERE id=$1", account.ID)
	if _, err = s.ActOnIssue(ctx, app.ID, blocked, account.ID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, now); err != nil {
		t.Fatal(err)
	}
	enqueue(false)

}
